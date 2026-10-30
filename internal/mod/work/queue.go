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
// one waits. tasks is every task Tick looks at.
func (m *Module) admit(tasks []Task) (start map[string]bool, wait map[string]string) {
	cfg := m.C.WS.LoadConfig()
	working, away := 0, 0
	var ahead, line []Task // ahead: being worked on, or being started right now
	m.mu.Lock()
	for _, t := range tasks {
		switch {
		case t.Queued() && !m.busy[t.ID]:
			// A worker a conflict sends back takes a slot as a new one
			// does: a main merge once reopened 19 at a pace of 1.
			line = append(line, t)
		case t.Stage == StageWorking && m.slotless(t):
			// A worker that can't take a turn now holds no slot (slotless):
			// three quiet ones held 3 of 8 slots for 10-12h while ~39 briefs
			// waited. A message or its reset resumes it (core.Runs), and
			// then it counts again, even if that puts the workers past the
			// limit until slots free; none start meanwhile, and its turn
			// waits for a slot of its own (turnSlot).
		case t.Stage == StageWorking && m.awayNow(t):
			// A worker started past the pace whose turns go to a runner
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
	back := m.sentBack(tasks)
	m.inLine(line)
	// One task a pass goes to an executor module with room: past a strained
	// machine (machineHold), or past the pace (its worker's turns go there
	// first, core.RunSpec.Away); the executor is asked at most once a pass.
	strained, loaded := core.Strained(cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax), core.Loaded(cfg.LoadMax)
	io := core.IOStrained(cfg.IOPressureMax, cfg.IOFullMax)
	var roomOK *bool
	elsewhere := func() bool {
		if roomOK == nil {
			ask := m.C.Runs.Elsewhere
			if m.elsewhere != nil {
				ask = m.elsewhere
			}
			ok := ask()
			roomOK = &ok
		}
		return *roomOK
	}
	// decide sorts the line at limit, and says whether a brief waits on the
	// limit itself: only then may the pace grow, as a held brief would leave
	// a new slot empty.
	decide := func(limit int) (start map[string]bool, wait map[string]string, capped bool) {
		start, wait = map[string]bool{}, map[string]string{}
		working, away := working, away
		sent := map[string]bool{}
		// One task a pass goes elsewhere, as one launch spends the room.
		once := false
		elsewhereOnce := func() bool {
			if once {
				return false
			}
			once = true
			return elsewhere()
		}
		// The reason is saved with the task, so it names the limit but not
		// its live value: the pace moved about every minute under load, and
		// each move re-saved every waiting task with its whole brief (one
		// 36 times in 40 minutes). The pace itself shows in Pace.
		full := fmt.Sprintf("waiting for a worker (max_workers %d)", cfg.MaxWorkers)
		if cfg.MaxWorkers == 0 || limit < cfg.MaxWorkers {
			full = "waiting for a worker (at the pace)"
		}
		hold := machineHold(strained, io, loaded, cmp.Or(cfg.MaxWorkers, defaultWorkers), elsewhereOnce)
		for _, t := range line {
			held := "" // asked only of a task nothing else holds back
			switch {
			case t.Held:
				wait[t.ID] = "held by " + cmp.Or(t.HeldBy, "the lead")
			case working >= limit && away < awayMax(cfg) && elsewhereOnce():
				// Past the pace while a runner has room: its worker's turns
				// go there first (core.RunSpec.Away). A worker a conflict
				// sends back is reopened, not launched, so it waits here.
				start[t.ID], sent[t.ID] = true, true
				away++
			case working >= limit:
				wait[t.ID], capped = full, true
			case func() bool { held = hold(working); return held != "" }():
				wait[t.ID] = "waiting: " + steady(held)
				if !onlyLoad(held) {
					m.C.Runs.Holding() // the turns after a strain ramp up (load alone holds only past the start)
				}
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
		return start, wait, capped
	}
	// The pace moves on what waits at its limit now; if that moves it, the
	// line is sorted again at the new one.
	limit := m.paceWorkers(cfg, working, false)
	start, wait, capped := decide(limit)
	if capped || back > 0 && working >= limit { // a worker sent back waits on the limit too
		if l := m.paceWorkers(cfg, working, true); l != limit {
			start, wait, _ = decide(l)
		}
	}
	return start, wait
}

// paceBack says why a worker's next turn waits on the workers' pace, or "":
// it is core's Runs.Admit. A worker's first turn starts with its task, which
// admit starts under the pace; every later turn takes a slot too (turnSlot).
// One sent back to its task (a conflict with main, a reviewer's findings, a
// retry) also waits, ahead of the briefs, until the tasks at work are under
// the pace (admit counts it as working). On 10-28 a merge sent 22
// conflicting PRs back at once through Reopen, which went past the pace: 19
// workers ran while it said 1, and load reached 481.
func (m *Module) paceBack(r core.Run) string {
	if r.Role != "worker" || r.Away || r.Turn == 0 && !r.Returned { // an away worker's turns go to a runner first
		return ""
	}
	limit := cmp.Or(m.workers.get(), m.C.WS.LoadConfig().MaxWorkers, defaultWorkers)
	tasks := m.List(func(t Task) bool { return !t.Terminal() && t.Worker != "" })
	running := m.C.Runs.List(func(x *core.Run) bool {
		return x.Role == "worker" && x.Status == core.StatusRunning && x.Where == "" && x.ID != r.ID
	})
	m.backMu.Lock()
	defer m.backMu.Unlock()
	if r.Returned && !m.backIn[r.ID] {
		mine, working := false, 0
		for _, t := range tasks {
			switch {
			case t.Worker == r.ID:
				mine = true
			case t.Stage == StageWorking && !m.slotless(t) && !m.awayNow(t) && !m.waitsBack(t):
				working++
			}
		}
		if mine && working >= limit {
			return "waiting for a worker (at the pace): sent back to its task"
		}
		if mine {
			m.backIn[r.ID] = true
		}
	}
	return m.turnSlot(r, len(running), limit, time.Now())
}

// turnSeen is how lately a run must have asked for a turn slot to be
// waiting for one: a tick steps every run about once a second.
var turnSeen = 10 * time.Second

// turnPass is how long a slot given to a turn that hasn't started (held
// after by a strain or the ramp) is kept for it.
var turnPass = 5 * time.Minute

// turnWaiter is a turn waiting for a slot: since when, and when it last asked.
type turnWaiter struct{ since, seen time.Time }

// turnPassed is a slot given to a run's turn after turn, at when.
type turnPassed struct {
	turn int
	at   time.Time
}

// turnSlot says why r's next turn waits for a slot, or "": it starts only
// while the worker turns running here (running, r's own left out) and those
// given a slot that haven't started yet are fewer than limit, the workers'
// pace, and the turns that have waited longer go first. Without it a turn the
// pace didn't start took no slot: a restart resumed every turn it cut at
// once (29 within 60 s on 10-30, at a pace of 1; load 50 to 158 on 20
// cores), and a quiet worker's message put it past the limit. backMu is held.
func (m *Module) turnSlot(r core.Run, running, limit int, now time.Time) string {
	if m.turnIn == nil {
		m.turnIn, m.turnWait = map[string]turnPassed{}, map[string]turnWaiter{}
	}
	if p, ok := m.turnIn[r.ID]; ok && p.turn == r.Turn && now.Sub(p.at) < turnPass {
		return "" // given its slot; held since by something else
	}
	delete(m.turnIn, r.ID)
	taken := running
	for id, p := range m.turnIn {
		cur, ok := m.C.Runs.Get(id)
		if !ok || cur.Terminal() || cur.Turn != p.turn || cur.Status == core.StatusRunning || now.Sub(p.at) >= turnPass {
			delete(m.turnIn, id) // started (counted as running), ended, or let go
			continue
		}
		taken++
	}
	me, ok := m.turnWait[r.ID]
	if !ok || now.Sub(me.seen) > turnSeen {
		me.since = now
	}
	me.seen = now
	m.turnWait[r.ID] = me
	ahead := 0
	for id, w := range m.turnWait {
		switch {
		case now.Sub(w.seen) > turnSeen:
			delete(m.turnWait, id)
		case id != r.ID && (w.since.Before(me.since) || w.since.Equal(me.since) && id < r.ID):
			ahead++
		}
	}
	if taken+ahead >= limit {
		return "waiting for a worker (at the pace): its next turn takes a slot"
	}
	delete(m.turnWait, r.ID)
	m.turnIn[r.ID] = turnPassed{turn: r.Turn, at: now}
	return ""
}

// waitsBack says whether t's worker was sent back to it and waits on the
// pace; backMu is held.
func (m *Module) waitsBack(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Returned && r.Role == "worker" && !r.Away && !m.backIn[r.ID]
}

// sentBack counts the workers at work in tasks that were sent back and wait
// on the pace, and forgets the ones let through whose turn has started.
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

// awayMax is how many workers may run past the pace on executor modules'
// runners at once: as many as the workers' own ceiling, so a runner gone
// (whose workers' turns come back here) at most doubles the work here.
func awayMax(cfg core.Config) int { return cmp.Or(cfg.MaxWorkers, defaultWorkers) }

// awayNow reports whether t's worker was started past the pace for a runner
// and its latest turn didn't run here: it has yet to start one, or that one
// went to a runner.
func (m *Module) awayNow(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Away && (r.Turn == 0 || r.Where != "")
}

// takeAway says whether admit started t past the pace for a runner, once.
func (m *Module) takeAway(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.away[id]
	delete(m.away, id)
	return a
}

// machineHold says, for each task in line in turn with working workers at
// work, why the machine holds it back, or "". A machine over load_max alone
// (loaded) holds a new worker only from start on, as the few workers there
// aren't what loads it, and never sends one elsewhere, since launch would
// run it here; one task a pass goes past a strained machine while an
// executor module has room, since launch hands a strained turn to it. A
// saturated disk (io, core.IOStrained) holds every new worker, and never
// sends one elsewhere either: launch doesn't read it, so would run it here.
func machineHold(strained, io, loaded string, start int, elsewhere func() bool) func(working int) string {
	asked := false
	return func(working int) string {
		if strained == "" {
			if io != "" {
				return io
			}
			if loaded != "" && working >= start {
				return loaded
			}
			return ""
		}
		if !asked {
			asked = true
			if elsewhere() {
				return ""
			}
		}
		return strained
	}
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
