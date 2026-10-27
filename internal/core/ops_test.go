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
	time.Sleep(60 * time.Millisecond)
	if n := woken.Load(); n != 1 {
		t.Fatalf("one request woke the loop %d times; want once", n)
	}
}

// settled waits for the last lines of a file still being written, and ends
// soon once it stops growing.
func TestSettledWaitsForTheLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.jsonl")
	os.WriteFile(path, []byte("a\n"), 0o600)
	go func() {
		for i := 0; i < 4; i++ {
			time.Sleep(20 * time.Millisecond)
			f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString("line\n")
			f.Close()
		}
	}()
	start := time.Now()
	settled(path, 50*time.Millisecond, time.Second)
	if raw, _ := os.ReadFile(path); strings.Count(string(raw), "line") != 4 {
		t.Fatalf("settled after %s with %q; want all four lines", time.Since(start), raw)
	}
	start = time.Now()
	settled(path, 50*time.Millisecond, 200*time.Millisecond)
	if d := time.Since(start); d >= 150*time.Millisecond {
		t.Fatalf("a still file took %s to settle; want about 50ms", d)
	}
}
