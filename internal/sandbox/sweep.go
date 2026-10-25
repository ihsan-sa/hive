package sandbox

import (
	"os"
	"path/filepath"
	"time"
)

// specStale is how long a spec file may wait for sandbox-exec, which reads
// and deletes it as it starts: one older was never read, and it may carry a
// short-lived token.
const specStale = time.Hour

// Sweep removes what sandboxes that died left on the host: their runtime
// folders (sweepRuntime) and spec files no sandbox-exec read. It returns how
// many it removed. The daemon's hourly clean job runs it, at start too.
func Sweep() int {
	n := sweepRuntime()
	ms, _ := filepath.Glob(filepath.Join(os.TempDir(), "hive-spec-*.json"))
	for _, m := range ms {
		if fi, err := os.Lstat(m); err == nil && time.Since(fi.ModTime()) > specStale && os.Remove(m) == nil {
			n++
		}
	}
	return n
}
