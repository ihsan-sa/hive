package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// A multi-line failure is one annotation, its lines kept.
func TestAnnotation(t *testing.T) {
	got := annotation(errors.New("the scan found 1 thing(s):\n  a.md:3: a private key (100%)"))
	want := "::error title=hive-scan::the scan found 1 thing(s):%0A  a.md:3: a private key (100%25)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The repository's own published files, tests included, pass the scan, so
// prose that trips a key rule (a task id after "token:", v0.1.99-v0.1.105) or
// a key-shaped fixture fails here, before a release tag, not in release.yml.
// The handle and deny lists are secrets this test doesn't have; it checks
// the keys only. A tree that is no git checkout of its own (a copy, as the
// recursive gate tests in) is read file by file, its .git aside.
func TestRepositoryPasses(t *testing.T) {
	t.Setenv(scan.HandlesEnv, "no-such-"+"handle-zq7") // split, or this line would hit it
	t.Setenv(scan.DenyEnv, "")
	root, _ := filepath.Abs("../..")
	if top, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output(); err == nil && sameDir(strings.TrimSpace(string(top)), root) {
		if err := run([]string{root}); err != nil {
			t.Fatal(err)
		}
		return
	}
	var names []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	public, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(scan.PublicList)))
	read := func(n string) ([]byte, error) { return os.ReadFile(filepath.Join(root, filepath.FromSlash(n))) }
	if err := scan.Run(names, read, scan.Lists{Public: scan.ParseList(string(public), false), Handles: []string{os.Getenv(scan.HandlesEnv)}}); err != nil {
		t.Fatal(err)
	}
}

func sameDir(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
