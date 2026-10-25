package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A worker sent back for a flaky forge check re-runs it: the round comes
// back, the merge waits for the new attempt (an old failure still listed
// doesn't send it back again) and merges once it passes. Re-runs are capped
// per head, and there must be a failure to re-run.
func TestRerunFailedForgeChecks(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.save(task, "")
	if _, err := m.RerunChecks(task.ID, "w1"); err == nil || !strings.Contains(err.Error(), "nothing to re-run") {
		t.Fatal("re-ran checks on a task with no failure")
	}
	flaky := core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, Detail: "failure"}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, flaky)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckPass}) // an answer, so no re-run of the Hive's own
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
	if f.Reruns(p.Repo, task.SHA) != 1 {
		t.Fatalf("%d re-runs on the forge, want 1 (%v)", f.Reruns(p.Repo, task.SHA), out)
	}
	*task, _ = m.Get(task.ID)
	if task.Round != 0 || task.Reruns[task.SHA] != 1 || !logHas(task, "round they cost is given back") {
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
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, flaky)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "re-run") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}

	// The cap: a second and third re-run go, a fourth doesn't.
	for i := 0; i < 2; i++ {
		if _, err := m.RerunChecks(task.ID, "lead"); err != nil {
			t.Fatal(err)
		}
		f.SetChecks(p.Repo, task.SHA, flaky)
	}
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
