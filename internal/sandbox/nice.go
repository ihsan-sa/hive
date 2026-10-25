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
// instead. Both nice and argv[0] are resolved here, in the daemon's own
// environment: nice would otherwise look argv[0] up on the command's PATH,
// which carries the tools bins an agent can write to, and a program
// planted there under the command's name (sandbox-exec, say) would run on
// the host. With n 0, no nice on this machine, or a command that doesn't
// resolve, argv runs as it is, resolved by exec as before.
func Niced(argv []string, n int) []string {
	if n <= 0 || runtime.GOOS == "windows" || len(argv) == 0 {
		return argv
	}
	nice, err := exec.LookPath("nice")
	if err != nil {
		return argv
	}
	prog, err := exec.LookPath(argv[0])
	if err != nil {
		return argv
	}
	return append([]string{nice, "-n", strconv.Itoa(n), prog}, argv[1:]...)
}

// Lowered is the level a command niced by `by` runs at under a parent at
// cur: nice adds to the parent's level, and the kernel caps it at 19.
func Lowered(cur, by int) int { return min(cur+by, 19) }
