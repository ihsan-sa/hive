package work

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The queue: a filed brief waits as queued until a worker can take it. Briefs
// start in the order they were filed, or the order the lead set (a brief
// waiting past overdue goes ahead of that order), and only as
// many work at once as the pace allows (pace.go, under max_workers) while the
// machine isn't loaded: the brake is the machine. Two briefs on the same
// files start side by side: a conflict is settled at merge, where the one
// that meets it goes back to its worker with main to merge. A hold on shared
// files took two thirds of all queue time and what it caught was never
// measured (docs/research/forced.md). Nothing here stops a worker that has
// started.
//
// A worker that can't take a turn now (quiet, finished awaiting take-out, or
// parked until a usage reset) counts toward no one's max_workers until it
// resumes (slotless).

// Queued reports whether t is waiting for a worker.
func (t Task) Queued() bool { return t.Stage == StageQueued || t.Stage == StageIssued }

// inLine sorts waiting tasks: those the lead started now first, then workers
// a conflict sends back, then those
// filed overdue ago, oldest first, then those the lead ordered, in its
// order, then the rest as filed.
func (m *Module) inLine(q []Task) {
	sort.SliceStable(q, func(i, j int) bool {
		a, b := q[i], q[j]
		if a.Now != b.Now {
			return a.Now
		}
		if a.Overdue() != b.Overdue() {
			return a.Overdue()
		}
		if a.Overdue() {
			return a.Created.Before(b.Created)
		}
		if (a.Order > 0) != (b.Order > 0) {
			return a.Order > 0
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Created.Before(b.Created)
	})
}

// overdue is how long a brief waits before it goes ahead of the lead's order:
// one the lead neither ordered nor started now moved only when nothing newer
// was ordered, and with the lead ordering about 19 and starting 10 a day,
// task 065256 waited 10h20m, passed by 30 newer briefs "to go first", and the
// check-homes fix 080252 waited 9h until /tmp filled the machine's memory.
// The lead still starts one ahead of it now, or holds it.
var overdue = 4 * time.Hour

// Overdue reports whether t has waited in the queue since overdue ago.
func (t Task) Overdue() bool {
	return t.Queued() && !t.Created.IsZero() && time.Since(t.Created) >= overdue
}

// admit decides which waiting tasks start a worker now, and why each other
// one waits: in line order, while the memory budget (core.MemBudget) has
// room for another turn beside those started this pass, and no measure of
// main holds turns on a full CPU (core.Runs.Yielding), as launch weighs
// every turn. On a saturated disk (core.DiskBusy) none starts while another
// worker works: the running ones go on, and the loop never stops for want
// of one. One any of those would hold starts anyway, one a pass, when an
// executor module has room for its project's worker (core.Runs.ElsewhereFor):
// it starts as an away run (m.away), whose turns go there first.
// tasks is every task Tick looks at.
func (m *Module) admit(tasks []Task) (start map[string]bool, wait map[string]string) {
	start, wait = map[string]bool{}, map[string]string{}
	cfg := m.C.WS.LoadConfig()
	var line []Task
	m.mu.Lock()
	for _, t := range tasks {
		if t.Queued() && !m.busy[t.ID] {
			line = append(line, t)
		}
	}
	m.mu.Unlock()
	m.inLine(line)
	disk, working := "", 0
	if len(line) > 0 {
		disk = diskBusy(cfg.DiskFull())
	}
	for _, t := range tasks {
		if disk != "" && t.Stage == StageWorking && !m.slotless(t) {
			working++
		}
	}
	n, away := 0, ""
	for _, t := range line {
		why := ""
		switch {
		case t.Held:
			wait[t.ID] = "held by " + cmp.Or(t.HeldBy, "the lead")
			continue
		case disk != "" && working+n > 0:
			why = disk + " for a worker"
		case core.MemBudget(cfg.TurnMB, n) != "":
			why = core.MemWait + " for a worker"
		case m.C.Runs.Yielding(cfg.TurnMB) != "":
			why = core.MeasureWait + " for a worker"
		default:
			start[t.ID] = true
			n++
			continue
		}
		if away == "" && elsewhereFor(m.C.Runs, t.Project) {
			start[t.ID], away = true, t.ID
			continue
		}
		wait[t.ID] = why
	}
	m.mu.Lock()
	for _, t := range line {
		delete(m.away, t.ID)
	}
	if away != "" {
		m.away[away] = true
	}
	m.mu.Unlock()
	return start, wait
}

// elsewhereFor is core.Runs.ElsewhereFor; a test says where there is room.
var elsewhereFor = (*core.Runs).ElsewhereFor

// parkedFree is how far off a parked worker's next turn must be for it to
// give up its slot: a retry's backoff is shorter and keeps it, a usage reset
// is hours off. On 10-28 a worker parked at 18:16 until a midnight reset held
// its slot, a finished one awaiting take-out held another for 1h20m, and the
// pace of 4-6 ran one turn while 14 briefs waited 2.5-4h.
var parkedFree = 10 * time.Minute

// slotless reports whether t's worker can't take a turn now and so holds no
// slot: it is quiet, has finished (its task waits to be taken out), or is
// parked until parkedFree or more from now.
func (m *Module) slotless(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	if !ok {
		return false
	}
	switch r.Status {
	case core.StatusQuiet, core.StatusDone, core.StatusBlocked, core.StatusStopped:
		return true
	case core.StatusParked:
		return time.Until(r.ParkedUntil) >= parkedFree
	}
	return false
}

// pathLike is a word that reads as a path in the project.
var pathLike = regexp.MustCompile(`[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+`)

// opQueue shows the queue, and with arguments sets its order or holds briefs.
func (m *Module) opQueue(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		Order   ids `json:"order"`
		Hold    ids `json:"hold"`
		Release ids `json:"release"`
		Now     ids `json:"now"`
		Why     string
	}
	if len(args) > 0 {
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
	}
	if len(a.Now) > 0 && strings.TrimSpace(a.Why) == "" {
		return nil, errors.New("now puts a brief first in line: say why with why (it goes on the record)")
	}
	type change struct {
		order int
		held  *bool
		now   bool
	}
	changes := map[string]*change{}
	at := func(id string) *change {
		if changes[id] == nil {
			changes[id] = &change{order: -1}
		}
		return changes[id]
	}
	yes, no := true, false
	for i, id := range a.Order {
		at(id).order = i + 1
	}
	for _, id := range a.Hold {
		at(id).held = &yes
	}
	for _, id := range a.Release {
		at(id).held = &no
	}
	for _, id := range a.Now {
		at(id).now, at(id).held = true, &no
	}
	var notes []string
	if len(a.Order) > 0 { // a new order replaces the old one: the rest go after, as filed
		// Only on the projects it names: another project's order is kept, and
		// not one note each for the briefs it never named.
		named := map[string]bool{}
		for _, id := range a.Order {
			if t, ok := m.Get(id); ok {
				named[t.Project] = true
			}
		}
		for _, t := range m.List(func(t Task) bool { return t.Queued() && t.Order > 0 && named[t.Project] }) {
			if changes[t.ID] == nil {
				at(t.ID).order = 0
			}
		}
	}
	for id, c := range changes {
		t, ok := m.Get(id)
		switch {
		case !ok:
			notes = append(notes, "no task "+id)
			continue
		case m.C.Runs.Walled(r, t.Project) != nil:
			notes = append(notes, "no task "+id)
			continue
		case !t.Queued():
			// One a conflict sent back waits in the line as the queue lists
			// it, so it is moved, held or started now like a brief (issue
			// #2085: the pivot #2046 sat 23rd behind 22 sendbacks).
			notes = append(notes, fmt.Sprintf("%s has left the queue (%s); a started worker isn't stopped by the queue", id, t.Stage))
			continue
		}
		m.mu.Lock()
		busy := m.busy[id]
		if !busy {
			m.busy[id] = true
		}
		m.mu.Unlock()
		if busy {
			notes = append(notes, id+" is starting its worker right now")
			continue
		}
		if t, ok = m.Get(id); !ok || !t.Queued() { // it started or was sent back before it was ours
			m.mu.Lock()
			delete(m.busy, id)
			m.mu.Unlock()
			notes = append(notes, id+" is starting its worker right now")
			continue
		}
		note := ""
		held := t.Held
		if c.held != nil {
			held = *c.held
		}
		if moved := c.order >= 0 && c.order != t.Order || c.now && !t.Now; moved && !held {
			// Its wait was weighed in the old line: the next tick says anew
			// (issue #701).
			t.Reason = "moved in line by " + r.ID + "; the next tick weighs it"
		}
		if c.order >= 0 && c.order != t.Order {
			t.Order, note = c.order, fmt.Sprintf("queue place set by %s", r.ID)
		}
		if c.held != nil && *c.held != t.Held {
			t.Held, note = *c.held, map[bool]string{true: "held by " + r.ID, false: "released by " + r.ID}[*c.held]
			t.HeldBy = map[bool]string{true: r.ID}[t.Held]
			if !t.Held && strings.HasPrefix(t.Reason, "held by ") {
				t.Reason = ""
			}
		}
		if c.now && !t.Now {
			t.Now, note = true, fmt.Sprintf("started now by %s, first in line: %s", r.ID, core.Clip(a.Why, 300))
		} else if note != "" && strings.TrimSpace(a.Why) != "" {
			note += ": " + core.Clip(a.Why, 300)
		}
		if note != "" {
			m.save(&t, note)
		}
		m.mu.Lock()
		delete(m.busy, id)
		m.mu.Unlock()
	}
	return map[string]any{"queue": m.Queue(r), "notes": notes}, nil
}

// ids are task ids as a list, or as one string split at commas or spaces (the CLI's --order a,b).
type ids []string

func (l *ids) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*l = strings.FieldsFunc(one, func(r rune) bool { return r == ',' || r == ' ' })
		return nil
	}
	return json.Unmarshal(b, (*[]string)(l))
}

// Queue lists the waiting briefs r may see, in the order they will start.
func (m *Module) Queue(r core.Run) []string {
	line := m.List(func(t Task) bool { return t.Queued() && m.C.Runs.Walled(r, t.Project) == nil })
	m.inLine(line)
	out := []string{}
	for i, t := range line {
		why := t.Reason
		if d := time.Since(t.Created); !t.Created.IsZero() && d >= time.Hour {
			// The lead sees the long waits, and which go ahead of its order.
			age := fmt.Sprintf("waited %s", d.Round(time.Minute))
			if t.Overdue() && !t.Now {
				age += ", overdue: ahead of the lead's order"
			}
			why = strings.TrimSuffix(age+"; "+why, "; ")
		}
		out = append(out, fmt.Sprintf("%d. %s %q (%s)%s", i+1, t.ID, t.Label(), t.Project, suffixed(why)))
	}
	return out
}

func suffixed(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}
