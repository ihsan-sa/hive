package kb

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

func newKB(t *testing.T) (*core.Core, *Module) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
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
	} {
		t.Run(name, func(t *testing.T) {
			c, m := newKB(t)
			path, err := m.Write(Note{Title: "plan", Text: "the real plan", Project: "secret"})
			if err != nil {
				t.Fatal(err)
			}
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
