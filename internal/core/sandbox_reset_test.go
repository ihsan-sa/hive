package core

import (
	"errors"
	"testing"
)

// Saving a setting that isn't the sandbox keeps a working choice, and drops a
// failed one so a sandbox fixed since is probed again at once.
func TestResetSandboxDropsOnlyAFailure(t *testing.T) {
	c := &Core{sbMode: "auto"}
	c.ResetSandbox(false)
	if c.sbMode != "auto" {
		t.Fatal("a working sandbox choice was dropped")
	}
	c.sbErr = errors.New("bwrap not found")
	c.ResetSandbox(false)
	if c.sbMode != "" {
		t.Fatal("a failed sandbox choice was kept")
	}
	c.sbErr, c.sbMode = nil, "auto"
	c.ResetSandbox(true)
	if c.sbMode != "" {
		t.Fatal("changing the sandbox kept the old choice")
	}
}
