package openagent

import "golang.org/x/sys/unix"

// noDump keeps the open agent's memory, which holds the model's key, from a
// tool it runs under the same user: /proc/<pid>/mem and ptrace need the
// process dumpable.
func noDump() { unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }
