package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
