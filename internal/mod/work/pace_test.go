package work

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// Workers and checks run as many at once as the config says, and no more:
// a waiting task's reason names the setting, not a live figure, and a
// changed count holds from the next tick or hand-off.
func TestFixedCountsHoldAndFreeTheQueueAndChecks(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers, cfg.MaxChecks = 1, 1
	cfg.MemFloorMB = 0
	m.C.WS.SaveConfig(cfg)
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
	want(b, StageQueued, "waiting for a worker (all 1 at work; max_workers in config.json)")
	saved := stageOf(t, m, b).Updated
	tick(m)
	if got := stageOf(t, m, b); !got.Updated.Equal(saved) {
		t.Fatalf("a second tick re-saved a waiting task: reason %q", got.Reason)
	}
	cfg.MaxWorkers = 2
	m.C.WS.SaveConfig(cfg)
	tick(m)
	want(b, StageWorking, "")

	slots := m.C.Effects.Slots
	first := slots.Acquire("p", "a", "test")
	got := make(chan func())
	go func() { got <- slots.Acquire("p", "b", "test") }()
	select {
	case <-got:
		t.Fatal("a second check ran at max_checks 1")
	case <-time.After(100 * time.Millisecond):
	}
	cfg.MaxChecks = 2
	m.C.WS.SaveConfig(cfg)
	slots.Fill()
	select {
	case second := <-got:
		second()
	case <-time.After(5 * time.Second):
		t.Fatal("the second check still waits at max_checks 2")
	}
	first()
}

// With no counts set, workers are three quarters of the slots and checks
// their default; the slots op changes them at once, saying why on the
// record, and refuses a change with no why.
func TestSlotsOpChangesTheCountsOnTheRecord(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.Slots, cfg.MaxWorkers, cfg.MaxChecks = 8, 0, 0
	m.C.WS.SaveConfig(cfg)
	if w, c := workers(cfg), checks(cfg); w != 6 || c != core.DefaultMaxChecks() {
		t.Fatalf("defaults at 8 slots: %d workers, %d checks", w, c)
	}
	r := core.Run{RunSpec: core.RunSpec{ID: "sweep-1", Role: "sweep"}}
	if _, err := m.opSlots(r, []byte(`{"slots":"4"}`)); err == nil {
		t.Fatal("a change with no why went through")
	}
	if _, err := m.opSlots(r, []byte(`{"slots":"4","workers":2,"why":"53 turns at once drove memory pressure to 92%"}`)); err != nil {
		t.Fatal(err)
	}
	cfg = m.C.WS.LoadConfig()
	if cfg.Slots != 4 || cfg.MaxWorkers != 2 || cfg.MaxChecks != 0 {
		t.Fatalf("after the op: slots %d, max_workers %d, max_checks %d", cfg.Slots, cfg.MaxWorkers, cfg.MaxChecks)
	}
	said := ""
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "slots.changed" {
			said = string(e.Data)
		}
	})
	if !strings.Contains(said, "sweep-1") || !strings.Contains(said, "pressure to 92%") || !strings.Contains(said, `"slots":8`) {
		t.Fatalf("slots.changed: %s", said)
	}
	if _, err := m.opSlots(r, []byte(`{"slots":-1,"why":"x"}`)); err == nil {
		t.Fatal("a negative count went through")
	}
}

// While the workers' plan is ahead of its window no new brief starts, and
// the waiting one's reason doesn't carry the live share.
func TestPlanAheadHoldsNewBriefs(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MemFloorMB = 0
	m.C.WS.SaveConfig(cfg)
	pick := cfg.Route("worker", "")[0]
	reset := time.Now().Add(3 * time.Hour)
	m.C.J.MustAppend("plan.usage", core.Plan{Harness: pick.Harness, Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour",
		Windows: map[string]harness.Window{"five_hour": {Utilization: 0.8, ResetsAt: reset}}}})
	a := file(t, m, "first", "")
	tick(m)
	if got := stageOf(t, m, a); got.Stage != StageQueued || got.Reason != "waiting: the workers' plan is ahead of the clock in a usage window and would run out before it resets" {
		t.Fatalf("with the plan ahead: stage %q reason %q", got.Stage, got.Reason)
	}
}

// Under strain with nothing running, the strain isn't the Hive's: the limit
// stays, at most at its start, rather than falling to 1 and taking minutes
// to climb back. This holds for the checks' pace as for the workers'.
// With no ceiling set, the workers' and checks' paces still stop at a
// ceiling sized to the machine.
// The workers' pace grows only for a brief that waits on the limit itself:
// a held one would leave the new slot empty. When it
// does grow, the brief starts in the same tick.
// New briefs wait when the workers' plan runs well ahead of its five-hour window,
// as on 10-25, when it ran out over an hour before each reset; on the clock
// they don't.
func TestPlanAheadOfTheWindow(t *testing.T) {
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

// steady keeps a reason's words up to its first reading: one with no
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

// A gate's wait reason, saved with its task, carries neither the gates'
// count nor the machine's readings.
func TestGateRoomReasonHoldsAsTheReadingsMove(t *testing.T) {
	defer func(g func(core.Config) string) { gateMachine = g }(gateMachine)
	short := ""
	gateMachine = func(core.Config) string { return short }
	dir := t.TempDir()
	j, err := core.OpenJournal(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	m := &Module{C: &core.Core{J: j, WS: core.Workspace{Root: filepath.Join(dir, "hive")}}}
	maxGates(m, 2)
	if got := m.gateRoom(1); got != "" {
		t.Fatalf("one of two in use: %q", got)
	}
	if got := m.gateRoom(2); got != "as many run at once here as may, and every one is in use" {
		t.Fatalf("two of two in use: %q", got)
	}
	for _, s := range []string{"machine low on memory (3000 MB free, floor 4096 MB)", "machine low on memory (2500 MB free, floor 4096 MB)"} {
		short = s
		if got := m.gateRoom(1); got != "no room for another: machine low on memory" {
			t.Fatalf("%q: reason %q", s, got)
		}
	}
	if got := m.gateRoom(0); got != "" {
		t.Fatalf("the first gate under the memory guard: %q, want it to start", got)
	}
}

// Under load, a plan running ahead of its window is still seen, and pulls the
// workers under their start as load alone wouldn't.
// The week is paced too: on 10-25 a login's week stood at 75% a day after
// its reset, on course to run out five and a half days early, while its
// five-hour window was on the clock.
func TestPlanAheadOfTheWeek(t *testing.T) {
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

// The plan is read route by route: on 10-27 Claude's week went from 26%
// to 65% at 14% of it passed, and it never fired, as a free model first on
// another project's route reads on the clock (issue #1797).
func TestPlanAheadWithAFreeModelOnAnotherRoute(t *testing.T) {
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
// While the reviewer's plan runs ahead of its week, a review goes on the
// cheaper model and the task's record says so; a change in a high class
// still needs its second, separate read (the warden's and tribunal's prompts
// are high so they never land on one), and that read is paced too.
func TestPaceHoldsReadsAheadOfTheWeek(t *testing.T) {
	for _, ahead := range []bool{true, false} {
		m, task, p, _ := gateRigSeeded(t, false, map[string]string{".hive/project.json": `{"test": "true", "high": ["feature.txt"]}`}, map[string]string{"feature.txt": "y"})
		pick := m.Agents.Route("reviewer", p.Name)[0]
		week := 0.1
		if ahead {
			week = 0.5
		}
		reset := time.Now().Add(3 * time.Hour)
		m.C.J.MustAppend("plan.usage", core.Plan{Harness: pick.Harness, Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: 0.1, ResetsAt: reset},
				"seven_day": {Utilization: week, ResetsAt: time.Now().Add(6 * 24 * time.Hour)}}}})
		if err := testStep(m, task, p); err != nil || task.Stage != StageReviewing {
			t.Fatalf("stage %q, err %v", task.Stage, err)
		}
		for read := 1; read <= 2; read++ {
			if err := m.review(task, p); err != nil {
				t.Fatal(err)
			}
			r, _ := m.C.Runs.Get(task.Reviewer)
			if paced := r.Inputs["paced"] != ""; paced != ahead || logged(task, "ahead of its week") != ahead {
				t.Fatalf("ahead %v, read %d: paced input %v, log %v", ahead, read, paced, task.Log)
			}
			if read == 2 && !strings.HasSuffix(r.ID, "-read2") {
				t.Fatalf("ahead %v: the second read is %q", ahead, r.ID)
			}
			m.C.J.MustAppend("run.turn_ended", map[string]any{"id": r.ID, "turn": 1, "status": core.StatusDone,
				"result": harness.Result{Structured: []byte(`{"verdict":"land","summary":"ok","token":"` + reviewToken(r.ID) + `"}`)}})
			if err := m.review(task, p); err != nil {
				t.Fatal(err)
			}
			main, _ := m.C.Effects.Sync(p)
			ch, _ := m.C.Effects.Classify(p, main, task.SHA)
			if len(ch.High) != 1 || m.C.Effects.Reviewed(p, ch, task.SHA) != (read == 2) {
				t.Fatalf("ahead %v: high %v, reviewed after %d reads: %v", ahead, ch.High, read, read != 2)
			}
		}
	}
}

// The cheaper read is sonnet where the reviewer's route is opus.
func TestCheaperRead(t *testing.T) {
	for _, c := range []struct {
		route []core.Pick
		want  string
	}{
		{[]core.Pick{{Harness: "claude", Model: "opus"}}, "claude:sonnet"},
		{[]core.Pick{{Harness: "claude", Model: "claude-opus-5-5"}}, "claude:claude-sonnet-5-5"},
		{[]core.Pick{{Harness: "codex", Model: "opus"}, {Harness: "claude", Model: "haiku"}}, "claude:haiku"},
		{[]core.Pick{{Harness: "claude", Model: "sonnet"}}, "claude:sonnet"},
		{[]core.Pick{{Harness: "codex", Model: "gpt-5.5"}}, "codex:gpt-5.5"},
	} {
		if got := cheaper(c.route).String(); got != c.want {
			t.Errorf("%v: %s, want %s", c.route, got, c.want)
		}
	}
}

// A load that is the disk's doesn't pull the checks' pace under its start,
// and brings it back there a growStep after another strain pulled it under:
// on 10-27 the checks sat at 1 for hours under a load of IO wait that never
// fell with them. A load that is the CPU's still cuts them.
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

// Main's own red tests run again ranked as main's measurement: every red
// change on that main waits on the answer.
func TestMainTestsRetestRanksAsMain(t *testing.T) {
	m, forge, p := helperRig(t)
	out, _ := core.Git(forge.RepoPath("me/p"), "rev-parse", "main")
	main := strings.TrimSpace(out)
	rank := -1
	m.Retest = func(p core.Project, sha, _ string, _ core.Check, _ bool) bool {
		rank = m.checkRank(p.Name, sha, "retest")
		return true
	}
	c, err := m.mainTests(p, main, "exit 1")
	if err != nil {
		t.Fatal(err)
	}
	if !c.OK || rank != rankMain {
		t.Fatalf("ok %v, retest ranked %d", c.OK, rank)
	}
	if got := m.checkRank(p.Name, main, "test"); got != 0 {
		t.Fatalf("answered: still ranked %d", got)
	}
}

// A worker sent back to its task (here a conflict with main) takes its next
// turn only while fewer than the workers' count are at work, like a brief
// that starts: on 10-28 a merge sent 22 PRs back at once, and 19 workers ran.
// The ones waiting go ahead of new briefs, and go as room frees.
func TestSentBackWorkersWaitForAWorker(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 2
	cfg.MemFloorMB = 0
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "first", "")
	b := file(t, m, "second", "")
	tick(m)
	cfg.MaxWorkers = 1 // set lower meanwhile
	m.C.WS.SaveConfig(cfg)
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
		t.Fatal("the work module doesn't hold core's launches")
	}
	if why := m.C.Runs.Admit(runs[0]); why != "" {
		t.Fatalf("the first sent back, with no worker at work: %q, want it let through", why)
	}
	if why := m.C.Runs.Admit(runs[1]); !strings.Contains(why, "all at work") {
		t.Fatalf("the second sent back at max_workers 1: %q, want it waiting", why)
	}
	if why := m.C.Runs.Admit(runs[0]); why != "" {
		t.Fatalf("the first again, already let through: %q", why)
	}
	c := file(t, m, "third", "")
	tick(m)
	if got := stageOf(t, m, c); got.Stage != StageQueued {
		t.Fatalf("a new brief with a worker sent back waiting: stage %q, want queued behind it", got.Stage)
	}
	cfg.MaxWorkers = 2
	m.C.WS.SaveConfig(cfg)
	if why := m.C.Runs.Admit(runs[1]); why != "" {
		t.Fatalf("the second at max_workers 2: %q, want it let through", why)
	}
}
