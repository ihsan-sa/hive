package main

import (
	"errors"
	"os"
	"os/exec"
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
	if _, err := os.Stat("../../.git"); err != nil {
		t.Skip("not a git checkout (an exported tarball): git ls-files has nothing to list")
	}
	t.Setenv("HIVE_SCAN_HANDLES", "no-such-handle-zq7")
	t.Setenv("HIVE_SCAN_DENY", "")
	if err := run([]string{"../.."}); err != nil {
		t.Fatal(err)
	}
}
