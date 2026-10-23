package e2e

import "syscall"

func init() {
	adopt = func() { syscall.RawSyscall(syscall.SYS_PRCTL, 36, 1, 0) } // PR_SET_CHILD_SUBREAPER
}
