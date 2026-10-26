package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A worker whose turns keep ending in prose is nudged twice; when the turns
// answering its nudges make no commit either, it ends blocked and frees its
// slot. One that keeps committing stays quiet for the lead.
func TestAWorkerThatNeverFinishesEndsBlocked(t *testing.T) {
	for _, commits := range []bool{false, true} {
		t.Run(map[bool]string{false: "no commits", true: "commits"}[commits], func(t *testing.T) {
			c, _ := testHive(t)
			co := t.TempDir()
			log := filepath.Join(co, ".git", "logs", "HEAD")
			os.MkdirAll(filepath.Dir(log), 0o755)
			os.WriteFile(log, []byte("0 1 commit\n"), 0o644)
			c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Checkout: co})
			var got Run
			for turn := 1; turn <= 3; turn++ {
				if commits && turn > 1 {
					f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
					f.WriteString("1 2 commit\n")
					f.Close()
				}
				r, _ := c.Runs.Get("w1")
				c.J.MustAppend("run.delivered", runNote{ID: "w1", N: len(r.Pending)}) // its nudge went into this turn
				c.J.MustAppend("run.turn_started", turnStarted{ID: "w1", Turn: turn})
				out := c.WS.TurnOut("w1", turn)
				os.MkdirAll(filepath.Dir(out), 0o755)
				os.WriteFile(out, []byte(`{"type":"result","subtype":"success","result":"All done.","session_id":"s1"}`+"\n"), 0o600)
				r, _ = c.Runs.Get("w1")
				c.Runs.endTurn(r)
				got, _ = c.Runs.Get("w1")
				if turn < 3 && got.Status != StatusIdle {
					t.Fatalf("turn %d: %q (%q), want nudged", turn, got.Status, got.Reason)
				}
			}
			switch {
			case !commits && (got.Status != StatusBlocked || !strings.HasPrefix(got.Reason, NoFinish)):
				t.Fatalf("after its nudges: %q (%q), want blocked: %s", got.Status, got.Reason, NoFinish)
			case !commits && !got.Terminal():
				t.Fatal("a blocked run holds its slot")
			case commits && got.Status != StatusQuiet:
				t.Fatalf("a committing run: %q (%q), want quiet", got.Status, got.Reason)
			}
		})
	}
}

// An agent ends a quiet run blocked only while it is still quiet and no turn
// of it is starting; a message after doesn't wake it, as one does a run
// blocked on its own.
func TestEndQuietOnlyWhileQuiet(t *testing.T) {
	c, _ := testHive(t)
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Checkout: t.TempDir()})
	c.Runs.SetStatus("w1", StatusIdle, "nudged")
	if err := c.Runs.EndQuiet("w1", StoppedQuiet+"lead: x"); err == nil {
		t.Fatal("ended a run that isn't quiet")
	}
	c.Runs.SetStatus("w1", StatusQuiet, "keeps ending turns")
	c.Runs.mu.Lock()
	c.Runs.starting = map[string]bool{"w1": true}
	c.Runs.mu.Unlock()
	if err := c.Runs.EndQuiet("w1", StoppedQuiet+"lead: x"); err == nil {
		t.Fatal("ended a run whose turn is starting")
	}
	c.Runs.mu.Lock()
	delete(c.Runs.starting, "w1")
	c.Runs.mu.Unlock()
	if err := c.Runs.EndQuiet("w1", StoppedQuiet+"lead: x"); err != nil {
		t.Fatal(err)
	}
	c.J.MustAppend("run.queued", runNote{ID: "w1", Text: "late message"})
	c.Runs.wake("w1")
	if r, _ := c.Runs.Get("w1"); r.Status != StatusBlocked || !strings.HasPrefix(r.Reason, StoppedQuiet) {
		t.Fatalf("after a message: %q (%q), want it still ended", r.Status, r.Reason)
	}
}
