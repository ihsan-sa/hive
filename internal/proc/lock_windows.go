package proc

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// TryLock takes an exclusive lock on f without waiting, reporting whether it
// got it. The kernel drops it when the process ends, however it ends.
func TryLock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRange())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

// Unlock drops a lock TryLock took.
func Unlock(f *os.File) { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRange()) }

// lockRange is past any byte the file holds, so the lock never blocks reading who holds it.
func lockRange() *windows.Overlapped { return &windows.Overlapped{OffsetHigh: 1} }
