package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A turn that starts before the same run's last turn ended is a twin; one
// after its end, another run's, or one older than the window isn't.
func TestTwinTurnsFindsATurnStartedOverALiveOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	lines := []string{
		`{"seq":1,"at":"2026-10-25T17:00:00Z","kind":"run.turn_started","data":{"id":"old","turn":1}}`,
		`{"seq":2,"at":"2026-10-25T17:00:02Z","kind":"run.turn_started","data":{"id":"old","turn":2}}`,
		`{"seq":3,"at":"2026-10-26T17:24:08Z","kind":"run.turn_started","data":{"id":"w1","turn":6}}`,
		`{"seq":4,"at":"2026-10-26T17:24:09Z","kind":"run.turn_started","data":{"id":"w2","turn":1}}`,
		`{"seq":5,"at":"2026-10-26T17:24:11Z","kind":"run.turn_started","data":{"id":"w1","turn":7}}`,
		`{"seq":6,"at":"2026-10-26T17:30:00Z","kind":"run.turn_ended","data":{"id":"w2","turn":1}}`,
		`{"seq":7,"at":"2026-10-26T17:31:00Z","kind":"run.turn_started","data":{"id":"w2","turn":2}}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	got, err := twinTurns(path, time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "w1 turn 7 started 3s after turn 6") {
		t.Fatalf("twins %q, want only w1's turn 7", got)
	}
}
