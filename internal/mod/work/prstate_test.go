package work

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

var leadRun = core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead", Project: "p"}}

func callOp(m *Module, r core.Run, op, args string) (any, error) {
	return m.C.Runs.Ops().Call(r, op, []byte(args))
}

// The lead drafts a PR the Hive opened: its task waits in merging, counting
// no failure and never stopped; marked ready, it merges at once.
func TestLeadDraftsAndReadiesItsPR(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	toMerging(t, m, task, p)
	if _, err := callOp(m, leadRun, "pr_draft", `{"task":"t1"}`); err == nil {
		t.Fatal("a draft without why")
	}
	if _, err := callOp(m, leadRun, "pr_draft", `{"task":"t1","why":"hold for the person's read"}`); err != nil {
		t.Fatal(err)
	}
	if pr, _ := forge.GetPR(p.Repo, task.PR.Number); !pr.Draft {
		t.Fatal("the PR isn't a draft")
	}
	if c := forge.Comments(p.Repo, task.PR.Number); len(c) == 0 || !strings.Contains(c[len(c)-1], "hold for the person's read") {
		t.Fatalf("no comment saying why: %q", c)
	}
	*task, _ = m.Get("t1")
	for range 8 {
		m.settle(task, m.step(task))
	}
	if task.Stage != StageMerging || task.Failures != 0 || !strings.Contains(task.Reason, "held by the lead: hold for the person's read") {
		t.Fatalf("stage %q, failures %d, reason %q", task.Stage, task.Failures, task.Reason)
	}
	if m.due(*task) {
		t.Fatal("a waiting draft is due at once")
	}
	if _, err := callOp(m, leadRun, "pr_ready", `{"project":"p","pr":"#`+strconv.Itoa(task.PR.Number)+`","why":"read it myself"}`); err != nil {
		t.Fatal(err)
	}
	if c := forge.Comments(p.Repo, task.PR.Number); len(c) == 0 || !strings.Contains(c[len(c)-1], "Marked ready by the Hive's lead: read it myself") || strings.Contains(c[len(c)-1], "didn't make") {
		t.Fatalf("no comment saying why it was readied: %q", c)
	}
	*task, _ = m.Get("t1")
	if !m.due(*task) {
		t.Fatal("a readied PR still waits for the draft's next look")
	}
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageMerged || prState(t, forge, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// The lead closes a PR it no longer wants (a revert, say) with a comment: the
// PR closes, its branch goes, its task ends and its worker stops, with no
// question to the person.
func TestLeadClosesItsPR(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	task.Kind = "revert"
	toMerging(t, m, task, p)
	if _, err := callOp(m, leadRun, "pr_close", `{"task":"t1"}`); err == nil {
		t.Fatal("closed without a comment")
	}
	if _, err := callOp(m, leadRun, "pr_close", `{"task":"t1","comment":"no longer wanted: the fix landed","delete_branch":true}`); err != nil {
		t.Fatal(err)
	}
	if prState(t, forge, task.PR.Number) != "CLOSED" {
		t.Fatal("the PR is still open")
	}
	if c := forge.Comments(p.Repo, task.PR.Number); len(c) == 0 || c[len(c)-1] != "no longer wanted: the fix landed" {
		t.Fatalf("comments %q", c)
	}
	if _, err := core.Git(forge.RepoPath(p.Repo), "rev-parse", "--verify", "-q", "refs/heads/hive/t1"); err == nil {
		t.Fatal("its branch is still there")
	}
	got, _ := m.Get("t1")
	if got.Stage != StageStopped || !strings.HasPrefix(got.Reason, "closed by the lead: ") || got.ByPerson {
		t.Fatalf("stage %q, reason %q, by person %v", got.Stage, got.Reason, got.ByPerson)
	}
	if r, _ := m.C.Runs.Get("w1"); !r.Terminal() && !r.StopAsked {
		t.Fatalf("its worker runs on: %s", r.Status)
	}
	if _, err := callOp(m, leadRun, "pr_ready", `{"task":"t1"}`); err == nil {
		t.Fatal("readied a closed PR")
	}
	// Recheck says the lead closed it, not the person, and leaves it ended.
	if err := m.Recheck("t1", "", "lead"); err == nil || !strings.Contains(err.Error(), "closed by the lead") || strings.Contains(err.Error(), "the person") {
		t.Fatalf("recheck of a closed task: %v", err)
	}
	// Closing again, with the PR already closed and the branch gone, says so.
	res, err := callOp(m, leadRun, "pr_close", `{"task":"t1","comment":"again","delete_branch":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if note := res.(map[string]string)["note"]; !strings.Contains(note, "already closed") || !strings.Contains(note, "branch hive/t1 deleted") {
		t.Fatalf("note %q", note)
	}
}

// Only a plain hive/ branch is deleted, by either forge.
func TestHiveBranch(t *testing.T) {
	for _, b := range []string{"hive/t1", "hive/w-261025-035941-bbfebfa5ce", "hive/revert-x/y"} {
		if err := core.HiveBranch(b); err != nil {
			t.Errorf("%q: %v", b, err)
		}
	}
	for _, b := range []string{"main", "hive/", "hive/../main", "hive/a..b", "hive//x", "hive/x?y", "hive/x.lock", "other/hive/x"} {
		if core.HiveBranch(b) == nil {
			t.Errorf("%q passed", b)
		}
	}
	_, _, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := forge.DeleteBranch(p.Repo, "main"); err == nil {
		t.Fatal("the local forge deleted main")
	}
	if err := forge.DeleteBranch(p.Repo, "hive/never-was"); err != nil {
		t.Fatalf("a branch already gone: %v", err)
	}
}

// A task waiting on the person for a protected path is closed by the lead
// rather than left to them.
func TestLeadClosesAPRWaitingOnThePerson(t *testing.T) {
	m, task, _, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	task.Stage = StagePerson
	m.save(task, "")
	ask := m.C.Person.Ask(core.Ask{Kind: "merge", Ref: "t1@" + task.SHA, Options: []string{"merge", "leave open"}})
	if _, err := callOp(m, leadRun, "pr_close", `{"task":"t1","comment":"superseded"}`); err != nil {
		t.Fatal(err)
	}
	if a, _ := m.C.Person.Get(ask.ID); a.Answer == "" {
		t.Fatal("the person is still asked about a closed PR")
	}
	if got, _ := m.Get("t1"); got.Stage != StageStopped || prState(t, forge, task.PR.Number) != "CLOSED" {
		t.Fatalf("stage %q", got.Stage)
	}
	// The person's answer coming late changes nothing.
	m.onPersonMerge(core.Ask{Ref: "t1@" + task.SHA, Answer: "merge"})
	if got, _ := m.Get("t1"); got.Stage != StageStopped {
		t.Fatalf("stage %q after a late answer", got.Stage)
	}
}

// Only the Hive's own PRs, only in reach, only by the lead; a merged one
// isn't closed.
func TestPROpsOnlyTheHivesOwn(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	m.save(task, "")
	other, _ := forge.CreatePR(p.Repo, "someone-else", "main", "theirs", "")
	if _, err := callOp(m, leadRun, "pr_close", `{"project":"p","pr":`+strconv.Itoa(other.Number)+`,"comment":"x"}`); err == nil || !strings.Contains(err.Error(), "isn't one the Hive opened") {
		t.Fatalf("closed a PR the Hive didn't open: %v", err)
	}
	if _, err := callOp(m, leadRun, "pr_close", `{"task":"t1","pr":`+strconv.Itoa(other.Number)+`,"comment":"x"}`); err == nil {
		t.Fatal("a task id with another task's PR number")
	}
	worker := core.Run{RunSpec: core.RunSpec{ID: "w1", Role: "worker", Project: "p"}}
	if _, err := callOp(m, worker, "pr_close", `{"task":"t1","comment":"x"}`); err == nil {
		t.Fatal("a worker closed a PR")
	}
	m.C.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	walled := core.Run{RunSpec: core.RunSpec{ID: "lead2", Role: "lead", Project: "secret"}}
	if _, err := callOp(m, walled, "pr_draft", `{"task":"t1","why":"x"}`); err == nil {
		t.Fatal("a walled lead drafted another project's PR")
	}
	if pr, _ := forge.GetPR(p.Repo, task.PR.Number); pr.State != "OPEN" || pr.Draft {
		t.Fatalf("the PR changed: %+v", pr)
	}
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if _, err := callOp(m, leadRun, "pr_close", `{"task":"t1","comment":"x"}`); err == nil {
		t.Fatal("closed a merged task's PR")
	}
}

// A task stopped over a draft before drafts waited comes back when the lead
// marks its PR ready; one the person stopped stays theirs.
func TestReadyBringsBackATaskStoppedOverItsDraft(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	forge.SetDraft(p.Repo, task.PR.Number, true)
	m.giveUp(task, gaveUpOnErrors+"gh pr merge: Pull Request is still a draft (mergePullRequest)")
	if _, err := callOp(m, leadRun, "pr_ready", `{"task":"t1"}`); err != nil {
		t.Fatal(err)
	}
	if pr, _ := forge.GetPR(p.Repo, task.PR.Number); pr.Draft {
		t.Fatal("still a draft")
	}
	if c := forge.Comments(p.Repo, task.PR.Number); len(c) == 0 || !strings.Contains(c[len(c)-1], "The lead didn't make this draft") {
		t.Fatalf("readying a draft the lead didn't make says nothing: %q", c)
	}
	if got, _ := m.Get("t1"); got.Stage != StageLanding {
		t.Fatalf("stage %q, reason %q", got.Stage, got.Reason)
	}

	*task, _ = m.Get("t1") // the recheck's save stands over an older copy
	task.Stage, task.Reason, task.ByPerson = StageStopped, "left open: draft for now", true
	m.save(task, "")
	forge.SetDraft(p.Repo, task.PR.Number, true)
	res, err := callOp(m, leadRun, "pr_ready", `{"task":"t1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get("t1"); got.Stage != StageStopped || !strings.Contains(res.(map[string]string)["note"], "the person stopped it") {
		t.Fatalf("stage %q, %v", got.Stage, res)
	}
}
