package work

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The clean job reaps every recursive gate run a dead gate left, never the
// one in flight.
func TestTidyGatesSkipsTheRunInFlight(t *testing.T) {
	m, _, _, p := noCommitRig(t, 0)
	dir := filepath.Join(m.C.WS.Gates(), p.Name, "recursive")
	dead, live := filepath.Join(dir, "261024-100000-aaaaaaaa"), filepath.Join(dir, "261024-110000-bbbbbbbb")
	for _, d := range []string{dead, live} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(dir, "lessons.md"), nil, 0o644)
	m.mu.Lock()
	m.gating = map[string]*gateRun{"k": {key: "k", since: time.Now(), out: live}}
	m.mu.Unlock()
	var reaped []string
	m.TidyGates(func(out string) int { reaped = append(reaped, out); return 0 })
	if !slices.Equal(reaped, []string{dead}) {
		t.Fatalf("reaped %v, want only %s", reaped, dead)
	}
}
