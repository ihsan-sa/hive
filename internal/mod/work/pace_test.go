package work

import (
	"fmt"
	"path/filepath"
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
	if why := m.planSpent(cfg); !strings.Contains(why, "ahead of the clock") {
		t.Fatalf("40%% ahead: %q", why)
	}
}

// steady keeps a strain's words up to its first reading: one with no
// readings is kept whole, and nested parentheses go with the first.
func TestSteady(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		"machine loaded": "machine loaded",
		"machine loaded (load 40 on 20 cores, limit 1 per core)":        "machine loaded",
		"machine low on memory (900 MB free (of 16 GB), floor 4096 MB)": "machine low on memory",
		"machine loaded(no space)":                                      "machine loaded(no space)",
	} {
		if got := steady(in); got != want {
			t.Errorf("steady(%q) = %q, want %q", in, got, want)
		}
	}
}

// A check of a task queued briefs wait on ranks first, a fix's next, and the
// rest, or a sha no task holds, not at all (issue #838).
func TestCheckRankFreesThenFixes(t *testing.T) {
	m, _, _ := helperRig(t)
	put := func(tk Task) {
		m.mu.Lock()
		m.tasks[tk.ID] = &tk
		m.mu.Unlock()
	}
	put(Task{ID: "blocker", Project: "p", SHA: "s1", Stage: StageTesting, Title: "holds"})
	put(Task{ID: "fix", Project: "p", SHA: "s2", Stage: StageTesting, Aim: AimFix})
	put(Task{ID: "gain", Project: "p", SHA: "s3", Stage: StageTesting})
	put(Task{ID: "done", Project: "p", SHA: "s4", Stage: StageDone})
	put(Task{ID: "waiter", Project: "p", Stage: StageQueued, Reason: fmt.Sprintf("waiting for %q (task %s) to land: both touch a.go", "holds", "blocker")})
	put(Task{ID: "waiter2", Project: "p", Stage: StageQueued, Reason: fmt.Sprintf("waiting for %q (task %s) to land: both touch a.go", "x", "done")})
	for sha, want := range map[string]int{"s1": rankFrees, "s2": rankFix, "s3": 0, "s4": 0, "nobody": 0} {
		if got := m.checkRank("p", sha, "test"); got != want {
			t.Errorf("rank of %s: %d, want %d", sha, got, want)
		}
	}
	if got := m.checkRank("other", "s1", "test"); got != 0 {
		t.Errorf("another project's sha ranked %d", got)
	}
}

// Main's own measurement goes ahead of every task's check while it runs,
// and keeps its place once done (issue #1078).
func TestCheckRankMainFirst(t *testing.T) {
	m, _, _ := helperRig(t)
	m.mu.Lock()
	m.tasks["blocker"] = &Task{ID: "blocker", Project: "p", SHA: "s1", Stage: StageTesting}
	m.tasks["waiter"] = &Task{ID: "waiter", Project: "p", Stage: StageQueued, Reason: fmt.Sprintf("waiting for %q (task %s) to land: both touch a.go", "x", "blocker")}
	m.mu.Unlock()
	done := m.MeasuringMain("p", "main1")
	again := m.MeasuringMain("p", "main1")
	if got := m.checkRank("p", "main1", "size"); got != rankMain || got <= m.checkRank("p", "s1", "test") {
		t.Errorf("main's measurement ranked %d, a freeing check %d", got, m.checkRank("p", "s1", "test"))
	}
	if got := m.checkRank("other", "main1", "score"); got != 0 {
		t.Errorf("another project's sha ranked %d", got)
	}
	done()
	done()
	if got := m.checkRank("p", "main1", "score"); got != rankMain {
		t.Errorf("one of two measurements done: ranked %d", got)
	}
	again()
	if got := m.checkRank("p", "main1", "score"); got != 0 {
		t.Errorf("measured: still ranked %d", got)
	}
}

// Load alone holds no worker from starting up to the pace's start, only past
// it; any other strain holds every start.
func TestLoadHoldsOnlyPastTheStart(t *testing.T) {
	load, mem := "machine loaded (load 40 on 20 cores, limit 1 per core)", "machine low on memory (900 MB free, floor 1024 MB)"
	for _, c := range []struct {
		strain  string
		working int
		want    bool
	}{{"", 9, false}, {load, 0, false}, {load, 3, false}, {load, 4, true}, {mem, 0, true}} {
		if got := loadHolds(c.strain, c.working, 4); got != c.want {
			t.Errorf("%q with %d at work: holds %v, want %v", c.strain, c.working, got, c.want)
		}
	}
}

// A gate's wait reason, saved with its task, stays the same as the gates'
// limit and the machine's readings move: neither is in it.
func TestGateRoomReasonHoldsAsTheLimitMoves(t *testing.T) {
	defer func(g func(core.Config) (string, bool)) { gateMachine = g }(gateMachine)
	strain := ""
	gateMachine = func(core.Config) (string, bool) { return strain, true }
	dir := t.TempDir()
	j, err := core.OpenJournal(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	m := &Module{C: &core.Core{J: j, WS: core.Workspace{Root: filepath.Join(dir, "hive")}}}
	maxGates(m, 5) // the ceiling and start, whatever the host's cores
	limit := func(l int) {
		m.gates.mu.Lock()
		m.gates.limit, m.gates.start, m.gates.at = l, 5, time.Now()
		m.gates.mu.Unlock()
	}
	full := ""
	for _, l := range []int{1, 2, 3} {
		limit(l)
		got := m.gateRoom(l)
		if full == "" {
			full = got
		}
		if got == "" || got != full {
			t.Fatalf("limit %d: reason %q, want %q at every limit", l, got, full)
		}
	}
	strained := ""
	for i, s := range []string{
		"machine loaded (load 40 on 20 cores, limit 1 per core)",
		"machine loaded (load 52 on 20 cores, limit 1 per core)",
	} {
		strain = s
		limit(3 + i)
		got := m.gateRoom(1)
		if strained == "" {
			strained = got
		}
		if got != "no room for another: machine loaded" || got != strained {
			t.Fatalf("strain %q at limit %d: reason %q", s, 3+i, got)
		}
	}
}

// Under load, a plan running ahead of its window is still seen, and pulls the
// workers under their start as load alone wouldn't.
func TestPaceSeesThePlanUnderLoad(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 1e-9
	if why, _ := machine(cfg); !onlyLoad(why) {
		t.Skip("no load to read here")
	}
	m.C.WS.SaveConfig(cfg)
	pick := cfg.Route("worker", "")[0]
	reset := time.Now().Add(3 * time.Hour)
	m.C.J.MustAppend("plan.usage", core.Plan{Harness: pick.Harness, Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour",
		Windows: map[string]harness.Window{"five_hour": {Utilization: 0.8, ResetsAt: reset}}}})
	m.workers = pace{limit: 4, start: defaultWorkers, at: time.Now().Add(-paceStep)}
	if l := m.paceWorkers(cfg, 2, true); l != 1 || !strings.Contains(m.workers.String(), "ahead of the clock") {
		t.Fatalf("under load with the plan ahead: %s, want 1 for the plan", m.workers.String())
	}
}

// The week is paced too: on 10-25 a login's week stood at 75% a day after
// its reset, on course to run out five and a half days early, while its
// five-hour window was on the clock.
func TestPacePullsBackAheadOfTheWeek(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	pick := cfg.Route("worker", "")[0]
	read := func(week float64, weekLeft time.Duration) {
		reset := time.Now().Add(3 * time.Hour)
		m.C.J.MustAppend("plan.usage", core.Plan{Harness: pick.Harness, Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: 0.3, ResetsAt: reset},
				"seven_day": {Utilization: week, ResetsAt: time.Now().Add(weekLeft)}}}})
	}
	read(0.5, 3*24*time.Hour)
	if why := m.planSpent(cfg); why != "" {
		t.Fatalf("on the clock in the week: %q", why)
	}
	read(0.75, 6*24*time.Hour)
	if why := m.planSpent(cfg); !strings.Contains(why, "ahead of the clock") {
		t.Fatalf("a week 75%% spent in its first day: %q", why)
	}
}

// Pulled under its start by CPU pressure, the workers' pace comes back to
// its start a growStep after only load is left, as load alone would block
// its growth for good; the checks' pace, whose builds make the load, may
// stay under it.
func TestPaceComesBackToTheStartUnderLoadAlone(t *testing.T) {
	const load = "machine loaded (load 30 on 20 cores, limit 1.5 per core)"
	now := time.Date(2026, 10, 26, 17, 0, 0, 0, time.UTC)
	workers, checks := pace{}, pace{loadCuts: true}
	for _, p := range []*pace{&workers, &checks} {
		p.next(now, 4, true, "", true, 4, 0)
		for i := range 4 {
			p.next(now.Add(time.Duration(i+1)*paceStep), p.limit, true, "machine under cpu pressure (80.00%, limit 40%)", true, 4, 0)
		}
		if p.limit != 1 {
			t.Fatalf("under cpu pressure: %d, want 1", p.limit)
		}
	}
	at := now.Add(4 * paceStep)
	for i := range 30 { // half an hour of load alone, a reading a minute
		at = at.Add(paceStep)
		w := workers.next(at, workers.limit, true, load, true, 4, 0)
		c := checks.next(at, checks.limit, true, load, true, 4, 0)
		if c != 1 {
			t.Fatalf("minute %d: the checks' pace moved under load alone: %d", i, c)
		}
		if i < 4 && w != 1 {
			t.Fatalf("minute %d: back within a growStep of the cpu pressure: %d", i, w)
		}
	}
	if workers.limit != 4 || !strings.Contains(workers.String(), "back to the start") {
		t.Fatalf("after load alone: %s, want back at its start 4", workers.String())
	}
	if l := checks.next(at.Add(paceStep), 4, true, load, true, 4, 0); l != 1 {
		t.Fatalf("checks under load alone: %d, want 1", l)
	}
	fresh := pace{limit: 4, start: 4, at: now, loadCuts: true}
	if l := fresh.next(now.Add(paceStep), 4, true, load, true, 4, 0); l != 3 {
		t.Fatalf("checks at their start under load alone: %d, want 3", l)
	}
}
