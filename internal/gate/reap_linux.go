package gate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// dieWithGate kills a child started bare when the gate dies, a crash or a
// kill included. Pdeathsig fires when the OS thread that started the child
// exits, not the process: Go ends a thread only when a goroutine that called
// runtime.LockOSThread returns still locked, and the gate starts no child
// from such a goroutine, so here the two are the same. A child started from
// a locked thread would be killed when that goroutine ends, mid-probe.
func dieWithGate(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }

// reap kills every process that belongs to the run in out: whose environment
// names a gate root under out (rootEnv, which a nested gate's own children
// carry too), or whose program or working directory is in one of the folders
// the run made there (runDirs). A daemon a child detached (setsid) has left
// the child's process group, so this, not the group, is what ends it. It
// returns how many it killed.
func reap(out string) int {
	dirs := runDirs(out)
	return sweep(func(env [][]byte, exe, cwd string) bool {
		if under(envValue(env, rootEnv), out) {
			return true
		}
		for _, d := range dirs {
			if under(exe, d) || under(cwd, d) {
				return true
			}
		}
		return false
	})
}

// reapOrphans kills what an earlier gate left when it died before it could
// reap: processes whose owning gate (ownerEnv) is no longer running.
func reapOrphans() int {
	return sweep(func(env [][]byte, _, _ string) bool {
		owner := envValue(env, ownerEnv)
		return owner != "" && !ownerAlive(owner)
	})
}

// ownerID is this process as "<pid>.<start>@<pid namespace>": a pid means
// something only in its own namespace, and only with its start time.
func ownerID() string {
	ns, _ := os.Readlink("/proc/self/ns/pid")
	pid := os.Getpid()
	return fmt.Sprintf("%d.%d@%s", pid, proc.StartTime(pid).Unix(), ns)
}

// ownerAlive says the gate named still runs. One in another pid namespace
// (inside a sandbox, or around this one) isn't this gate's to judge.
func ownerAlive(id string) bool {
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
	h := proc.Handle{PID: pid}
	if start > 0 {
		h.Started = time.Unix(start, 0)
	}
	return proc.Alive(h)
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

// sweep kills the matching processes, again until none is left: a parent
// killed in one pass may have started another first.
func sweep(match func(env [][]byte, exe, cwd string) bool) int {
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

func under(p, root string) bool {
	return p != "" && root != "" && (p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/"))
}

func envValue(env [][]byte, key string) string {
	prefix := []byte(key + "=")
	for _, kv := range env {
		if bytes.HasPrefix(kv, prefix) {
			return string(kv[len(prefix):])
		}
	}
	return ""
}
