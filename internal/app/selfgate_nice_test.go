//go:build !windows

package app

import "syscall"

// setSelfNice puts the test process at nice level n (a process can only go
// higher), standing for a daemon that already runs niced.
func setSelfNice(n int) error { return syscall.Setpriority(syscall.PRIO_PROCESS, 0, n) }
