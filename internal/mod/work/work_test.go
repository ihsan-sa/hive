package work

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// flakyForge fails the first `fail` closes, the way GitHub does on a 503, and
// keeps the body of each pull request it opens.
type flakyForge struct {
	*core.Local
	fail   int
	closed []int
	bodies []string
}

func (f *flakyForge) CreatePR(repo, head, base, title, body string) (core.PR, error) {
	f.bodies = append(f.bodies, body)
	return f.Local.CreatePR(repo, head, base, title, body)
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
	if task.Stage != StageDone || task.IssueOpen || forge.fail != 92 || !logged(task, "gave up closing the issue: HTTP 503") {
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

// helperRig is a work module on a project with one commit on main, in bare
// sandboxes so a checkout's commits can be extracted.
func helperRig(t *testing.T) (*Module, *flakyForge, core.Project) {
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	ws.SaveConfig(cfg)
	forge := &flakyForge{Local: &core.Local{Dir: filepath.Join(dir, "forge")}}
	if _, err := forge.EnsureRepo("me/p", ""); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(dir, "seed")
	mustGit(t, "", "clone", "-q", forge.RepoPath("me/p"), seed)
	os.WriteFile(filepath.Join(seed, "README.md"), []byte("# p\n"), 0o644)
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-q", "-m", "seed")
	mustGit(t, seed, "push", "-q", "origin", "HEAD:main")
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	p, err := c.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	if err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c}
	return New(c, a, chat.New(c, a)), forge, p
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := core.Git(dir, args...); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// A spawned helper's commits are never left in its checkout: its task pushes
// them to a branch of its own and opens a pull request, with no issue to close.
func TestHelperCommitsGetTheirOwnPR(t *testing.T) {
	m, forge, p := helperRig(t)
	spec, err := m.Spawn(core.Run{RunSpec: core.RunSpec{ID: "parent", Role: "worker"}}, "worker", "p", "write the helper part", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.C.Runs.Start(spec); err != nil {
		t.Fatal(err)
	}
	task, ok := m.Get(spec.Task)
	if !ok || task.Kind != "helper" || task.Worker != spec.ID || task.Stage != StageWorking || task.From != "parent" || task.Branch != "hive/"+task.ID {
		t.Fatalf("spec %+v, task %+v", spec, task)
	}
	os.WriteFile(filepath.Join(spec.Checkout, "helper.txt"), []byte("helped\n"), 0o644)
	mustGit(t, spec.Checkout, "add", "-A")
	mustGit(t, spec.Checkout, "commit", "-q", "-m", "helper work")
	if err := m.land(&task, p); err != nil {
		t.Fatal(err)
	}
	pr, found, err := forge.FindPR("me/p", task.Branch)
	if err != nil || !found || pr.HeadSHA != task.SHA || task.Stage != StageTesting {
		t.Fatalf("pr %+v found %v err %v, task %+v", pr, found, err, task)
	}
	if forge.Issues("me/p") != 0 || len(forge.bodies) != 1 || strings.Contains(forge.bodies[0], "Closes #") {
		t.Fatalf("issues %d, bodies %q", forge.Issues("me/p"), forge.bodies)
	}
}

// A helper with no commits ends its task done, with nothing said on the forge:
// its account already went to the agent that spawned it.
func TestHelperNoCommitsIsDone(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 0)
	task.Kind, task.Issue = "helper", core.Issue{}
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}, Result: "answered"})
	if task.Stage != StageDone || task.IssueOpen || len(forge.closed) != 0 || !logged(task, "the helper made no commits") {
		t.Fatalf("stage %q, issue open %v, closed %v, log %v", task.Stage, task.IssueOpen, forge.closed, task.Log)
	}
}
