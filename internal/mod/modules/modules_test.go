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

// TestInstallMovesInWhole: a module is cloned out of sight and moved into
// .modules/ whole, so it never lists without its files, and a failed clone
// leaves nothing behind.
func TestInstallMovesInWhole(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(ws.Hive(), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	src := filepath.Join(t.TempDir(), "ledger")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "module.json"), []byte(`{"name": "ledger"}`), 0o644)
	os.WriteFile(filepath.Join(src, "ledger.md"), []byte("# ledger\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "ledger"}} {
		if out, err := core.Git(src, args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	r := New(ws, j)
	if _, err := r.Install(filepath.Join(t.TempDir(), "missing"), "gone"); err == nil {
		t.Fatal("a clone of nothing installed")
	}
	if _, err := r.Install(src, "ledger"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(ws.Modules())
	for _, e := range entries {
		if e.Name() != "ledger" && e.Name()[0] == '.' {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
	if m, ok := r.Get("ledger"); !ok || !core.Exists(filepath.Join(m.Dir, "ledger.md")) {
		t.Fatalf("installed %+v without its file", m)
	}
	if m, _ := r.Get("ledger"); m.Source.Source != src || m.Source.Release == "" {
		t.Fatalf("installed without its source record: %+v", m.Source)
	}
	if _, ok := r.Get("gone"); ok {
		t.Fatal("the failed install lists")
	}
	// A newer commit of the source reaches the files, not only the record.
	os.WriteFile(filepath.Join(src, "ledger.md"), []byte("# ledger, newer\n"), 0o644)
	if out, err := core.Git(src, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "newer"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if changed := r.Update(); len(changed) != 1 {
		t.Fatalf("update changed %v", changed)
	}
	m, _ := r.Get("ledger")
	if got, _ := os.ReadFile(filepath.Join(m.Dir, "ledger.md")); string(got) != "# ledger, newer\n" {
		t.Fatalf("updated to %s, but the file reads %q", m.Source.Release, got)
	}
}
