package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

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

func mustSync(t *testing.T, m *Module, p core.Project) string {
	t.Helper()
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	return main
}
