package app

import (
	"os/exec"
	"runtime"
	"syscall"
)

// dieWithDaemon ends the gate if the daemon goes, so a restart never has
// two gates running; its sandboxes go with it (bwrap --die-with-parent).
func dieWithDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// runLocked runs cmd from an OS thread of its own for as long as it lives.
// Pdeathsig fires when the thread that started the process exits, not the
// process, and Go retires idle threads, so started from any goroutine the
// gate could be killed midway. The goroutine stays locked to its thread
// until Wait returns; it never unlocks, so Go ends that thread after, never
// hands it to other work while the gate runs.
func runLocked(cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		done <- cmd.Run()
	}()
	return <-done
}
