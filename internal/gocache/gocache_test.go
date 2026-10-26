package gocache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The test binary is the cache program when the go command starts it.
func TestMain(m *testing.M) {
	if args := os.Getenv("GOCACHE_TEST_PROG"); args != "" {
		os.Exit(Main(strings.Split(args, "|")))
	}
	os.Exit(m.Run())
}

func tree(t *testing.T, dir string) map[string]bool {
	files := map[string]bool{}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files[p] = true
		}
		return nil
	})
	return files
}

// A check of main fills the shared cache with plain GOCACHE; a check of
// another commit then reuses its builds and passed tests through the
// program, writes only its own folder, and still builds what changed.
func TestChecksReuseMainsCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	dir := t.TempDir()
	mod := filepath.Join(dir, "mod")
	shared, own := filepath.Join(dir, "shared"), filepath.Join(dir, "own")
	os.MkdirAll(mod, 0o755)
	os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(mod, "m.go"), []byte("package m\n\nfunc One() int { return 1 }\n"), 0o644)
	os.WriteFile(filepath.Join(mod, "m_test.go"), []byte("package m\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {\n\tif One() != 1 {\n\t\tt.Fatal(1)\n\t}\n}\n"), 0o644)
	self, _ := os.Executable()
	run := func(env ...string) string {
		cmd := exec.Command("go", "test", "./...")
		cmd.Dir = mod
		cmd.Env = append(os.Environ(), append([]string{"GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local"}, env...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go test: %v\n%s", err, out)
		}
		return string(out)
	}
	if out := run("GOCACHE=" + shared); strings.Contains(out, "(cached)") {
		t.Fatalf("a fresh cache gave a cached result: %s", out)
	}
	before := tree(t, shared)
	prog := []string{"GOCACHE=" + filepath.Join(dir, "gocache"), "GOCACHEPROG=" + self, "GOCACHE_TEST_PROG=" + shared + "|" + own}
	if out := run(prog...); !strings.Contains(out, "(cached)") {
		t.Fatalf("main's passed test wasn't reused through the program: %s", out)
	}
	if after := tree(t, shared); len(after) != len(before) {
		t.Fatalf("the program wrote to the shared cache: %d files, then %d", len(before), len(after))
	}
	os.WriteFile(filepath.Join(mod, "m.go"), []byte("package m\n\nfunc One() int { println(\"changed\"); return 1 }\n"), 0o644)
	if out := run(prog...); strings.Contains(out, "(cached)") {
		t.Fatalf("changed code reused main's result: %s", out)
	}
	if len(tree(t, own)) == 0 {
		t.Fatal("the changed build kept nothing in the check's own folder")
	}
	if out := run(prog...); !strings.Contains(out, "(cached)") {
		t.Fatalf("the check's own builds weren't reused within it: %s", out)
	}
}

func TestLookupRefusesWhatIsntAnEntry(t *testing.T) {
	dir := t.TempDir()
	id, out := make([]byte, 32), make([]byte, 32)
	id[0], out[0] = 1, 2
	if _, err := put(dir, id, out, []byte("body")); err != nil {
		t.Fatal(err)
	}
	if e, ok := lookup(dir, id); !ok || e.size != 4 || string(e.out) != string(out) {
		t.Fatalf("lookup of a put: %+v %v", e, ok)
	}
	os.WriteFile(fileName(dir, out, "d"), []byte("longer body"), 0o644)
	if _, ok := lookup(dir, id); ok {
		t.Fatal("an output of the wrong size was served")
	}
	os.Remove(fileName(dir, out, "d"))
	os.Mkdir(fileName(dir, out, "d"), 0o755) // a cached executable
	if _, ok := lookup(dir, id); ok {
		t.Fatal("a folder was served as an output")
	}
	other := make([]byte, 32)
	other[0] = 1
	other[31] = 9
	os.WriteFile(fileName(dir, other, "a"), []byte("v1 garbage\n"), 0o644)
	if _, ok := lookup(dir, other); ok {
		t.Fatal("a malformed entry was served")
	}
	if _, ok := lookup("", id); ok {
		t.Fatal("no shared folder served something")
	}
}
