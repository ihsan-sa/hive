//go:build !linux

package app

import "os/exec"

// dieWithDaemon: only Linux has bubblewrap, so only there does a gate run.
func dieWithDaemon(cmd *exec.Cmd) {}
