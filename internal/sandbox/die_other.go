//go:build !linux

package sandbox

import "os/exec"

// dieWithDaemon is Linux's alone: elsewhere a synchronous command's group
// (or job object) is ended when it returns, and the hourly sweep removes
// what a daemon that died left.
func dieWithDaemon(*exec.Cmd) {}
