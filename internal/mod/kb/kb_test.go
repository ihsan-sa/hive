package kb

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

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

// An update refreshes a practice doc the Hive filed and nobody touched, and
// leaves one a person edited since.
func TestUpdateRefreshesUntouchedPracticeDocs(t *testing.T) {
	ws, c, mods := freshWorkspace(t)
	m := New(c, &agents.Agents{C: c, Mod: mods})
	edited := filepath.Join(ws.KB(), "practice", "risk.md")
	if err := os.WriteFile(edited, []byte("my own risk rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"writing.md", "risk.md"} {
		p := filepath.Join(ws.Modules(), "kb", "practice", name)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(raw, "\nnew in this release\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.filePractice()
	m.filePractice()
	if raw, _ := os.ReadFile(filepath.Join(ws.KB(), "practice", "writing.md")); !strings.Contains(string(raw), "new in this release") {
		t.Error("untouched practice doc not refreshed on update")
	}
	if raw, _ := os.ReadFile(edited); string(raw) != "my own risk rules\n" {
		t.Errorf("edited practice doc overwritten: %q", raw)
	}
	if out, _ := core.Git(ws.KB(), "status", "--porcelain"); strings.TrimSpace(out) != "M practice/risk.md" {
		t.Errorf("refresh not committed, or the person's edit swept into it:\n%s", out)
	}
	kept := 0
	c.J.Replay(func(e core.Event) {
		if e.Kind == "kb.practice" {
			kept++
		}
	})
	if kept != 1 {
		t.Errorf("kept edit journaled %d times over two starts, want once", kept)
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

func newKB(t *testing.T) (*core.Core, *Module) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.MemFloorMB = 0 // a loaded machine mustn't hold the queue
	ws.SaveConfig(cfg)
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	mods := modules.New(ws, c.J)
	return c, New(c, &agents.Agents{C: c, Mod: mods})
}

// The filer reads every open agent's notes, so a planted note can steer it.
// Its commits are refused when they reach isolated/, where an isolated
// project's notes sit untracked, or add a link, and those notes stay as they were.
func TestFilerCannotReachIsolatedNotes(t *testing.T) {
	for name, plant := range map[string]func(dir string){
		"note in isolated/": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "isolated", "secret"), 0o755)
			os.WriteFile(filepath.Join(dir, "isolated", "secret", "plan.md"), []byte("planted"), 0o644)
		},
		"link at isolated/secret": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "isolated"), 0o755)
			os.Symlink(t.TempDir(), filepath.Join(dir, "isolated", "secret"))
		},
		"link elsewhere": func(dir string) {
			os.Symlink("/etc/passwd", filepath.Join(dir, "passwd.md"))
		},
		// A case-insensitive disk folds this onto isolated/.
		"note in Isolated/": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "Isolated", "secret"), 0o755)
			os.WriteFile(filepath.Join(dir, "Isolated", "secret", "x.md"), []byte("planted"), 0o644)
		},
		"nested note": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "notes"), 0o755)
			os.WriteFile(filepath.Join(dir, "notes", "x.md"), []byte("planted"), 0o644)
		},
		"not a note": func(dir string) {
			os.WriteFile(filepath.Join(dir, "x.sh"), []byte("planted"), 0o644)
		},
		// The next start's move would put it over the real note.
		"note naming an isolated project": func(dir string) {
			os.WriteFile(filepath.Join(dir, "plan.md"), []byte("---\ntitle: plan\nproject: secret\n---\n\nplanted\n"), 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, m := newKB(t)
			path, err := m.Write(Note{Title: "plan", Text: "the real plan", Project: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			kb := c.WS.KB()
			base := file(t, c, m, plant)
			m.moveIsolated() // what the next start does
			if head, _ := core.Git(kb, "rev-parse", "HEAD"); head != base {
				t.Error("the filer's commits were taken")
			}
			if b, err := os.ReadFile(path); err != nil || string(b) == "planted" {
				t.Errorf("the isolated note changed: %q %v", b, err)
			}
			if fi, err := os.Lstat(filepath.Dir(path)); err != nil || !fi.IsDir() {
				t.Error("the isolated folder is no longer a real folder")
			}
		})
	}
}

// A note for an isolated project is refused when its folder is a link, so it
// never lands where the link points.
func TestIsolatedNoteRefusesALinkedFolder(t *testing.T) {
	c, m := newKB(t)
	elsewhere := t.TempDir()
	os.MkdirAll(filepath.Join(c.WS.KB(), "isolated"), 0o755)
	if err := os.Symlink(elsewhere, filepath.Join(c.WS.KB(), "isolated", "secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(Note{Title: "plan", Text: "the real plan", Project: "secret"}); err == nil {
		t.Error("a note was written through a link")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Error("the note landed where the link points")
	}
}

// file runs a filer whose commit is what plant leaves in its checkout, and
// returns the knowledge base's head from before.
func file(t *testing.T, c *core.Core, m *Module, plant func(dir string)) string {
	kb := c.WS.KB()
	base, _ := core.Git(kb, "rev-parse", "HEAD")
	dir := filepath.Join(t.TempDir(), "filer")
	if _, err := core.Git("", "clone", "-q", "--no-local", kb, dir); err != nil {
		t.Fatal(err)
	}
	plant(dir)
	core.Git(dir, "add", "-A")
	if _, err := core.Git(dir, "commit", "-q", "-m", "consolidate"); err != nil {
		t.Fatal(err)
	}
	r, err := c.Runs.Start(core.RunSpec{Role: "filer", Harness: "fake", Checkout: dir, Inputs: map[string]string{"base": base}})
	if err != nil {
		t.Fatal(err)
	}
	c.J.MustAppend("run.turn_ended", map[string]any{"id": r.ID, "status": core.StatusDone, "result": map[string]any{"text": "filed"}})
	m.filing = r.ID
	m.Tick()
	return base
}

// A filer that merges two notes into one, edits the README and adds a note
// has its commit taken.
func TestFilerConsolidates(t *testing.T) {
	c, m := newKB(t)
	a, _ := m.Write(Note{Title: "a", Text: "one", Project: "open"})
	b, _ := m.Write(Note{Title: "b", Text: "two", Project: "open"})
	base := file(t, c, m, func(dir string) {
		os.Remove(filepath.Join(dir, filepath.Base(b)))
		os.WriteFile(filepath.Join(dir, filepath.Base(a)), []byte("one and two"), 0o644)
		os.WriteFile(filepath.Join(dir, "README.md"), []byte("# KB"), 0o644)
		os.WriteFile(filepath.Join(dir, "c-2.md"), []byte("three"), 0o644)
	})
	if head, _ := core.Git(c.WS.KB(), "rev-parse", "HEAD"); head == base {
		t.Error("a plain consolidation was refused")
	}
	if got, _ := os.ReadFile(a); string(got) != "one and two" || core.Exists(b) {
		t.Errorf("the consolidation didn't land: %q", got)
	}
}

// Notes an isolated project left in the shared knowledge base move to its
// folder, never over a note already there.
func TestMoveIsolatedWithoutOverwriting(t *testing.T) {
	c, m := newKB(t)
	real, _ := m.Write(Note{Title: "plan", Text: "the real plan", Project: "secret"})
	kb := c.WS.KB()
	for _, name := range []string{"plan.md", "old.md"} {
		os.WriteFile(filepath.Join(kb, name), []byte("---\ntitle: x\nproject: secret\n---\n\nleft over\n"), 0o644)
	}
	core.Git(kb, "add", "-A")
	core.Git(kb, "commit", "-q", "-m", "left over")
	m.moveIsolated()
	if b, _ := os.ReadFile(real); !strings.Contains(string(b), "the real plan") {
		t.Errorf("the move replaced the isolated note: %q", b)
	}
	if core.Exists(filepath.Join(kb, "old.md")) || !core.Exists(filepath.Join(filepath.Dir(real), "old.md")) {
		t.Error("the left-over note didn't move")
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(real), "plan-2.md")); !strings.Contains(string(b), "left over") {
		t.Errorf("the clashing note didn't move under a free name: %q", b)
	}
	if out, _ := core.Git(kb, "ls-files", "plan.md"); core.Exists(filepath.Join(kb, "plan.md")) || out != "" {
		t.Error("the clashing note stayed in the shared knowledge base")
	}
	if out, _ := core.Git(kb, "log", "--all", "-p"); strings.Contains(out, "left over") {
		t.Error("the knowledge base's history still holds the moved notes")
	}
}

// A project made isolated after its notes were committed has them moved and
// purged from history at the next start, and the filer's old clones dropped.
func TestMadeIsolatedLaterPurgesHistory(t *testing.T) {
	c, m := newKB(t)
	c.Projects.Add(core.Project{Name: "later", Repo: "me/later"})
	if _, err := m.Write(Note{Title: "plan", Text: "the later plan", Project: "later"}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(c.WS.Hive(), "scratch", "filer-1")
	os.MkdirAll(clone, 0o755)
	p, _ := c.Projects.Get("later")
	p.Isolated = true
	c.Projects.Update(p)
	m.moveIsolated()
	kb := c.WS.KB()
	if !core.Exists(filepath.Join(kb, "isolated", "later", "plan.md")) {
		t.Error("the note didn't move")
	}
	if out, _ := core.Git(kb, "log", "--all", "-p"); strings.Contains(out, "the later plan") {
		t.Error("the knowledge base's history still holds the note")
	}
	if core.Exists(clone) {
		t.Error("the filer's old clone stayed")
	}
}

// A start cut short, with the notes moved but the history not yet rebuilt or
// with the old history set aside, is finished by the next start.
func TestInterruptedPurgeFinishesNextStart(t *testing.T) {
	c, m := newKB(t)
	kb := c.WS.KB()
	os.WriteFile(filepath.Join(kb, "plan.md"), []byte("---\ntitle: x\nproject: secret\n---\n\nleft over\n"), 0o644)
	core.Git(kb, "add", "-A")
	core.Git(kb, "commit", "-q", "-m", "left over")
	dir, _ := c.WS.IsolatedKB("secret")
	os.Rename(filepath.Join(kb, "plan.md"), filepath.Join(dir, "plan.md"))
	core.Git(kb, "commit", "-q", "-am", "moved") // cut short here
	m.moveIsolated()
	if out, _ := core.Git(kb, "log", "--all", "-p"); strings.Contains(out, "left over") {
		t.Error("the history kept the moved note")
	}
	old := filepath.Join(c.WS.Hive(), "kb-old-git")
	os.MkdirAll(old, 0o755) // cut short after setting the old history aside
	m.moveIsolated()
	if core.Exists(old) {
		t.Error("the old history set aside stayed")
	}
}

// A clean history is left alone.
func TestCleanHistoryIsKept(t *testing.T) {
	c, m := newKB(t)
	m.Write(Note{Title: "open", Text: "open", Project: "open"})
	m.Write(Note{Title: "plan", Text: "the plan", Project: "secret"})
	head, _ := core.Git(c.WS.KB(), "rev-parse", "HEAD")
	m.moveIsolated()
	if now, _ := core.Git(c.WS.KB(), "rev-parse", "HEAD"); now != head {
		t.Error("a clean history was rebuilt")
	}
}

// A note is never written through a link left at its name, even a dangling one.
func TestNoteSkipsADanglingLink(t *testing.T) {
	c, m := newKB(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(target, filepath.Join(c.WS.KB(), "note.md")); err != nil {
		t.Fatal(err)
	}
	path, err := m.Write(Note{Title: "note", Text: "text", Project: "open"})
	if err != nil || filepath.Base(path) != "note-2.md" {
		t.Errorf("wrote %s, %v", path, err)
	}
	if core.Exists(target) {
		t.Error("the note was written where the link points")
	}
}
