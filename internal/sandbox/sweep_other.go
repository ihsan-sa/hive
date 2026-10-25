//go:build !linux

package sandbox

// sweepRuntime is Linux's alone: only its sandbox-exec makes runtime folders.
func sweepRuntime(...string) int { return 0 }
