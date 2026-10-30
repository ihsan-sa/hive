package sandbox

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// diskUsed reads a waited-for process's block I/O from its rusage, which
// counts in 512-byte blocks and takes in every child it waited for in turn
// (a namespace's pid 1 reaps every orphan, so a check's whole tree). A child
// some parent between never waited for takes its count with it, so
// sandbox-exec adopts bwrap's reaper (adoptOrphans).
func diskUsed(ps *os.ProcessState) Disk {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return Disk{}
	}
	return Disk{Read: int64(ru.Inblock) * 512, Wrote: int64(ru.Oublock) * 512}
}

// adoptWait bounds how long reapAdopted waits for an adopted child to end:
// bwrap's reaper is ending as bwrap exits (--die-with-parent kills it if not).
const adoptWait = 5 * time.Second

// adoptOrphans makes this process the one every orphan below it is
// reparented to, so reapAdopted can wait for them and their rusage reaches
// whoever waits for this process.
func adoptOrphans() error { return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }

// reapAdopted waits for every child left, up to max: none left returns at once.
func reapAdopted(max time.Duration) {
	end := time.Now().Add(max)
	for {
		var ws syscall.WaitStatus
		got, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		switch {
		case err == syscall.EINTR || got > 0:
			continue
		case err != nil || time.Now().After(end):
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
