//go:build !unix

package gate

import "os/exec"

func killTree(cmd *exec.Cmd) {}

func stopTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
