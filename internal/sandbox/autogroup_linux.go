package sandbox

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Linux's autogroups (kernel.sched_autogroup_enabled, on by default on
// Ubuntu and Fedora desktops) share the cpu between sessions first and by
// nice only inside one: every setsid starts a session whose autogroup weighs
// as much as nice 0, whatever its processes' nice. The sandbox's launcher is
// started in a session of its own, bubblewrap's --new-session starts
// another, and a harness starts one for each shell command it runs (Claude
// Code does), so a worker's pytest at nice 10 took as large a share as the
// person's whole terminal, and ten of them ten times it (10-28 ~19:20Z: load
// 110-137 on 20 cores beside the person's own sessions). So the launcher
// gives every autogroup its sandbox starts the sandbox's own level, at its
// start and then every autogroupEvery while it runs: a session made since
// runs at its full share for at most that long. Without autogroups (the
// sysctl off, or a cpu cgroup the processes sit in) the kernel keeps the
// value and ignores it, and the nice levels alone hold as before.

// autogroupEvery is how many of the launcher's one-second ticks go between
// two sweeps of the sandbox's autogroups.
const autogroupEvery = 3

// ownLevel is the calling thread's nice level (the raw getpriority answers
// 20 minus it).
func ownLevel() int {
	prio, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		return 0
	}
	return 20 - prio
}

// lowerOwnAutogroup gives the launcher's own session's autogroup level, when
// the launcher leads that session (as the daemon starts it): its init and,
// in the namespace sandbox, the command run in it. A launcher in its
// caller's session leaves it alone, as that session's autogroup is its
// caller's too (a daemon's, a test's).
func lowerOwnAutogroup(level int) {
	if sid, err := unix.Getsid(0); err == nil && sid == os.Getpid() {
		lowerAutogroup("/proc/self/autogroup", level)
	}
}

// lowerAutogroups gives level to the autogroup of every process under root
// (root itself too) that is in a session other than the launcher's, so
// started inside the sandbox; the launcher's own is lowerOwnAutogroup's.
func lowerAutogroups(root, level int) {
	own, err := unix.Getsid(0)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, pid := range descendants(root) {
		if sid, err := unix.Getsid(pid); err != nil || sid == own {
			continue
		}
		path := "/proc/" + strconv.Itoa(pid) + "/autogroup"
		if group, _ := readAutogroup(path); group != "" && !seen[group] {
			seen[group] = true
			lowerAutogroup(path, level)
		}
	}
}

// lowerAutogroup sets the autogroup at path to level when it sits above it:
// raising a level back needs a privilege the launcher doesn't have, and a
// group a command lowered further on its own keeps its level.
func lowerAutogroup(path string, level int) {
	if group, nice := readAutogroup(path); group != "" && nice < level {
		os.WriteFile(path, []byte(strconv.Itoa(level)), 0)
	}
}

// readAutogroup reads an autogroup file ("/autogroup-12 nice 0"): the group
// and its level, or "" when there is none to read.
func readAutogroup(path string) (string, int) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0
	}
	f := strings.Fields(string(raw))
	if len(f) != 3 || f[1] != "nice" {
		return "", 0
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		return "", 0
	}
	return f[0], n
}

// descendants is root and every process under it, read from each thread's
// children list (/proc/<pid>/task/<tid>/children). Only the sandbox's own
// tree is read, not the whole process table, as a launcher runs per command
// and sweeps every few seconds.
func descendants(root int) []int {
	out, next := []int{}, []int{root}
	seen := map[int]bool{}
	for len(next) > 0 {
		pid := next[len(next)-1]
		next = next[:len(next)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		dir := "/proc/" + strconv.Itoa(pid) + "/task"
		tasks, _ := os.ReadDir(dir)
		for _, t := range tasks {
			raw, _ := os.ReadFile(dir + "/" + t.Name() + "/children")
			for _, c := range strings.Fields(string(raw)) {
				if n, err := strconv.Atoi(c); err == nil {
					next = append(next, n)
				}
			}
		}
	}
	return out
}
