package sandbox

import (
	"os"
	"path/filepath"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// specStale is how long a spec file may wait for sandbox-exec, which reads
// and deletes it as it starts: one older was never read, and it may carry a
// short-lived token.
const specStale = time.Hour

// Sweep removes what sandboxes that died left on the host: their runtime
// folders (sweepRuntime), spec files no sandbox-exec read, and the processes
// a bare command left when its daemon died (OwnerEnv). It returns how many it
// removed. The daemon's hourly clean job runs it, at start too.
//
// /dev/shm and the temp directory are shared by every Hive on the machine,
// and so is the process table: what another live Hive's sandbox holds is
// kept by its lock (runtimeDead), its spec files by their age, and its bare
// commands' processes because their daemon still runs.
func Sweep() int { return sweepOrphans() + SweepIn("/dev/shm", os.TempDir()) }

// sweepOrphans kills the processes bare commands left whose daemon is gone.
func sweepOrphans() int {
	return proc.Sweep(func(env [][]byte, _, _ string) bool {
		o := proc.EnvValue(env, OwnerEnv)
		return o != "" && !proc.OwnerAlive(o)
	})
}

// SweepIn is Sweep's part on disk, with shm and tmp in place of /dev/shm and
// the temp directory, so a test can point it away from the host's.
func SweepIn(shm, tmp string) int {
	n := sweepRuntime(shm, tmp)
	ms, _ := filepath.Glob(filepath.Join(tmp, "hive-spec-*.json"))
	for _, m := range ms {
		if fi, err := os.Lstat(m); err == nil && time.Since(fi.ModTime()) > specStale && os.Remove(m) == nil {
			n++
		}
	}
	return n
}
