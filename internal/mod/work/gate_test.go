package work

import (
	"errors"
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
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0 // a loaded machine mustn't hold the queue
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
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

// A merge the gate refuses says what refused it, not the reason of a hold it
// already passed (a security read, say).
func TestMergeRefusalReplacesStaleReason(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"feature.txt": "y"})
	task.Stage, task.Reason = StageMerging, "waiting on a security read from the Hive's security reviewer"
	if err := m.merge(task, p); err == nil {
		t.Fatal("a commit with no test run merged")
	}
	if !strings.HasPrefix(task.Reason, "refusing to merge") || !strings.Contains(task.Reason, "no test run recorded") {
		t.Fatalf("reason %q", task.Reason)
	}
}

// A waiting change that conflicts once main goes green is sent back without
// still saying it waits on main.
func TestGateWaitThenConflict(t *testing.T) {
	m, task, p, forge := gateRig(t, true, map[string]string{"README.md": "branch\n"})
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	pushMain(t, forge, map[string]string{"BROKEN": "", "README.md": "main\n"})
	if err := testStep(m, task, p); err != nil {
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

// A draft whose head moved past the commit the Hive read isn't readied: it
// waits without counting failures, the lead hears once, and once its head is
// the commit read again it is readied and merges.
func TestMovedDraftIsNotReadied(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := testStep(m, task, p); err != nil {
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
	// An update's pause doesn't hold it: it waits for its read before the gate.
	resume, ok := m.PauseMerges(0)
	m.settle(task, m.step(task))
	resume()
	if !ok || m.inMerge != 0 || !strings.HasPrefix(task.Reason, draftWait) {
		t.Fatalf("paused %v, past the gate %d, reason %q", ok, m.inMerge, task.Reason)
	}
	// A merge that reaches the ready after the push isn't a failure: the
	// moved head is ErrHeadMoved, and nothing is readied.
	main, _ := m.C.Effects.Sync(p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main, Task: task.ID, Draft: true}); !errors.Is(err, core.ErrHeadMoved) || readied(m) != 0 {
		t.Fatalf("merge at a moved draft: %v, readied %d", err, readied(m))
	}
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
	if err := testStep(m, task, p); err != nil {
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
// drafts comes back once and merges through the gate; one stopped for
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
	// Once a task: stopped on the draft again, it stays for the lead, across
	// restarts too (the marker is on the task); another task stopped later in
	// the run comes back on its own.
	got.Stage, got.Reason = StageStopped, task.Reason
	m.save(&got, got.Reason)
	again := &Task{ID: "t3", Project: "p", Title: "again", Kind: "work", Stage: StageStopped, Reason: task.Reason}
	m.save(again, again.Reason)
	m.Tick()
	m.Wait()
	if g, _ := m.Get(task.ID); g.Stage != StageStopped || !g.DraftRevived {
		t.Fatalf("brought back twice: %q", g.Stage)
	}
	if a, _ := m.Get("t3"); a.Stage == StageStopped || !a.DraftRevived {
		t.Fatalf("a task stopped later in the run stayed: %q, log %v", a.Stage, a.Log)
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

// A change whose tests can't find their tool isn't failed or sent back: it
// waits, the tool is named once, and with the tool there it goes on.
func TestGateMissingToolCantRun(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"NEEDTOOL": "x"})
	for i := 0; i < 2; i++ {
		if err := testStep(m, task, p); err != nil {
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
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage == StageTesting || task.Stage == StageWorking || task.Reason != "" {
		t.Fatalf("with the tool there: stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A change faking a missing tool (exit 127 with "not found", a new tool each
// time) gets one can't-run comment however often it is tried; after
// CantRunTries it counts as red: stopped, not merged, and the person is asked
// for the tool by name.
func TestFakedCantRunCommentsOnceThenRed(t *testing.T) {
	m, task, p, forge := gateRigSeeded(t, false,
		map[string]string{".hive/project.json": `{"test": "test ! -f FAKE || { echo \"sh: 1: tool$$: not found\"; exit 127; }"}`},
		map[string]string{"FAKE": "x"})
	before, _ := m.C.Effects.Sync(p)
	for i := 1; i < CantRunTries; i++ {
		if err := testStep(m, task, p); err != nil {
			t.Fatal(err)
		}
		if task.Stage != StageTesting {
			t.Fatalf("try %d: stage %q, want it still waiting", i, task.Stage)
		}
	}
	said := 0
	for _, c := range forge.Comments(p.Repo, task.PR.Number) {
		if strings.Contains(c, "can't run") {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("%d can't-run comments on the PR over %d tries, want 1", said, CantRunTries-1)
	}
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageStopped || !strings.Contains(task.Reason, "counts as red") {
		t.Fatalf("after %d tries: stage %q, reason %q", CantRunTries, task.Stage, task.Reason)
	}
	var ask core.Ask
	for _, a := range m.C.Person.Open() {
		if a.Ref == "cant-run:"+task.ID {
			ask = a
		}
	}
	if !strings.Contains(ask.Title, "Install tool") {
		t.Fatalf("the person isn't asked for the tool by name: %+v", ask)
	}
	if after, _ := m.C.Effects.Sync(p); after != before {
		t.Fatal("a change whose tests never ran was merged")
	}
	err := m.Recheck(task.ID, "", "lead")
	if got, _ := m.Get(task.ID); err != nil || got.CantRuns != 0 {
		t.Fatalf("the lead's recheck: %v, count %d, want a fresh count", err, got.CantRuns)
	}
	for _, a := range m.C.Person.Open() {
		if a.Ref == "cant-run:"+task.ID {
			t.Fatalf("the ask stays open once the change is tested again: %+v", a)
		}
	}
}

// A checkpoint whose tests can't find their tool is dropped with that reason:
// not a failure, nothing sent back, its round unspent, and the tool named once.
// The alert clears once the tests run.
func TestCheckpointMissingToolDropped(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"NEEDTOOL": "x"})
	task.Checkpoint = task.SHA
	if err := testStep(m, task, p); err != nil {
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
	if err := testStep(m, task, p); err != nil {
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

// A task waiting for the person sees its PR closed on its first look, then
// looks again only after a while.
func TestClosedPRStopsWaitingForPerson(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := testStep(m, task, p); err != nil {
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

// A draft closed while it waits stops as the person's call, rather than
// waiting in merging for good; one that leaves merging drops its wait, so a
// later draft wait starts its clock afresh.
func TestClosedDraftStops(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"x.txt": "x\n"})
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	forge.SetDraft(p.Repo, task.PR.Number, true)
	task.DraftWhy = "the lead holds it" // else the gate would ready it
	m.settle(task, m.step(task))
	if task.DraftSince.IsZero() {
		t.Fatalf("not waiting: stage %q, reason %q", task.Stage, task.Reason)
	}
	forge.ClosePR(p.Repo, task.PR.Number, "")
	m.settle(task, m.step(task))
	if task.Stage != StageStopped || !task.ByPerson || !task.DraftSince.IsZero() || task.DraftTold {
		t.Fatalf("stage %q, by person %v, draft since %v", task.Stage, task.ByPerson, task.DraftSince)
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

// The lead closes a PR whose task's tests wait in the check line, even while
// a step holds the task (it used to be refused as busy for as long as the
// step took): the wait leaves the line, the check never runs, and the
// step's copy isn't saved over the close.
func TestLeadClosesPRWaitingInCheckLine(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"feature.txt": "y"})
	task.Stage = StageTesting
	m.save(task, "")
	m.C.Effects.Slots.Resize(func() int { return 1 })
	hold := m.C.Effects.Slots.Acquire(p.Name, "someone-else", "test")
	defer hold()
	if err := m.test(task, p); err != nil { // queues its test behind the step
		t.Fatal(err)
	}
	for end := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if _, w := m.C.Effects.Slots.State(); len(w) == 1 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the test check never joined the line")
		}
	}
	m.mu.Lock()
	m.busy[task.ID] = true // a step holds it, as Tick does
	m.mu.Unlock()
	stepCopy := *task
	// The step holds the task until after the close, so a close that waited
	// on it would never return; no wall-clock bound beyond that, as a loaded
	// machine's local forge alone can take seconds.
	closed := make(chan error, 1)
	go func() { _, err := m.ClosePR(task.ID, "lead", "a duplicate", false); closed <- err }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close refused while a step holds the task: %v", err)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("close waited on the step holding the task")
	}
	m.save(&stepCopy, "the step's older copy") // dropped: the close was steered over it
	m.release(task.ID)
	if _, w := m.C.Effects.Slots.State(); len(w) != 0 {
		t.Fatalf("the closed PR's check still waits in the line: %v", w)
	}
	m.steps.Wait()
	if _, ok := m.C.Effects.GetCheck(p.Name, task.SHA, "test"); ok {
		t.Fatal("the closed PR's tests ran")
	}
	if got, _ := m.Get(task.ID); got.Stage != StageStopped || !strings.HasPrefix(got.Reason, closedByLead) {
		t.Fatalf("stage %q, reason %q", got.Stage, got.Reason)
	}
}
