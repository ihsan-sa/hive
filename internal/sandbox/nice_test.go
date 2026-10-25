package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A niced command runs under `nice -n` where there is one, both resolved
// to their paths in the daemon's environment; at 0 it runs as it is.
func TestNicedPrefixesArgv(t *testing.T) {
	argv := []string{"sh", "-c", "true"}
	if got := Niced(argv, 0); !slices.Equal(got, argv) {
		t.Errorf("nice 0: %q, want argv as it is", got)
	}
	got := Niced(argv, 10)
	nice, err := exec.LookPath("nice")
	if runtime.GOOS == "windows" || err != nil {
		if !slices.Equal(got, argv) {
			t.Errorf("no nice here: %q, want argv as it is", got)
		}
		return
	}
	sh, _ := exec.LookPath("sh")
	if !slices.Equal(got, []string{nice, "-n", "10", sh, "-c", "true"}) {
		t.Errorf("nice 10: %q, want %q", got, []string{nice, "-n", "10", sh, "-c", "true"})
	}
	if got := Niced([]string{"no-such-program-here"}, 10); !slices.Equal(got, []string{"no-such-program-here"}) {
		t.Errorf("a command that doesn't resolve: %q, want as it is", got)
	}
}

// The command a niced argv names is the one the daemon's own PATH finds,
// never one an agent planted under its name in a tools bin that the
// command's PATH would put first: that one would run on the host.
func TestNicedResolvesInTheDaemonsPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no nice on Windows")
	}
	if _, err := exec.LookPath("nice"); err != nil {
		t.Skip("no nice here")
	}
	dir := t.TempDir()
	host, planted := filepath.Join(dir, "host-bin"), filepath.Join(dir, "tools", "python", "bin")
	for _, d := range []string{host, planted} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "sandbox-exec"), []byte("#!/bin/sh\n"), 0o755)
	}
	t.Setenv("PATH", host+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The command's own PATH, with the tools bin first, is what nice would
	// search; Niced doesn't give it the chance.
	spec := Spec{Argv: []string{"sandbox-exec", "-f", "profile"}, Tools: []string{filepath.Join(planted, "python")}}
	if env := withPathPrefix([]string{"PATH=" + host}, pathDirs(spec)); !strings.HasPrefix(env[0], "PATH="+planted+string(os.PathListSeparator)) {
		t.Fatalf("the command's PATH %q, want the tools bin first", env[0])
	}
	got := Niced(spec.Argv, 10)
	if len(got) != 6 || got[3] != filepath.Join(host, "sandbox-exec") || !filepath.IsAbs(got[0]) {
		t.Fatalf("niced %q: %q, want the daemon's %s", spec.Argv, got, filepath.Join(host, "sandbox-exec"))
	}
	if s := strings.Join(got, " "); strings.Contains(s, planted) {
		t.Fatalf("the planted program was chosen: %q", got)
	}
}
