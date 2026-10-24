package work

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// toMerging takes a rig's task through its tests to the merge.
func toMerging(t *testing.T, m *Module, task *Task, p core.Project) {
	t.Helper()
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageMerging {
		t.Fatalf("stage %q, want merging (reason %q)", task.Stage, task.Reason)
	}
}

func mergeOnce(t *testing.T, m *Module, task *Task, p core.Project) {
	t.Helper()
	if err := m.merge(task, p); err != nil {
		t.Fatal(err)
	}
}

func prState(t *testing.T, f *core.Local, n int) string {
	t.Helper()
	pr, err := f.GetPR("me/p", n)
	if err != nil {
		t.Fatal(err)
	}
	return pr.State
}

// A forge check still running holds the merge, saying so on the PR once; once
// every check passes on that exact head, it merges.
func TestMergeWaitsForForgeChecks(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPending})
	for i := 0; i < 3; i++ {
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "sandboxed-e2e") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q, PR %s", task.Stage, task.Reason, prState(t, f, task.PR.Number))
	}
	if n := strings.Count(strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"), "Holding the merge"); n != 1 {
		t.Fatalf("%d hold comments, want 1", n)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.Reason != "" || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A forge check that fails sends the change back as a red test would.
func TestMergeForgeCheckFails(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (macos-latest)", State: core.CheckFail, Detail: "failure", URL: "https://ci/1"}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPending})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || !strings.Contains(task.Findings, "test (macos-latest) (failure) https://ci/1") {
		t.Fatalf("stage %q, round %d, findings %q", task.Stage, task.Round, task.Findings)
	}
	if prState(t, f, task.PR.Number) != "OPEN" || alerts(m, "main-red") != 0 {
		t.Fatal("a red check merged or raised main")
	}
}

// A forge check main fails too isn't the worker's: the change waits on main.
func TestMergeForgeCheckFailsOnMainToo(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckFail})
	mergeOnce(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 || !strings.HasPrefix(task.Reason, "waiting on main") || alerts(m, "main-red") != 1 {
		t.Fatalf("stage %q, round %d, reason %q, %d alerts", task.Stage, task.Round, task.Reason, alerts(m, "main-red"))
	}
}

// A forge check that can't start is the environment's: the change waits, it
// is raised once, and it is never sent back to the worker.
func TestMergeForgeCheckCantStart(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckCantStart, Detail: "startup_failure"})
	for i := 0; i < 3; i++ {
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || task.Round != 0 || !strings.Contains(task.Reason, "can't start") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
	if n := alerts(m, "main-red"); n != 1 {
		t.Fatalf("%d alerts, want 1", n)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q once the check ran", task.Stage)
	}
}

func TestAsksOutsideRead(t *testing.T) {
	for s, want := range map[string]bool{
		"This touches login routing, so per goals.md it needs one read from the box's perms seat or architect before landing": true,
		"Needs a read from the architect.":                                 true,
		"The architect should read this first":                             true,
		"Updated docs/architecture.md and read the tests":                  false,
		"Fixes the README.":                                                false,
		"Changes login handling; needs one read from the box's Perms Seat": true,
	} {
		if asksOutsideRead(s) != want {
			t.Errorf("%q: got %v", s, !want)
		}
	}
}

// A PR that asks for a read from the box's perms seat or architect waits for
// one recorded as land on its head; a main merge that leaves its own diff
// alone keeps that read, and a fix goes back to the worker.
func TestMergeWaitsForOutsideRead(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	task.NeedsRead = true
	m.save(task, "")
	toMerging(t, m, task, p)
	read := task.SHA
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "perms seat or architect") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	if _, err := m.RecordRead("t1", core.Short(read), "the lead", "land", ""); err == nil {
		t.Fatal("recorded a read from someone not the perms seat or architect")
	}
	if _, err := m.RecordRead("#"+strconv.Itoa(task.PR.Number), core.Short(read), "perms", "land", "reads fine"); err != nil {
		t.Fatal(err)
	}
	// Main moves without touching the change: the read carries to the caught-up head.
	pushMain(t, f, map[string]string{"other.txt": "z"})
	mergeOnce(t, m, task, p)
	if task.SHA == read || task.Stage != StageTesting {
		t.Fatalf("not caught up: stage %q", task.Stage)
	}
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q: the read should carry over a main merge that leaves the diff alone", task.Stage, task.Reason)
	}
}

func TestOutsideReadFixGoesBack(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"feature.txt": "y"})
	task.NeedsRead = true
	m.save(task, "")
	toMerging(t, m, task, p)
	if _, err := m.RecordRead("t1", task.SHA, "architect", "fix", "token logic nobody read"); err != nil {
		t.Fatal(err)
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || !strings.Contains(task.Findings, "token logic nobody read") {
		t.Fatalf("stage %q, findings %q", task.Stage, task.Findings)
	}
}

// In a self project, where every head needs a read, a catch-up merge of main
// that leaves the change's own diff alone keeps the read instead of asking
// for a fresh one.
func TestCatchUpKeepsTheRead(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.go": "package p\n"})
	p.Self = true
	main, _ := m.C.Effects.Sync(p)
	ch, _ := m.C.Effects.Classify(p, main, task.SHA)
	if _, err := m.C.Runs.Start(core.RunSpec{ID: "r1", Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": task.SHA, "diff.patch": ch.Diff}}); err != nil {
		t.Fatal(err)
	}
	if err := m.C.Effects.RecordVerdict(core.Verdict{Project: p.Name, SHA: task.SHA, Run: "r1", Verdict: "land"}); err != nil {
		t.Fatal(err)
	}
	toMerging(t, m, task, p)
	pushMain(t, f, map[string]string{"other.txt": "z"})
	old := task.SHA
	mergeOnce(t, m, task, p)
	if task.SHA == old {
		t.Fatal("not caught up")
	}
	toMerging(t, m, task, p) // not reviewing: the read carried
	if task.Reviews != 0 || task.Reviewer != "" {
		t.Fatal("a reviewer was asked again")
	}
}
