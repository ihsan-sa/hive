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
	if _, err := forge.EnsureRepo("me/p", "", false); err != nil {
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

// A lock left by a git that died (the machine ran out of memory) stalled every
// later checkout in the project clone (#440): a stale one is cleared and the
// call retried; a fresh one, maybe a live git's, is left alone.
func TestGitClearsStaleLock(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "x"}} {
		if _, err := Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	lock := filepath.Join(dir, ".git", "refs", "heads", "main.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Git(dir, "checkout", "-q", "-f", "-B", "main", "HEAD"); err == nil || !strings.Contains(err.Error(), "File exists") {
		t.Fatalf("a fresh lock was cleared: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(lock, old, old)
	if _, err := Git(dir, "checkout", "-q", "-f", "-B", "main", "HEAD"); err != nil {
		t.Fatalf("stale lock still stalls git: %v", err)
	}
	if Exists(lock) {
		t.Fatal("stale lock left in place")
	}
	if clearStaleLock(dir, "Unable to create '"+filepath.Join(t.TempDir(), "x.lock")+"': File exists.") {
		t.Fatal("cleared a lock outside the repo's .git")
	}
}

// A call fixed by the commits it names runs git once; a ref, a single-commit
// diff (against the working tree) or merge-tree runs every time, and an
// ancestry's "no" (exit 1) is kept like its "yes".
func TestGitMemoPureCalls(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, args := range [][]string{{"merge-base", "--is-ancestor", a, b}, {"diff", "--binary", a, b}, {"show", a + ":x/y"}, {"show", a}} {
		if !pureGit(args) {
			t.Errorf("%v isn't taken as pure", args)
		}
	}
	for _, args := range [][]string{{"merge-base", "--is-ancestor", "refs/heads/main", b}, {"diff", "--quiet", a}, {"merge-tree", "--write-tree", a, b}, {"diff", a, b, "--", "x"}, {"show", "main:x"}, {"-c", "x=y", "diff", a, b}, {"rev-parse", a}} {
		if pureGit(args) {
			t.Errorf("%v is taken as pure", args)
		}
	}
	dir := t.TempDir()
	must := func(args ...string) string {
		out, err := Git(dir, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	must("init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("1"), 0o644)
	must("add", "f")
	must("commit", "-q", "-m", "1")
	one := must("rev-parse", "HEAD")
	os.WriteFile(filepath.Join(dir, "f"), []byte("2"), 0o644)
	must("commit", "-q", "-am", "2")
	two := must("rev-parse", "HEAD")
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE", trace)
	for i := 0; i < 3; i++ {
		if _, err := Git(dir, "merge-base", "--is-ancestor", two, one); err == nil {
			t.Fatal("the later commit is taken for an ancestor")
		}
		if out, _ := Git(dir, "show", one+":f"); out != "1" {
			t.Fatalf("show %q", out)
		}
		Git(dir, "rev-parse", "HEAD")
	}
	got, _ := os.ReadFile(trace)
	for want, n := range map[string]int{"git merge-base ": 1, "git show ": 1, "git rev-parse ": 3} {
		if c := strings.Count(string(got), want); c != n {
			t.Errorf("%s ran %d times, want %d", want, c, n)
		}
	}
}
