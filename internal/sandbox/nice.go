package sandbox

import (
	"log"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Niced prefixes argv with `nice -n n` so the command, and everything it
// starts, runs at that lower scheduling priority: the host-side way, for a
// sandbox that starts its command from the daemon's own process (bare,
// Seatbelt). The Linux namespace sandbox sets it on its own launcher
// instead. Both nice and argv[0] are resolved here, in the daemon's own
// environment: nice would otherwise look argv[0] up on the command's PATH,
// which carries the tools bins an agent can write to, and a program
// planted there under the command's name (sandbox-exec, say) would run on
// the host. With n 0, no nice on this machine (said once in daemon.log), a
// command that doesn't resolve, or a relative path to one (./x: exec finds
// it from the command's own directory, which LookPath here wouldn't), argv
// runs as it is, resolved by exec as before.
func Niced(argv []string, n int) []string {
	if n <= 0 || runtime.GOOS == "windows" || len(argv) == 0 {
		return argv
	}
	if !filepath.IsAbs(argv[0]) && strings.ContainsRune(argv[0], filepath.Separator) {
		return argv
	}
	nice, err := exec.LookPath("nice")
	if err != nil {
		noNice.Do(func() {
			log.Printf("sandbox: no nice on the daemon's PATH, so commands run at the daemon's own priority")
		})
		return argv
	}
	prog, err := LookTool(argv[0])
	if err != nil {
		return argv
	}
	return append([]string{nice, "-n", strconv.Itoa(n), prog}, argv[1:]...)
}

var noNice sync.Once

// Lowered is the level a command niced by `by` runs at under a parent at
// cur: nice adds to the parent's level, and the kernel caps it at 19.
func Lowered(cur, by int) int { return min(cur+by, 19) }
