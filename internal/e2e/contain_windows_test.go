package e2e

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows a killed daemon's children live on: a check's sh and what it
// started, a detached turn. They keep files in the test's TempDir open, and
// Windows won't delete an open file, so the test fails at cleanup. Each
// daemon goes in a job of its own (a run's attempt to break away is refused,
// so it falls back to staying in), and the test's cleanup ends the job. A
// crash in a test kills the daemon alone, so its runs outlive it as they do
// on a person's machine.
func init() {
	contain = func(pid int) func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return func() {}
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
		if err == nil {
			windows.AssignProcessToJobObject(job, p)
			windows.CloseHandle(p)
		}
		return func() {
			windows.TerminateJobObject(job, 1)
			// Ended processes let go of their files a moment later.
			for end := time.Now().Add(10 * time.Second); active(job) > 0 && time.Now().Before(end); {
				time.Sleep(100 * time.Millisecond)
			}
			windows.CloseHandle(job)
		}
	}
}

// active counts the processes still in job.
func active(job windows.Handle) uint32 {
	var acct struct { // JOBOBJECT_BASIC_ACCOUNTING_INFORMATION
		TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
		TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
	}
	if windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&acct)), uint32(unsafe.Sizeof(acct)), nil) != nil {
		return 0
	}
	return acct.ActiveProcesses
}
