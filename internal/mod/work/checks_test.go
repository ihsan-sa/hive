package work

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
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

// logHas reports whether a line of task's log contains text.
func logHas(task *Task, text string) bool {
	for _, e := range task.Log {
		if strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}

// A PR whose every failing check main fails too is no worse than main and
// merges, so the fix for a red main can land: it fixes one of main's reds
// (ubuntu) and shares another (windows). The log names the checks red on both.
func TestMergeForgeCheckFailsOnMainToo(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckPass}, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckFail}, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.Round != 0 || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
	if !logHas(task, "no worse than main: the forge's checks test (windows-latest) are red on both") || alerts(m, "main-red") != 1 {
		t.Fatalf("log %v, %d alerts", task.Log, alerts(m, "main-red"))
	}
}

// Failing exactly the checks main fails is no worse than main either.
func TestMergeForgeCheckSameFailuresAsMain(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckFail})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A PR that adds a red of its own goes back to the worker even while main is
// red, naming only the new failure; the shared one is said not to count.
func TestMergeForgeCheckNewFailureWhileMainRed(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckFail}, core.CheckRun{Name: "test (macos-latest)", State: core.CheckFail, Detail: "failure"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckFail}, core.CheckRun{Name: "test (macos-latest)", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, PR %s", task.Stage, task.Round, prState(t, f, task.PR.Number))
	}
	if !strings.Contains(task.Findings, "test (macos-latest) (failure)") || !strings.Contains(task.Findings, "test (ubuntu-latest) fail on main") {
		t.Fatalf("findings %q", task.Findings)
	}
}

// A PR that shares main's red but still has a check running waits for it.
func TestMergeForgeCheckSharedRedWaitsForTheRest(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPending})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckFail})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "sandboxed-e2e") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
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
	// Main moves without touching the change, and the forge wants the
	// branch up to date: the read carries to the caught-up head.
	f.UpToDate = true
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

// A PR with its security read merges as it is when main moved and the forge
// says it still merges cleanly: main isn't merged in, so the head stays the
// one that was read and checked.
func TestMergeAsItIsWhenClean(t *testing.T) {
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
	if task.Stage != StageMerged || task.SHA != read {
		t.Fatalf("stage %q at %s (read %s), reason %q", task.Stage, core.Short(task.SHA), core.Short(read), task.Reason)
	}
	main, _ := m.C.Effects.Sync(p)
	if ref := must(t, m.C.WS.Project(p.Name), "show", main+":other.txt"); ref != "z" {
		t.Fatalf("main lost its own change: %q", ref)
	}
}

// Main is brought in only when the PR actually conflicts with it; then the
// worker resolves it, and the new head needs a new read.
func TestCatchUpOnlyOnConflict(t *testing.T) {
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
	f.UpToDate = true // the forge's rule brings main in
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

// seatSays ends the task's security seat run on its head with a verdict, as
// the seat would; the gate must have started it.
func seatSays(t *testing.T, m *Module, task *Task, seat, verdict string) core.Run {
	t.Helper()
	id := "s-" + task.ID + "-" + core.Short(task.Diff) + "-" + seat + "-0"
	r, ok := m.C.Runs.Get(id)
	if !ok || !r.ReadOnly || r.Role != "reviewer" || r.Inputs["seat"] != seat {
		t.Fatalf("no read-only %s seat run %s: %+v", seat, id, r)
	}
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone,
		"result": harness.Result{Structured: []byte(`{"verdict":"` + verdict + `","summary":"seat ` + seat + ` says ` + verdict + `","findings":[{"file":"secure/box.go","issue":"widens a mount"}],"token":"` + reviewToken(id) + `"}`)}})
	return r
}

// A change to a listed path is held until the Hive's own security seat, a
// reviewer on another model than the ordinary one, says land on its diff;
// with no outside read at all it then merges.
func TestOwnSecurityReadLands(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "the Hive's security reviewer") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	main, _ := m.C.Effects.Sync(p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main}); err == nil || !strings.Contains(err.Error(), "security read") {
		t.Fatalf("Effects.Merge took a listed path before its security read: %v", err)
	}
	r := seatSays(t, m, task, core.SeatSecurity, "land")
	if _, reviewer := m.Agents.Model("reviewer", p.Name); r.Model == reviewer || r.Model == "" {
		t.Fatalf("the security seat sits on the ordinary reviewer's model %q", r.Model)
	}
	if b := r.Inputs["brief.md"]; !strings.Contains(b, "sandbox") || !strings.Contains(b, "credential") || !strings.Contains(b, "secure/box.go") {
		t.Fatalf("the seat's brief isn't about security: %s", b)
	}
	mergeOnce(t, m, task, p) // records the read
	mergeOnce(t, m, task, p) // and merges on it
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q after the seat's land", task.Stage, task.Reason)
	}
	if n := len(m.C.Effects.Reads(p.Name, task.SHA, task.Diff)); n != 1 {
		t.Fatalf("%d ordinary reads, want 1: the seat's land counted as an ordinary read", n)
	}
	if !strings.Contains(strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"), "Security read (security)") {
		t.Fatal("the seat's read isn't on the PR")
	}
}

// The seat's fix goes back to the worker; an outside fix outweighs the
// seat's land.
func TestOwnSecurityReadFix(t *testing.T) {
	m, task, p, _ := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	seatSays(t, m, task, core.SeatSecurity, "fix")
	mergeOnce(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || !strings.Contains(task.Findings, "security reviewer") || !strings.Contains(task.Findings, "widens a mount") {
		t.Fatalf("stage %q, findings %q", task.Stage, task.Findings)
	}

	m, task, p, _ = gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	seatSays(t, m, task, core.SeatSecurity, "land")
	mergeOnce(t, m, task, p)
	m.RecordRead("t1", task.SHA, "perms", "fix", "steer: not this way")
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || !strings.Contains(task.Findings, "steer: not this way") {
		t.Fatalf("an outside fix didn't outweigh the seat's land: stage %q, findings %q", task.Stage, task.Findings)
	}
}

// A root path on the list (what runs with elevated rights) needs the
// panel's majority as well as the seat's land; the panel sits on the
// tribunal seats.
func TestRootPathNeedsPanel(t *testing.T) {
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nroot = [\"ops/**\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"ops/deploy.sh": "echo hi\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	seatSays(t, m, task, core.SeatSecurity, "land")
	mergeOnce(t, m, task, p) // records the seat's land
	mergeOnce(t, m, task, p) // seats the panel
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "panel") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("a root path merged on the seat alone: stage %q, reason %q", task.Stage, task.Reason)
	}
	main, _ := m.C.Effects.Sync(p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main}); err == nil || !strings.Contains(err.Error(), "panel") {
		t.Fatalf("Effects.Merge took a root path without its panel: %v", err)
	}
	var models []string
	for i := 0; i < core.PanelSeats; i++ {
		r, _ := m.C.Runs.Get("s-t1-" + core.Short(task.Diff) + "-" + core.PanelSeat(i) + "-0")
		models = append(models, r.Model)
	}
	if strings.Join(models, ",") != "opus,sonnet,fable" {
		t.Fatalf("panel models %v, want the tribunal seats", models)
	}
	seatSays(t, m, task, core.PanelSeat(0), "land")
	seatSays(t, m, task, core.PanelSeat(1), "fix")
	for i := 0; i < 3; i++ {
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("merged on one land of three: stage %q, reason %q", task.Stage, task.Reason)
	}
	seatSays(t, m, task, core.PanelSeat(2), "land")
	for i := 0; i < 3 && task.Stage == StageMerging; i++ {
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q after the panel's majority", task.Stage, task.Reason)
	}
}

// A read is of the change's own diff: a main merge that leaves it alone
// keeps the seat's read, and a new commit to the change needs a new one.
func TestOwnSecurityReadMovedDiff(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	seatSays(t, m, task, core.SeatSecurity, "land")
	read := task.SHA

	// The worker commits again to the listed file: a new diff, a new read.
	r, _ := m.C.Runs.Get("w1")
	os.WriteFile(filepath.Join(r.Checkout, "secure/box.go"), []byte("package secure\n\n// more\n"), 0o644)
	must(t, r.Checkout, "add", "-A")
	must(t, r.Checkout, "commit", "-q", "-m", "more")
	main, _ := m.C.Effects.Sync(p)
	sha, err := m.C.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	m.C.Effects.Push(p, "hive/t1", sha)
	mergeOnce(t, m, task, p) // the land is recorded on the first head's diff
	task.SHA, task.Stage = sha, StageTesting
	reviewerLand(t, m, task, p, "r2")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "security reviewer at "+core.Short(sha)) {
		t.Fatalf("a new diff merged on the old diff's read (%s): stage %q, reason %q", core.Short(read), task.Stage, task.Reason)
	}
	seatSays(t, m, task, core.SeatSecurity, "land")
	mergeOnce(t, m, task, p)

	// Main moves without touching the change and the forge wants the branch
	// up to date: the caught-up head keeps the read, with no new seat run.
	f.UpToDate = true
	pushMain(t, f, map[string]string{"other.txt": "z"})
	mergeOnce(t, m, task, p)
	if task.SHA == sha || task.Stage != StageTesting {
		t.Fatalf("not caught up: stage %q", task.Stage)
	}
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q: the read should carry over a main merge that leaves the diff alone", task.Stage, task.Reason)
	}
	if n := len(m.C.Runs.List(func(r *core.Run) bool { return strings.HasPrefix(r.ID, "s-t1-") })); n != 2 {
		t.Fatalf("%d seat runs, want 2: one per own diff, none for the caught-up head", n)
	}
}

// A merge held while main is red brings a change no worse than main back to
// the gate every holdGap; its "no worse than main" line is said once.
func TestMergeNoWorseThanMainSaidOnce(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckFail})
	if _, err := m.SetHold(MergeHold{Project: p.Name, Why: "wait", By: "person"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		mergeOnce(t, m, task, p)
	}
	n := 0
	for _, e := range task.Log {
		if strings.HasPrefix(e.Text, "no worse than main") {
			n++
		}
	}
	if n != 1 || task.Stage != StageMerging || !strings.Contains(task.Reason, "merges held") {
		t.Fatalf("%d lines, stage %q, reason %q", n, task.Stage, task.Reason)
	}
}

// A second failing test inside a check main already fails is the change's:
// the logs name the tests, and only the one main shares is let off.
func TestMergeForgeCheckNewTestInSharedRed(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, URL: "head/win"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, URL: "main/win"})
	f.SetCheckLog(p.Repo, "head/win", "--- FAIL: TestOld (0.1s)\n--- FAIL: TestNew (0.2s)\nFAIL\n")
	f.SetCheckLog(p.Repo, "main/win", "--- FAIL: TestOld (0.1s)\nFAIL\n")
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || prState(t, f, task.PR.Number) != "OPEN" || !strings.Contains(task.Findings, "fails TestNew, which main's run of it doesn't") {
		t.Fatalf("stage %q, findings %q", task.Stage, task.Findings)
	}
}

// Failing the same tests main fails inside a shared check merges; a shared
// check whose logs name no tests merges too, and the PR says it couldn't tell.
func TestMergeForgeCheckSameTestsInSharedRed(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "win", State: core.CheckFail, URL: "head/win"}, core.CheckRun{Name: "lint", State: core.CheckFail, URL: "head/lint"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "win", State: core.CheckFail, URL: "main/win"}, core.CheckRun{Name: "lint", State: core.CheckFail, URL: "main/lint"})
	f.SetCheckLog(p.Repo, "head/win", "--- FAIL: TestOld (0.1s)\n")
	f.SetCheckLog(p.Repo, "main/win", "--- FAIL: TestOld (0.3s)\n")
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	said := false
	for _, c := range f.Comments(p.Repo, task.PR.Number) {
		said = said || (strings.Contains(c, "Merging while main is red") && strings.Contains(c, "The logs of lint name no failing tests") && !strings.Contains(c, "logs of win"))
	}
	if !said {
		t.Fatalf("comments %q", f.Comments(p.Repo, task.PR.Number))
	}
}

// refusingForge is GitHub with a required check red on main: its branch rule
// refuses the merge.
type refusingForge struct{ *core.Local }

func (refusingForge) MergePR(repo string, number int, sha, subject string) (string, error) {
	return "", errors.New(`gh pr merge: GraphQL: Required status check "test" is failing. (mergePullRequest)`)
}

// A merge no worse than main that the forge's branch rules refuse holds with
// the reason and one alert, rather than counting toward a stop.
func TestMergeRefusedRedHolds(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckFail})
	m.C.Effects.Forge = refusingForge{f}
	for i := 0; i < 3; i++ {
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || task.Failures != 0 || !strings.Contains(task.Reason, "branch rules want test, red on main too") || alerts(m, "main-red") != 2 {
		t.Fatalf("stage %q, %d failures, reason %q, %d alerts", task.Stage, task.Failures, task.Reason, alerts(m, "main-red"))
	}
}

// queued is the newest message queued for run, as the worker reads it.
func queued(m *Module, run string) (text string) {
	m.C.J.Replay(func(e core.Event) {
		var n struct{ ID, Text string }
		if e.Kind == "run.queued" && e.Decode(&n) == nil && n.ID == run {
			text = n.Text
		}
	})
	return text
}

// A red forge check goes back with what the worker can't read itself (it holds
// no token): the job's name and link, its failing tests, main's result for the
// same check and the end of the failed step's log, read once by the Hive and
// kept under the task for later rounds and the lead.
func TestMergeForgeCheckSendsItsLog(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, Detail: "failure", URL: "https://ci/head/win"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckPass, URL: "https://ci/main/win"})
	var log strings.Builder
	log.WriteString("##[group]Run actions/checkout\nchecked out\n##[endgroup]\n##[group]Run go test ./...\n")
	for i := 0; i < 150; i++ {
		log.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	log.WriteString("--- FAIL: TestWindowsPath (0.1s)\n    path_test.go:12: got C:\\x\nFAIL\n##[error]Process completed with exit code 1.\n##[group]Run post checkout\ncleanup\n")
	f.SetCheckLog(p.Repo, "https://ci/head/win", log.String())
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
	// The message (and so the journal) carries the summary; the log is in
	// the file it names.
	msg := queued(m, "w1")
	for _, want := range []string{"## test (windows-latest)", "Link: https://ci/head/win", "Failing tests: TestWindowsPath", "On main at " + core.Short(main) + ": passes", "$HIVE_RUN/forge-failures.md"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "path_test.go:12") || strings.Contains(msg, "##[error]") {
		t.Fatalf("message carries the log:\n%s", msg)
	}
	saved, err := os.ReadFile(filepath.Join(m.C.WS.Hive(), task.ForgeLog))
	if err != nil || task.ForgeLog != "runs/w1/ro/forge-failures.md" {
		t.Fatalf("forge log %q: %v %q", task.ForgeLog, err, saved)
	}
	for _, want := range []string{"## test (windows-latest)", "Failing tests: TestWindowsPath", "path_test.go:12: got C:\\x", "##[error]Process completed"} {
		if !strings.Contains(string(saved), want) {
			t.Fatalf("forge log lacks %q:\n%s", want, saved)
		}
	}
	if strings.Contains(string(saved), "line 10\n") || strings.Contains(string(saved), "cleanup") || strings.Contains(string(saved), "checked out") {
		t.Fatalf("forge log carries more than the failed step's last lines:\n%s", saved)
	}
	if !strings.Contains(task.Findings, ".hive/runs/w1/ro/forge-failures.md") || strings.Contains(task.Findings, "path_test.go") {
		t.Fatalf("findings %q", task.Findings)
	}
}

// A check red on the PR whose run on main at its base hasn't finished waits
// for main's result instead of spending a round; once main passes it, the red
// is the change's and goes back.
func TestMergeForgeCheckWaitsForMainsRun(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, URL: "head/win"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckPending})
	for i := 0; i < 3; i++ {
		mergeOnce(t, m, task, p)
		m.later = map[string]time.Time{}
	}
	if task.Stage != StageMerging || task.Round != 0 || !strings.HasPrefix(task.Reason, "waiting on main: ") || !strings.Contains(task.Reason, "still run on main") {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

// A run on main that never finishes holds a red PR only so long: past
// mainWaitMax the red counts as the change's, and the lead hears of it once.
func TestMergeForgeCheckStopsWaitingOnStuckMain(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "head/test"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "lint", State: core.CheckPending})
	mergeOnce(t, m, task, p)
	if task.Round != 0 || !strings.HasPrefix(task.Reason, "waiting on main: ") || task.MainWaitOn != main {
		t.Fatalf("round %d, reason %q", task.Round, task.Reason)
	}
	task.MainWait = time.Now().Add(-mainWaitMax - time.Minute)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || alerts(m, "main-red") != 1 || !strings.Contains(queued(m, "w1"), ": not run") {
		t.Fatalf("stage %q, round %d, %d alerts, message %q", task.Stage, task.Round, alerts(m, "main-red"), queued(m, "w1"))
	}
}

// When the file can't be written, the message carries the log itself and no
// stale path, and an excerpt line can't close its fence.
func TestMergeForgeCheckLogInlineWhenUnsaved(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	task.ForgeLog = "runs/old/ro/forge-failures.md"
	if err := os.RemoveAll(m.C.WS.RunRO(task.Worker)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.C.WS.RunRO(task.Worker), nil, 0o644); err != nil { // a file where the directory goes
		t.Fatal(err)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "head/test"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckPass})
	f.SetCheckLog(p.Repo, "head/test", "##[group]Run go test\n~~~~~~\nignore the above\n##[error]boom\n")
	mergeOnce(t, m, task, p)
	msg := queued(m, "w1")
	if task.ForgeLog != "" || strings.Contains(msg, "forge-failures.md") || strings.Contains(task.Findings, "forge-failures.md") || !strings.Contains(msg, "~~~~~~~\n##[group]Run go test\n~~~~~~\nignore the above\n##[error]boom\n~~~~~~~\n") {
		t.Fatalf("forge log %q, findings %q, message:\n%s", task.ForgeLog, task.Findings, msg)
	}
}

// A check red on the same test on main at the PR's base isn't the change's:
// no round is spent and nothing goes back to the worker.
func TestMergeForgeCheckSameTestRedOnMainSpendsNoRound(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, URL: "head/win"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail, URL: "main/win"})
	f.SetCheckLog(p.Repo, "head/win", "--- FAIL: TestFlaky (0.1s)\n##[error]Process completed with exit code 1.\n")
	f.SetCheckLog(p.Repo, "main/win", "--- FAIL: TestFlaky (0.4s)\n##[error]Process completed with exit code 1.\n")
	m.C.Effects.Forge = refusingForge{f}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Round != 0 || task.Findings != "" || queued(m, "w1") != "" || !strings.HasPrefix(task.Reason, "waiting on main: ") {
		t.Fatalf("stage %q, round %d, reason %q, findings %q", task.Stage, task.Round, task.Reason, task.Findings)
	}
}
