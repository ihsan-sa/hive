package goscope

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// module writes a small module: a imports b, c's tests import b, d's tests
// build ../a and read ../docs through a name, e embeds its notes, root
// embeds e's folder, and f imports root.
func module(t *testing.T) string {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":          "module example.com/m\n\ngo 1.21\n",
		"root.go":         "package m\n\nimport \"embed\"\n\n//go:embed e\nvar Src embed.FS\n",
		"a/a.go":          "package a\n\nimport _ \"example.com/m/b\"\n",
		"b/b.go":          "package b\n",
		"b/b_test.go":     "package b\n",
		"b/testdata/x":    "x",
		"c/c.go":          "package c\n",
		"c/c_test.go":     "package c_test\n\nimport _ \"example.com/m/b\"\n",
		"d/d_test.go":     "package d\n\nimport \"path/filepath\"\n\nconst notes = \"notes.md\"\n\nvar _ = \"../a\"\n\nfunc f() { root := filepath.Join(\"..\", \"docs\"); _ = filepath.Join(root, notes) }\n",
		"docs/notes.md":   "notes",
		"docs/other.md":   "other",
		"e/e.go":          "package e\n\nimport \"embed\"\n\n//go:embed notes.txt\nvar N embed.FS\n",
		"e/notes.txt":     "n",
		"f/f.go":          "package f\n\nimport _ \"example.com/m\"\n",
		"g/g_windows.go":  "package g\n",
		"h/h.go":          "package h\n",
		"h/sub/README.md": "readme",
		"i/i.go":          "package i\n",
		"i/lint_test.go":  "package i\n\nimport \"path/filepath\"\n\nvar _ = filepath.WalkDir(\".\", nil)\nvar _ = filepath.Join(\"notes\", \"core.md\")\n",
		"i/notes/core.md": "core",
		"i/j/j.go":        "package j\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAffected(t *testing.T) {
	dir := module(t)
	for _, tc := range []struct {
		changed []string
		want    []string
	}{
		// b's code: a imports it, c's tests do, d's tests build a.
		{[]string{"b/b.go"}, []string{"./a", "./b", "./c", "./d"}},
		// Only b's own tests: nothing that imports b sees them.
		{[]string{"b/b_test.go"}, []string{"./b"}},
		{[]string{"b/testdata/x"}, []string{"./b"}},
		// The file d's tests name through root and a constant, not its
		// neighbour; root's tests may read either, as the nearest package.
		{[]string{"docs/notes.md"}, []string{".", "./d"}},
		{[]string{"docs/other.md"}, []string{"."}},
		// An embedded file changes e's build, root's that embeds e, and f's.
		{[]string{"e/notes.txt"}, []string{".", "./e", "./f"}},
		// A file in a folder with no package belongs to the nearest above.
		{[]string{"h/sub/README.md"}, []string{"./h"}},
		// i's tests walk their own folder: a change in the package below,
		// with its own package, still runs them.
		{[]string{"i/j/j.go"}, []string{"./i", "./i/j"}},
	} {
		got, ok := Affected(dir, tc.changed)
		if !ok || !slices.Equal(got, tc.want) {
			t.Errorf("Affected(%v) = %v, %v; want %v", tc.changed, got, ok, tc.want)
		}
	}
	for _, all := range [][]string{{"go.mod"}, {"go.sum"}, {"vendor/x/y.go"}, nil} {
		if got, ok := Affected(dir, all); ok {
			t.Errorf("Affected(%v) = %v; want everything to run", all, got)
		}
	}
	// A go.mod that isn't a regular file of the checkout is never read.
	for name, bad := range map[string]func(p string) error{
		"symlink": func(p string) error {
			out := filepath.Join(t.TempDir(), "go.mod")
			os.WriteFile(out, []byte("module example.com/m\n"), 0o644)
			return os.Symlink(out, p)
		},
		"too big": func(p string) error {
			return os.WriteFile(p, append([]byte("module example.com/m\n"), make([]byte, maxGoMod)...), 0o644)
		},
	} {
		other := module(t)
		os.Remove(filepath.Join(other, "go.mod"))
		if err := bad(filepath.Join(other, "go.mod")); err != nil {
			t.Fatal(err)
		}
		if got, ok := Affected(other, []string{"b/b.go"}); ok {
			t.Errorf("go.mod %s: Affected = %v; want everything to run", name, got)
		}
	}
	// A package deleted whole: what still imports it, or whose tests do, runs.
	gone := module(t)
	os.Remove(filepath.Join(gone, "b", "b.go"))
	os.Remove(filepath.Join(gone, "b", "b_test.go"))
	if got, ok := Affected(gone, []string{"b/b.go", "b/b_test.go"}); !ok || !slices.Contains(got, "./a") || !slices.Contains(got, "./c") || slices.Contains(got, "./b") {
		t.Errorf("deleted b: Affected = %v, %v; want ./a and ./c, not ./b", got, ok)
	}
	if got, _ := Affected(dir, []string{"g/g_windows.go"}); slices.Contains(got, "./g") {
		t.Errorf("g has no file for this platform, so no package to list: %v", got)
	}
}

func TestNarrow(t *testing.T) {
	pkgs := []string{"./a", "./b"}
	got, ok := Narrow("go vet ./... &&  go test -short ./...", pkgs)
	if want := "go vet ./a ./b && go test -short ./a ./b"; !ok || got != want {
		t.Errorf("Narrow = %q, %v; want %q", got, ok, want)
	}
	for _, cmd := range []string{"make test", "go test ./... | tee log", "go test ./internal/...", "go vet ./... && ./script.sh", "go test -run 'A B' ./..."} {
		if got, ok := Narrow(cmd, pkgs); ok {
			t.Errorf("Narrow(%q) = %q; want it left as it is", cmd, got)
		}
	}
}
