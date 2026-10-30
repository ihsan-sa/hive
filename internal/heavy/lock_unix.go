//go:build unix

package heavy

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes f's exclusive flock if no one holds it.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
