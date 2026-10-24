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
