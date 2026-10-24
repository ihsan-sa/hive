package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnreachable(t *testing.T) {
	for text, want := range map[string]string{
		"API Error: Connection error.":                                       "can't reach Claude: the connection failed",
		"getaddrinfo ENOTFOUND api.anthropic.com":                            "can't reach Claude: no internet connection",
		"OAuth token has expired. Please obtain a new token":                 "Claude login expired",
		"Invalid API key · Please run /login":                                "Claude login expired",
		"API Error: 500 Internal server error":                               "",
		"error: the worker made no commits":                                  "",
		"error connecting to api.github.com\ncheck your internet connection": "can't reach Claude: the connection failed",
	} {
		if got := Unreachable("Claude", text); got != want {
			t.Errorf("Unreachable(%q) = %q, want %q", text, got, want)
		}
	}
}

// A turn that can't reach Claude parks with one plain line, checks back
// within minutes, and the line clears once a turn gets through.
func TestUnreachableTurnSaysWhyAndClears(t *testing.T) {
	c, _ := testHive(t)
	c.J.MustAppend("run.created", RunSpec{ID: "r1", Role: "lead"})
	r, _ := c.Runs.Get("r1")
	r.Turn = 1
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out, []byte(`{"type":"result","subtype":"success","is_error":true,"result":"API Error: Connection error.","session_id":"s1"}`+"\n"), 0o600)
	c.Runs.endTurn(r)
	got, _ := c.Runs.Get("r1")
	if got.Offline != "can't reach Claude: the connection failed" || got.Reason != got.Offline+"; retrying" || got.Status != StatusParked {
		t.Fatalf("after the failed turn: status %q, reason %q, offline %q", got.Status, got.Reason, got.Offline)
	}
	if time.Until(got.ParkedUntil) > 5*time.Minute {
		t.Fatalf("parked until %v, want a retry within 5 minutes", got.ParkedUntil)
	}
	c.J.MustAppend("run.turn_ended", turnEnded{ID: "r1", Turn: 2, Status: StatusIdle})
	if got, _ := c.Runs.Get("r1"); got.Offline != "" {
		t.Fatalf("offline %q after a turn got through", got.Offline)
	}
}

// Under WSL, sandbox-exec writes its early failures to .wsl, and the park
// reason carries them.
func TestParkReasonReadsTheWSLLog(t *testing.T) {
	c, _ := testHive(t)
	r := Run{RunSpec: RunSpec{ID: "r1", Role: "lead"}, Turn: 1}
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out+".wsl", []byte("sandbox-exec: open /mnt/c/Users/JOHNSM~1/spec.json: no such file or directory\n"), 0o600)
	c.Runs.endTurn(r)
	var te turnEnded
	c.J.Replay(func(e Event) {
		if e.Kind == "run.turn_ended" {
			e.Decode(&te)
		}
	})
	if !strings.HasPrefix(te.Reason, "interrupted; retrying") || !strings.Contains(te.Reason, "open /mnt/c/Users/JOHNSM~1/spec.json") {
		t.Fatalf("reason %q, want the sandbox-exec error", te.Reason)
	}
}

// A run that finishes blocked with a message waiting (often the answer it was
// blocked on) is woken by it, whether the message came before or during the
// turn's end; the wake reads the run after the turn ended.
func TestBlockedFinishIsWokenByAMessage(t *testing.T) {
	c, _ := testHive(t)
	c.J.MustAppend("run.created", RunSpec{ID: "r1", Role: "worker"})
	c.J.MustAppend("run.status", runNote{ID: "r1", Status: StatusRunning})
	c.J.MustAppend("run.finish", map[string]any{"id": "r1", "finish": Finish{Status: "blocked", Account: "asked a warden"}})
	r, _ := c.Runs.Get("r1")
	r.Turn = 1
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out, []byte(`{"type":"result","subtype":"success","result":"ok","session_id":"s1"}`+"\n"), 0o600)
	if err := c.Runs.Send("r1", "[request x] declined"); err != nil {
		t.Fatal(err)
	}
	c.Runs.endTurn(r)
	if got, _ := c.Runs.Get("r1"); got.Status != StatusIdle || got.Reason != "woken by a message" || len(got.Pending) != 1 {
		t.Fatalf("run is %q (%q) with %d pending", got.Status, got.Reason, len(got.Pending))
	}
}
