//go:build unix

package core

import "golang.org/x/sys/unix"

// statRoom reads the free space and size of the filesystem holding path, as
// this user may use it.
func statRoom(path string) (free, size int64, ok bool) {
	var s unix.Statfs_t
	if unix.Statfs(path, &s) != nil {
		return 0, 0, false
	}
	return int64(s.Bavail) * int64(s.Bsize), int64(s.Blocks) * int64(s.Bsize), true
}
