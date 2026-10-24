//go:build !windows

package risk

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// shell runs a script in its own session: the timeout ends the script's
// children too, not just sh, and with no controlling terminal a sudo that
// wants a password fails at once instead of prompting where nobody looks.
func shell(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
