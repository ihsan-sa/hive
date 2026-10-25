//go:build unix

package proc

import (
	"errors"
	"os"
	"syscall"
)

// TryLock takes an exclusive lock on f without waiting, reporting whether it
// got it. The kernel drops it when the process ends, however it ends.
func TryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

// Unlock drops a lock TryLock took.
func Unlock(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
