package sandbox

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// A sandbox-exec keeps its runtime folder (in /dev/shm, which is memory) as
// long as it runs and removes it as it ends; one killed outright can't. It
// holds a lock on runtimeLock inside the folder while it runs, so a sweep
// tells a dead one's folder from a live one's by taking that lock. A folder
// without the lock file is a moment old or was made before sandboxes took
// it; it goes only once runtimeStale old, as no turn runs that long.
const (
	runtimeLock  = "held"
	runtimeGrace = time.Minute
	runtimeStale = 24 * time.Hour
)

// holdRuntime takes the lock that says rt's sandbox still runs. The caller
// keeps the file open until it ends; the kernel lets the lock go when it dies.
func holdRuntime(rt string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(rt, runtimeLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// sweepRuntime removes the runtime folders of sandboxes that are gone.
func sweepRuntime() int {
	n := 0
	for _, base := range []string{"/dev/shm", os.TempDir()} {
		ms, _ := filepath.Glob(filepath.Join(base, "hive-sbx-*"))
		for _, rt := range ms {
			if runtimeDead(rt) && os.RemoveAll(rt) == nil {
				n++
			}
		}
	}
	return n
}

func runtimeDead(rt string) bool {
	fi, err := os.Lstat(rt)
	if err != nil || !fi.IsDir() || time.Since(fi.ModTime()) < runtimeGrace {
		return false
	}
	f, err := os.OpenFile(filepath.Join(rt, runtimeLock), os.O_RDWR, 0)
	if err != nil {
		return os.IsNotExist(err) && time.Since(fi.ModTime()) > runtimeStale
	}
	defer f.Close()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
