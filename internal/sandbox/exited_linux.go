package sandbox

import "golang.org/x/sys/unix"

// exited waits for pid to exit but leaves it unreaped, so its pid, and with
// it its process group's id, can't be given to another process until Wait
// reaps it: group can kill what is left of the group without the chance of
// hitting an unrelated one. It reports whether pid exited.
func exited(pid int) bool {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return err == nil
		}
	}
}
