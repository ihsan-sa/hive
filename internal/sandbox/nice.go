package sandbox

import (
	"os/exec"
	"runtime"
	"strconv"
)

// Niced prefixes argv with `nice -n n` so the command, and everything it
// starts, runs at that lower scheduling priority: the host-side way, for a
// sandbox that starts its command from the daemon's own process (bare,
// Seatbelt). The Linux namespace sandbox sets it on its own launcher
// instead. With n 0, or no nice on this machine, argv runs as it is.
func Niced(argv []string, n int) []string {
	if n <= 0 || runtime.GOOS == "windows" {
		return argv
	}
	if _, err := exec.LookPath("nice"); err != nil {
		return argv
	}
	return append([]string{"nice", "-n", strconv.Itoa(n)}, argv...)
}
