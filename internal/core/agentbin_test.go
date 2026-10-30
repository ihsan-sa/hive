package core

import (
	"os"
	"path/filepath"
	"testing"
)

// A test workspace's hive is the test binary hardlinked, not a fresh copy.
func TestAgentBinaryLinksInTests(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "hive-v0")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Core{WS: Workspace{Root: filepath.Join(dir, "ws")}}
	dst := c.agentBinary(exe)
	if dst != filepath.Join(c.WS.Bin(), "hive") {
		t.Fatalf("agent binary %s", dst)
	}
	a, _ := os.Stat(exe)
	b, err := os.Stat(dst)
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("%s is not a link of %s (%v)", dst, exe, err)
	}
	if again := c.agentBinary(exe); again != dst {
		t.Fatalf("second call gave %s", again)
	}
}
