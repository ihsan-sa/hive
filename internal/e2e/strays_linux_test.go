package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A daemon's process group isn't all it started: module programs lead groups
// of their own and turns sessions of their own, so a daemon killed as a crash
// leaves them running, still writing under .hive (the kb's dream under
// .hive/modules), and t.TempDir's removal fails with "directory not empty"
// (TestControlCenterFollowsARestart, issue #664). Whatever still works in the
// test's folder is found in /proc instead: its working directory, executable,
// an open file, its command line or its environment names a path in it. A
// sandboxed turn is found by its bwrap, whose binds name the folder and whose
// death ends the sandbox.
func init() {
	strays = func(dir string) {
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
			pids := within(dir)
			if len(pids) == 0 {
				return
			}
			for _, pid := range pids {
				syscall.Kill(pid, syscall.SIGKILL)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// within lists the processes other than this one that use a path in dir.
func within(dir string) []int {
	ents, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		if uses(filepath.Join("/proc", e.Name()), dir) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func uses(p, dir string) bool {
	in := func(path string) bool { return path == dir || strings.HasPrefix(path, dir+"/") }
	for _, l := range []string{"cwd", "exe"} {
		if t, err := os.Readlink(filepath.Join(p, l)); err == nil && in(t) {
			return true
		}
	}
	for _, f := range []string{"cmdline", "environ"} {
		if raw, err := os.ReadFile(filepath.Join(p, f)); err == nil && strings.Contains(string(raw), dir) {
			return true
		}
	}
	fds, _ := os.ReadDir(filepath.Join(p, "fd"))
	for _, fd := range fds {
		if t, err := os.Readlink(filepath.Join(p, "fd", fd.Name())); err == nil && in(t) {
			return true
		}
	}
	return false
}

// TestStraysEnded: a process in a session of its own, writing in a test's
// folder as an orphaned module program does, is ended by settle, and the
// folder goes, where the daemon's group kill alone left it behind.
func TestStraysEnded(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "hive", ".hive", "modules", "kb")
	os.MkdirAll(work, 0o755)
	cmd := exec.Command("sh", "-c", "while :; do mkdir -p a; touch a/$$; sleep 0.01; done")
	cmd.Dir = work
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	settle(dir, filepath.Join(dir, "hive"))
	if len(within(dir)) > 0 || core.Exists(filepath.Join(dir, "hive")) {
		t.Fatal("the stray still works in the test's folder")
	}
}
