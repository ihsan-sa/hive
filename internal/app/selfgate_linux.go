package app

import (
	"os/exec"
	"syscall"
)

// dieWithDaemon ends the gate if the daemon goes, so a restart never has
// two gates running; its sandboxes go with it (bwrap --die-with-parent).
func dieWithDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
