//go:build windows

package proc

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const (
	createNewProcessGroup  = 0x00000200
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// detachAttempts: first try to leave the caller's job object (so a run
// survives whatever started the daemon); some environments forbid that.
var detachAttempts = []uint32{createNewProcessGroup | createNoWindow | createBreakawayFromJob, createNewProcessGroup | createNoWindow}

func detach(cmd *exec.Cmd, flags uint32) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
}

func running(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// StartTime returns the process creation time, or zero.
func StartTime(pid int) time.Time {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if windows.GetProcessTimes(h, &c, &e, &k, &u) != nil {
		return time.Time{}
	}
	return time.Unix(0, c.Nanoseconds()).UTC()
}

// Kill stops the process and its children.
func Kill(h Handle) error {
	if !Alive(h) {
		return nil
	}
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(h.PID)).Run()
}

// Terminate asks politely; on Windows there is no SIGTERM for console-less
// processes, so it is the same as Kill.
func Terminate(h Handle) error { return Kill(h) }

// Group starts cmd in a process group of its own.
func Group(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createNoWindow, HideWindow: true}
}

// KillGroup kills cmd and every process under it.
func KillGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		cmd.Process.Kill()
	}
}
