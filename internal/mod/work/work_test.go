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
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// flakyForge fails the first `fail` closes, the way GitHub does on a 503, and
// keeps the body of each pull request it opens.
type flakyForge struct {
	*core.Local
	fail   int
	down   bool
	users  int
	closed []int
	bodies []string
}

func (f *flakyForge) CreatePR(repo, head, base, title, body string) (core.PR, error) {
	f.bodies = append(f.bodies, body)
	return f.Local.CreatePR(repo, head, base, title, body)
}

func (f *flakyForge) User() (string, error) {
	f.users++
	if f.down {
		return "", errors.New("dial tcp: lookup api.github.com: no such host")
	}
	return f.Local.User()
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

// While GitHub is unreachable a task's gaps grow to a ceiling and it's never
// stopped; the first step that gets through wakes every waiting task at once.
func TestOutageBacksOffAndWakes(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	var gaps []time.Duration
	for range 20 {
		m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
		gaps = append(gaps, time.Until(task.Retry).Round(time.Second))
	}
	why := "can't reach GitHub: no internet connection"
	if task.Stage != StageLanding || task.Failures != 0 || m.Offline() != why || !logged(task, why+"; retrying") {
		t.Fatalf("stage %q, failures %d, offline %q, log %v", task.Stage, task.Failures, m.Offline(), task.Log)
	}
	if gaps[0] != 30*time.Second || gaps[1] != time.Minute || gaps[4] != 8*time.Minute || gaps[5] != 10*time.Minute || gaps[19] != 10*time.Minute {
		t.Fatalf("gaps %v", gaps)
	}
	if m.due(*task) {
		t.Fatal("due inside its gap while offline")
	}
	forge.down = true
	m.settle(&Task{ID: "t2"}, nil) // a step that never touched GitHub
	if m.Offline() != why || m.due(*task) {
		t.Fatalf("offline %q, due %v after a step that proves nothing", m.Offline(), m.due(*task))
	}
	forge.down = false
	m.probe = time.Time{}          // the shared check's gap is over
	m.settle(&Task{ID: "t2"}, nil) // and once GitHub answers
	if m.Offline() != "" || !m.due(*task) {
		t.Fatalf("offline %q, due %v once back", m.Offline(), m.due(*task))
	}
	// A real error after the outage counts from zero and keeps its own gap.
	m.settle(task, errors.New("HTTP 422"))
	if task.Failures != 1 || task.Stage != StageLanding || m.due(*task) {
		t.Fatalf("failures %d, stage %q, due %v", task.Failures, task.Stage, m.due(*task))
	}
}

// Idle tasks during an outage share one GitHub check per backoff step.
func TestOutageChecksOncePerStep(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
	forge.down = true
	for step, gap := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute} {
		for range 50 {
			m.settle(&Task{ID: "idle"}, nil)
		}
		if forge.users != step+1 || m.Offline() == "" {
			t.Fatalf("step %d: %d checks, offline %q", step, forge.users, m.Offline())
		}
		if got := time.Until(m.probe).Round(time.Second); got != gap {
			t.Fatalf("step %d: next check in %v, want %v", step, got, gap)
		}
		m.probe = time.Time{} // the gap passes
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
	a := &agents.Agents{C: c, Mod: modules.New(ws, c.J)}
	return New(c, a, chat.New(c, a)), forge, p
}

// A helper whose run fails to start leaves no task behind, so nothing points
// at a run that doesn't exist and no second worker takes up its brief.
func TestHelperFailedStartLeavesNoTask(t *testing.T) {
	m, _, _ := helperRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.Harness = "no-such-harness"
	m.C.WS.SaveConfig(cfg)
	if _, err := m.Spawn(core.Run{RunSpec: core.RunSpec{ID: "parent", Role: "worker"}}, "worker", "p", "write the helper part", ""); err == nil {
		t.Fatal("the spawn started on a harness that doesn't exist")
	}
	if tasks := m.List(nil); len(tasks) != 0 {
		t.Fatalf("a failed start left tasks %+v", tasks)
	}
}

// A helper worker on a project with nothing on main yet is refused, since no
// task could land its commits; the parent files a brief instead.
func TestHelperOnEmptyProject(t *testing.T) {
	m, forge, _ := helperRig(t)
	if _, err := forge.EnsureRepo("me/e", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.C.Projects.Add(core.Project{Name: "e", Repo: "me/e"}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Spawn(core.Run{RunSpec: core.RunSpec{ID: "parent", Role: "worker"}}, "worker", "e", "start the project", "")
	if err == nil || !strings.Contains(err.Error(), "file a brief") || len(m.List(nil)) != 0 || len(m.C.Runs.List(nil)) != 0 {
		t.Fatalf("err %v, tasks %+v", err, m.List(nil))
	}
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
	if r, ok := m.C.Runs.Get(spec.ID); !ok || r.Parent != "parent" {
		t.Fatalf("the helper's run wasn't started: %+v", r)
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

// A lead with no project of its own must name one even when only one exists,
// since the ask may be a module or about the Hive itself; a sweep files to the
// only project, and a project's own agents file to their project. An isolated
// project it can't reach goes unnamed.
func TestBriefNamesItsProject(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	m.C.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	_, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}, []byte(`{"title":"a cost ledger"}`))
	if err == nil || !strings.Contains(err.Error(), "one of: p)") || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "create_project") {
		t.Fatalf("want a refusal naming the projects and the module path, got %v", err)
	}
	if len(m.List(nil)) != 0 {
		t.Fatal("a refused brief filed a task")
	}
	if _, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "s", Role: "sweep"}}, []byte(`{"title":"a sweep finding"}`)); err != nil {
		t.Fatalf("a sweep with one project to file in was refused: %v", err)
	}
	if _, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "w", Role: "worker", Project: "p"}}, []byte(`{"title":"a follow-up"}`)); err != nil {
		t.Fatal(err)
	}
	if ts := m.List(nil); len(ts) != 2 || ts[0].Project != "p" || ts[1].Project != "p" {
		t.Fatalf("want two tasks in p, got %+v", ts)
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
