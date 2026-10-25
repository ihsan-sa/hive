//go:build !windows

package sandbox

import (
	"os/exec"
	"syscall"
)

// group ends what is left of a synchronous command's process group once it
// returns: a check's background job, or a test's helper that outlived it.
// runSync starts the command as the leader of a group of its own (syncAttr).
// A daemon that called setsid has left the group; inside the Linux sandbox
// the namespace's pid 1 ends those with it.
func group(pid int) func(kill bool) {
	return func(kill bool) {
		if kill {
			syscall.Kill(-pid, syscall.SIGKILL)
		}
	}
}

// ownGroup puts cmd in a process group of its own, so group can end it.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
