package daemon

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRange())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRange()) }

// lockRange is past any byte the file holds, so the lock never blocks reading who holds it.
func lockRange() *windows.Overlapped { return &windows.Overlapped{OffsetHigh: 1} }
