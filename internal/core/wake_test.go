package core

import (
	"os"
	"path/filepath"
	"testing"
)

// A worker asks a warden and finishes blocked at once; the warden's answer
// can land just as that turn ends. Whichever side records last, the run is
// woken for it (TestBlockedWorkComesBack on a slow Windows runner).
// Only the first order reproduces that race; the second passed before the fix
// too and is here to keep endTurn's own wake from regressing.
func TestAnswerAtABlockedTurnsEndWakesIt(t *testing.T) {
	for _, order := range []string{"the turn ends as the answer lands", "the answer waits at the turn's end"} {
		t.Run(order, func(t *testing.T) {
			c, _ := testHive(t)
			c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker"})
			c.J.MustAppend("run.turn_started", turnStarted{ID: "w1", Turn: 1})
			c.J.MustAppend("run.finish", map[string]any{"id": "w1", "finish": Finish{Status: "blocked", Account: "asked a warden"}})
			r, _ := c.Runs.Get("w1")
			out := c.WS.TurnOut("w1", 1)
			os.MkdirAll(filepath.Dir(out), 0o755)
			os.WriteFile(out, []byte(`{"type":"result","subtype":"success","result":"blocked","session_id":"s1"}`+"\n"), 0o600)
			if order == "the turn ends as the answer lands" {
				// Send saw the run still running; its turn ends before Send can look again.
				ended := false
				c.J.Subscribe(func(e Event) {
					if e.Kind == "run.queued" && !ended {
						ended = true
						c.J.MustAppend("run.turn_ended", turnEnded{ID: "w1", Turn: 1, Status: StatusBlocked, Reason: "asked a warden"})
					}
				})
				if err := c.Runs.Send("w1", "[request q1] done"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := c.Runs.Send("w1", "[request q1] done"); err != nil {
					t.Fatal(err)
				}
				c.Runs.endTurn(r)
			}
			got, _ := c.Runs.Get("w1")
			if got.Status != StatusIdle || len(got.Pending) != 1 {
				t.Fatalf("run is %q (%q) with %d pending, want idle with the answer", got.Status, got.Reason, len(got.Pending))
			}
		})
	}
}

// TestAnswerAsEndTurnRecordsWakesIt: the answer lands after endTurn read the
// run but before the run's state takes it, so endTurn's own read misses it;
// endTurn's read after recording the end still wakes the run.
func TestAnswerAsEndTurnRecordsWakesIt(t *testing.T) {
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
