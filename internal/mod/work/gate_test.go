package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

func must(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := core.Git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pushMain commits files onto the forge's main (a file set to "" is removed).
func pushMain(t *testing.T, f *core.Local, files map[string]string) {
	t.Helper()
	tmp := t.TempDir()
	must(t, "", "clone", "-q", f.RepoPath("me/p"), tmp)
	for name, body := range files {
		if body == "" {
			os.Remove(filepath.Join(tmp, name))
		} else {
			os.MkdirAll(filepath.Dir(filepath.Join(tmp, name)), 0o755)
			os.WriteFile(filepath.Join(tmp, name), []byte(body), 0o644)
		}
	}
	must(t, tmp, "add", "-A")
	must(t, tmp, "commit", "-q", "-m", "main")
	must(t, tmp, "push", "-q", "origin", "HEAD:main")
}

// gateRig is a project on bare runs whose tests fail while BROKEN exists,
// with BROKEN on main when mainRed, and a task in testing whose worker made
// one commit on main with the given files ("" removes one).
func gateRig(t *testing.T, mainRed bool, files map[string]string) (*Module, *Task, core.Project, *core.Local) {
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	ws.SaveConfig(cfg)
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	forge.EnsureRepo("me/p", "")
	seed := map[string]string{".hive/project.json": `{"test": "test ! -f BROKEN"}`, "README.md": "# p\n"}
	if mainRed {
		seed["BROKEN"] = "x"
	}
	pushMain(t, forge, seed)
	p, err := c.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	if err != nil {
		t.Fatal(err)
	}
	main, err := c.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	co, err := c.Effects.NewCheckout(p, "w1", main)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if body == "" {
			os.Remove(filepath.Join(co, name))
		} else {
			os.WriteFile(filepath.Join(co, name), []byte(body), 0o644)
		}
	}
	must(t, co, "add", "-A")
	must(t, co, "commit", "-q", "-m", "work")
	r, err := c.Runs.Start(core.RunSpec{ID: "w1", Role: "worker", Project: p.Name, Checkout: co, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Effects.Push(p, "hive/t1", sha); err != nil {
		t.Fatal(err)
	}
	pr, _ := forge.CreatePR(p.Repo, "hive/t1", "main", "work", "")
	a := &agents.Agents{C: c}
	m := New(c, a, chat.New(c, a))
	task := &Task{ID: "t1", Project: "p", Title: "work", Kind: "work", Stage: StageTesting, Worker: "w1", Branch: "hive/t1", SHA: sha, PR: pr}
	return m, task, p, forge
}

func alerts(m *Module, kind string) (n int) {
	m.C.J.Replay(func(e core.Event) {
		var a struct{ Kind string }
		if e.Kind == "alert.raised" && e.Decode(&a) == nil && a.Kind == kind {
			n++
		}
	})
	return n
}

// A failure main has too doesn't go back to the worker: the change waits,
// main's failure is raised once however often the gate looks, and when main
// goes green the change is brought up to date and tested again.
func TestGateFailsOnBoth(t *testing.T) {
	m, task, p, forge := gateRig(t, true, map[string]string{"feature.txt": "y"})
	for i := 0; i < 3; i++ {
		if err := m.test(task, p); err != nil {
			t.Fatal(err)
		}
	}
	if task.Stage != StageTesting || task.Round != 0 || !strings.HasPrefix(task.Reason, "waiting on main") {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
	if n := alerts(m, "main-red"); n != 1 {
		t.Fatalf("%d main-red alerts, want 1", n)
	}
	if main, _ := m.C.Effects.Sync(p); !func() bool { _, ok := m.C.Effects.GetCheck(p.Name, main, "test"); return ok }() {
		t.Fatal("main's result isn't kept, so it would be measured again")
	}
	old := task.SHA
	pushMain(t, forge, map[string]string{"BROKEN": ""})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageTesting || task.SHA == old || task.Reason != "" || task.Round != 0 {
		t.Fatalf("after main went green: stage %q, sha %s (was %s), reason %q, round %d", task.Stage, task.SHA, old, task.Reason, task.Round)
	}
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage == StageTesting || task.Stage == StageWorking {
		t.Fatalf("the caught-up change should pass the gate, stage %q", task.Stage)
	}
}

// A failure only the branch has still goes back to the worker.
func TestGateFailsOnBranchOnly(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"BROKEN": "x"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageWorking || task.Round != 1 || !strings.Contains(task.Findings, "The tests failed on your commit") {
		t.Fatalf("stage %q, round %d, findings %q", task.Stage, task.Round, task.Findings)
	}
	if n := alerts(m, "main-red"); n != 0 {
		t.Fatalf("%d main-red alerts on a green main", n)
	}
}

// A change that passes while main fails goes on, here because it fixes main.
func TestGateFailsOnMainOnly(t *testing.T) {
	m, task, p, _ := gateRig(t, true, map[string]string{"BROKEN": ""})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage == StageTesting || task.Stage == StageWorking || task.Round != 0 {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

// Main's failure is raised once per main commit, even when main moves away
// and back (A -> B -> A) and no task still names A.
func TestGateMainRedOncePerCommit(t *testing.T) {
	m, task, p, forge := gateRig(t, true, map[string]string{"feature.txt": "y"})
	a, _ := m.C.Effects.Sync(p)
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	pushMain(t, forge, map[string]string{"other.txt": "z"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	must(t, forge.RepoPath("me/p"), "update-ref", "refs/heads/main", a)
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Reason, core.Short(a)) {
		t.Fatalf("main wasn't reset to A: reason %q", task.Reason)
	}
	if n := alerts(m, "main-red"); n != 2 {
		t.Fatalf("%d main-red alerts for A -> B -> A, want 2", n)
	}
}

// With no sandbox, main isn't measured (as red) and nothing is raised.
func TestGateMainNeedsSandbox(t *testing.T) {
	m, task, p, _ := gateRig(t, true, map[string]string{"feature.txt": "y"})
	m.C.Effects.RunCheck(p, task.SHA, "test", "test ! -f BROKEN")
	cfg := m.C.WS.LoadConfig()
	cfg.BareAccepted = ""
	m.C.WS.SaveConfig(cfg)
	if err := m.test(task, p); err == nil {
		t.Fatal("the gate ran with no sandbox")
	}
	main, _ := m.C.Effects.Sync(p)
	if _, ok := m.C.Effects.GetCheck(p.Name, main, "test"); ok || alerts(m, "main-red") != 0 || task.Reason != "" {
		t.Fatalf("main measured without a sandbox: reason %q", task.Reason)
	}
}

// A waiting change that conflicts once main goes green is sent back without
// still saying it waits on main.
func TestGateWaitThenConflict(t *testing.T) {
	m, task, p, forge := gateRig(t, true, map[string]string{"README.md": "branch\n"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	pushMain(t, forge, map[string]string{"BROKEN": "", "README.md": "main\n"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageWorking || task.Reason != "" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}
