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
	if _, err := m.C.Runs.Ops().Call(other, "rerun-checks", json.RawMessage(`{"task":"t1"}`)); err == nil {
		t.Fatal("a worker re-ran another task's checks")
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

	// The cap: a third re-run doesn't go.
	if _, err := m.RerunChecks(task.ID, "lead"); err == nil || !strings.Contains(err.Error(), "most for one head") {
		t.Fatalf("third re-run on one head: %v", err)
	}
	if f.Reruns(p.Repo, task.SHA) != 2 {
		t.Fatalf("%d re-runs on the forge, want 2", f.Reruns(p.Repo, task.SHA))
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

// A red forge check is run again before it goes back: a pass on the re-run
// merges, spends no round and is recorded as a flake, once.
func TestForgeFlakePassesOnRerun(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, URL: "https://ci/1"})
	f.SetCheckLog(p.Repo, "https://ci/1", "--- FAIL: TestModuleLifecycle (3.1s)\nFAIL\n")
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
	var flaked []struct {
		Task, SHA string
		Tests     []string
	}
	for _, e := range m.C.J.Since(0) {
		if e.Kind == "forge.flaked" {
			var x struct {
				Task, SHA string
				Tests     []string
			}
			e.Decode(&x)
			flaked = append(flaked, x)
		}
	}
	if len(flaked) != 1 || flaked[0].Task != task.ID || flaked[0].SHA != task.SHA || len(flaked[0].Tests) != 1 || flaked[0].Tests[0] != "TestModuleLifecycle" {
		t.Fatalf("flakes recorded: %+v", flaked)
	}
	if !logHas(task, "a flake: the forge's checks test (windows-latest) (TestModuleLifecycle) failed on") {
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
	f.SetCheckLog(p.Repo, "https://ci/1", "--- FAIL: TestBroken (0.1s)\nFAIL\n")
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 {
		t.Fatalf("first red: stage %q, round %d", task.Stage, task.Round)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "https://ci/2"})
	f.SetCheckLog(p.Repo, "https://ci/2", "--- FAIL: TestBroken (0.1s)\nFAIL\n")
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
		if e.Kind == "forge.tests_failed" && e.Decode(&x) == nil && x.Task == task.ID && len(x.Tests) == 1 && x.Tests[0] == "TestBroken" {
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
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	m.C.Effects.Quarantined = func(project string) []string {
		if project != p.Name {
			return nil
		}
		return []string{"TestStalls", "TestModuleLifecycle"}
	}
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (macos-latest)", State: core.CheckFail, URL: "https://ci/mac"}, core.CheckRun{Name: "lint", State: core.CheckPass})
	f.SetCheckLog(p.Repo, "https://ci/mac", "--- FAIL: TestStalls (2.0s)\n--- FAIL: TestModuleLifecycle (1.0s)\nFAIL\nFAIL\tgithub.com/x/hive/internal/e2e\t300.1s\n")
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

// A quarantined test failing alongside a failure that names no test (a
// package that doesn't build, here) shows no failure in flakes alone: the
// check counts, and the PR goes back.
func TestForgeQuarantineKeepsBuildFailure(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	m.C.Effects.Quarantined = func(string) []string { return []string{"TestStalls"} }
	toMerging(t, m, task, p)
	rerunOnce(task)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckPass})
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "https://ci/1"})
	f.SetCheckLog(p.Repo, "https://ci/1", "--- FAIL: TestStalls (2.0s)\nFAIL\nFAIL\tx/e2e\t3.0s\n# x/win\nwin.go:3:1: undefined: syscall.Foo\nFAIL\tx/win [build failed]\n")
	f.SetCheckSteps(p.Repo, "https://ci/1", core.JobStep{Name: "the suite", Conclusion: "failure"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

// A check that fails a test outside the quarantine as well still counts.
func TestForgeQuarantineKeepsOtherFailures(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	m.C.Effects.Quarantined = func(string) []string { return []string{"TestStalls"} }
	toMerging(t, m, task, p)
	rerunOnce(task)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckPass})
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "https://ci/1"})
	f.SetCheckLog(p.Repo, "https://ci/1", "--- FAIL: TestStalls (2.0s)\n--- FAIL: TestMine (0.1s)\nFAIL\n")
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}
