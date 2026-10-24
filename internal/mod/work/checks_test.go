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
	read(t, m, task, p, id, "", "land")
}

// read records a reviewer run's verdict on the task's head, an ordinary read
// for kind "" and the security seat's for "security".
func read(t *testing.T, m *Module, task *Task, p core.Project, id, kind, verdict string) {
	t.Helper()
	main, _ := m.C.Effects.Sync(p)
	ch, _ := m.C.Effects.Classify(p, main, task.SHA)
	in := map[string]string{"sha": task.SHA, "diff.patch": ch.Diff}
	if kind != "" {
		in["kind"] = kind
	}
	if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: in}); err != nil {
		t.Fatal(err)
	}
	if err := m.C.Effects.RecordVerdict(core.Verdict{Project: p.Name, SHA: task.SHA, Run: id, Verdict: verdict, Summary: "security says " + verdict}); err != nil {
		t.Fatal(err)
	}
}

// commitMore has the worker commit to file and the task land that head.
func commitMore(t *testing.T, m *Module, task *Task, p core.Project, file, body string) {
	t.Helper()
	r, _ := m.C.Runs.Get("w1")
	base := must(t, r.Checkout, "rev-parse", "HEAD~1") // where the worker started
	os.WriteFile(filepath.Join(r.Checkout, file), []byte(body), 0o644)
	must(t, r.Checkout, "add", "-A")
	must(t, r.Checkout, "commit", "-q", "-m", "more")
	sha, err := m.C.Effects.Extract(p, r, base)
	if err != nil {
		t.Fatal(err)
	}
	m.C.Effects.Push(p, "hive/t1", sha)
	task.SHA, task.Stage = sha, StageTesting
}

// A change to a path on main's security list, once the ordinary reviewer
// landed it, gets a separate security read from the Hive's own seat bound
// to its own diff; nothing outside the Hive is needed (goals.md, Security
// reads), and neither merge step lets it through before that read.
func TestSecuritySeatReadsListedChange(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageReviewing || task.ReadKind != "security" {
		t.Fatalf("stage %q, read kind %q", task.Stage, task.ReadKind)
	}
	main, _ := m.C.Effects.Sync(p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main}); err == nil || !strings.Contains(err.Error(), "security read") {
		t.Fatalf("Effects.Merge took a listed path without its security read: %v", err)
	}
	if err := m.review(task, p); err != nil {
		t.Fatal(err)
	}
	r, ok := m.C.Runs.Get(task.Reviewer)
	if !ok || !strings.HasPrefix(r.ID, "rs-") || r.Inputs["kind"] != "security" || r.Inputs["diff.patch"] == "" {
		t.Fatalf("the security seat wasn't asked: %+v", r.Inputs)
	}
	// Its land is no ordinary read, and the ordinary one is no security read.
	if err := m.C.Effects.RecordVerdict(core.Verdict{Project: p.Name, SHA: task.SHA, Run: r.ID, Verdict: "land"}); err != nil {
		t.Fatal(err)
	}
	if got := m.C.Effects.Reads(p.Name, task.SHA, task.Diff); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("ordinary reads %v", got)
	}
	task.Stage = StageTesting
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// The security read carries over a catch-up of main that leaves the own diff
// alone, and is needed again once the worker changes the listed file.
func TestSecurityReadFollowsTheDiff(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	read(t, m, task, p, "rs1", "security", "land")
	toMerging(t, m, task, p)
	pushMain(t, f, map[string]string{"other.txt": "z"})
	old := task.SHA
	mergeOnce(t, m, task, p)
	if task.SHA == old || task.Stage != StageTesting {
		t.Fatalf("not caught up: stage %q", task.Stage)
	}
	toMerging(t, m, task, p) // neither read is asked again
	main, _ := m.C.Effects.Sync(p)
	if _, err := core.Git(m.C.WS.Project(p.Name), "merge-base", "--is-ancestor", main, task.SHA); err != nil {
		t.Fatal("the head that will merge doesn't hold main")
	}

	commitMore(t, m, task, p, "secure/box.go", "package secure\n\n// more\n")
	reviewerLand(t, m, task, p, "r2")
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageReviewing || task.ReadKind != "security" {
		t.Fatalf("a new own diff should need a new security read: stage %q, kind %q", task.Stage, task.ReadKind)
	}
	read(t, m, task, p, "rs2", "security", "land")
	task.Stage = StageTesting
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p) // the new head predates main: caught up, reads kept
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// An outside read (perms seat, architect) still counts and can still send a
// change back: a land stands in for the seat's read, and a fix sends it back
// even after the seat said land.
func TestOutsideReadCountsAndCanSendBack(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m.save(task, "")
	reviewerLand(t, m, task, p, "r1")
	if _, err := m.RecordRead("#"+strconv.Itoa(task.PR.Number), core.Short(task.SHA), "perms", "land", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RecordRead("t1", task.SHA, "the lead", "land", ""); err == nil {
		t.Fatal("recorded a read from someone not the perms seat or architect")
	}
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}

	m2, task2, p2, _ := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m2.save(task2, "")
	reviewerLand(t, m2, task2, p2, "r1")
	read(t, m2, task2, p2, "rs1", "security", "land")
	toMerging(t, m2, task2, p2)
	if _, err := m2.RecordRead("t1", task2.SHA, "architect", "fix", "token logic nobody read"); err != nil {
		t.Fatal(err)
	}
	mergeOnce(t, m2, task2, p2)
	if task2.Stage != StageWorking || !strings.Contains(task2.Findings, "token logic nobody read") {
		t.Fatalf("stage %q, findings %q", task2.Stage, task2.Findings)
	}
}

// The security seat's fix sends the change back to the worker.
func TestSecuritySeatFixGoesBack(t *testing.T) {
	m, task, p, _ := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	read(t, m, task, p, "rs1", "security", "fix")
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageWorking || !strings.Contains(task.Findings, "security says fix") {
		t.Fatalf("stage %q, findings %q", task.Stage, task.Findings)
	}
}

// A security read doesn't skip a forge check still running.
func TestSecurityListThenChecks(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	read(t, m, task, p, "rs1", "security", "land")
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPending})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "forge's checks") {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A PR that conflicts with main goes back to the worker.
func TestConflictGoesBack(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	read(t, m, task, p, "rs1", "security", "land")
	toMerging(t, m, task, p)
	read0 := task.SHA
	pushMain(t, f, map[string]string{"secure/box.go": "package secure\n\n// main's own\n"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.SHA != read0 || prState(t, f, task.PR.Number) != "OPEN" {
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
