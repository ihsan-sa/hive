package release

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A new binary that doesn't run is put back before the restart, and the
// update says so.
func TestUpdateThatDoesntRunIsPutBack(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	tryBinary = func(string, string) error { return errors.New("exec format error") }
	restarted := false
	m.Restart = func() { restarted = true }
	_, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), "v9.9.9 doesn't run here (exec format error); kept v0.1.2") || string(raw) != "old" {
		t.Fatalf("update: %v, binary %q", err, raw)
	}
	if _, ok := readPending(m.C.WS); ok || restarted || journaled(m, "hive.updated") {
		t.Fatal("an update that was put back still counts as one")
	}
}

// The new daemon counts its starts; the third (two failed) puts the old
// binary back and names it for the old daemon, which says so and doesn't
// install that release again unless asked for it by name.
func TestFailedStartsRollBack(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	if _, err := m.Update(""); err != nil {
		t.Fatal(err)
	}
	core.Version = "v9.9.9" // the new daemon starts, and fails, twice
	for i := 0; i < failedStarts; i++ {
		if back := Boot(m.C.WS); back != "" {
			t.Fatalf("start %d rolled back", i+1)
		}
	}
	if back := Boot(m.C.WS); back != exe {
		t.Fatalf("the third start returned %q, want %q", back, exe)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" {
		t.Fatalf("binary after the rollback: %q", raw)
	}
	if back := Boot(m.C.WS); back != "" {
		t.Fatal("a second rollback")
	}
	core.Version = "v0.1.2" // the old daemon starts again
	m.Report()
	if !journaled(m, "hive.rolledback") {
		t.Fatal("the rollback wasn't journaled")
	}
	if _, ok := readPending(m.C.WS); ok {
		t.Fatal("update.json outlived the report")
	}
	var alert string
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "alert.raised" {
			alert = string(e.Data)
		}
	})
	if !strings.Contains(alert, "rolled back to v0.1.2") {
		t.Fatalf("no alert says so: %s", alert)
	}
	msg, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.Contains(msg, "was rolled back") || string(raw) != "old" {
		t.Fatalf("the next update: %q, %v, binary %q", msg, err, raw)
	}
	if _, err := m.Update("v9.9.9"); err != nil {
		t.Fatalf("asked for by name: %v", err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary after asking by name: %q", raw)
	}
}

// A new daemon that stays up keeps its update: later starts never roll back.
func TestSettledUpdateStays(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	if _, err := m.Update(""); err != nil {
		t.Fatal(err)
	}
	core.Version = "v9.9.9"
	Boot(m.C.WS)
	Settled(m.C.WS)
	for i := 0; i < 5; i++ {
		if back := Boot(m.C.WS); back != "" {
			t.Fatal("a settled update rolled back")
		}
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary: %q", raw)
	}
}

// An update whose restart never came finds the binary in place; updating
// again keeps the .old a rollback goes back to.
func TestUpdateAgainKeepsTheOld(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	for i := 0; i < 2; i++ {
		if _, err := m.Update(""); err != nil {
			t.Fatal(err)
		}
	}
	if raw, _ := os.ReadFile(exe + ".old"); string(raw) != "old" {
		t.Fatalf(".old after a second update: %q", raw)
	}
}

// tryBinary passes a binary that runs and names the tag, and nothing else.
func TestTryBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for the binary")
	}
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good"), filepath.Join(dir, "bad")
	os.WriteFile(good, []byte("#!/bin/sh\necho hive v9.9.9\n"), 0o755)
	os.WriteFile(bad, []byte("not a binary"), 0o755)
	if err := tryBinary(good, "v9.9.9"); err != nil {
		t.Fatalf("a good binary: %v", err)
	}
	if err := tryBinary(good, "v9.9.8"); err == nil {
		t.Fatal("a binary that says another version passed")
	}
	if err := tryBinary(bad, "v9.9.9"); err == nil {
		t.Fatal("a file that doesn't run passed")
	}
}
