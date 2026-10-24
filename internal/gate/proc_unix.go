//go:build unix

package gate

import (
	"os/exec"
	"syscall"
)

// killTree makes the command the head of its own process group and has a
// deadline kill the whole group, not only the process: a child's daemon and
// agents go with it. Under bubblewrap, killing bwrap ends its pid namespace,
// so nothing of the child outlives it even past a setsid.
func killTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// stopTree kills a started command's whole process group.
func stopTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
