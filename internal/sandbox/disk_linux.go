package sandbox

import (
	"os"
	"syscall"
)

// diskUsed reads a waited-for process's block I/O from its rusage, which
// counts in 512-byte blocks and takes in every child it waited for in turn
// (a namespace's pid 1 reaps every orphan, so a check's whole tree).
func diskUsed(ps *os.ProcessState) Disk {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return Disk{}
	}
	return Disk{Read: int64(ru.Inblock) * 512, Wrote: int64(ru.Oublock) * 512}
}
