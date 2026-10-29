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
// many work at once as workers() says (pace.go), each of their turns in one of
// the core's slots. Two briefs on the same
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
		if a.Resending() != b.Resending() { // work already under way finishes first
			return a.Resending()
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
// one waits. tasks is every task Tick looks at.
func (m *Module) admit(tasks []Task) (start map[string]bool, wait map[string]string) {
	cfg := m.C.WS.LoadConfig()
	working, away := 0, 0
	var ahead, line []Task // ahead: being worked on, or being started right now
	m.mu.Lock()
	for _, t := range tasks {
		switch {
		case (t.Queued() || t.Resending()) && !m.busy[t.ID]:
			// A worker a conflict sends back takes a slot as a new one
			// does: a main merge once reopened 19 at a pace of 1.
			line = append(line, t)
		case t.Stage == StageWorking && m.slotless(t):
			// A worker that can't take a turn now holds no slot (slotless):
			// three quiet ones held 3 of 8 slots for 10-12h while ~39 briefs
			// waited. A message or its reset resumes it (core.Runs), and
			// then it counts again, even if that puts the workers past the
			// limit (by as many as resume at once) until slots free; none
			// start meanwhile.
		case t.Stage == StageWorking && m.awayNow(t):
			// A worker started past max_workers whose turns go to a runner
			// holds no slot here, until a turn of its runs here again.
			away++
		case t.Stage == StageWorking || t.Queued(): // or a queued or resent one being started right now
			ahead = append(ahead, t)
			working++
			if m.away[t.ID] {
				away++
			}
		}
		// The rest hold no slot: new ones join the line once issued, and
		// pushed ones (landing, testing, reviewing, merging) merge main on
		// a conflict, with their worker if need be.
	}
	m.mu.Unlock()
	m.sentBack(tasks)
	m.inLine(line)
	limit, spent := workers(cfg), m.planSpent(cfg)
	// The reason is saved with the task, so it names the limit but not
	// a live value.
	full := fmt.Sprintf("waiting for a worker (all %d at work; max_workers in config.json)", limit)
	start, wait = map[string]bool{}, map[string]string{}
	// One task a pass goes past the limit to an executor module with room
	// (its worker's turns go there first, core.RunSpec.Away); the executor
	// is asked at most once a pass, as one launch spends the room.
	asked := false
	elsewhere := func() bool {
		if asked {
			return false
		}
		asked = true
		if m.elsewhere != nil {
			return m.elsewhere()
		}
		return m.C.Runs.Elsewhere()
	}
	sent := map[string]bool{}
	for _, t := range line {
		switch {
		case t.Held:
			wait[t.ID] = "held by " + cmp.Or(t.HeldBy, "the lead")
		case spent != "" && !t.Resending():
			// Work under way goes on: a worker sent back still takes its turn.
			wait[t.ID] = "waiting: " + steady(spent)
		case working >= limit && !t.Resending() && away < limit && elsewhere():
			// A worker a conflict sends back is reopened, not launched, so
			// it waits here.
			start[t.ID], sent[t.ID] = true, true
			away++
		case working >= limit:
			wait[t.ID] = full
		default:
			start[t.ID] = true
			working++
		}
	}
	m.mu.Lock()
	for id := range sent {
		if m.away == nil {
			m.away = map[string]bool{}
		}
		m.away[id] = true
	}
	m.mu.Unlock()
	return start, wait
}

// paceBack says why a worker sent back to its task (a conflict with main, a
// reviewer's findings, a retry) waits to take its next turn, or "": it takes
// a place among the workers() like a brief that starts, ahead of the
// briefs (admit counts it as working). On 10-28 a merge sent 22 conflicting
// PRs back at once through Reopen, which went past the pace: 19 workers ran
// while it said 1, and load reached 481. It is core's Runs.Admit.
func (m *Module) paceBack(r core.Run) string {
	if !r.Returned || r.Role != "worker" || r.Away { // an away worker's turns go to a runner first
		return ""
	}
	mine, working := false, 0
	tasks := m.List(func(t Task) bool { return !t.Terminal() && t.Worker != "" })
	m.backMu.Lock()
	defer m.backMu.Unlock()
	if m.backIn[r.ID] {
		return ""
	}
	for _, t := range tasks {
		switch {
		case t.Worker == r.ID:
			mine = true
		case t.Stage == StageWorking && !m.slotless(t) && !m.awayNow(t) && !m.waitsBack(t):
			working++
		}
	}
	if !mine {
		return ""
	}
	if working >= workers(m.C.WS.LoadConfig()) {
		return "waiting for a worker (all at work): sent back to its task"
	}
	m.backIn[r.ID] = true
	return ""
}

// waitsBack says whether t's worker was sent back to it and waits on the
// workers' count; backMu is held.
func (m *Module) waitsBack(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Returned && r.Role == "worker" && !r.Away && !m.backIn[r.ID]
}

// sentBack counts the workers at work in tasks that were sent back and wait
// on the workers' count, and forgets the ones let through whose turn has started.
func (m *Module) sentBack(tasks []Task) int {
	m.backMu.Lock()
	defer m.backMu.Unlock()
	for id := range m.backIn {
		if r, ok := m.C.Runs.Get(id); !ok || !r.Returned {
			delete(m.backIn, id)
		}
	}
	n := 0
	for _, t := range tasks {
		if t.Stage == StageWorking && !m.slotless(t) && m.waitsBack(t) {
			n++
		}
	}
	return n
}

// awayNow reports whether t's worker was started past max_workers for a runner
// and its latest turn didn't run here: it has yet to start one, or that one
// went to a runner.
func (m *Module) awayNow(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Away && (r.Turn == 0 || r.Where != "")
}

// takeAway says whether admit started t past max_workers for a runner, once.
func (m *Module) takeAway(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.away[id]
	delete(m.away, id)
	return a
}

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
		// Only in the caller's reach: another project's order is kept, and
		// not one note each for the briefs it never named.
		for _, t := range m.List(func(t Task) bool { return t.Queued() && t.Order > 0 && m.reach(r, t.Project) == nil }) {
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
		case m.reach(r, t.Project) != nil && strings.TrimSpace(a.Why) == "":
			// Every agent has the same say (the person, 10-28): another
			// project's line is anyone's to steer, saying why on the record.
			notes = append(notes, fmt.Sprintf("%s is on project %s; say why with why to steer another project's line", id, t.Project))
			continue
		case !t.Queued():
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
	line := m.List(func(t Task) bool { return (t.Queued() || t.Resending()) && m.C.Runs.Walled(r, t.Project) == nil })
	m.inLine(line)
	out := []string{}
	for i, t := range line {
		why := t.Reason
		if t.Resending() { // it started long ago: its wait is for a worker, not the brief's
			why = strings.TrimSuffix("its worker goes back to merge main; "+why, "; ")
		} else if d := time.Since(t.Created); !t.Created.IsZero() && d >= time.Hour {
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
