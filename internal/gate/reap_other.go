//go:build !linux

package gate

import "syscall"

func dieWithGate(*syscall.SysProcAttr) {}

// reap and reapOrphans read /proc: elsewhere a child's process group is all
// the gate ends (the gate's children run on Linux).
func reap(string) int  { return 0 }
func reapOrphans() int { return 0 }
func ownerID() string  { return "" }
