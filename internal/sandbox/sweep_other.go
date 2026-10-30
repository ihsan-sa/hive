//go:build !linux

package sandbox

// sweepRuntime is Linux's alone: only its sandbox-exec makes runtime folders.
func sweepRuntime(...string) int { return 0 }

// SettleMemory and sweepRuns are Linux's alone: only there does the daemon
// give each run a cgroup of its own (memory_linux.go).
func SettleMemory() error { return nil }
func sweepRuns() int      { return 0 }
