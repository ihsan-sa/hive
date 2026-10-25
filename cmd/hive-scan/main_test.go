package main

import (
	"errors"
	"os/exec"
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
// the keys only.
func TestRepositoryPasses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if out, err := exec.Command("git", "-C", "../..", "rev-parse", "--show-toplevel").Output(); err != nil || len(out) == 0 {
		t.Skip("not a git checkout")
	}
	t.Setenv(scan.HandlesEnv, "no-such-"+"handle-zq7") // split, or this line would hit it
	t.Setenv(scan.DenyEnv, "")
	if err := run([]string{"../.."}); err != nil {
		t.Fatal(err)
	}
}
