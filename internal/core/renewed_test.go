package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Renewing a Claude login revokes the token every turn running on it holds.
// A turn that ends on that 401 after a renewal resumes at once, not as a
// harness error with a retry's backoff; one whose login wasn't renewed since
// is still a harness error.
func TestTurnCutOffByRenewalResumesAtOnce(t *testing.T) {
	c, _ := testHive(t)
	dir := filepath.Join(c.WS.LoginsDir("claude"), "cc0")
	os.MkdirAll(dir, 0o700)
	creds := func(exp time.Time) {
		raw := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"t%d","expiresAt":%d}}`, exp.Unix(), exp.UnixMilli())
		if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	handed := time.Now().Add(4 * time.Minute)
	creds(handed)
	l := Login{Harness: "claude", Name: "cc0", Dir: dir}
	if got := c.Runs.tokenExpiry(l); !got.Equal(handed.Truncate(time.Millisecond)) {
		t.Fatalf("the handed token's expiry: %v want %v", got, handed)
	}
	if _, err := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "claude", Model: "opus", Brief: "b", Checkout: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	revoked := `{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Failed to authenticate: OAuth token revoked. Please log in again or contact your administrator.","session_id":"s1"}`
	end := func(turn int) Run {
		c.Runs.append("run.turn_started", turnStarted{ID: "w1", Session: "s1", Turn: turn, Login: l.Key(), Harness: "claude", Model: "opus", TokenExpiry: c.Runs.tokenExpiry(l)})
		r, _ := c.Runs.Get("w1")
		out := c.WS.TurnOut(r.ID, turn)
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte(revoked+"\n"), 0o600)
		c.Runs.endTurn(r)
		got, _ := c.Runs.Get("w1")
		return got
	}

	// Not renewed since the turn began: a harness error, retried with backoff.
	got := end(1)
	if got.Status != StatusParked || !strings.Contains(got.Reason, "harness error") || got.Retries != 1 {
		t.Fatalf("a 401 with no renewal: %s %q retries %d", got.Status, got.Reason, got.Retries)
	}

	// Renewed mid-turn: it resumes at once, and isn't counted a retry.
	c.Runs.append("run.turn_started", turnStarted{ID: "w1", Session: "s1", Turn: 2, Login: l.Key(), Harness: "claude", Model: "opus", TokenExpiry: c.Runs.tokenExpiry(l)})
	creds(time.Now().Add(8 * time.Hour))
	r, _ := c.Runs.Get("w1")
	out := c.WS.TurnOut(r.ID, 2)
	os.WriteFile(out, []byte(revoked+"\n"), 0o600)
	c.Runs.endTurn(r)
	got, _ = c.Runs.Get("w1")
	if got.Status != StatusParked || got.ParkedUntil.After(time.Now()) || !strings.Contains(got.Reason, "renewed mid-turn") {
		t.Fatalf("a 401 after a renewal: %s until %v %q", got.Status, got.ParkedUntil, got.Reason)
	}
	if got.Retries != 1 || got.Offline != "" {
		t.Fatalf("a renewal's 401 counted as a retry or offline: retries %d offline %q", got.Retries, got.Offline)
	}
	if len(got.Pending) == 0 || !strings.Contains(got.Pending[0], "renewed its login") {
		t.Fatalf("the next turn isn't told why: %q", got.Pending)
	}
}
