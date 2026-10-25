package work

import (
	"fmt"
	"os"
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
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0 // a loaded machine mustn't hold the queue
	ws.SaveConfig(cfg)
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	forge.EnsureRepo("me/p", "")
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

// A change whose every failing test main fails too is no worse than main: it
// goes on rather than wait for main to go green, so the fix for a red main
// (a revert, say) can land. Anything more of its own and it waits: another
// failing test, the same test in another package, or a package failing with
// no test named (a timeout) beside main's failure.
func TestGateNoWorseThanRedMainGoesOn(t *testing.T) {
	const red = "echo '--- FAIL: TestClock (0.01s)'\necho 'FAIL\tp/a\t0.01s'\n"
	onMain := map[string]string{".hive/project.json": `{"test": "sh ./t.sh"}`, "t.sh": red + "exit 1\n"}
	m, task, p, _ := gateRigSeeded(t, false, onMain, map[string]string{"feature.txt": "y"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage == StageTesting || task.Stage == StageWorking || task.Round != 0 || strings.HasPrefix(task.Reason, "waiting on main") {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
	for name, more := range map[string]string{
		"another test":          "echo '--- FAIL: TestNew (0.01s)'\necho 'FAIL\tp/b\t0.01s'\n",
		"another package":       "echo '--- FAIL: TestClock (0.01s)'\necho 'FAIL\tp/b\t0.01s'\n",
		"a timeout":             "echo 'panic: test timed out after 2s'\necho 'FAIL\tp/b\t2.01s'\n",
		"no test named":         "echo 'PASS'\necho 'FAIL\tp/b\t0.01s'\n",
		"a panic beside a test": "echo '--- FAIL: TestClock (0.01s)'\necho 'panic: boom'\necho 'FAIL\tp/a\t0.01s'\n",
	} {
		script := red + more + "exit 1\n"
		if name == "a panic beside a test" {
			script = more + "exit 1\n"
		}
		m, task, p, _ := gateRigSeeded(t, false, onMain, map[string]string{"t.sh": script})
		if err := m.test(task, p); err != nil {
			t.Fatal(err)
		}
		if task.Stage != StageTesting || !strings.HasPrefix(task.Reason, "waiting on main") {
			t.Fatalf("%s beside main's failure went on: stage %q, reason %q", name, task.Stage, task.Reason)
		}
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
	// Moving main back relies on Sync's fetch being forced (+refs/heads/*).
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
	if err := m.test(task, p); err != nil {
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

// A draft whose head moved past the commit the Hive read isn't readied: it
// waits without counting failures, the lead hears once, and once its head is
// the commit read again it is readied and merges.
func TestMovedDraftIsNotReadied(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageMerging {
		t.Fatalf("stage %q, log %v", task.Stage, task.Log)
	}
	forge.SetDraft(p.Repo, task.PR.Number, true)
	tmp := t.TempDir()
	must(t, "", "clone", "-q", "-b", "hive/t1", forge.RepoPath(p.Repo), tmp)
	os.WriteFile(filepath.Join(tmp, "unread.txt"), []byte("not read\n"), 0o644)
	must(t, tmp, "add", "-A")
	must(t, tmp, "commit", "-q", "-m", "unread")
	must(t, tmp, "push", "-q", "origin", "HEAD:hive/t1")
	m.settle(task, m.step(task))
	updated := task.Updated
	for range 12 {
		m.settle(task, m.step(task))
	}
	pr, _ := forge.GetPR(p.Repo, task.PR.Number)
	if task.Stage != StageMerging || task.Failures != 0 || !strings.HasPrefix(task.Reason, draftWait) || !pr.Draft || readied(m) != 0 {
		t.Fatalf("stage %q, failures %d, reason %q, draft %v, readied %d", task.Stage, task.Failures, task.Reason, pr.Draft, readied(m))
	}
	if n := strings.Count(fmt.Sprint(task.Log), draftWait); n != 1 || !task.Updated.Equal(updated) {
		t.Fatalf("the wait was logged %d times, saved again %v: %v", n, !task.Updated.Equal(updated), task.Log)
	}
	if m.due(*task) {
		t.Fatal("a waiting draft is looked at again before its next look")
	}
	old := draftTell
	draftTell = 0
	t.Cleanup(func() { draftTell = old })
	m.step(task)
	m.step(task)
	if n := strings.Count(fmt.Sprint(task.Log), "told the lead"); !task.DraftTold || n != 1 {
		t.Fatalf("told %v, %d times", task.DraftTold, n)
	}
	must(t, tmp, "push", "-q", "-f", "origin", task.SHA+":refs/heads/hive/t1")
	for i := 0; i < 5 && task.Stage == StageMerging; i++ {
		m.settle(task, m.step(task))
	}
	if task.Stage != StageMerged || !task.DraftSince.IsZero() || readied(m) != 1 {
		t.Fatalf("stage %q, reason %q, draft since %v, readied %d", task.Stage, task.Reason, task.DraftSince, readied(m))
	}
}

// A draft the lead holds (pr_draft) isn't readied though its head was read:
// it waits, counting no failure, and merges once the lead marks it ready.
func TestLeadHeldDraftIsNotReadied(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	m.save(task, "")
	if _, err := m.MarkDraft(task.ID, "lead", "waiting on #12 first"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(task.ID)
	for range 5 {
		m.settle(&got, m.step(&got))
	}
	pr, _ := forge.GetPR(p.Repo, got.PR.Number)
	if got.Stage != StageMerging || got.Failures != 0 || !pr.Draft || readied(m) != 0 || !strings.Contains(got.Reason, "held by the lead: waiting on #12 first") {
		t.Fatalf("stage %q, failures %d, draft %v, readied %d, reason %q", got.Stage, got.Failures, pr.Draft, readied(m), got.Reason)
	}
	m.save(&got, "")
	if _, err := m.MarkReady(task.ID, "lead", "#12 merged"); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Get(task.ID)
	for i := 0; i < 5 && got.Stage == StageMerging; i++ {
		m.settle(&got, m.step(&got))
	}
	if got.Stage != StageMerged || got.DraftWhy != "" {
		t.Fatalf("stage %q, draft why %q, log %v", got.Stage, got.DraftWhy, got.Log)
	}
}

// A task stopped on GitHub's "still a draft" before the Hive readied its
// drafts comes back once a run and merges through the gate; one stopped for
// anything else stays stopped.
func TestStoppedOnDraftResumes(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	forge.SetDraft(p.Repo, task.PR.Number, true)
	task.Stage, task.Failures = StageStopped, 8
	task.Reason = gaveUpOnErrors + fmt.Sprintf("gh pr merge %d: GraphQL: Pull Request is still a draft (mergePullRequest)", task.PR.Number)
	m.save(task, task.Reason)
	other := &Task{ID: "t2", Project: "p", Title: "other", Kind: "work", Stage: StageStopped, Reason: gaveUpOnErrors + "gh pr merge 9: something else"}
	m.save(other, other.Reason)
	m.Tick()
	m.Wait()
	got, _ := m.Get(task.ID)
	if got.Stage == StageStopped || got.Failures != 0 || !strings.Contains(fmt.Sprint(got.Log), "brought back") {
		t.Fatalf("stage %q, failures %d, log %v", got.Stage, got.Failures, got.Log)
	}
	if o, _ := m.Get("t2"); o.Stage != StageStopped {
		t.Fatalf("a task stopped on another error came back: %q", o.Stage)
	}
	for i := 0; i < 10 && got.Stage != StageMerged; i++ {
		m.settle(&got, m.step(&got))
	}
	if got.Stage != StageMerged || readied(m) != 1 {
		t.Fatalf("stage %q, readied %d, log %v", got.Stage, readied(m), got.Log)
	}
	// Once a run: stopped on the draft again, it stays for the lead's recheck.
	again := &Task{ID: "t3", Project: "p", Title: "again", Kind: "work", Stage: StageStopped, Reason: task.Reason}
	m.save(again, again.Reason)
	m.Tick()
	m.Wait()
	if a, _ := m.Get("t3"); a.Stage != StageStopped {
		t.Fatalf("brought back twice in one run: %q", a.Stage)
	}
}

// An update's pause holds a ready merge at the gate, in merging, without a
// failure; once the gate opens (or after the restart) it merges.
func TestPausedMergeWaitsAtTheGate(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := m.test(task, p); err != nil {
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
	if err := m.test(task, p); err != nil {
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

// A change whose tests can't find their tool isn't failed or sent back: it
// waits, the tool is named once, and with the tool there it goes on.
func TestGateMissingToolCantRun(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"NEEDTOOL": "x"})
	for i := 0; i < 2; i++ {
		if err := m.test(task, p); err != nil {
			t.Fatal(err)
		}
	}
	if task.Stage != StageTesting || task.Round != 0 || !strings.Contains(task.Reason, "can't run: hive-gate-tool-xyz") || !task.Retry.After(time.Now()) {
		t.Fatalf("stage %q, round %d, reason %q, retry %v", task.Stage, task.Round, task.Reason, task.Retry)
	}
	if n := alerts(m, "main-red"); n != 1 {
		t.Fatalf("%d main-red alerts, want 1", n)
	}
	if _, ok := m.C.Effects.GetCheck(p.Name, task.SHA, "test"); ok {
		t.Fatal("a check that couldn't start is kept as the commit's result")
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "hive-gate-tool-xyz"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage == StageTesting || task.Stage == StageWorking || task.Reason != "" {
		t.Fatalf("with the tool there: stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A checkpoint whose tests can't find their tool is dropped with that reason:
// not a failure, nothing sent back, its round unspent, and the tool named once.
// The alert clears once the tests run.
func TestCheckpointMissingToolDropped(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"NEEDTOOL": "x"})
	task.Checkpoint = task.SHA
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Checkpoint != "" || task.Stage != StageWorking || task.Round != 0 {
		t.Fatalf("checkpoint %q, stage %q, round %d", task.Checkpoint, task.Stage, task.Round)
	}
	said := false
	for _, c := range forge.Comments(p.Repo, task.PR.Number) {
		if strings.Contains(c, "failed") {
			t.Fatalf("a checkpoint that couldn't be tested was told it failed: %q", c)
		}
		said = said || strings.Contains(c, "can't run: hive-gate-tool-xyz")
	}
	if !said || alerts(m, "main-red") != 1 {
		t.Fatalf("the PR heard %v, %d main-red alerts; want the tool named once", said, alerts(m, "main-red"))
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "hive-gate-tool-xyz"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, open := m.cantRunOpen[p.Name]
	m.mu.Unlock()
	if open {
		t.Fatal("the can't-run alert stays open once the tests run")
	}
}

// A pull request someone closed on the forge mid-landing ends its task as the
// person's stop, even when main moved (which used to send it round testing
// and merging forever), and nothing is merged.
func TestClosedPRStopsTheTask(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := m.test(task, p); err != nil || task.Stage != StageMerging {
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
	if err := m.Recheck(task.ID, "", "lead"); err == nil {
		t.Fatal("the lead brought back a task the person closed")
	}
	if _, err := forge.MergePR(p.Repo, task.PR.Number, task.SHA, "x"); err == nil {
		t.Fatal("the local forge merged a closed PR")
	}
}

// A closed PR found while testing stops the task before its tests run.
func TestClosedPRStopsTesting(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	forge.ClosePR(p.Repo, task.PR.Number, "not wanted")
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.C.Effects.GetCheck(p.Name, task.SHA, "test"); ok || task.Stage != StageStopped || !task.ByPerson {
		t.Fatalf("tested %v, stage %q, by person %v", ok, task.Stage, task.ByPerson)
	}
}

// A PR closed while its task is read or sent back stops the task there, so
// the worker's next push can't open a new PR over the person's close.
func TestClosedPRStopsReviewAndSendBack(t *testing.T) {
	for _, step := range []string{"review", "sendBack"} {
		m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
		if err := m.test(task, p); err != nil {
			t.Fatal(err)
		}
		forge.ClosePR(p.Repo, task.PR.Number, "not wanted")
		var err error
		if step == "review" {
			task.Stage = StageReviewing
			err = m.review(task, p)
		} else {
			err = m.sendBack(task, p, "fix it")
		}
		if err != nil || task.Stage != StageStopped || !task.ByPerson || task.Reviewer != "" || task.Round != 0 {
			t.Fatalf("%s: stage %q, by person %v, reviewer %q, round %d, err %v", step, task.Stage, task.ByPerson, task.Reviewer, task.Round, err)
		}
	}
}

// A task waiting for the person sees its PR closed on its first look, then
// looks again only after a while.
func TestClosedPRStopsWaitingForPerson(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	task.Stage = StagePerson
	if err := m.step(task); err != nil || task.Stage != StagePerson {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
	forge.ClosePR(p.Repo, task.PR.Number, "not wanted")
	if err := m.step(task); err != nil || task.Stage != StagePerson {
		t.Fatalf("looked again at once: stage %q, err %v", task.Stage, err)
	}
	m.prPolled[task.ID] = time.Now().Add(-11 * time.Minute)
	if err := m.step(task); err != nil || task.Stage != StageStopped || !task.ByPerson {
		t.Fatalf("stage %q, by person %v, err %v", task.Stage, task.ByPerson, err)
	}
}

// A closed checkpoint PR is dropped without a comment saying it takes the
// worker's later commits.
func TestClosedCheckpointNotTold(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := m.test(task, p); err != nil {
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
