//go:build !linux

package sandbox

// sweepRuntime is Linux's alone: only its sandbox-exec makes runtime folders.
func sweepRuntime() int { return 0 }
