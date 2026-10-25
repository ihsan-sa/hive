package proc

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// OwnerID is this process as "<pid>.<start>@<pid namespace>": a pid means
// something only in its own namespace, and only with its start time. A
// process tags what it starts with it, so a later sweep can end what it
// left when it died (OwnerAlive).
func OwnerID() string {
	ns, _ := os.Readlink("/proc/self/ns/pid")
	pid := os.Getpid()
	return fmt.Sprintf("%d.%d@%s", pid, StartTime(pid).Unix(), ns)
}

// OwnerAlive says the process OwnerID named still runs. One in another pid
// namespace (inside a sandbox, or around this one) isn't this one's to judge.
func OwnerAlive(id string) bool {
	at := strings.LastIndexByte(id, '@')
	if at < 0 {
		return true
	}
	if ns, _ := os.Readlink("/proc/self/ns/pid"); id[at+1:] != ns {
		return true
	}
	pidS, startS, _ := strings.Cut(id[:at], ".")
	pid, err1 := strconv.Atoi(pidS)
	start, err2 := strconv.ParseInt(startS, 10, 64)
	if err1 != nil || err2 != nil {
		return true
	}
	h := Handle{PID: pid}
	if start > 0 {
		h.Started = time.Unix(start, 0)
	}
	return Alive(h)
}

// ancestors are this process and the ones above it, which a sweep never
// kills: a nested gate runs inside a child, and the daemon's gate in its run folder.
func ancestors() map[int]bool {
	up := map[int]bool{}
	for pid := os.Getpid(); pid > 1 && !up[pid]; {
		up[pid] = true
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			break
		}
		s := string(raw)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) < 2 {
			break
		}
		pid, _ = strconv.Atoi(f[1])
	}
	return up
}

// Sweep kills this user's processes that match (by their environment,
// program and working directory), again until none is left: a parent killed
// in one pass may have started another first. It never kills this process or
// one above it. It returns how many it killed.
func Sweep(match func(env [][]byte, exe, cwd string) bool) int {
	n, keep := 0, ancestors()
	for pass := 0; pass < 10; pass++ {
		ents, _ := os.ReadDir("/proc")
		found := 0
		for _, e := range ents {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || keep[pid] || pid == 1 {
				continue
			}
			dir := filepath.Join("/proc", e.Name())
			raw, err := os.ReadFile(filepath.Join(dir, "environ"))
			if err != nil {
				continue // another user's, or gone
			}
			exe, _ := os.Readlink(filepath.Join(dir, "exe"))
			cwd, _ := os.Readlink(filepath.Join(dir, "cwd"))
			if match(bytes.Split(raw, []byte{0}), strings.TrimSuffix(exe, " (deleted)"), strings.TrimSuffix(cwd, " (deleted)")) {
				if syscall.Kill(pid, syscall.SIGKILL) == nil {
					found++
				}
			}
		}
		n += found
		if found == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return n
}
