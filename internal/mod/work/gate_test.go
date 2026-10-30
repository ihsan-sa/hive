package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
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
	commitTo(t, f.RepoPath("me/p"), "main", "main", "main", files)
}

// commitTo commits files (a file set to "" is removed) on top of from (none
// if it doesn't exist yet) as ref in the bare repo and returns the commit.
// It writes straight into the repo with git fast-import, not through a clone
// and a push: a clone writes a config, refs, logs and an index, some 100 KB a
// call on disk, and the package's tests make hundreds.
func commitTo(t *testing.T, repo, ref, from, msg string, files map[string]string) string {
	t.Helper()
	var in strings.Builder
	fmt.Fprintf(&in, "commit refs/heads/%s\ncommitter Hive <hive@localhost> %d +0000\ndata %d\n%s\n", ref, time.Now().Unix(), len(msg), msg)
	if sha, err := core.Git(repo, "rev-parse", "-q", "--verify", "refs/heads/"+from+"^{commit}"); err == nil {
		fmt.Fprintf(&in, "from %s\n", sha)
	}
	for name, body := range files {
		if body == "" {
			fmt.Fprintf(&in, "D %s\n", name)
		} else {
			fmt.Fprintf(&in, "M 100644 inline %s\ndata %d\n%s\n", name, len(body), body)
		}
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir, cmd.Stdin = repo, strings.NewReader(in.String()+"\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v %s", err, out)
	}
	return must(t, repo, "rev-parse", "refs/heads/"+ref)
}

// gateRig is a project on bare runs whose tests fail while BROKEN exists and
// call the tool hive-gate-tool-xyz while NEEDTOOL exists,
// with BROKEN on main when mainRed, and a task in testing whose worker made
// one commit on main with the given files ("" removes one).
func gateRig(t *testing.T, mainRed bool, files map[string]string) (*Module, *Task, core.Project, *core.Local) {
	return gateRigSeeded(t, mainRed, nil, files)
}

// gateRigSeeded is gateRig with more files on main to start with.
func gateRigSeeded(t *testing.T, mainRed bool, onMain, files map[string]string) (*Module, *Task, core.Project, *core.Local) {
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.TurnMB = 0 // a loaded machine mustn't hold the queue
	ws.SaveConfig(cfg)
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	forge.EnsureRepo("me/p", "", false)
	seed := map[string]string{".hive/project.json": `{"test": "test ! -f BROKEN && { test ! -f NEEDTOOL || hive-gate-tool-xyz; }"}`, "README.md": "# p\n"}
	if mainRed {
		seed["BROKEN"] = "x"
	}
	for name, body := range onMain {
		seed[name] = body
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
			os.MkdirAll(filepath.Dir(filepath.Join(co, name)), 0o755)
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
	a := &agents.Agents{C: c, Mod: modules.New(ws, c.J)}
	m := New(c, a, chat.New(c, a))
	// A merged task's checkouts go off the save's path (ended): they must be
	// gone before the workspace is closed and removed.
	t.Cleanup(m.Wait)
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

// A change that passes while main fails goes on, here because it fixes main.
func TestGateFailsOnMainOnly(t *testing.T) {
	m, task, p, _ := gateRig(t, true, map[string]string{"BROKEN": ""})
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage == StageTesting || task.Stage == StageWorking || task.Round != 0 {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

func readied(m *Module) (n int) {
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "effect.readied" {
			n++
		}
	})
	return n
}

// A draft whose head the Hive's gate cleared is readied with the repo owner's
// token and merged, with no failure counted on the way.
func TestClearedDraftIsReadiedAndMerged(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := forge.SetDraft(p.Repo, task.PR.Number, true); err != nil {
		t.Fatal(err)
	}
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5 && task.Stage == StageMerging; i++ {
		m.settle(task, m.step(task))
	}
	if task.Stage != StageMerged || task.Failures != 0 {
		t.Fatalf("stage %q, failures %d, log %v", task.Stage, task.Failures, task.Log)
	}
	if pr, _ := forge.GetPR(p.Repo, task.PR.Number); pr.Draft || pr.State != "MERGED" || readied(m) != 1 {
		t.Fatalf("draft %v, state %s, readied %d", pr.Draft, pr.State, readied(m))
	}
}

// An update's pause holds a ready merge at the gate, in merging, without a
// failure; once the gate opens (or after the restart) it merges.
func TestPausedMergeWaitsAtTheGate(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	resume, ok := m.PauseMerges(0)
	if !ok {
		t.Fatal("nothing was merging, yet the pause timed out")
	}
	m.settle(task, m.step(task))
	if task.Stage != StageMerging || task.Failures != 0 || task.MergeSHA != "" {
		t.Fatalf("merged through a shut gate: stage %q, failures %d", task.Stage, task.Failures)
	}
	resume()
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	m.settle(task, m.step(task))
	if task.Stage != StageMerged {
		t.Fatalf("stage %q after the gate opened, reason %q", task.Stage, task.Reason)
	}
}

// A merge already running when an update pauses merges finishes and is
// recorded before the pause returns.
func TestPauseWaitsForTheRunningMerge(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	inside, finish := make(chan struct{}), make(chan struct{})
	m.OnMerged = func(Task) { close(inside); <-finish } // runs past the gate, before the merge leaves it
	stepped := make(chan error, 1)
	go func() { stepped <- m.step(task) }()
	<-inside
	paused := make(chan bool, 1)
	go func() { _, ok := m.PauseMerges(10 * time.Second); paused <- ok }()
	select {
	case <-paused:
		t.Fatal("the pause returned while a merge was still running")
	case <-time.After(300 * time.Millisecond):
	}
	close(finish)
	if !<-paused {
		t.Fatal("the pause timed out though the merge ended")
	}
	if err := <-stepped; err != nil || task.Stage != StageMerged {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
	if got, _ := m.Get(task.ID); got.Stage != StageMerged {
		t.Fatalf("the merge wasn't recorded before the pause returned: %q", got.Stage)
	}
}

// A steady stream of merges can't starve an update: the pause shuts the gate
// to new ones and waits only for those running, however many more come.
func TestMergeStreamDoesNotStarveAPause(t *testing.T) {
	m := &Module{}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if m.enterMerge() {
					time.Sleep(20 * time.Millisecond)
					m.leaveMerge()
				} else {
					time.Sleep(time.Millisecond)
				}
			}
		}()
	}
	defer func() { close(stop); wg.Wait() }()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	resume, ok := m.PauseMerges(5 * time.Second)
	if !ok || time.Since(start) > 2*time.Second {
		t.Fatalf("paused %v after %v", ok, time.Since(start))
	}
	if m.enterMerge() {
		t.Fatal("a new merge started through a shut gate")
	}
	resume()
	if !m.enterMerge() {
		t.Fatal("the gate stayed shut after resume")
	}
	m.leaveMerge()
}

// A merge that outlasts the wait is reported, so the update keeps waiting
// for its next look instead of restarting mid-merge.
func TestPauseGivesUpOnALongMerge(t *testing.T) {
	m := &Module{}
	m.enterMerge()
	resume, ok := m.PauseMerges(150 * time.Millisecond)
	resume()
	if ok {
		t.Fatal("the pause says no merge is running")
	}
}

// A pull request someone closed on the forge mid-landing ends its task as the
// person's stop, even when main moved (which used to send it round testing
// and merging forever), and nothing is merged.
func TestClosedPRStopsTheTask(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := testStep(m, task, p); err != nil || task.Stage != StageMerging {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
	forge.ClosePR(p.Repo, task.PR.Number, "not wanted")
	pushMain(t, forge, map[string]string{"other.txt": "z"})
	before, _ := m.C.Effects.Sync(p)
	if err := m.merge(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageStopped || !task.ByPerson || !strings.Contains(task.Reason, "closed on the forge") {
		t.Fatalf("stage %q, by person %v, reason %q", task.Stage, task.ByPerson, task.Reason)
	}
	if after, _ := m.C.Effects.Sync(p); after != before {
		t.Fatalf("main moved from %s to %s after the PR was closed", before, after)
	}
	if _, err := forge.MergePR(p.Repo, task.PR.Number, task.SHA, "x"); err == nil {
		t.Fatal("the local forge merged a closed PR")
	}
}

// A closed PR found while testing stops the task before its tests run.
func TestClosedPRStopsTesting(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	forge.ClosePR(p.Repo, task.PR.Number, "not wanted")
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.C.Effects.GetCheck(p.Name, task.SHA, "test"); ok || task.Stage != StageStopped || !task.ByPerson {
		t.Fatalf("tested %v, stage %q, by person %v", ok, task.Stage, task.ByPerson)
	}
}

// A closed checkpoint PR is dropped without a comment saying it takes the
// worker's later commits.
func TestClosedCheckpointNotTold(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	task.Checkpoint = task.SHA
	forge.ClosePR(p.Repo, task.PR.Number, "not wanted")
	if err := m.merge(task, p); err != nil || task.Checkpoint != "" || task.Stage != StageWorking {
		t.Fatalf("checkpoint %q, stage %q, err %v", task.Checkpoint, task.Stage, err)
	}
	for _, c := range forge.Comments(p.Repo, task.PR.Number) {
		if strings.Contains(c, "later commits") {
			t.Fatalf("told a closed PR it takes later commits: %q", c)
		}
	}
}

// Findings sent back carry main as a bundle, so a fix that needs main's newer
// commits can merge them, even when the reviewer's own words name main.bundle;
// a message from the forge's checks keeps its own.
func TestSendBackBundlesMain(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	pushMain(t, f, map[string]string{"newer.txt": "only on main"})
	main, _ := m.C.Effects.Sync(p)
	if err := m.sendBack(task, p, "The reviewer asked for fixes: use main's newer helper, as main.bundle would show"); err != nil || task.Stage != StageWorking {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
	if msg := queued(m, task.Worker); !strings.Contains(msg, "main.bundle holds main at "+core.Short(main)) {
		t.Fatalf("message:\n%s", msg)
	}
	out := must(t, "", "bundle", "list-heads", filepath.Join(m.C.WS.RunRO(task.Worker), "main.bundle"))
	if !strings.Contains(out, main) {
		t.Fatalf("bundle heads %q, want main %s", out, main)
	}
}
