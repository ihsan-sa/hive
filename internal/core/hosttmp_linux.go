package core

import "golang.org/x/sys/unix"

// inMemory reports whether path is on a tmpfs, whose files are memory.
func inMemory(path string) bool {
	var s unix.Statfs_t
	return unix.Statfs(path, &s) == nil && s.Type == unix.TMPFS_MAGIC
}
