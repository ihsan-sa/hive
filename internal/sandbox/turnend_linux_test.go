//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Every process a turn starts ends with the turn, a detached one included:
// each turn is a pid namespace of its own, so nothing an earlier turn left
// running can read what the next turn is handed (a model's key file sits in
// the run's home until the open agent reads it). Runs the built binary on
// each sandbox path this host allows; ci runs it under bubblewrap (its
// BothPaths step).
func TestTurnEndsItsProcessesBothPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "hive")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hive").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, path := range []struct{ name, env string }{{"namespaces", "0"}, {"bubblewrap", "1"}} {
		t.Run(path.name, func(t *testing.T) {
			if path.env == "1" && exec.Command("bwrap", "--unshare-user", "--ro-bind", "/", "/", "true").Run() != nil {
				t.Skip("bwrap can't create namespaces here")
			}
			if path.env == "0" && (bwrapPath() != "" || exec.Command("unshare", "-Ur", "true").Run() != nil) {
				t.Skip("unprivileged user namespaces are restricted here")
			}
			dir := t.TempDir()
			work := filepath.Join(dir, "work")
			os.MkdirAll(work, 0o755)
			s := Spec{
				ID:     "turn-end",
				Argv:   []string{"/bin/sh", "-c", "setsid nohup /bin/sh -c 'sleep 1; touch /work/late' >/dev/null 2>&1 & touch /work/started"},
				Env:    InsideEnv("/tmp", false),
				Cwd:    "/work",
				Mounts: []Mount{{Host: work, Inside: "/work", RW: true}},
				Stdout: filepath.Join(dir, "out"),
				UID:    os.Getuid(), GID: os.Getgid(),
			}
			spec := filepath.Join(dir, "spec.json")
			if err := WriteSpec(spec, s); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "sandbox-exec", "--spec", spec)
			cmd.Env = append(os.Environ(), "HIVE_SANDBOX_BWRAP="+path.env)
			if out, err := cmd.CombinedOutput(); err != nil {
				e, _ := os.ReadFile(s.Stdout + ".err")
				t.Fatalf("sandbox-exec: %v %s %s", err, out, e)
			}
			if _, err := os.Stat(filepath.Join(work, "started")); err != nil {
				t.Fatal("the turn didn't run")
			}
			time.Sleep(2500 * time.Millisecond)
			if _, err := os.Stat(filepath.Join(work, "late")); err == nil {
				t.Fatal("a process the turn detached outlived it")
			}
		})
	}
}
