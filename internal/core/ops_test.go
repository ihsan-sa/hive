package core

import (
	"encoding/json"
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
