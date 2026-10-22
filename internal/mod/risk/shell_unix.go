//go:build !windows

package risk

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// shell runs a script in its own process group, so the timeout ends the
// script's children too, not just sh.
func shell(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
