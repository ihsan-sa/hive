package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnswerAsATurnEndsBlockedWakesTheRun: the answer a worker finished
// blocked on, sent while Send still saw its turn running but applied after
// the turn ended, wakes it. That message once sat unseen on a blocked run
// (Windows ci, TestBlockedWorkComesBack).
func TestAnswerAsATurnEndsBlockedWakesTheRun(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	var rs *Runs
	ended := false
	// Before NewRuns' own subscriber, so the turn ends between Send's read
	// of the run and its message reaching the run's state.
	j.Subscribe(func(e Event) {
		if e.Kind == "run.queued" && !ended {
			ended = true
			r, _ := rs.Get("w1")
			rs.endTurn(r)
		}
	})
	rs = NewRuns(ws, j, nil, nil, func() []string { return nil })
	if _, err := rs.Start(RunSpec{ID: "w1", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	rs.append("run.turn_started", turnStarted{ID: "w1", Turn: 1})
	rs.append("run.finish", map[string]any{"id": "w1", "finish": Finish{Status: "blocked", Account: "asked a warden"}})
	os.WriteFile(ws.TurnOut("w1", 1), []byte(`{"type":"result","subtype":"success","result":"blocked"}`+"\n"), 0o644)
	if err := rs.Send("w1", "[request r1] done"); err != nil {
		t.Fatal(err)
	}
	r, _ := rs.Get("w1")
	if !ended || r.Status != StatusIdle || len(r.Pending) != 1 {
		t.Fatalf("ended %v; the run is %q (%q) with %d pending", ended, r.Status, r.Reason, len(r.Pending))
	}
}
