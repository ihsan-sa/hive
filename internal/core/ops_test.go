package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// "lead" reaches the run named lead, not the newest run whose id starts with
// "lead-"; "lead:demo" reaches that project's lead, and an id prefix still works.
func TestFindPrefersAnExactName(t *testing.T) {
	rs := &Runs{runs: map[string]*Run{}}
	now := time.Now()
	rs.runs["lead-lead-1"] = &Run{RunSpec: RunSpec{ID: "lead-lead-1", Name: "lead", Role: "lead"}, Created: now.Add(-time.Hour)}
	rs.runs["lead-project-demo-2"] = &Run{RunSpec: RunSpec{ID: "lead-project-demo-2", Name: "lead:demo", Role: "lead", Project: "demo"}, Created: now}
	for addr, want := range map[string]string{"lead": "lead-lead-1", "lead:demo": "lead-project-demo-2", "lead-project": "lead-project-demo-2", "demo-2": "lead-project-demo-2"} {
		if r, ok := rs.Find(addr); !ok || r.ID != want {
			t.Errorf("Find(%q) = %q, want %q", addr, r.ID, want)
		}
	}
}

// An experiment run may only report on its own copy: it can't spawn helpers,
// file work, write the knowledge base, schedule, ask a warden or message.
func TestExperimentOnlyReports(t *testing.T) {
	o := &Ops{ops: map[string]Op{}}
	refused := []string{"spawn", "brief", "remember", "schedule", "warden", "message"}
	for _, name := range append([]string{"finish", "progress"}, refused...) {
		o.Register(Op{Name: name, Fn: func(Run, json.RawMessage) (any, error) { return "ok", nil }})
	}
	exp := Run{RunSpec: RunSpec{Role: "worker", OneTurn: true}}
	for _, name := range refused {
		if _, err := o.Call(exp, name, nil); err == nil {
			t.Errorf("an experiment ran %s", name)
		}
		if _, err := o.Call(Run{RunSpec: RunSpec{Role: "worker"}}, name, nil); err != nil {
			t.Errorf("a worker was refused %s: %v", name, err)
		}
	}
	for _, name := range []string{"finish", "progress"} {
		if _, err := o.Call(exp, name, nil); err != nil {
			t.Errorf("an experiment was refused %s: %v", name, err)
		}
	}
}

// A run with no parent messaging "parent" fails, rather than reaching
// whichever run Find("") happens to match.
func TestDeliverToParentWithNoParentFails(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	rs.runs["other"] = &Run{RunSpec: RunSpec{ID: "other"}}
	_, err = rs.ops.Deliver(Run{RunSpec: RunSpec{ID: "w1"}}, "parent", "hi")
	if err == nil || !strings.Contains(err.Error(), "no parent") {
		t.Fatalf("Deliver to parent with no Parent: %v, want a no-parent error", err)
	}
}

// A spawner that started its run itself (a helper worker's task) has it
// started once, not twice; the parent hears where a done helper's commits go,
// and a blocked helper's account doesn't claim a pull request.
func TestSpawnedHelper(t *testing.T) {
	c, _ := testHive(t)
	parent, err := c.Runs.Start(RunSpec{ID: "parent", Role: "worker", Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.Ops().SetSpawner(func(p Run, role, project, brief, model string) (RunSpec, error) {
		s := RunSpec{ID: "h-" + brief, Role: "worker", Harness: "fake", Parent: p.ID, Task: "t-" + brief}
		_, err := c.Runs.Start(s)
		return s, err
	})
	for _, status := range []string{"done", "blocked"} {
		if _, err := c.Runs.Ops().Call(parent, "spawn", json.RawMessage(`{"brief": "`+status+`"}`)); err != nil {
			t.Fatal(err)
		}
		h, _ := c.Runs.Get("h-" + status)
		if _, err := c.Runs.Ops().Call(h, "finish", json.RawMessage(`{"status": "`+status+`", "account": "did it"}`)); err != nil {
			t.Fatal(err)
		}
		h.Turn = 1
		out := c.WS.TurnOut(h.ID, 1)
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte(`{"type":"result","subtype":"success","result":"ok"}`+"\n"), 0o600)
		c.Runs.endTurn(h)
	}
	created := 0
	c.J.Replay(func(e Event) {
		if e.Kind == "run.created" && strings.Contains(string(e.Data), `"h-done"`) {
			created++
		}
	})
	p, _ := c.Runs.Get("parent")
	if created != 1 || len(p.Pending) != 2 || !strings.Contains(p.Pending[0], "pull request, task t-done") || strings.Contains(p.Pending[1], "pull request") {
		t.Fatalf("started %d times, parent's queue %q", created, p.Pending)
	}
}

// An agent spawns only workers and reviewers: a lead's role carries the
// strain exemption and the top priority, so only the daemon makes one.
func TestSpawnNeverMakesALead(t *testing.T) {
	c, _ := testHive(t)
	parent, err := c.Runs.Start(RunSpec{ID: "parent", Role: "worker", Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	asked := false
	c.Runs.Ops().SetSpawner(func(Run, string, string, string, string) (RunSpec, error) {
		asked = true
		return RunSpec{}, errors.New("not reached")
	})
	for _, role := range []string{"lead", "warden", "sweep"} {
		if _, err := c.Runs.Ops().Call(parent, "spawn", json.RawMessage(`{"role": "`+role+`", "brief": "x"}`)); err == nil || !strings.Contains(err.Error(), "workers or reviewers") {
			t.Errorf("spawn as %s: %v, want refused", role, err)
		}
	}
	if asked {
		t.Fatal("the spawner was asked for a role an agent may not spawn")
	}
}

// TestSpawnRefusesAnEmptyBrief: a helper started with nothing to do can only
// finish blocked, so spawn refuses a missing or blank brief before any
// checkout or task is made.
func TestSpawnRefusesAnEmptyBrief(t *testing.T) {
	c, _ := testHive(t)
	parent, err := c.Runs.Start(RunSpec{ID: "parent", Role: "worker", Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	asked := false
	c.Runs.Ops().SetSpawner(func(Run, string, string, string, string) (RunSpec, error) {
		asked = true
		return RunSpec{}, errors.New("not reached")
	})
	for _, args := range []string{`{}`, `{"brief": ""}`, `{"brief": " \n\t "}`, `{"role": "reviewer"}`} {
		if _, err := c.Runs.Ops().Call(parent, "spawn", json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), "needs a brief") {
			t.Errorf("spawn %s: %v, want refused for an empty brief", args, err)
		}
	}
	if asked {
		t.Fatal("the spawner was asked to start a helper with an empty brief")
	}
}

// TestChatMaxZero: chat_max_mb set to 0 means the default, not a limit that
// refuses every message.
func TestChatMaxZero(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.ChatMaxMB = 0
	ws.SaveConfig(cfg)
	if got := ws.LoadConfig().ChatMaxMB; got != DefaultConfig().ChatMaxMB {
		t.Fatalf("chat_max_mb 0 loads as %d", got)
	}
}

// A step still running at shutdown writes after the journal closed; the write
// is dropped instead of panicking the process.
func TestAppendAfterCloseIsDropped(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
	j.MustAppend("task.saved", map[string]string{"id": "late"})
}

// A new request in a live run's outbox wakes the loop once, not on every look.
func TestANewRequestWakesTheLoopOnce(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	rs.runs["w1"] = &Run{RunSpec: RunSpec{ID: "w1"}, Status: StatusRunning}
	outbox := filepath.Join(ws.RunBox("w1"), "outbox")
	os.MkdirAll(outbox, 0o755)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var woken atomic.Int32
	go rs.WatchRequests(ctx, 5*time.Millisecond, func() { woken.Add(1) })
	time.Sleep(30 * time.Millisecond)
	if n := woken.Load(); n != 0 {
		t.Fatalf("an empty outbox woke the loop %d times", n)
	}
	os.WriteFile(filepath.Join(outbox, "r1.json"), []byte(`{"op":"progress"}`), 0o600)
	for end := time.Now().Add(5 * time.Second); woken.Load() == 0 && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // ten more looks at the same request
	if n := woken.Load(); n != 1 {
		t.Fatalf("one request woke the loop %d times; want once", n)
	}
}

// settle waits for the last lines of a file still being written, and ends
// once it has been still for quiet, or at most. It runs on a fake clock: on a
// loaded machine a real writer goroutine was once not scheduled for 370ms.
func TestSettledWaitsForTheLastLines(t *testing.T) {
	var clock time.Time
	now := func() time.Time { return clock }
	// The file grows by a line every 20ms until 80ms, then is still.
	size := func() int64 {
		ms := clock.Sub(time.Time{}).Milliseconds()
		return 2 + 5*min(ms/20, 4)
	}
	sleep := func(d time.Duration) { clock = clock.Add(d) }
	settle(size, now, sleep, 50*time.Millisecond, 5*time.Second)
	if got := size(); got != 22 {
		t.Fatalf("settled at %v with %d bytes; want all four lines (22)", clock.Sub(time.Time{}), got)
	}
	if d := clock.Sub(time.Time{}); d < 130*time.Millisecond || d > 150*time.Millisecond {
		t.Fatalf("settled at %v; want 50ms after the last line at 80ms", d)
	}

	// A file that never stops growing ends at most.
	clock = time.Time{}
	grow := int64(0)
	settle(func() int64 { grow++; return grow }, now, sleep, 50*time.Millisecond, time.Second)
	if d := clock.Sub(time.Time{}); d != time.Second {
		t.Fatalf("a growing file ended at %v; want the 1s bound", d)
	}

	// The real file wrapper ends soon on a still file.
	path := filepath.Join(t.TempDir(), "out.jsonl")
	os.WriteFile(path, []byte("a\n"), 0o600)
	start := time.Now()
	settled(path, 50*time.Millisecond, 5*time.Second)
	if d := time.Since(start); d >= 2*time.Second {
		t.Fatalf("a still file took %s to settle; want about 50ms", d)
	}
}
