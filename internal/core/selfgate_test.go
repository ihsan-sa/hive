package core

import (
	"path/filepath"
	"testing"
	"time"
)

// A can't-run at a main that isn't tied to its head is kept per main from
// the journal, so a restart remembers it; a judging run there clears it,
// and one tied to its own head never sets it.
func TestGateAtMainOutlivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	open := func() (*Effects, *Journal) {
		j, err := OpenJournal(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { j.Close() })
		return NewEffects(Workspace{}, j, nil, nil, nil, nil), j
	}
	e, j := open()
	e.RecordGate(GateResult{Project: "hive", Head: "h1", Main: "m1", Outcome: GateCantRun, Reason: "the control fails", MainFails: true, At: time.Now()})
	e.RecordGate(GateResult{Project: "hive", Head: "h2", Main: "m2", Outcome: GateCantRun, Reason: "its own build", Alone: true, At: time.Now()})
	j.Close()
	e, _ = open()
	if g, ok := e.GateAtMain("hive", "m1"); !ok || !g.MainFails || g.Head != "h1" {
		t.Fatalf("after a restart: %+v %v, want main's failure remembered", g, ok)
	}
	if _, ok := e.GateAtMain("hive", "m2"); ok {
		t.Fatal("a can't-run tied to its own head held the main")
	}
	e.RecordGate(GateResult{Project: "hive", Head: "h3", Main: "m1", Outcome: GateRollback, Reason: "worse", At: time.Now()})
	if _, ok := e.GateAtMain("hive", "m1"); ok {
		t.Fatal("a run that judged at main left it held")
	}
}
