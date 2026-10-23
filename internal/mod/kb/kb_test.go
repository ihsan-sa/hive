package kb

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// A fresh workspace's knowledge base answers a question on each practice
// topic, the way an agent searches it: grep the notes for the words.
func TestFreshKBKnowsHowAHiveWorks(t *testing.T) {
	ws, c, mods := freshWorkspace(t)
	m := New(c, &agents.Agents{C: c, Mod: mods})

	for query, want := range map[string]string{
		"what you didn't check":        "looking-first.md",
		"report after":                 "deciding.md",
		"can't be put back":            "risk.md",
		"answer is the first sentence": "writing.md",
		"what done is":                 "briefs.md",
		"hand off before you run out":  "handoffs.md",
		"independent":                  "parallel.md",
		"reverted":                     "measuring.md",
	} {
		if hits := grep(t, filepath.Join(ws.KB(), "practice"), query); !slices.Contains(hits, want) {
			t.Errorf("query %q: want %s among %v", query, want, hits)
		}
	}
	if n := m.Notes(); len(n) != 0 {
		t.Errorf("practice docs counted as notes: %v", n)
	}
	if out, _ := core.Git(ws.KB(), "status", "--porcelain"); out != "" {
		t.Errorf("practice docs not committed:\n%s", out)
	}
}

// Seeding never overwrites what a person wrote: a doc already in the store
// and a note titled like a practice doc both survive byte for byte, the note
// still counts as a note, and the missing docs are still filed.
func TestSeedingKeepsExistingFiles(t *testing.T) {
	ws, c, mods := freshWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws.KB(), "practice"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := map[string]string{
		filepath.Join(ws.KB(), "practice", "risk.md"): "my own risk rules\n",
		filepath.Join(ws.KB(), "practice-risk.md"):    "---\ntitle: Practice risk\n---\n\nmy note\n",
	}
	for p, body := range mine {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := New(c, &agents.Agents{C: c, Mod: mods})
	for p, body := range mine {
		if raw, _ := os.ReadFile(p); string(raw) != body {
			t.Errorf("%s overwritten: %q", p, raw)
		}
	}
	if n := m.Notes(); !slices.Contains(n, "practice-risk.md") {
		t.Errorf("note titled like a practice doc hidden: %v", n)
	}
	if !core.Exists(filepath.Join(ws.KB(), "practice", "writing.md")) {
		t.Error("missing practice docs not filed")
	}
}

// A commit that fails leaves a journal line instead of vanishing.
func TestSeedingReportsAFailedCommit(t *testing.T) {
	ws, c, mods := freshWorkspace(t)
	if _, err := core.Git("", "init", "-q", "-b", "main", ws.KB()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.KB(), ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	New(c, &agents.Agents{C: c, Mod: mods})
	found := false
	c.J.Replay(func(e core.Event) { found = found || e.Kind == "kb.practice" })
	if !found {
		t.Error("failed commit of the practice docs left no journal line")
	}
}

func freshWorkspace(t *testing.T) (core.Workspace, *core.Core, *modules.Registry) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	mods := modules.New(ws, c.J)
	if err := mods.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	return ws, c, mods
}

// The practice docs are generic: nothing of the machine, the people or the
// projects the Hive was built on leaks into them.
func TestPracticeDocsCarryNoIdentity(t *testing.T) {
	leak := regexp.MustCompile(`(?i)iiks|ihsan|salari|autobox|tailae|\bcc[- ]|~/|/home/|slack|C0[A-Z0-9]{8}|board row|owner`)
	dir := filepath.Join("..", "..", "..", "modules", "kb", "practice")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no practice docs in %s: %v", dir, err)
	}
	size := 0
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		size += len(raw)
		if hit := leak.Find(raw); hit != nil {
			t.Errorf("%s names %q", e.Name(), hit)
		}
	}
	if size > 15000 {
		t.Errorf("practice docs are %d bytes; keep the set under 15 KB", size)
	}
}

func grep(t *testing.T, dir, query string) []string {
	entries, _ := os.ReadDir(dir)
	var hits []string
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && strings.Contains(strings.ToLower(string(raw)), strings.ToLower(query)) {
			hits = append(hits, e.Name())
		}
	}
	return hits
}
