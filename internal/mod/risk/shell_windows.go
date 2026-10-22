package risk

import (
	"context"
	"os/exec"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// shell runs a script with Git for Windows' sh. The timeout ends sh; its
// children may outlive it, as Windows has no process group to kill here.
func shell(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, core.WindowsShell(), "-c", script)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
