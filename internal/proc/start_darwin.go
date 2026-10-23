//go:build darwin

package proc

import (
	"time"

	"golang.org/x/sys/unix"
)

// StartTime returns when pid started, from the kernel's process table.
func StartTime(pid int) time.Time {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}
	}
	tv := kp.Proc.P_starttime
	return time.Unix(int64(tv.Sec), int64(tv.Usec)*1000).UTC()
}

// zombie reports a process that has exited but not been reaped.
func zombie(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && kp.Proc.P_stat == 5 // SZOMB
}
