//go:build !linux

package sandbox

// exited can't wait without reaping here, so runSync ends the group after
// Wait: on macOS the leader's pid could in principle be reused in between, a
// window of microseconds (and on Windows, group is a job object, not an id).
func exited(int) bool { return false }
