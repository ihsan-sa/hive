package modules

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// TestRemovingTheLastGrant: a module taken from every project, or from the
// only project it had, is granted to none, not to all.
func TestRemovingTheLastGrant(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(ws.Hive(), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	r := New(ws, j)
	if err := r.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	projects := func() []string { m, _ := r.Get("kb"); return m.Source.Projects }
	for _, step := range []struct {
		project string
		allow   bool
		want    []string
	}{
		{"*", false, []string{}},
		{"textkit", true, []string{"textkit"}},
		{"textkit", false, []string{}},
		{"*", true, []string{"*"}},
	} {
		if err := r.SetAccess("kb", step.project, step.allow); err != nil {
			t.Fatal(err)
		}
		granted := slices.Contains(r.Granted("textkit"), "kb")
		if got := projects(); !slices.Equal(got, step.want) || granted != (len(step.want) > 0) {
			t.Fatalf("after %s allow=%v: projects %v (textkit granted %v), want %v", step.project, step.allow, got, granted, step.want)
		}
	}
}

// TestNullProjectsFromV010: a record with "projects": null, as v0.1.0 wrote
// after the last grant was removed, grants the module to nobody.
func TestNullProjectsFromV010(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(ws.Hive(), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	r := New(ws, j)
	if err := r.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	m, _ := r.Get("kb")
	raw := `{"source":"builtin","release":"` + m.Source.Release + `","hash":"` + m.Source.Hash + `","projects":null}`
	if err := os.WriteFile(sourcePath(m.Dir), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.Granted("textkit"), "kb") {
		t.Fatal("a null projects list was read as every project")
	}
}

// TestInstallShowsOnlyWhenRecorded: a module is cloned and recorded out of
// sight and appears only whole, with its release; nothing of the clone stays
// beside it.
func TestInstallShowsOnlyWhenRecorded(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(ws.Hive(), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	r := New(ws, j)
	src := filepath.Join(t.TempDir(), "greet")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "skill.md"), []byte("PLUM42\n"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "one"}} {
		if _, err := core.Git(src, args...); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(ws.Modules(), ".other.installing", ".git"), 0o755) // another install, half there
	if _, ok := r.Get("other"); ok {
		t.Fatal("a module still installing is listed")
	}
	m, err := r.Install(src, "greet")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source.Release == "" || m.Source.Source != src {
		t.Fatalf("installed without its record: %+v", m.Source)
	}
	if core.Exists(filepath.Join(ws.Modules(), ".greet.installing")) {
		t.Fatal("the clone's staging folder stayed behind")
	}
	if _, err := r.Install(src, "greet"); err == nil {
		t.Fatal("installed over an existing module")
	}
}
