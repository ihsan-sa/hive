package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Auto maintenance after a commit, and after a push into the local forge,
// runs before git returns: a detached gc kept writing in test repos after the
// test ended, so their folders couldn't be removed (#190).
func TestGitMaintenanceStaysInForeground(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	forge := &Local{Dir: filepath.Join(dir, "forge")}
	if _, err := forge.EnsureRepo("me/p", ""); err != nil {
		t.Fatal(err)
	}
	co := filepath.Join(dir, "co")
	env := []string{"GIT_TRACE=" + trace}
	for _, args := range [][]string{{"init", "-q", "-b", "main", co}, {"-C", co, "commit", "-q", "--allow-empty", "-m", "x"},
		{"-C", co, "push", "-q", forge.RepoPath("me/p"), "HEAD:main"}} {
		if _, err := GitEnvT(time.Minute, "", env, args...); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(trace)
	if !strings.Contains(string(b), "maintenance run --auto") {
		t.Skip("this git runs no auto maintenance")
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "built-in:") && strings.HasSuffix(strings.TrimSpace(line), "--detach") {
			t.Fatalf("detached: %s", line)
		}
	}
}
