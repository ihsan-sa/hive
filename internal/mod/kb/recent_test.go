package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// TestRecent reads what's new and what changed from a fixture knowledge
// base's history: newest first, an edit told from an add, a deleted note and
// the Hive's own source left out, titles from the header or the heading.
func TestRecent(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	commit := func(i int, files map[string]string, rm ...string) {
		t.Helper()
		for name, text := range files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(text), 0o644)
		}
		for _, name := range rm {
			os.Remove(filepath.Join(dir, name))
		}
		git("add", "-A")
		when := day.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
		t.Setenv("GIT_COMMITTER_DATE", when)
		t.Setenv("GIT_AUTHOR_DATE", when)
		git("commit", "-q", "-m", "change")
	}
	git("init", "-q", "-b", "main")
	commit(0, map[string]string{"README.md": "# Knowledge base\n", "practice/risk.md": "# Risk\n\nwhat can't be put back\n", "old.md": "# Old\n"})
	commit(1, map[string]string{"sandbox.md": "---\ntitle: Bubblewrap needs userns\ndate: 2026-10-20\n---\n\n# not this\n"})
	commit(2, map[string]string{"hive/core/core.go": "package core\n"})
	commit(3, map[string]string{"practice/risk.md": "# Risk\n\nwhat can't be put back, and what can\n"}, "old.md")
	commit(4, map[string]string{"gate.md": "no heading at all\n"})

	got := Recent(dir, 10)
	at := func(i int) time.Time { return time.Unix(day.Add(time.Duration(i)*time.Hour).Unix(), 0) }
	want := []Change{
		{Path: "gate.md", Title: "gate", At: at(4), New: true},
		{Path: "practice/risk.md", Title: "Risk", At: at(3)},
		{Path: "sandbox.md", Title: "Bubblewrap needs userns", At: at(1), New: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recent\n got %+v\nwant %+v", got, want)
	}
	if got := Recent(dir, 2); len(got) != 2 || got[1].Path != "practice/risk.md" {
		t.Fatalf("Recent(2): %+v", got)
	}
	if got := Recent(t.TempDir(), 5); got != nil {
		t.Fatalf("no repository: %+v", got)
	}
}
