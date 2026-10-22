package work

import (
	"errors"
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
