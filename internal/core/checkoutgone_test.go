package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A run whose task ended had its checkout removed; an answer that came
// after reopened it, and every turn since failed in the sandbox and parked
// for another (issue #2008). Reopen refuses it, and a turn that fails
// without its checkout ends the run instead of parking it.
func TestRunWithoutItsCheckoutStopsRetrying(t *testing.T) {
	c, _ := testHive(t)
	dir := filepath.Join(t.TempDir(), "w1")
	os.MkdirAll(dir, 0o755)
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Checkout: dir})
	c.J.MustAppend("run.status", runNote{ID: "w1", Status: StatusDone})

	if err := c.Runs.Reopen("w1", "an answer"); err != nil {
		t.Fatalf("reopen with its checkout: %v", err)
	}
	os.RemoveAll(dir)
	r, _ := c.Runs.Get("w1")
	r.Turn = 1
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out+".err", []byte("bwrap: Can't find source path "+dir+": No such file or directory\n"), 0o600)
	c.Runs.endTurn(r)
	got, _ := c.Runs.Get("w1")
	if got.Status != StatusBlocked || !strings.Contains(got.Reason, "is gone") {
		t.Fatalf("after a turn without its checkout: status %q, reason %q; want it ended", got.Status, got.Reason)
	}
	if err := c.Runs.Reopen("w1", "another answer"); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("reopen without its checkout: %v, want refused", err)
	}
	if got, _ := c.Runs.Get("w1"); got.Status != StatusBlocked {
		t.Fatalf("status %q after the refused reopen", got.Status)
	}
}
