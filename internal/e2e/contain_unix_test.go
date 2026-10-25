//go:build !windows

package e2e

import (
	"os/exec"
	"syscall"
	"time"
)

// On Unix a killed daemon's children live on too: the git that seeds the
// knowledge base with the Hive's own source keeps writing kb/.git while the
// test's TempDir is removed, which then fails with "directory not empty".
// Each daemon leads a process group of its own, and the test's cleanup kills
// what is left in it once the daemon has gone. Turns start in sessions of
// their own, so a crash in a test still kills the daemon alone.
func init() {
	group = func(cmd *exec.Cmd) func() {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return func() {
			if cmd.Process == nil {
				return
			}
			// SIGKILL lands at once but a process takes a moment to end:
			// wait for the group to empty (a zombie no one reaps counts as
			// still in it, so at most a few seconds).
			pg := -cmd.Process.Pid
			syscall.Kill(pg, syscall.SIGKILL)
			for end := time.Now().Add(5 * time.Second); syscall.Kill(pg, 0) == nil && time.Now().Before(end); {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
}
