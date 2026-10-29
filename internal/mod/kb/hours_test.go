package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The record keeps the journal's recent hours one file each, as they were
// journaled: a restart rewrites the window without doubling a line, and an
// hour past the window goes.
func TestHoursKeepTheRecentJournal(t *testing.T) {
	dir := t.TempDir()
	c, _ := open(t, dir)
	c.J.MustAppend("task.saved", map[string]string{"id": "t1", "stage": "working"})
	c.J.MustAppend("task.saved", map[string]string{"id": "t1", "stage": "merged"})
	hrs := filepath.Join(c.WS.KB(), "record", "hours")
	old := filepath.Join(hrs, hourOf(time.Now().Add(-(hoursKept+2)*time.Hour))+".jsonl")
	os.WriteFile(old, []byte("{}\n"), 0o644)
	c.Close()

	c, _ = open(t, dir) // the catch-up, then one more live
	c.J.MustAppend("task.saved", map[string]string{"id": "t2", "stage": "queued"})
	ents, _ := os.ReadDir(hrs)
	var raw []byte
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(hrs, e.Name()))
		raw = append(raw, b...)
	}
	n := 0
	core.ReadJSONL(filepath.Join(c.WS.Events()), func([]byte) { n++ })
	if got := strings.Count(string(raw), "\n"); got != n || !strings.Contains(string(raw), `"stage":"queued"`) {
		t.Fatalf("hours hold %d lines of the journal's %d:\n%s", got, n, raw)
	}
	if core.Exists(old) {
		t.Fatal("an hour past the window is still there")
	}
}
