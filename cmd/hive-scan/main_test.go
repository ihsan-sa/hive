package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A multi-line failure is one annotation, its lines kept.
func TestAnnotation(t *testing.T) {
	got := annotation(errors.New("the scan found 1 thing(s):\n  a.md:3: a private key (100%)"))
	want := "::error title=hive-scan::the scan found 1 thing(s):%0A  a.md:3: a private key (100%25)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The repository's own published files pass the scan, so prose that trips a
// key rule (a task id after "token:", v0.1.99-v0.1.105) fails here, before a
// release tag, not in release.yml. The handle and deny lists are secrets this
// test doesn't have; it checks the keys only.
func TestRepositoryPasses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("HIVE_SCAN_HANDLES", "no-such-handle-zq7")
	t.Setenv("HIVE_SCAN_DENY", "")
	if err := run([]string{"../.."}); err != nil {
		t.Fatal(err)
	}
}

// A failure's annotation, which anyone can read, names file, line and kind:
// never the deny word or handle it found, nor the others on either list.
func TestAnnotationKeepsTheListsSecret(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".hive/public.txt", "README.md\n")
	write("README.md", "made by Zorbleton\nsee the Quuxcorp wiki\n")
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	t.Setenv("HIVE_SCAN_HANDLES", "zorbleton, otherperson")
	t.Setenv("HIVE_SCAN_DENY", "quuxcorp\nsecretproject")
	err := run([]string{dir})
	if err == nil {
		t.Fatal("the scan passed a file with a handle and a deny word")
	}
	a := annotation(err)
	for _, want := range []string{"README.md:1: handle 1", "README.md:2: word 1"} {
		if !strings.Contains(a, want) {
			t.Errorf("the annotation doesn't say %q: %s", want, a)
		}
	}
	for _, secret := range []string{"zorbleton", "otherperson", "quuxcorp", "secretproject"} {
		if strings.Contains(strings.ToLower(a), secret) {
			t.Errorf("the annotation prints %q: %s", secret, a)
		}
	}
}
