package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A red forge check is run again once before it goes back; one red on the
// re-run too goes back, and the worker can re-run it once more: the round
// comes back, the merge waits for the new attempt (an old failure still
// listed doesn't send it back again) and merges once it passes, recorded as a
// flake. Re-runs are capped per head, and there must be a failure to re-run.
func TestRerunFailedForgeChecks(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.save(task, "")
	if _, err := m.RerunChecks(task.ID, "w1"); err == nil || !strings.Contains(err.Error(), "nothing to re-run") {
		t.Fatal("re-ran checks on a task with no failure")
	}
	flaky := core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, Detail: "failure", URL: "https://ci/attempt1"}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, flaky)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckPass}) // an answer, so no re-run of the Hive's own
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 || f.Reruns(p.Repo, task.SHA) != 1 || !strings.Contains(task.Reason, "run again once before a red goes back") {
		t.Fatalf("first red: stage %q, round %d, %d re-runs, reason %q", task.Stage, task.Round, f.Reruns(p.Repo, task.SHA), task.Reason)
	}
	// The re-run's own red (a new attempt, its own URL) goes back at once.
	again := flaky
	again.URL = "https://ci/attempt2"
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, again)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || !strings.Contains(task.Findings, "rerun-checks") {
		t.Fatalf("stage %q, round %d, findings %q", task.Stage, task.Round, task.Findings)
	}

	// A wait on main spent on this run of the checks.
	task.MainWait, task.MainWaitOn = time.Now().Add(-mainWaitMax), task.SHA
	m.save(task, "")

	// Through the op, as the worker: its own task by default.
	w1, _ := m.C.Runs.Get("w1")
	out, err := m.C.Runs.Ops().Call(w1, "rerun-checks", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Reruns(p.Repo, task.SHA) != 2 {
		t.Fatalf("%d re-runs on the forge, want 2 (%v)", f.Reruns(p.Repo, task.SHA), out)
	}
	*task, _ = m.Get(task.ID)
	if task.Round != 0 || task.Reruns[task.SHA] != 2 || !logHas(task, "round they cost is given back") {
		t.Fatalf("round %d, reruns %v, log %v", task.Round, task.Reruns, task.Log)
	}
	// A wait on main spent on the first run isn't carried into the re-run's.
	if !task.MainWait.IsZero() || task.MainWaitOn != "" {
		t.Fatalf("the re-run kept the old wait on main (%v on %q)", task.MainWait, task.MainWaitOn)
	}
	other := core.Run{RunSpec: core.RunSpec{ID: "w2", Role: "worker", Project: "p"}}
	// Another worker on the project passes reach (refused here only because
	// nothing has failed since); one on another project doesn't.
	if _, err := m.C.Runs.Ops().Call(other, "rerun-checks", json.RawMessage(`{"task":"t1"}`)); err == nil || !strings.Contains(err.Error(), "nothing to re-run") {
		t.Fatalf("a worker on the project: %v", err)
	}

	// The worker finishes again on the same head: its test isn't run again,
	// and the old failure still listed holds the merge rather than sending
	// it back.
	if err := m.open(task, p, task.SHA, "again"); err != nil {
		t.Fatal(err)
	}
	if task.Recheck {
		t.Fatal("finishing on a re-run head runs its tests again")
	}
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, again)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "re-run") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}

	// The cap: a third re-run goes, a fourth doesn't.
	if _, err := m.RerunChecks(task.ID, "lead"); err != nil {
		t.Fatal(err)
	}
	f.SetChecks(p.Repo, task.SHA, again)
	if _, err := m.RerunChecks(task.ID, "lead"); err == nil || !strings.Contains(err.Error(), "most for one head") {
		t.Fatalf("fourth re-run on one head: %v", err)
	}
	if f.Reruns(p.Repo, task.SHA) != 3 {
		t.Fatalf("%d re-runs on the forge, want 3", f.Reruns(p.Repo, task.SHA))
	}

	*task, _ = m.Get(task.ID)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, core.CheckRun{Name: "test (windows-latest)", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	if !logHas(task, "a flake: the forge's checks test (windows-latest) failed on") {
		t.Fatalf("no flake recorded: %v", task.Log)
	}
}

// A red job whose workflow run has other jobs still going isn't re-run (the
// forge refuses), nor sent back as if it had been: the task waits for the run
// to finish, then the red is re-run once before it costs a round, and
// rerun-checks says what is still running.
func TestRedRerunWaitsForItsRunToFinish(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.save(task, "")
	red := core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckFail, Detail: "failure", URL: "https://ci/attempt1"}
	going := core.CheckRun{Name: "test (macos-latest)", State: core.CheckPending}
	f.SetChecks(p.Repo, task.SHA, red, going)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckPass}, core.CheckRun{Name: "test (macos-latest)", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 || f.Reruns(p.Repo, task.SHA) != 0 || !strings.Contains(task.Reason, "still running (test (macos-latest))") {
		t.Fatalf("red beside a running job: stage %q, round %d, %d re-runs, reason %q", task.Stage, task.Round, f.Reruns(p.Repo, task.SHA), task.Reason)
	}
	if _, err := m.RerunChecks(task.ID, "w1"); err == nil || !strings.Contains(err.Error(), "test (macos-latest) on "+core.Short(task.SHA)+" are still running") {
		t.Fatalf("rerun-checks beside a running job: %v", err)
	}
	// The run finishes: the red is re-run once, still costing no round.
	going.State = core.CheckPass
	f.SetChecks(p.Repo, task.SHA, red, going)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 || f.Reruns(p.Repo, task.SHA) != 1 || !strings.Contains(task.Reason, "run again once before a red goes back") {
		t.Fatalf("after the run finished: stage %q, round %d, %d re-runs, reason %q", task.Stage, task.Round, f.Reruns(p.Repo, task.SHA), task.Reason)
	}
}

// Once the grace after a re-run is over, a failure on that head goes back to
// the worker as any red check does, without the hint once the cap is reached.
func TestRerunFailureAfterGraceGoesBack(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (macos-latest)", State: core.CheckFail})
	task.Reruns, task.RerunAt = map[string]int{task.SHA: rerunsPerHead}, time.Now()
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "re-run") {
		t.Fatalf("within the grace: stage %q, reason %q", task.Stage, task.Reason)
	}
	old := rerunGrace
	rerunGrace = 0
	t.Cleanup(func() { rerunGrace = old })
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || strings.Contains(task.Findings, "rerun-checks") {
		t.Fatalf("after the grace: stage %q, findings %q", task.Stage, task.Findings)
	}
}

// Finishing again on a head re-run long ago runs its tests again, as on any
// head: the skip holds only while the re-run's result may still be coming.
func TestFinishAfterRerunGraceRechecks(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	task.Reruns, task.RerunAt = map[string]int{task.SHA: 1}, time.Now().Add(-2*rerunGrace)
	if err := m.open(task, p, task.SHA, "a tool was installed"); err != nil {
		t.Fatal(err)
	}
	if !task.Recheck {
		t.Fatal("finishing on a head re-run long ago skips its tests")
	}
}

// A check that never started (a startup failure) isn't re-run, on GitHub or
// the local forge, and the error says why; a cancelled one is.
func TestRerunSkipsStartupFailure(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.save(task, "")
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "ci", State: core.CheckCantStart, Detail: "startup_failure"})
	if _, err := m.RerunChecks(task.ID, "lead"); err == nil || !strings.Contains(err.Error(), "never started") {
		t.Fatalf("re-ran a startup failure: %v", err)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "ci", State: core.CheckCantStart, Detail: "cancelled"})
	if _, err := m.RerunChecks(task.ID, "lead"); err != nil || f.Reruns(p.Repo, task.SHA) != 1 {
		t.Fatalf("cancelled check: %v, %d re-runs", err, f.Reruns(p.Repo, task.SHA))
	}
}

// A lead can't re-run checks across a wall: not on a task in an isolated
// project, nor from its own isolated project on another's task.
func TestRerunChecksWalled(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.save(task, "")
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "ci", State: core.CheckFail, Detail: "failure"})
	m.C.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	from := core.Run{RunSpec: core.RunSpec{ID: "lead-secret", Role: "lead", Project: "secret"}}
	if _, err := m.C.Runs.Ops().Call(from, "rerun-checks", json.RawMessage(`{"task":"`+task.ID+`"}`)); err == nil || !strings.Contains(err.Error(), "out of reach") {
		t.Fatalf("an isolated project's lead re-ran another project's checks: %v", err)
	}
	p.Isolated = true
	m.C.Projects.Update(p)
	from = core.Run{RunSpec: core.RunSpec{ID: "lead-q", Role: "lead", Project: "q"}}
	if _, err := m.C.Runs.Ops().Call(from, "rerun-checks", json.RawMessage(`{"task":"`+task.ID+`"}`)); err == nil || !strings.Contains(err.Error(), "out of reach") {
		t.Fatalf("a lead re-ran an isolated project's checks: %v", err)
	}
	if f.Reruns(p.Repo, task.SHA) != 0 {
		t.Fatalf("%d re-runs on the forge across a wall", f.Reruns(p.Repo, task.SHA))
	}
}

// A worker steers only its own project's tasks: recheck, rerun-checks and
// landed on another project's task are refused by reach, and nothing moves.
func TestWorkerSteersOnlyItsProject(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.save(task, "")
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "ci", State: core.CheckFail, Detail: "failure"})
	before, _ := m.Get(task.ID)
	stranger := core.Run{RunSpec: core.RunSpec{ID: "w-q", Role: "worker", Project: "q"}}
	for op, args := range map[string]string{
		"recheck":      `{"task":"` + task.ID + `","back":"look again"}`,
		"rerun-checks": `{"task":"` + task.ID + `"}`,
		"landed":       `{"task":"` + task.ID + `","why":"it's on main"}`,
	} {
		if _, err := m.C.Runs.Ops().Call(stranger, op, json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), "you act only on your own (q)") {
			t.Fatalf("%s from a worker on another project: %v", op, err)
		}
	}
	after, _ := m.Get(task.ID)
	if after.Stage != before.Stage || len(after.Log) != len(before.Log) || f.Reruns(p.Repo, task.SHA) != 0 {
		t.Fatalf("a refused call moved the task: stage %q, log %d -> %d, %d re-runs", after.Stage, len(before.Log), len(after.Log), f.Reruns(p.Repo, task.SHA))
	}
	// Another worker on its project rechecks it, on the record by its run id.
	after.Stage, after.Reason = StageBlocked, "a flake"
	m.save(&after, "")
	peer := core.Run{RunSpec: core.RunSpec{ID: "w-peer", Role: "worker", Project: p.Name}}
	if _, err := m.C.Runs.Ops().Call(peer, "recheck", json.RawMessage(`{"task":"`+task.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(task.ID); got.Stage != StageLanding || !logHas(&got, "asked by w-peer") {
		t.Fatalf("a worker's recheck on its project: stage %q, log %v", got.Stage, got.Log)
	}
}

// e2eKey is a test's quarantine key in the module flakeRig seeds.
const e2eKey = "example.com/p/e2e::"

// flakeRig is gateRig on a Go module (example.com/p), with quarantined, keyed
// package::test, quarantined for its task's PR alone.
func flakeRig(t *testing.T, files map[string]string, quarantined ...string) (*Module, *Task, core.Project, *core.Local) {
	m, task, p, f := gateRigSeeded(t, false, map[string]string{"go.mod": "module example.com/p\n"}, files)
	m.C.Effects.Quarantined = func(project, id string) []string {
		if project != p.Name || id != task.ID {
			return nil
		}
		return quarantined
	}
	return m, task, p, f
}

// e2eLog is a go test log where tests fail in example.com/p/e2e.
func e2eLog(tests ...string) string {
	var b strings.Builder
	for _, n := range tests {
		b.WriteString("--- FAIL: " + n + " (1.0s)\n")
	}
	return b.String() + "FAIL\nFAIL\texample.com/p/e2e\t300.1s\nok  \texample.com/p/core\t1.2s\n"
}

// A red forge check is run again before it goes back: a pass on the re-run
// merges, spends no round and is recorded as a flake, once, keyed by package.
func TestForgeFlakePassesOnRerun(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, URL: "https://ci/1"})
	f.SetCheckLog(p.Repo, "https://ci/1", e2eLog("TestModuleLifecycle"))
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 || f.Reruns(p.Repo, task.SHA) != 1 || queued(m, "w1") != "" {
		t.Fatalf("stage %q, round %d, %d re-runs, reason %q", task.Stage, task.Round, f.Reruns(p.Repo, task.SHA), task.Reason)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckPass, URL: "https://ci/2"})
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.Round != 0 || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
	type flake struct {
		Task, SHA string
		Tests     []string
	}
	var flaked []flake
	for _, e := range m.C.J.Since(0) {
		if e.Kind == "forge.flaked" {
			var x flake
			e.Decode(&x)
			flaked = append(flaked, x)
		}
	}
	if len(flaked) != 1 || flaked[0].Task != task.ID || flaked[0].SHA != task.SHA || len(flaked[0].Tests) != 1 || flaked[0].Tests[0] != e2eKey+"TestModuleLifecycle" {
		t.Fatalf("flakes recorded: %+v", flaked)
	}
	if !logHas(task, "a flake: the forge's checks test (windows-latest) ("+e2eKey+"TestModuleLifecycle) failed on") {
		t.Fatalf("log %v", task.Log)
	}
}

// A red that comes back on its re-run is the change's: it goes back to the
// worker, spending one round, and its failing tests are journaled as seen.
func TestForgeRealFailureGoesBack(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckPass})
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "https://ci/1"})
	f.SetCheckLog(p.Repo, "https://ci/1", e2eLog("TestBroken"))
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 {
		t.Fatalf("first red: stage %q, round %d", task.Stage, task.Round)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "https://ci/2"})
	f.SetCheckLog(p.Repo, "https://ci/2", e2eLog("TestBroken"))
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || !strings.Contains(task.Findings, forgeFailed) || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, findings %q", task.Stage, task.Round, task.Findings)
	}
	seen := 0
	for _, e := range m.C.J.Since(0) {
		var x struct {
			Task  string
			Tests []string
		}
		if e.Kind == "forge.tests_failed" && e.Decode(&x) == nil && x.Task == task.ID && len(x.Tests) == 1 && x.Tests[0] == e2eKey+"TestBroken" {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("%d sightings of TestBroken, want one per attempt", seen)
	}
}

// A check failing only quarantined tests doesn't send a PR back or hold it:
// it merges with no re-run and the PR says why.
func TestForgeQuarantinedFailureIgnored(t *testing.T) {
	m, task, p, f := flakeRig(t, map[string]string{"feature.txt": "y"}, e2eKey+"TestStalls", e2eKey+"TestModuleLifecycle")
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (macos-latest)", State: core.CheckFail, URL: "https://ci/mac"}, core.CheckRun{Name: "lint", State: core.CheckPass})
	f.SetCheckLog(p.Repo, "https://ci/mac", e2eLog("TestStalls", "TestModuleLifecycle"))
	f.SetCheckSteps(p.Repo, "https://ci/mac", core.JobStep{Name: "checkout", Conclusion: "success"}, core.JobStep{Name: "the suite", Conclusion: "failure"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.Round != 0 || f.Reruns(p.Repo, task.SHA) != 0 {
		t.Fatalf("stage %q, round %d, %d re-runs, reason %q", task.Stage, task.Round, f.Reruns(p.Repo, task.SHA), task.Reason)
	}
	said := false
	for _, c := range f.Comments(p.Repo, task.PR.Number) {
		said = said || strings.Contains(c, "Merging past known flakes: test (macos-latest)")
	}
	if !said || !logHas(task, "known flakes: the forge's checks test (macos-latest)") {
		t.Fatalf("comments %q, log %v", f.Comments(p.Repo, task.PR.Number), task.Log)
	}
	var ev struct{ Quarantined []string }
	for _, e := range m.C.J.Since(0) {
		if e.Kind == "effect.merged" {
			e.Decode(&ev)
		}
	}
	if len(ev.Quarantined) != 1 || ev.Quarantined[0] != "test (macos-latest)" {
		t.Fatalf("merge event quarantined %v", ev.Quarantined)
	}
}

// redOnce sets one red check "test" on t's head with log, main passing it,
// the head's one re-run already spent.
func redOnce(t *testing.T, m *Module, task *Task, p core.Project, f *core.Local, log string) {
	t.Helper()
	toMerging(t, m, task, p)
	rerunSpent(task)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckPass})
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "https://ci/1"})
	f.SetCheckLog(p.Repo, "https://ci/1", log)
	f.SetCheckSteps(p.Repo, "https://ci/1", core.JobStep{Name: "the suite", Conclusion: "failure"})
	mergeOnce(t, m, task, p)
}

func sentBack(t *testing.T, f *core.Local, task *Task) {
	t.Helper()
	if task.Stage != StageWorking || task.Round != 1 || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

// A quarantined test failing alongside a failure that names no test (a
// package that doesn't build, here) shows no failure in flakes alone: the
// check counts, and the PR goes back.
func TestForgeQuarantineKeepsBuildFailure(t *testing.T) {
	m, task, p, f := flakeRig(t, map[string]string{"feature.txt": "y"}, e2eKey+"TestStalls")
	redOnce(t, m, task, p, f, e2eLog("TestStalls")+"# example.com/p/win\nwin.go:3:1: undefined: syscall.Foo\nFAIL\texample.com/p/win [build failed]\n")
	sentBack(t, f, task)
}

// A check that fails a test outside the quarantine as well still counts.
func TestForgeQuarantineKeepsOtherFailures(t *testing.T) {
	m, task, p, f := flakeRig(t, map[string]string{"feature.txt": "y"}, e2eKey+"TestStalls")
	redOnce(t, m, task, p, f, e2eLog("TestStalls", "TestMine"))
	sentBack(t, f, task)
}

// The quarantine is keyed by package: a test of the same name failing in
// another package isn't the quarantined one.
func TestForgeQuarantineKeysByPackage(t *testing.T) {
	m, task, p, f := flakeRig(t, map[string]string{"feature.txt": "y"}, e2eKey+"TestStalls")
	redOnce(t, m, task, p, f, "--- FAIL: TestStalls (2.0s)\nFAIL\nFAIL\texample.com/p/core\t3.0s\n")
	sentBack(t, f, task)
}

// A change to a quarantined test's own package is never excused for it: a
// change mustn't break a known flake for real and pass.
func TestForgeQuarantineNotForItsOwnPackage(t *testing.T) {
	m, task, p, f := flakeRig(t, map[string]string{"e2e/testdata.txt": "y"}, e2eKey+"TestStalls")
	redOnce(t, m, task, p, f, e2eLog("TestStalls"))
	sentBack(t, f, task)
}

// A step-back steers the loop it diagnoses on its own project, as a worker or
// sweep does, and not another project's; a reviewer doesn't steer at all.
func TestStepbackSteersItsProject(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	task.Stage, task.Reason = StageBlocked, "a flake"
	m.save(task, "")
	reviewer := core.Run{RunSpec: core.RunSpec{ID: "r-1", Role: "reviewer", Project: p.Name}}
	if _, err := m.C.Runs.Ops().Call(reviewer, "recheck", json.RawMessage(`{"task":"`+task.ID+`"}`)); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("recheck from a reviewer: %v", err)
	}
	elsewhere := core.Run{RunSpec: core.RunSpec{ID: "stepback-q", Role: "stepback", Project: "q"}}
	if _, err := m.C.Runs.Ops().Call(elsewhere, "recheck", json.RawMessage(`{"task":"`+task.ID+`"}`)); err == nil || !strings.Contains(err.Error(), "you act only on your own (q)") {
		t.Fatalf("recheck from a step-back on another project: %v", err)
	}
	sb := core.Run{RunSpec: core.RunSpec{ID: "stepback-p", Role: "stepback", Project: p.Name}}
	if _, err := m.C.Runs.Ops().Call(sb, "queue", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("queue from a step-back: %v", err)
	}
	if _, err := m.C.Runs.Ops().Call(sb, "recheck", json.RawMessage(`{"task":"`+task.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(task.ID); got.Stage != StageLanding || !logHas(&got, "asked by stepback-p") {
		t.Fatalf("a step-back's recheck on its project: stage %q, log %v", got.Stage, got.Log)
	}
}

// localFlakeRig is flakeRig whose own test check prints the head's flaky.txt
// file as its log and fails, with main green.
func localFlakeRig(t *testing.T, log string, files map[string]string, quarantined ...string) (*Module, *Task, core.Project, *core.Local) {
	t.Helper()
	files["flaky.txt"] = log
	m, task, p, f := gateRigSeeded(t, false, map[string]string{"go.mod": "module example.com/p\n", ".hive/project.json": `{"test": "test ! -f flaky.txt || { cat flaky.txt; exit 1; }"}`}, files)
	m.C.Effects.Quarantined = func(project, id string) []string {
		if project != p.Name || id != task.ID {
			return nil
		}
		return quarantined
	}
	return m, task, p, f
}

// The Hive's own test check failing only quarantined tests doesn't send a PR
// back or hold it: it goes on to merge, and Effects.Merge takes that red run.
func TestLocalQuarantinedFailureIgnored(t *testing.T) {
	m, task, p, f := localFlakeRig(t, e2eLog("TestAnEventBringsTheNextTickForward"), map[string]string{"feature.txt": "y"}, e2eKey+"TestAnEventBringsTheNextTickForward")
	toMerging(t, m, task, p)
	if c, ok := m.C.Effects.GetCheck(p.Name, task.SHA, "test"); !ok || c.OK {
		t.Fatalf("the head's test run: %+v, want red", c)
	}
	if task.Round != 0 || !logHas(task, "known flakes: its tests fail on") {
		t.Fatalf("round %d, log %v", task.Round, task.Log)
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	var ev struct {
		Local []string `json:"quarantined_local"`
	}
	for _, e := range m.C.J.Since(0) {
		if e.Kind == "effect.merged" {
			e.Decode(&ev)
		}
	}
	if len(ev.Local) != 1 || ev.Local[0] != e2eKey+"TestAnEventBringsTheNextTickForward" {
		t.Fatalf("merge event quarantined_local %v", ev.Local)
	}
}

// A local red that fails a test outside the quarantine, or a quarantined one
// in a package the change touches, still counts.
func TestLocalQuarantineKeepsRealFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		log   string
		files map[string]string
	}{
		"other test":  {e2eLog("TestStalls", "TestMine"), map[string]string{"feature.txt": "y"}},
		"own package": {e2eLog("TestStalls"), map[string]string{"e2e/testdata.txt": "y"}},
		"build":       {e2eLog("TestStalls") + "FAIL\texample.com/p/win [build failed]\n", map[string]string{"feature.txt": "y"}},
	} {
		m, task, p, _ := localFlakeRig(t, tc.log, tc.files, e2eKey+"TestStalls")
		if err := m.test(task, p); err != nil {
			t.Fatal(err)
		}
		if task.Stage == StageMerging || task.Stage == StageReviewing || logHas(task, "known flakes") {
			t.Fatalf("%s: stage %q, log %v", name, task.Stage, task.Log)
		}
		if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: mustSync(t, m, p), Task: task.ID}); err == nil || !strings.Contains(err.Error(), "test run is red") {
			t.Fatalf("%s: merge of a red head: %v", name, err)
		}
	}
}

func mustSync(t *testing.T, m *Module, p core.Project) string {
	t.Helper()
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	return main
}
