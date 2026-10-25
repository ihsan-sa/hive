package gate

import (
	"strings"
	"syscall"

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
// carry too), or whose program or, with byCwd, working directory is in one of
// the folders the run made there (runDirs). A daemon a child detached
// (setsid) has left the child's process group, so this, not the group, is
// what ends it. It returns how many it killed.
func reap(out string, byCwd bool) int {
	dirs := runDirs(out)
	return proc.Sweep(func(env [][]byte, exe, cwd string) bool {
		if under(proc.EnvValue(env, rootEnv), out) {
			return true
		}
		for _, d := range dirs {
			if under(exe, d) || byCwd && under(cwd, d) {
				return true
			}
		}
		return false
	})
}

// reapOrphans kills what an earlier gate left when it died before it could
// reap: processes whose owning gate (ownerEnv) is no longer running.
func reapOrphans() int {
	return proc.Sweep(func(env [][]byte, _, _ string) bool {
		owner := proc.EnvValue(env, ownerEnv)
		return owner != "" && !proc.OwnerAlive(owner)
	})
}

func ownerID() string { return proc.OwnerID() }

func under(p, root string) bool {
	return p != "" && root != "" && (p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/"))
}
