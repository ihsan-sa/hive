//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTool puts an executable named claude, a link to a versioned file as
// the real installer leaves it, in a dir of its own on the test's PATH only.
func fakeTool(t *testing.T) string {
	dir := t.TempDir()
	real := filepath.Join(dir, "claude-2.1.0")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho tool ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir
}

// An executor module gets each tool by the absolute path the daemon finds it
// at, under its own name, so a program with another PATH and HOME finds it;
// a system tool stays bare, to run where it is inside.
func TestDelegateNamesToolsByAbsolutePath(t *testing.T) {
	dir := fakeTool(t)
	work := t.TempDir()
	got := filepath.Join(work, "spec.json")
	d := &Delegate{Module: "dispatch", Argv: []string{"/bin/sh", "-c", `cp "$3" "$0"`, got}, Env: []string{"PATH=/usr/bin:/bin", "HOME=" + work}, Dir: work}
	if _, err := d.Run(Spec{Tools: []string{"claude", "sh", "/abs/hive"}, Stdout: filepath.Join(work, "out")}, time.Minute); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSpec(got)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, "claude"), "sh", "/abs/hive"}; strings.Join(s.Tools, " ") != strings.Join(want, " ") {
		t.Fatalf("tools handed over: %q, want %q", s.Tools, want)
	}
}

// The fallback an executor takes when no runner has room, `hive
// sandbox-exec` with the module's PATH and HOME, finds the tool the daemon
// found: 142 dispatch turns ended 'tool "claude" not found' without it.
func TestDelegateFallbackFindsTools(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	if bwrapPath() != "" && exec.Command("bwrap", "--unshare-user", "--ro-bind", "/", "/", "true").Run() != nil ||
		bwrapPath() == "" && exec.Command("unshare", "-Ur", "true").Run() != nil {
		t.Skip("no sandbox path on this host")
	}
	fakeTool(t)
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "hive")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hive").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	work := filepath.Join(tmp, "work")
	os.MkdirAll(work, 0o755)
	s := Spec{
		ID:     "fallback",
		Argv:   []string{"/bin/sh", "-c", "claude > /work/ran"},
		Env:    InsideEnv("/tmp", false),
		Cwd:    "/work",
		Mounts: []Mount{{Host: work, Inside: "/work", RW: true}},
		Tools:  []string{"claude"},
		Stdout: filepath.Join(tmp, "out"),
		UID:    os.Getuid(), GID: os.Getgid(),
	}
	// The module's program: `turn --spec F` run here, as dispatch does.
	d := &Delegate{Module: "dispatch", Argv: []string{"/bin/sh", "-c", `exec "$0" sandbox-exec --spec "$3"`, bin},
		Env: []string{"PATH=/usr/bin:/bin", "HOME=" + tmp}, Dir: tmp}
	code, err := d.Run(s, 2*time.Minute)
	if err != nil || code != 0 {
		e, _ := os.ReadFile(s.Stdout + ".err")
		o, _ := os.ReadFile(s.Stdout + ".dispatch")
		t.Fatalf("fallback: code %d %v %s %s", code, err, e, o)
	}
	if out, _ := os.ReadFile(filepath.Join(work, "ran")); strings.TrimSpace(string(out)) != "tool ran" {
		t.Fatalf("the tool didn't run inside: %q", out)
	}
}
