package kb

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
)

// The dream program's seam: it reads the shared notes (no isolated note, no
// record), and a pass it sends is committed in one commit and journaled with
// what it merged, linked, distilled and retired. A pass that touches a
// shipped practice doc, a path outside notes, practice and dreams, an
// isolated project, or a note changed since it read it is refused whole.
func TestDreamPass(t *testing.T) {
	ws, c, mods := freshWorkspace(t)
	c.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	c.Projects.Add(core.Project{Name: "mine", Repo: "me/mine", Private: true})
	m := New(c, &agents.Agents{C: c, Mod: mods})
	a, _ := m.Write(Note{Title: "Gate", Text: "first", Project: "hive"})
	b, _ := m.Write(Note{Title: "Gate", Text: "second", Project: "hive"})
	m.Write(Note{Title: "Plan", Text: "hidden plan", Project: "secret"})
	os.WriteFile(filepath.Join(ws.KB(), "loose.md"), []byte("never committed"), 0o644)
	os.MkdirAll(filepath.Join(ws.KB(), "record", "task"), 0o755)
	os.WriteFile(filepath.Join(ws.KB(), "record", "task", "t.md"), []byte("# task\n"), 0o644)

	shared := m.SharedNotes()
	var paths []string
	was := map[string]string{}
	for _, n := range shared.Notes {
		paths = append(paths, n.Path)
		was[n.Path] = hashOf([]byte(n.Text))
		if strings.Contains(n.Text, "hidden plan") {
			t.Fatalf("an isolated note is shared: %s", n.Path)
		}
	}
	if !slices.Contains(paths, "gate.md") || !slices.Contains(paths, "gate-2.md") || !slices.Contains(paths, "practice/risk.md") || slices.ContainsFunc(paths, func(p string) bool { return strings.HasPrefix(p, "record/") }) {
		t.Fatalf("shared %v", paths)
	}
	if !slices.Contains(shared.Shipped, "practice/risk.md") || !slices.Equal(shared.Hidden, []string{"mine", "secret"}) {
		t.Fatalf("shipped %v hidden %v", shared.Shipped, shared.Hidden)
	}

	for name, bad := range map[string]FileChange{
		"shipped doc":   {Path: "practice/risk.md", Text: "x", Was: was["practice/risk.md"]},
		"nested":        {Path: "notes/x.md", Text: "x"},
		"record":        {Path: "record/task/t.md", Text: "x"},
		"isolated":      {Path: "x.md", Text: "---\ntitle: x\nproject: secret\n---\n\nx\n"},
		"private":       {Path: "x.md", Text: "---\ntitle: x\nproject: mine\n---\n\nx\n"},
		"stale":         {Path: "gate.md", Text: "x", Was: "0"},
		"gone":          {Path: "nope.md", Remove: true, Was: "0"},
		"overwrite new": {Path: "gate.md", Text: "x"},
		"practice gone": {Path: "practice/risk.md", Remove: true, Was: was["practice/risk.md"]},
	} {
		head, _ := core.Git(ws.KB(), "rev-parse", "HEAD")
		if _, err := m.Dream("kb", Pass{Changes: []FileChange{{Path: "fine.md", Text: "fine"}, bad}}); err == nil {
			t.Errorf("%s: pass taken", name)
		}
		if now, _ := core.Git(ws.KB(), "rev-parse", "HEAD"); now != head || core.Exists(filepath.Join(ws.KB(), "fine.md")) {
			t.Errorf("%s: a refused pass wrote something", name)
		}
	}

	res, err := m.Dream("kb", Pass{Trigger: "scheduled", Seq: 7,
		Changes: []FileChange{
			{Path: "gate.md", Text: "---\ntitle: Gate\nconsolidated: 2026-10-25\n---\n\nsecond\n\n## History\n\nfirst\n", Was: was["gate.md"]},
			{Path: "gate-2.md", Remove: true, Was: was["gate-2.md"]},
			{Path: "loose.md", Remove: true, Was: was["loose.md"]},
			{Path: "practice/learned-from-the-record.md", Text: "learned"},
			{Path: "dreams/2026-10-25-0400.md", Text: "---\ntitle: What the Hive learned since the start\n---\n\nMerged 2 notes.\n"},
		},
		Merged:    []Merge{{Into: "gate.md", From: []string{"gate-2.md"}}},
		Distilled: []string{"practice/learned-from-the-record.md"},
		Digest:    "dreams/2026-10-25-0400.md"})
	if err != nil || !strings.HasPrefix(res, "committed ") {
		t.Fatalf("pass: %q %v", res, err)
	}
	if core.Exists(b) || !core.Exists(a) {
		t.Error("the merged note is still there, or the living one is gone")
	}
	if out, _ := core.Git(ws.KB(), "status", "--porcelain", "--", "gate.md", "gate-2.md", "practice", "dreams"); out != "" {
		t.Errorf("the pass left changes uncommitted:\n%s", out)
	}
	if msg, _ := core.Git(ws.KB(), "log", "-1", "--format=%s"); msg != "Dream (scheduled): merged 1, linked 0, distilled 1, retired 0" {
		t.Errorf("commit %q", msg)
	}
	var found bool
	c.J.Replay(func(e core.Event) {
		found = found || (e.Kind == "kb.dreamed" && strings.Contains(string(e.Data), `"into":"gate.md"`) && strings.Contains(string(e.Data), `"digest":"dreams/2026-10-25-0400.md"`))
	})
	if !found {
		t.Error("the pass is not journaled with what it merged and its digest")
	}
	if d, ok := LatestDigest(ws.KB()); !ok || d.Path != "dreams/2026-10-25-0400.md" || d.Title != "What the Hive learned since the start" {
		t.Errorf("latest digest %+v %v", d, ok)
	}
	for _, c := range Recent(ws.KB(), 10) {
		if strings.HasPrefix(c.Path, "dreams/") {
			t.Error("a digest is listed as recently updated as well as pinned")
		}
	}
	if res, err := m.Dream("kb", Pass{Trigger: "quiet"}); err != nil || res != "nothing to change" {
		t.Errorf("empty pass: %q %v", res, err)
	}
}

// Search ranks a living note the dream consolidated above a plain note that
// matches as well, and a retired one last.
func TestSearchRanksConsolidatedFirst(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a-retired.md"), []byte("---\ntitle: Gate old\ndate: 2026-10-26\nretired: 2026-10-26 · superseded\n---\n\nthe gate rolls back\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b-plain.md"), []byte("---\ntitle: Gate plain\ndate: 2026-10-25\n---\n\nthe gate rolls back\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "c-living.md"), []byte("---\ntitle: Gate living\ndate: 2026-10-20\nconsolidated: 2026-10-25\n---\n\nthe gate rolls back\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "d-weak.md"), []byte("---\ntitle: Weak\nconsolidated: 2026-10-25\n---\n\nonly gate\n"), 0o644)
	var got []string
	for _, h := range Search(dir, "gate rolls back") {
		got = append(got, h.File)
	}
	if !slices.Equal(got, []string{"c-living.md", "b-plain.md", "d-weak.md", "a-retired.md"}) {
		t.Errorf("order %v", got)
	}
}

// A pass can't write through a linked practice/ or dreams/ folder, can't name
// a hidden project its text didn't name before, and a write that fails partway
// leaves the knowledge base as it was.
func TestDreamGuardsAndPutsBack(t *testing.T) {
	ws, c, mods := freshWorkspace(t)
	c.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	m := New(c, &agents.Agents{C: c, Mod: mods})
	m.Write(Note{Title: "Gate", Text: "first", Project: "hive"})
	m.Write(Note{Title: "Old", Text: "once about secret", Project: "hive"})
	was := map[string]string{}
	text := map[string]string{}
	for _, n := range m.SharedNotes().Notes {
		was[n.Path], text[n.Path] = hashOf([]byte(n.Text)), n.Text
	}

	outside := t.TempDir()
	os.RemoveAll(filepath.Join(ws.KB(), "dreams"))
	if err := os.Symlink(outside, filepath.Join(ws.KB(), "dreams")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := m.Dream("kb", Pass{Changes: []FileChange{{Path: "dreams/d.md", Text: "digest"}}}); err == nil || core.Exists(filepath.Join(outside, "d.md")) {
		t.Errorf("a pass wrote through a linked dreams/: %v", err)
	}
	os.Remove(filepath.Join(ws.KB(), "dreams"))

	if _, err := m.Dream("kb", Pass{Changes: []FileChange{{Path: "dreams/d.md", Text: "the secret project rolled back"}}}); err == nil {
		t.Error("a digest naming a hidden project was taken")
	}
	if _, err := m.Dream("kb", Pass{Changes: []FileChange{{Path: "old.md", Text: text["old.md"] + "\nSee also [[gate]].\n", Was: was["old.md"]}}}); err != nil {
		t.Errorf("relinking a note that already named a hidden project was refused: %v", err)
	}

	if runtime.GOOS == "windows" {
		return
	}
	practice := filepath.Join(ws.KB(), "practice")
	os.Chmod(practice, 0o555)
	defer os.Chmod(practice, 0o755)
	if core.Exists(filepath.Join(practice, ".probe")) || os.WriteFile(filepath.Join(practice, ".probe"), nil, 0o644) == nil {
		t.Skip("a read-only folder is writable here")
	}
	_, err := m.Dream("kb", Pass{Changes: []FileChange{
		{Path: "fine.md", Text: "fine"},
		{Path: "gate.md", Text: "rewritten", Was: was["gate.md"]},
		{Path: "practice/new.md", Text: "new"},
	}})
	if err == nil {
		t.Fatal("a pass whose write failed was taken")
	}
	if raw, _ := os.ReadFile(filepath.Join(ws.KB(), "gate.md")); core.Exists(filepath.Join(ws.KB(), "fine.md")) || string(raw) != text["gate.md"] {
		t.Error("a pass that failed partway left what it wrote")
	}
}
