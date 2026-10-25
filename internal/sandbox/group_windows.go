package sandbox

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// group puts pid in a job object, so a timed-out check's children (its sh
// and what that started) end with it: Windows has no process group to kill.
// The returned func ends what is still in the job when kill is set, and
// lets the job go either way. What pid starts before it is in the job, in
// the moment after its start, isn't caught; nor is anything when the job
// can't be made (a person's machine then keeps the old behaviour).
func group(pid int) func(kill bool) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func(bool) {}
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, p)
		windows.CloseHandle(p)
	}
	if err != nil {
		windows.CloseHandle(job)
		return func(bool) {}
	}
	return func(kill bool) {
		if kill {
			windows.TerminateJobObject(job, 1)
		}
		windows.CloseHandle(job)
	}
}

// ownGroup is a no-op here: group puts the process in a job object instead.
func ownGroup(*exec.Cmd) {}
