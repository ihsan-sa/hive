package work

import (
	"os"
	"path/filepath"
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

// A PR on the security list waits for a read recorded as land on its head;
// a main merge that leaves its own diff alone keeps that read.
func TestMergeWaitsForOutsideRead(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m.save(task, "")
	reviewerLand(t, m, task, p, "r1")
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
	// Main moves without touching the change: the read carries to the
	// caught-up head.
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

// When main moved, a PR with its security read is brought up to date and
// tested again before it merges, never squashed onto a main it wasn't tested
// with; its own diff is unchanged, so its reads carry and it merges at the
// caught-up head without a new read.
func TestCleanPRIsCaughtUpAndKeepsItsRead(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m.save(task, "")
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	read := task.SHA
	if _, err := m.RecordRead("t1", read, "perms", "land", ""); err != nil {
		t.Fatal(err)
	}
	pushMain(t, f, map[string]string{"other.txt": "z"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageTesting || task.SHA == read {
		t.Fatalf("not caught up and tested again: stage %q at %s", task.Stage, core.Short(task.SHA))
	}
	main, _ := m.C.Effects.Sync(p)
	if _, err := core.Git(m.C.WS.Project(p.Name), "merge-base", "--is-ancestor", main, task.SHA); err != nil {
		t.Fatal("the head that will merge doesn't hold main")
	}
	toMerging(t, m, task, p)
	if _, ok := m.C.Effects.GetCheck(p.Name, task.SHA, "test"); !ok {
		t.Fatal("the caught-up head wasn't tested")
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.Reviews != 0 {
		t.Fatalf("stage %q, reason %q, reviews %d", task.Stage, task.Reason, task.Reviews)
	}
}

// A PR that conflicts with main goes back to the worker, and the head it
// resolves to needs a new read.
func TestConflictGoesBack(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m.save(task, "")
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	m.RecordRead("t1", task.SHA, "perms", "land", "")
	read := task.SHA
	pushMain(t, f, map[string]string{"secure/box.go": "package secure\n\n// main's own\n"})
	if pr, _ := f.GetPR(p.Repo, task.PR.Number); pr.Mergeable != "CONFLICTING" {
		t.Fatalf("the local forge should see the conflict: %+v", pr)
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.SHA != read || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

func TestOutsideReadFixGoesBack(t *testing.T) {
	m, task, p, _ := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m.save(task, "")
	reviewerLand(t, m, task, p, "r1")
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

// securityList puts only secure/** on main's security list.
var securityList = map[string]string{core.SecurityList: "# paths that need a security read\npaths = [\n  \"secure/**\", # the boundary\n  \"tests/LANDING-security.toml\",\n]\n"}

// reviewerLand records the Hive's own reviewer's land on the task's head.
func reviewerLand(t *testing.T, m *Module, task *Task, p core.Project, id string) {
	t.Helper()
	main, _ := m.C.Effects.Sync(p)
	ch, _ := m.C.Effects.Classify(p, main, task.SHA)
	if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": task.SHA, "diff.patch": ch.Diff}}); err != nil {
		t.Fatal(err)
	}
	if err := m.C.Effects.RecordVerdict(core.Verdict{Project: p.Name, SHA: task.SHA, Run: id, Verdict: "land"}); err != nil {
		t.Fatal(err)
	}
}

// A change to a path on main's security list is held, with one line on the
// PR, until a perms or architect read says land at its head, even after the
// Hive's own reviewer landed it and the forge's checks pass; a head that
// moves to a new diff waits again.
func TestSecurityListHolds(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass})
	for i := 0; i < 3; i++ {
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "security read") || !strings.Contains(task.Reason, "secure/box.go") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q, PR %s", task.Stage, task.Reason, prState(t, f, task.PR.Number))
	}
	if n := strings.Count(strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"), "waiting on a security read"); n != 1 {
		t.Fatalf("%d lines saying it waits on a security read, want 1", n)
	}
	// The Hive's own merge step refuses it too.
	main, _ := m.C.Effects.Sync(p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main}); err == nil || !strings.Contains(err.Error(), "security read") {
		t.Fatalf("Effects.Merge took a listed path unread: %v", err)
	}
	first := task.SHA
	if _, err := m.RecordRead("t1", first, "perms", "land", ""); err != nil {
		t.Fatal(err)
	}

	// The worker pushes a new commit to the listed file: the read was of the
	// old diff, so the new head waits again.
	r, _ := m.C.Runs.Get("w1")
	co := r.Checkout
	os.WriteFile(filepath.Join(co, "secure/box.go"), []byte("package secure\n\n// more\n"), 0o644)
	must(t, co, "add", "-A")
	must(t, co, "commit", "-q", "-m", "more")
	sha, err := m.C.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	m.C.Effects.Push(p, "hive/t1", sha)
	task.SHA, task.Stage = sha, StageTesting
	reviewerLand(t, m, task, p, "r2")
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, sha, core.CheckRun{Name: "test", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "security read") || !strings.Contains(task.Reason, core.Short(sha)) {
		t.Fatalf("a moved head merged on the old head's read: stage %q, reason %q", task.Stage, task.Reason)
	}
	if _, err := m.RecordRead("t1", sha, "architect", "land", ""); err != nil {
		t.Fatal(err)
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q after a land at the head", task.Stage, task.Reason)
	}
}

// Only a land at the head lets a listed change through after its checks
// pass: a read recorded while a check still runs waits for the check.
func TestSecurityListThenChecks(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	m.RecordRead("t1", task.SHA, "perms", "land", "")
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPending})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "forge's checks") {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A change to paths not on the list merges on the Hive's own read.
func TestSecurityListUnlistedPasses(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"plain/x.go": "package plain\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// With checks required on main's list, a head with none, or with a required
// one skipped, is held until each has succeeded.
func TestRequiredChecksHold(t *testing.T) {
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nrequired_checks = [\"test (ubuntu-latest)\", \"sandboxed-e2e\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "sandboxed-e2e") || !strings.Contains(task.Reason, "test (ubuntu-latest)") {
		t.Fatalf("a head with no checks: stage %q, reason %q", task.Stage, task.Reason)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckPass}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckSkipped})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "sandboxed-e2e") || strings.Contains(task.Reason, "ubuntu") {
		t.Fatalf("a skipped required check: stage %q, reason %q", task.Stage, task.Reason)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckPass}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, core.CheckRun{Name: "lint", State: core.CheckSkipped})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q once each required check succeeded", task.Stage, task.Reason)
	}
}
