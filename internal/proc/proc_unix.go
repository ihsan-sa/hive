//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
)

var detachAttempts = []uint32{0}

func detach(cmd *exec.Cmd, _ uint32) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// exists reports a pid the kernel still holds, a zombie included.
func exists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// running reports a live pid. A zombie still answers signals, and one whose
// parent never reaps it (an orphaned turn under a container's PID 1) stays so.
func running(pid int) bool { return exists(pid) && !zombie(pid) }

// signal signals h's process group while h's pid is still its own, even as a
// zombie: a leader that exited unreaped can leave children running there.
func signal(h Handle, sig syscall.Signal) error {
	if h.PID <= 0 || !exists(h.PID) || !sameStart(h) {
		return nil
	}
	return syscall.Kill(-h.PID, sig)
}

// Terminate sends SIGTERM to the process group.
func Terminate(h Handle) error { return signal(h, syscall.SIGTERM) }

// Kill sends SIGKILL to the process group.
func Kill(h Handle) error { return signal(h, syscall.SIGKILL) }

// Group starts cmd in a process group of its own, so KillGroup reaches what
// it starts (the program `go run` builds and runs, for one).
func Group(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// KillGroup kills cmd's process group.
func KillGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
