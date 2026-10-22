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
	m := New(c, &agents.Agents{C: c, Mod: mods})

	for query, want := range map[string]string{
		"what you didn't check":        "practice-looking-first.md",
		"report after":                 "practice-deciding.md",
		"can't be put back":            "practice-risk.md",
		"answer is the first sentence": "practice-writing.md",
		"what done is":                 "practice-briefs.md",
		"hand off before you run out":  "practice-handoffs.md",
		"independent":                  "practice-parallel.md",
		"reverted":                     "practice-measuring.md",
	} {
		if hits := grep(t, ws.KB(), query); !slices.Contains(hits, want) {
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
