package work

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// The pace grows by one a growStep while work waits on it and nothing
// strains, pulls back to three quarters of what runs under strain, changes at
// most once a step, and keeps under the person's ceiling.
func TestPaceGrowsShrinksAndKeepsTheCeiling(t *testing.T) {
	var p pace
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	next := func(d time.Duration, running int, waiting bool, strain string, ceiling int) int {
		now = now.Add(d)
		return p.next(now, running, waiting, strain, true, 4, ceiling)
	}
	if l := next(0, 0, true, "", 0); l != 4 {
		t.Fatalf("starts at %d, want 4", l)
	}
	if l := next(10*time.Second, 4, true, "", 0); l != 4 {
		t.Fatalf("changed within a step: %d", l)
	}
	for want := 5; want <= 8; want++ { // the machine has room and briefs wait
		if l := next(growStep, want-1, true, "", 0); l != want {
			t.Fatalf("grew to %d, want %d", l, want)
		}
	}
	if l := next(growStep, 3, true, "", 0); l != 8 {
		t.Fatalf("grew while under its limit: %d", l)
	}
	if l := next(growStep, 8, false, "", 0); l != 8 {
		t.Fatalf("grew with nothing waiting: %d", l)
	}
	if l := next(paceStep, 8, true, "machine loaded (load 40 on 20 cores, limit 1 per core)", 0); l != 6 {
		t.Fatalf("under strain: %d, want 6", l)
	}
	if !strings.Contains(p.String(), "pulled back: machine loaded") {
		t.Fatalf("says %q", p.String())
	}
	if l := next(time.Second, 8, true, "machine loaded", 0); l != 6 {
		t.Fatalf("pulled back again within a step: %d", l)
	}
	if l := next(paceStep, 1, true, "machine loaded", 0); l != 4 {
		t.Fatalf("under load alone with one running: %d, want its start 4", l)
	}
	if l := next(paceStep, 1, true, "machine under memory pressure (30.00%, limit 20%)", 0); l != 1 {
		t.Fatalf("under memory pressure with one running: %d, want 1", l)
	}
	for range 20 {
		next(growStep, p.limit, true, "", 0)
	}
	if l := next(growStep, p.limit, true, "", 0); l != 22 {
		t.Fatalf("with no ceiling it grows on: %d", l)
	}
	if l := next(time.Second, 22, true, "", 5); l != 5 || !strings.Contains(p.String(), "ceiling") {
		t.Fatalf("over a ceiling of 5: %d (%s)", l, p.String())
	}
	if l := next(growStep, 5, true, "", 5); l != 5 {
		t.Fatalf("grew past the ceiling: %d", l)
	}
}

// A load hovering round its limit, strained one minute and not the next as
// on 10-25 from 18:00Z, doesn't swing the pace every minute: it pulls back
// no further than its start, and grows only after a growStep with no strain.
func TestPaceSettlesUnderAHoveringLoad(t *testing.T) {
	p := pace{limit: 6, start: 4}
	now := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	p.at = now.Add(-growStep)
	changes, last := 0, p.limit
	for i := range 120 { // two hours, a reading a minute
		now = now.Add(paceStep)
		strain := ""
		if i%2 == 0 {
			strain = "machine loaded (load 30 on 20 cores, limit 1 per core)"
		}
		l := p.next(now, p.limit, true, strain, true, 4, 0)
		if l < 4 {
			t.Fatalf("minute %d: load alone pulled the pace under its start: %d", i, l)
		}
		if l != last {
			changes, last = changes+1, l
		}
	}
	if changes > 2 {
		t.Fatalf("the pace changed %d times in two hours of a hovering load", changes)
	}
	if last != 4 {
		t.Fatalf("settled at %d, want its start 4", last)
	}
	for range 3 { // the load clears: still no growth within a growStep of it
		now = now.Add(paceStep)
		if l := p.next(now, 4, true, "", true, 4, 0); l != 4 {
			t.Fatalf("grew within a growStep of the last strain: %d", l)
		}
	}
	now = now.Add(paceStep)
	if l := p.next(now, 4, true, "", true, 4, 0); l != 5 {
		t.Fatalf("a growStep clear: %d, want 5", l)
	}
}

// With no machine reading on, the pace can't see, so it stays at its start;
// a spent plan still pulls it back, and it comes back to the start after. A
// restart starts from what already runs.
func TestPaceBlindAndAfterARestart(t *testing.T) {
	var p pace
	now := time.Now()
	if l := p.next(now, 0, true, "", false, 4, 0); l != 4 {
		t.Fatalf("starts at %d", l)
	}
	now = now.Add(paceStep)
	if l := p.next(now, 4, true, "", false, 4, 0); l != 4 {
		t.Fatalf("grew blind: %d", l)
	}
	now = now.Add(paceStep)
	if l := p.next(now, 4, true, "the workers' plan is nearly spent (5% left)", false, 4, 0); l != 3 {
		t.Fatalf("a spent plan: %d, want 3", l)
	}
	now = now.Add(growStep)
	if l := p.next(now, 3, true, "", false, 4, 0); l != 4 {
		t.Fatalf("back after the plan: %d, want 4", l)
	}
	var r pace
	if l := r.next(time.Now(), 20, true, "", true, 4, 0); l != 20 {
		t.Fatalf("after a restart with 20 running: %d", l)
	}
	var c pace
	if l := c.next(time.Now(), 20, true, "", true, 4, 8); l != 8 {
		t.Fatalf("after a restart with 20 running under a ceiling of 8: %d", l)
	}
}

// A ceiling the person sets or moves is where the pace starts again.
func TestPaceStartsAgainAtANewCeiling(t *testing.T) {
	var p pace
	now := time.Now()
	p.next(now, 1, true, "", true, 1, 1)
	if l := p.next(now.Add(time.Second), 1, true, "", true, 3, 3); l != 3 {
		t.Fatalf("a ceiling raised to 3: %d", l)
	}
	if l := p.next(now.Add(2*time.Second), 3, true, "", true, 2, 2); l != 2 {
		t.Fatalf("a ceiling lowered to 2: %d", l)
	}
}

// The pace's limit is what the queue and the check slots hold to: pulled
// back to one, a second brief and a second check wait; grown to two, both go.
func TestPaceHoldsAndFreesTheQueueAndChecks(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers, cfg.MaxChecks = 0, 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0
	m.C.WS.SaveConfig(cfg)
	set := func(p *pace, l int, start int) {
		p.mu.Lock()
		p.limit, p.start, p.at, p.why = l, start, time.Now(), "pulled back: machine loaded"
		p.mu.Unlock()
	}
	set(&m.workers, 1, defaultWorkers)
	a := file(t, m, "first", "")
	b := file(t, m, "second", "")
	tick(m)
	want := func(task Task, stage, reason string) {
		t.Helper()
		if got := stageOf(t, m, task); got.Stage != stage || !strings.Contains(got.Reason, reason) {
			t.Fatalf("%q: stage %q reason %q, want %q with %q", task.Title, got.Stage, got.Reason, stage, reason)
		}
	}
	want(a, StageWorking, "")
	want(b, StageQueued, "waiting for a worker (at the pace)")
	// The pace moving at the same limit or below leaves a waiting task as
	// saved: its reason doesn't carry the live pace.
	saved := stageOf(t, m, b).Updated
	set(&m.workers, 1, defaultWorkers)
	m.workers.mu.Lock()
	m.workers.why = "grew: room on the machine and plan"
	m.workers.mu.Unlock()
	tick(m)
	if got := stageOf(t, m, b); !got.Updated.Equal(saved) {
		t.Fatalf("a pace move re-saved a waiting task: reason %q", got.Reason)
	}
	set(&m.workers, 2, defaultWorkers)
	tick(m)
	want(b, StageWorking, "")

	set(&m.checks, 1, core.DefaultMaxChecks())
	slots := m.C.Effects.Slots
	first := slots.Acquire("p", "a", "test")
	got := make(chan func())
	go func() { got <- slots.Acquire("p", "b", "test") }()
	select {
	case <-got:
		t.Fatal("a second check ran at a pace of 1")
	case <-time.After(100 * time.Millisecond):
	}
	set(&m.checks, 2, core.DefaultMaxChecks())
	slots.Fill()
	select {
	case second := <-got:
		second()
	case <-time.After(5 * time.Second):
		t.Fatal("the second check still waits at a pace of 2")
	}
	first()
}

// Under strain with nothing running, the strain isn't the Hive's: the limit
// stays, at most at its start, rather than falling to 1 and taking minutes
// to climb back. This holds for the checks' pace as for the workers'.
func TestPaceUnderStrainWithNothingRunning(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name               string
		limit, start, want int
	}{
		{"grown past its start", 8, 4, 4},
		{"below its start", 3, 4, 3},
		{"the checks at their start", 2, 2, 2},
	} {
		p := pace{limit: c.limit, start: c.start, at: now.Add(-paceStep)}
		if l := p.next(now, 0, true, "machine loaded", true, c.start, 0); l != c.want {
			t.Errorf("%s: %d, want %d", c.name, l, c.want)
		}
	}
}

// With no ceiling set, the workers' and checks' paces still stop at a
// ceiling sized to the machine.
func TestPaceHasASafetyCeiling(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers, cfg.MaxChecks = 0, 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 1e6 // watched, never strained
	if _, watching := machine(cfg); !watching {
		t.Skip("no /proc/loadavg here")
	}
	m.C.WS.SaveConfig(cfg)
	m.workers = pace{limit: safeWorkers(), start: defaultWorkers, at: time.Now().Add(-paceStep)}
	if l := m.paceWorkers(cfg, safeWorkers(), true); l != safeWorkers() {
		t.Fatalf("workers grew past the safety ceiling %d: %d", safeWorkers(), l)
	}
	m.checks = pace{limit: safeChecks(), start: core.DefaultMaxChecks(), at: time.Now().Add(-paceStep)}
	m.paceChecks()
	if l := m.checks.get(); l != safeChecks() {
		t.Fatalf("checks past the safety ceiling %d: %d", safeChecks(), l)
	}
}

// The workers' pace grows only for a brief that waits on the limit itself:
// one waiting on another's files would leave the new slot empty. When it
// does grow, the brief starts in the same tick.
func TestPaceGrowsOnlyForBriefsTheLimitHolds(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers, cfg.MaxChecks = 0, 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 1e6
	if _, watching := machine(cfg); !watching {
		t.Skip("no /proc/loadavg here")
	}
	m.C.WS.SaveConfig(cfg)
	set := func(l int) {
		m.workers.mu.Lock()
		m.workers.limit, m.workers.start, m.workers.at = l, defaultWorkers, time.Now().Add(-growStep)
		m.workers.mu.Unlock()
	}
	set(1)
	a := file(t, m, "cap the checks", "Checks run via internal/core/effects.go.")
	tick(m)
	b := file(t, m, "hold strained turns", "Also in internal/core/effects.go, launch.")
	set(1)
	tick(m)
	if got := stageOf(t, m, b); got.Stage != StageQueued || !strings.Contains(got.Reason, "both touch") {
		t.Fatalf("overlapping brief: stage %q reason %q", got.Stage, got.Reason)
	}
	if l := m.workers.get(); l != 1 {
		t.Fatalf("grew for a brief that waits on another's files: %d", l)
	}
	c := file(t, m, "show the queue", "On the dashboard, internal/tui/views.go.")
	set(1)
	tick(m)
	if l := m.workers.get(); l != 2 {
		t.Fatalf("didn't grow for a brief the limit holds: %d", l)
	}
	for _, task := range []Task{a, c} {
		if got := stageOf(t, m, task); got.Stage != StageWorking {
			t.Fatalf("%q: stage %q reason %q", task.Title, got.Stage, got.Reason)
		}
	}
}

// Workers pull back when their plan runs well ahead of its five-hour window,
// as on 10-25, when it ran out over an hour before each reset; on the clock
// they don't.
func TestPacePullsBackAheadOfTheWindow(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	pick := cfg.Route("worker", "")[0]
	read := func(u float64, left time.Duration) {
		reset := time.Now().Add(left)
		m.C.J.MustAppend("plan.usage", core.Plan{Harness: pick.Harness, Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: u, ResetsAt: reset}}}})
	}
	read(0.5, 3*time.Hour)
	if why := m.planSpent(cfg); why != "" {
		t.Fatalf("on the clock: %q", why)
	}
	read(0.8, 3*time.Hour)
	if why := m.planSpent(cfg); !strings.Contains(why, "ahead of its five-hour window") {
		t.Fatalf("40%% ahead: %q", why)
	}
}
