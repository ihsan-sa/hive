//go:build !linux

package core

// inMemory reports whether path is in memory; only Linux's tmpfs is told.
func inMemory(string) bool { return false }
