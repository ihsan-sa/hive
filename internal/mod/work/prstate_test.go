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

// Only the Hive's own PRs, only in reach, by any agent but a check seat; a merged one
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
