package sandbox

import (
	"os/exec"
	"syscall"
)

// dieWithDaemon kills a synchronous command (a check, a tools install) when
// the daemon that started it dies, however it dies. The Hive's systemd unit
// keeps its children across a restart (KillMode=process) so agents' turns go
// on, but a check whose daemon is gone has no one to read its answer, time it
// out or remove its home: it would run on, holding its memory, for good.
// Pdeathsig fires when the thread that started the child ends; Go ends a
// thread only when a goroutine that locked it returns still locked, and the
// daemon starts no check from one.
func dieWithDaemon(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
