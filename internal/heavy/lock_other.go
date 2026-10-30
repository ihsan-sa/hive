//go:build !unix

package heavy

import (
	"errors"
	"os"
)

// tryLock has no flock here: the command runs without the lock.
func tryLock(*os.File) (bool, error) { return false, errors.New("no flock on this system") }
