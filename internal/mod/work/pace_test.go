package work

import (
	"slices"
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
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
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
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
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
// a held one would leave the new slot empty. When it
// does grow, the brief starts in the same tick.
func TestPaceGrowsOnlyForBriefsTheLimitHolds(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers, cfg.MaxChecks = 0, 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 1e6
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
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
	held := stageOf(t, m, b)
	held.Held, held.HeldBy = true, "lead"
	m.save(&held, "held")
	set(1)
	tick(m)
	if got := stageOf(t, m, b); got.Stage != StageQueued || !strings.Contains(got.Reason, "held by") {
		t.Fatalf("held brief: stage %q reason %q", got.Stage, got.Reason)
	}
	if l := m.workers.get(); l != 1 {
		t.Fatalf("grew for a held brief: %d", l)
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

// A fix's check ranks first, and the rest, or a sha no task holds, not at
// all.
func TestCheckRankFixes(t *testing.T) {
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
	for sha, want := range map[string]int{"s1": 0, "s2": rankFix, "s3": 0, "s4": 0, "nobody": 0} {
		if got := m.checkRank("p", sha, "test"); got != want {
			t.Errorf("rank of %s: %d, want %d", sha, got, want)
		}
	}
	if got := m.checkRank("other", "s2", "test"); got != 0 {
		t.Errorf("another project's sha ranked %d", got)
	}
}

// While main is red, the fix whose brief names one of its failing tests goes
// ahead of every other fix, though behind main's own measurement; once main
// passes it is one fix among the rest (issue #1877).
func TestCheckRankRedMainFix(t *testing.T) {
	m, _, _ := helperRig(t)
	m.mu.Lock()
	m.tasks["red"] = &Task{ID: "red", Project: "p", SHA: "s1", Stage: StageTesting, Aim: AimFix, Title: "Fix the flaky example.com/x/internal/core::TestStaleModuleCacheRunsAgain"}
	m.tasks["why"] = &Task{ID: "why", Project: "p", SHA: "s2", Stage: StageTesting, Aim: AimFix, AimWhy: "TestMerge fails on main"}
	m.tasks["fix"] = &Task{ID: "fix", Project: "p", SHA: "s3", Stage: StageTesting, Aim: AimFix, Title: "TestStaleModuleCacheRunsAgainSlowly fails"}
	m.tasks["gain"] = &Task{ID: "gain", Project: "p", SHA: "s4", Stage: StageTesting, Known: "TestMerge"}
	m.mainFails["p"] = []string{"TestStaleModuleCacheRunsAgain", "TestMerge/sub_case"}
	m.mu.Unlock()
	for sha, want := range map[string]int{"s1": rankRedFix, "s2": rankRedFix, "s3": rankFix, "s4": 0} {
		if got := m.checkRank("p", sha, "test"); got != want {
			t.Errorf("while main is red, rank of %s: %d, want %d", sha, got, want)
		}
	}
	if got := m.checkRank("other", "s1", "test"); got != 0 {
		t.Errorf("another project's sha ranked %d", got)
	}
	done := m.MeasuringMain("p", "main1")
	if m.checkRank("p", "main1", "test") <= m.checkRank("p", "s1", "test") {
		t.Error("the red main's fix ranks with main's own measurement")
	}
	done()
	m.mu.Lock()
	delete(m.mainFails, "p")
	m.mu.Unlock()
	if got := m.checkRank("p", "s1", "test"); got != rankFix {
		t.Errorf("main green: the fix ranked %d", got)
	}
	if RankWhy(rankRedFix) != "fixes red main" || RankWhy(rankMain) != "measures main" || RankWhy(rankFix) != "a fix" {
		t.Error("RankWhy doesn't tell the ranks apart")
	}
}

// A red main's pytest ids name their fix by the test or the file, not by the
// directory before the first slash (hive-hwde main 5f5bd3af, issue #1989).
func TestCheckRankRedMainPytestFix(t *testing.T) {
	m, _, _ := helperRig(t)
	m.mu.Lock()
	m.tasks["fix"] = &Task{ID: "fix", Project: "p", SHA: "s1", Stage: StageTesting, Aim: AimFix, Title: "Main is green again", Known: "test_known_answers_fire_where_expected and test_parts_search_missing_httpx_names_cause fail on main 5f5bd3af"}
	m.tasks["file"] = &Task{ID: "file", Project: "p", SHA: "s2", Stage: StageTesting, Aim: AimFix, Title: "test_route_auto.py hangs on main"}
	m.tasks["word"] = &Task{ID: "word", Project: "p", SHA: "s3", Stage: StageTesting, Aim: AimFix, Title: "The tests under tests/ run faster"}
	m.tasks["param"] = &Task{ID: "param", Project: "p", SHA: "s4", Stage: StageTesting, Aim: AimFix, Title: "test_fit fails for the lid"}
	m.tasks["near"] = &Task{ID: "near", Project: "p", SHA: "s5", Stage: StageTesting, Aim: AimFix, Title: "test_parts fails"}
	m.mainFails["p"] = []string{
		"tests/test_parts.py::test_parts_search_missing_httpx_names_cause",
		"tests/test_verify.py::TestKnown::test_known_answers_fire_where_expected",
		"tests/test_route_auto.py::test_run_freerouting_silent_but_growing_ses_is_not_killed",
		"tests/test_mech.py::test_fit[lid-0.2]",
	}
	m.mu.Unlock()
	for sha, want := range map[string]int{"s1": rankRedFix, "s2": rankRedFix, "s3": rankFix, "s4": rankRedFix, "s5": rankFix} {
		if got := m.checkRank("p", sha, "test"); got != want {
			t.Errorf("rank of %s: %d, want %d", sha, got, want)
		}
	}
}

// Main's own measurement goes ahead of every task's check while it runs,
// and keeps its place once done (issue #1078).
func TestCheckRankMainFirst(t *testing.T) {
	m, _, _ := helperRig(t)
	m.mu.Lock()
	m.tasks["fix"] = &Task{ID: "fix", Project: "p", SHA: "s1", Stage: StageTesting, Aim: AimFix}
	m.mu.Unlock()
	done := m.MeasuringMain("p", "main1")
	again := m.MeasuringMain("p", "main1")
	if got := m.checkRank("p", "main1", "size"); got != rankMain || got <= m.checkRank("p", "s1", "test") {
		t.Errorf("main's measurement ranked %d, a fix's check %d", got, m.checkRank("p", "s1", "test"))
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

// Under load, a plan running ahead of its window is still seen, and pulls the
// workers under their start as load alone wouldn't.
func TestPaceSeesThePlanUnderLoad(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 1e-9
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
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

// The pull-back reads route by route: on 10-27 Claude's week went from 26%
// to 65% at 14% of it passed, and it never fired, as a free model first on
// another project's route reads on the clock (issue #1797).
func TestPacePullsBackWithAFreeModelOnAnotherRoute(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	pick := cfg.Route("worker", "")[0]
	cfg.Models = map[string]core.OpenModel{"gpt-oss": {Endpoint: "https://openrouter.ai/api/v1", Model: "openai/gpt-oss-120b:free", Key: true}}
	cfg.Projects = map[string]core.ProjectSettings{"deck": {Routes: map[string][]string{"worker": {"open:gpt-oss", pick.Harness + ":" + pick.Model}}}}
	week := time.Now().Add(6 * 24 * time.Hour)
	m.C.J.MustAppend("plan.usage", core.Plan{Harness: pick.Harness, Limit: harness.RateLimit{Status: "allowed", ResetsAt: week, Window: "seven_day",
		Windows: map[string]harness.Window{"seven_day": {Utilization: 0.6, ResetsAt: week}}}})
	if why := m.planSpent(cfg); !strings.Contains(why, "ahead of the clock") {
		t.Fatalf("the workspace's route 45%% ahead, a free model on deck's: %q", why)
	}
	cfg.Routes = map[string][]string{"worker": {"open:gpt-oss"}}
	if why := m.planSpent(cfg); why != "" {
		t.Fatalf("every route has a free model: %q", why)
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

// A load that is the disk's doesn't pull the checks' pace under its start,
// and brings it back there a growStep after another strain pulled it under:
// on 10-27 the checks sat at 1 for hours under a load of IO wait that never
// fell with them. A load that is the CPU's still cuts them.
func TestDiskLoadKeepsTheChecksAtTheirStart(t *testing.T) {
	const disk = "machine loaded on its disk (io pressure 57%, cpu pressure 3%; load 34 on 20 cores, limit 1.5 per core)"
	now := time.Date(2026, 10, 27, 22, 0, 0, 0, time.UTC)
	checks := pace{limit: 3, start: 3, at: now, loadCuts: true}
	for i := range 30 {
		if l := checks.next(now.Add(time.Duration(i+1)*paceStep), 3, true, disk, true, 3, 6); l != 3 {
			t.Fatalf("minute %d under disk load: %d, want its start 3", i, l)
		}
	}
	checks = pace{limit: 3, start: 3, at: now, loadCuts: true}
	at := now
	for range 3 {
		at = at.Add(paceStep)
		checks.next(at, checks.limit, true, "machine under memory pressure (57.91%, limit 10%)", true, 3, 6)
	}
	if checks.limit != 1 {
		t.Fatalf("under memory pressure: %d, want 1", checks.limit)
	}
	for range 6 {
		at = at.Add(paceStep)
		checks.next(at, checks.limit, true, disk, true, 3, 6)
	}
	if checks.limit != 3 || !strings.Contains(checks.String(), "back to the start") {
		t.Fatalf("after disk load alone: %s, want back at its start 3", checks.String())
	}
	if l := checks.next(at.Add(paceStep), 3, true, "machine loaded (load 34 on 20 cores, limit 1.5 per core)", true, 3, 6); l != 2 {
		t.Fatalf("checks under cpu load: %d, want 2", l)
	}
}

// Driven through the check slots: a rank-0 check already waiting in a full
// line starts next once MeasuringMain marks its sha, ahead of one that asked
// before it (issue #1713).
func TestMeasuringMainMovesAWaiterUp(t *testing.T) {
	m, _, _ := helperRig(t)
	slots := m.C.Effects.Slots
	slots.Resize(func() int { return 1 })
	hold := slots.Acquire("p", "running", "test")
	started := make(chan string, 4)
	for _, sha := range []string{"pr", "main1"} {
		go func() {
			rel := slots.Acquire("p", sha, "score")
			started <- sha
			rel()
		}()
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			_, w := slots.State()
			if slices.ContainsFunc(w, func(u core.SlotUse) bool { return u.SHA == sha }) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("%s never queued", sha)
			}
		}
	}
	if _, w := slots.State(); w[0].SHA != "pr" || w[1].Rank != 0 {
		t.Fatalf("before the mark, waiting %+v", w)
	}
	done := m.MeasuringMain("p", "main1")
	defer done()
	if _, w := slots.State(); w[0].SHA != "main1" || w[0].Rank != rankMain {
		t.Fatalf("after the mark, waiting %+v", w)
	}
	hold()
	if a, b := <-started, <-started; a != "main1" || b != "pr" {
		t.Fatalf("started %s then %s", a, b)
	}
}

// A worker sent back to its task (here a conflict with main) takes its next
// turn under the workers' pace like a brief that starts: on 10-28 a merge
// sent 22 PRs back at once past it, and 19 workers ran while it said 1. The
// ones waiting go ahead of new briefs, and go as slots free.
func TestSentBackWorkersWaitOnThePace(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
	m.C.WS.SaveConfig(cfg)
	set := func(l int) {
		m.workers.mu.Lock()
		m.workers.limit, m.workers.start, m.workers.at = l, defaultWorkers, time.Now()
		m.workers.mu.Unlock()
	}
	set(2)
	a := file(t, m, "first", "")
	b := file(t, m, "second", "")
	tick(m)
	set(1) // the pace pulled back meanwhile
	var runs []core.Run
	for _, task := range []Task{a, b} {
		got := stageOf(t, m, task)
		if got.Stage != StageWorking || got.Worker == "" {
			t.Fatalf("%q: stage %q worker %q", task.Title, got.Stage, got.Worker)
		}
		m.C.Runs.SetStatus(got.Worker, core.StatusDone, "finished")
		if err := m.C.Runs.Reopen(got.Worker, "Main moved and your branch conflicts with it."); err != nil {
			t.Fatal(err)
		}
		r, _ := m.C.Runs.Get(got.Worker)
		runs = append(runs, r)
	}
	if m.C.Runs.Admit == nil {
		t.Fatal("the work module doesn't pace core's launches")
	}
	if why := m.C.Runs.Admit(runs[0]); why != "" {
		t.Fatalf("the first sent back, with no worker at work: %q, want it let through", why)
	}
	if why := m.C.Runs.Admit(runs[1]); !strings.Contains(why, "at the pace") {
		t.Fatalf("the second sent back at a pace of 1: %q, want it waiting", why)
	}
	if why := m.C.Runs.Admit(runs[0]); why != "" {
		t.Fatalf("the first again, already let through: %q", why)
	}
	c := file(t, m, "third", "")
	tick(m)
	if got := stageOf(t, m, c); got.Stage != StageQueued {
		t.Fatalf("a new brief with a worker sent back waiting: stage %q, want queued behind it", got.Stage)
	}
	set(2)
	if why := m.C.Runs.Admit(runs[1]); why != "" {
		t.Fatalf("the second at a pace of 2: %q, want it let through", why)
	}
}

// IO pressure over its limit holds a pace at what runs: no more workers or
// checks start on a saturated disk, none running is stopped, the status says
// io pressure, and the limit grows back once it falls. On 10-29 turn starts
// rose from 5 to 13 per 10 minutes at IO pressure some avg10 87%.
func TestIOPressureHoldsThePace(t *testing.T) {
	const io = core.IOReason + " (some avg10 87%, limit 80%)"
	now := time.Date(2026, 10, 29, 13, 20, 0, 0, time.UTC)
	for _, loadCuts := range []bool{false, true} { // the workers' pace, the checks'
		p := pace{limit: 6, start: 4, at: now.Add(-time.Hour), loadCuts: loadCuts}
		at := now
		next := func(d time.Duration, running int, strain string) int {
			at = at.Add(d)
			return p.next(at, running, true, strain, true, 4, 12)
		}
		if l := next(0, 6, io); l != 6 {
			t.Fatalf("loadCuts %v: six running under io pressure: %d, want held at 6", loadCuts, l)
		}
		if !strings.Contains(p.String(), "held: "+core.IOReason) {
			t.Fatalf("loadCuts %v: says %q", loadCuts, p.String())
		}
		for range 10 {
			if l := next(growStep, 6, io); l != 6 {
				t.Fatalf("loadCuts %v: grew or cut under io pressure: %d", loadCuts, l)
			}
		}
		if l := next(paceStep, 3, io); l != 3 {
			t.Fatalf("loadCuts %v: three left running: %d, want 3", loadCuts, l)
		}
		if l := next(paceStep, 3, ""); l != 3 {
			t.Fatalf("loadCuts %v: grew at once as io pressure fell: %d", loadCuts, l)
		}
		if l := next(growStep, 3, ""); l != 4 {
			t.Fatalf("loadCuts %v: a growStep after it fell: %d, want 4", loadCuts, l)
		}
	}
}

// IO pressure holds every new worker, below the start too, and never sends
// one to an executor: launch doesn't read it, so would run it here.
func TestIOPressureHoldsNewWorkers(t *testing.T) {
	asked := 0
	hold := machineHold("", core.IOReason+" (some avg10 87%, limit 80%)", "machine loaded", 4, func() bool { asked++; return true })
	for _, working := range []int{0, 3, 5} {
		if why := hold(working); !strings.HasPrefix(why, core.IOReason) {
			t.Errorf("%d working: %q", working, why)
		}
	}
	if asked != 0 {
		t.Errorf("asked the executor %d times", asked)
	}
	if why := machineHold("machine low on memory", core.IOReason, "", 4, func() bool { return false })(0); why != "machine low on memory" {
		t.Errorf("memory and io: %q", why)
	}
}

// A restart starts the workers' pace where the last daemon left it, not
// from what runs: on 10-30 a pace of 1 became 13 at the start, and 29 turns
// the stop had cut resumed within 60 s (issue #2135).
func TestPaceResumesWhereTheLastDaemonLeftIt(t *testing.T) {
	m := queueRig(t)
	m.workers.mu.Lock()
	m.workers.limit, m.workers.start = 0, 0
	m.workers.mu.Unlock()
	m.C.J.MustAppend("pace.changed", map[string]any{"limit": "checks", "from": 2, "to": 5, "why": "grew"})
	m.C.J.MustAppend("pace.changed", map[string]any{"limit": "workers", "from": 2, "to": 1, "why": "pulled back"})
	if l := m.workers.get(); l != 1 {
		t.Fatalf("before the first read: %d, want the last daemon's 1", l)
	}
	if l := m.workers.next(time.Now(), 13, true, "", true, 4, 20); l != 1 {
		t.Fatalf("first read with 13 at work: %d, want 1", l)
	}
	if !strings.Contains(m.workers.String(), "before the restart") {
		t.Fatalf("says %q", m.workers.String())
	}
	p := pace{before: 30}
	if l := p.next(time.Now(), 3, true, "", true, 4, 8); l != 8 {
		t.Fatalf("a journaled 30 under a ceiling of 8: %d", l)
	}
}

// Every turn of a worker after its first takes a slot under the workers'
// pace: one resumed (after a restart, a message, a nudge) starts only while
// the worker turns running, and those let through to start, are under it,
// and the turn that has waited longest goes first.
func TestResumedTurnsTakeASlot(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
	m.C.WS.SaveConfig(cfg)
	set := func(l int) {
		m.workers.mu.Lock()
		m.workers.limit, m.workers.start, m.workers.at = l, defaultWorkers, time.Now()
		m.workers.mu.Unlock()
	}
	set(2)
	a := file(t, m, "first", "")
	b := file(t, m, "second", "")
	tick(m)
	set(1)
	var ids []string
	for _, task := range []Task{a, b} {
		got := stageOf(t, m, task)
		if got.Worker == "" {
			t.Fatalf("%q: no worker", task.Title)
		}
		ids = append(ids, got.Worker)
	}
	turn := func(id string, n int, end bool) {
		if end {
			m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": n, "status": core.StatusIdle})
			m.C.J.MustAppend("run.queued", map[string]any{"id": id, "text": "a reply"})
		} else {
			m.C.J.MustAppend("run.turn_started", map[string]any{"id": id, "turn": n})
		}
	}
	admit := func(id string) string {
		r, _ := m.C.Runs.Get(id)
		return m.C.Runs.Admit(r)
	}
	for _, id := range ids { // both took a turn, which ended (a restart cut it)
		turn(id, 1, false)
		turn(id, 1, true)
	}
	if why := admit(ids[0]); why != "" {
		t.Fatalf("the first resumed, nothing running: %q", why)
	}
	if why := admit(ids[1]); !strings.Contains(why, "takes a slot") {
		t.Fatalf("the second resumed at a pace of 1 with one let through: %q, want it waiting", why)
	}
	if why := admit(ids[0]); why != "" {
		t.Fatalf("the first again, already let through: %q", why)
	}
	turn(ids[0], 2, false)
	if why := admit(ids[1]); why == "" {
		t.Fatal("the second while the first's turn runs at a pace of 1: let through")
	}
	turn(ids[0], 2, true)
	if why := admit(ids[0]); why == "" {
		t.Fatal("the first's next turn went ahead of the second, which waited longer")
	}
	if why := admit(ids[1]); why != "" {
		t.Fatalf("the second once the first's turn ended: %q", why)
	}
	set(2)
	if why := admit(ids[0]); why != "" {
		t.Fatalf("the first at a pace of 2, one let through: %q", why)
	}
}
