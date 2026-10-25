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

// The repository passes its own release scan's key check, tests included, so
// a key-shaped fixture fails here rather than at the next release. The
// handle and deny lists are the owner's secrets, so only CI's scan job
// checks those.
func TestRepoPassesItsOwnScan(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if out, err := exec.Command("git", "-C", "../..", "rev-parse", "--show-toplevel").Output(); err != nil || len(out) == 0 {
		t.Skip("not a git checkout")
	}
	t.Setenv(scan.HandlesEnv, "nobody-"+"at-all-xq") // split, or this line would hit it
	t.Setenv(scan.DenyEnv, "")
	if err := run([]string{"../.."}); err != nil {
		t.Fatal(err)
	}
}
