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

// quotaShown says statRoom's figures count a per-user quota. statfs counts a
// filesystem's own size and a project quota (as on tmpfs or XFS), never the
// user's quota: that needs quotactl on the block device, which differs by
// filesystem.
const quotaShown = false
