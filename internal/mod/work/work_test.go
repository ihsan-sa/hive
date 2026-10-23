package work

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// flakyForge fails the first `fail` closes, the way GitHub does on a 503.
type flakyForge struct {
	*core.Local
	fail   int
	closed []int
}

func (f *flakyForge) CloseIssue(repo string, number int) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("HTTP 503")
	}
	f.closed = append(f.closed, number)
	return f.Local.CloseIssue(repo, number)
}

// noCommitRig is a work module with one project and a task whose worker
// finished with no commit.
func noCommitRig(t *testing.T, fail int) (*Module, *flakyForge, *Task, core.Project) {
	dir := t.TempDir()
	forge := &flakyForge{Local: &core.Local{Dir: filepath.Join(dir, "forge")}, fail: fail}
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	p, err := c.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	if err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c}
	m := New(c, a, chat.New(c, a))
	task := &Task{ID: "t1", Project: "p", Title: "restart the box", Kind: "work", Stage: StageLanding, Issue: core.Issue{Number: 7}}
	return m, forge, task, p
}

func logged(t *Task, s string) bool {
	for _, e := range t.Log {
		if strings.Contains(e.Text, s) {
			return true
		}
	}
	return false
}

// With no step summary wired, a worker with no commit blocks the task as it
// did before outside steps existed, instead of panicking.
func TestNoCommitsWithoutSteps(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 0)
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}, Result: "gave up"})
	if task.Stage != StageBlocked || !strings.Contains(task.Reason, "without any commits: gave up") {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	if strings.Contains(task.Reason, "outside the checkout") || len(forge.closed) != 0 {
		t.Fatalf("reason %q, closed %v", task.Reason, forge.closed)
	}
}

// A step still pending when the worker finishes blocks the task and is named
// as pending, not as a failure.
func TestNoCommitsWithPendingStep(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 0)
	m.Steps = func(string) (string, bool) { return "- restart: still pending (tribunal)", false }
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}})
	if task.Stage != StageBlocked || !strings.Contains(task.Reason, "not all done:\n- restart: still pending (tribunal)") {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	if len(forge.closed) != 0 || task.IssueOpen {
		t.Fatalf("closed %v, issue open %v", forge.closed, task.IssueOpen)
	}
}

// When every outside step ran, the task is done and says so on its issue
// first; a close that fails is journaled and the next pass closes it.
func TestNoCommitsDoneRetriesClose(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 1)
	m.Steps = func(string) (string, bool) { return "- restart: ran", true }
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}})
	if task.Stage != StageDone || !task.IssueOpen || !logged(task, "closing the issue failed (will retry): HTTP 503") {
		t.Fatalf("stage %q, issue open %v, log %v", task.Stage, task.IssueOpen, task.Log)
	}
	if c := forge.Comments("me/p", 7); len(c) != 1 || !strings.Contains(c[0], "Done with no commit") {
		t.Fatalf("comments %q", c)
	}
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageDone || task.IssueOpen || len(forge.closed) != 1 || forge.closed[0] != 7 {
		t.Fatalf("stage %q, issue open %v, closed %v", task.Stage, task.IssueOpen, forge.closed)
	}
}

// A close that keeps failing backs off and gives up once, leaving the task done.
func TestCloseIssueGivesUp(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 100)
	task.Stage, task.IssueOpen = StageDone, true
	for i := 0; i < 20 && task.IssueOpen; i++ {
		m.closeIssue(task, p)
	}
	if task.Stage != StageDone || task.IssueOpen || forge.fail != 92 || !logged(task, "gave up closing the issue, left open on the forge: HTTP 503") {
		t.Fatalf("stage %q, issue open %v, attempts %d, log %v", task.Stage, task.IssueOpen, 100-forge.fail, task.Log)
	}
}

// A done task whose project is gone drops its pending close and stays done.
func TestDoneProjectGone(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Stage, task.IssueOpen, task.Project = StageDone, true, "gone"
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageDone || task.IssueOpen || !logged(task, "project gone is gone, so issue #7 is left open") {
		t.Fatalf("stage %q, issue open %v, log %v", task.Stage, task.IssueOpen, task.Log)
	}
}

// A lead with no project of its own must name one even when only one exists,
// since the ask may be a module or about the Hive itself; a project's own
// agents file to their project.
func TestBriefNamesItsProject(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	_, err := m.opBrief(core.Run{ID: "lead", Role: "lead"}, []byte(`{"title":"a cost ledger"}`))
	if err == nil || !strings.Contains(err.Error(), "one of: p") || !strings.Contains(err.Error(), "create_project") {
		t.Fatalf("want a refusal naming the projects and the module path, got %v", err)
	}
	if len(m.List(nil)) != 0 {
		t.Fatal("a refused brief filed a task")
	}
	if _, err := m.opBrief(core.Run{ID: "w", Role: "worker", Project: "p"}, []byte(`{"title":"a follow-up"}`)); err != nil {
		t.Fatal(err)
	}
	if ts := m.List(nil); len(ts) != 1 || ts[0].Project != "p" {
		t.Fatalf("want one task in p, got %+v", ts)
	}
}

// Nothing files a brief into the only project unasked: the ask may be a
// module or about the Hive.
func TestBriefWithoutProjectRefused(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	if _, err := m.File(Brief{Title: "a cost ledger", From: "person"}); err == nil || !strings.Contains(err.Error(), "one of: p") {
		t.Fatalf("want a refusal naming the projects, got %v", err)
	}
}

// A revert the Hive made has no worker, so findings on it stop the task
// cleanly instead of reopening a run that doesn't exist.
func TestRevertFindingsStop(t *testing.T) {
	m, _, _, p := noCommitRig(t, 0)
	task := &Task{ID: "t2", Project: "p", Title: "Revert: x", Kind: "revert", Stage: StageReviewing}
	if err := m.sendBack(task, p, "it reverts too much"); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageStopped || task.Round != 0 || !strings.Contains(task.Reason, "it reverts too much") {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

// briefAt renders a brief for a project whose main holds config.
func briefAt(t *testing.T, config string, task *Task) string {
	m, p := rigAt(t, config)
	return m.renderBrief(task, p, "HEAD")
}

// rigAt is a rig whose project's main holds config.
func rigAt(t *testing.T, config string) (*Module, core.Project) {
	m, _, _, p := noCommitRig(t, 0)
	dir := m.C.WS.Project(p.Name)
	os.MkdirAll(filepath.Join(dir, ".hive"), 0o755)
	os.WriteFile(filepath.Join(dir, ".hive/project.json"), []byte(config), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "seed"}} {
		if out, err := core.Git(dir, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	return m, p
}

// A scored project's worker gets the person's goal as written, with no bar an
// agent set; the Hive's setup brief and an unscored brief keep their done.
func TestScoredBriefCarriesTheGoal(t *testing.T) {
	goal := "The kernel is slow. Make it faster."
	task := func() *Task {
		return &Task{ID: "t1", Project: "p", Title: goal, Done: "score above 3.139", Known: "the kernel is in k.c"}
	}
	b := briefAt(t, `{"test":"true","score":{"command":"cat s","direction":"higher"}}`, task())
	if !strings.Contains(b, "## The ask\n\n"+goal+"\n") || strings.Contains(b, "3.139") || !strings.Contains(b, "as far as you can take it") {
		t.Fatalf("scored brief:\n%s", b)
	}
	setup := task()
	setup.From = "hive"
	if b := briefAt(t, `{"test":"true","score":{"command":"cat s","direction":"higher"}}`, setup); !strings.Contains(b, "score above 3.139") {
		t.Fatalf("the Hive's own setup brief lost its done line:\n%s", b)
	}
	b = briefAt(t, `{"test":"true"}`, task())
	if !strings.Contains(b, "## What done looks like\n\nscore above 3.139\n") {
		t.Fatalf("unscored brief:\n%s", b)
	}
	m, p := rigAt(t, `{"test":"true","score":{"command":"cat s","direction":"higher"}}`)
	if b := m.issueBody(task(), p); strings.Contains(b, "3.139") || !strings.Contains(b, "as far as you can take it") {
		t.Fatalf("scored issue body:\n%s", b)
	}
	m, p = rigAt(t, `{"test":"true"}`)
	if b := m.issueBody(task(), p); !strings.Contains(b, "score above 3.139") {
		t.Fatalf("unscored issue body:\n%s", b)
	}
}
