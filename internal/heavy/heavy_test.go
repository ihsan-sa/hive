//go:build unix

package heavy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two heavy commands on one lock run one at a time: the second waits for
// the first's release, and says so.
func TestTakeWaitsForTheOneRunning(t *testing.T) {
	old := pollEvery
	pollEvery = 10 * time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	lock := filepath.Join(t.TempDir(), LockName)
	var first bytes.Buffer
	release := Take(lock, "build", 0, &first)
	if first.Len() != 0 {
		t.Fatalf("a free lock said: %s", first.String())
	}
	got := make(chan string)
	go func() {
		var say bytes.Buffer
		r := Take(lock, "suite", 0, &say)
		r()
		got <- say.String()
	}()
	select {
	case s := <-got:
		t.Fatalf("the second took a held lock: %q", s)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case s := <-got:
		if !strings.Contains(s, "suite waits") || !strings.Contains(s, "suite starts after waiting") {
			t.Errorf("the wait went unsaid: %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second never took the released lock")
	}
}

// Past its patience a command runs anyway, without the lock.
func TestTakeRunsAnywayPastPatience(t *testing.T) {
	old := pollEvery
	pollEvery = 10 * time.Millisecond
	t.Cleanup(func() { pollEvery = old })
	lock := filepath.Join(t.TempDir(), LockName)
	release := Take(lock, "build", 0, &bytes.Buffer{})
	defer release()
	var say bytes.Buffer
	Take(lock, "suite", 50*time.Millisecond, &say)()
	if !strings.Contains(say.String(), "running suite anyway") {
		t.Errorf("past patience: %q", say.String())
	}
}

// A lock file that can't be opened (a read-only mount where the daemon made
// none) never stops the command.
func TestTakeWithoutALockFileRuns(t *testing.T) {
	var say bytes.Buffer
	Take(filepath.Join(t.TempDir(), "no", "such", LockName), "build", 0, &say)()
	if !strings.Contains(say.String(), "without the machine's lock") {
		t.Errorf("said %q", say.String())
	}
}

// Main runs the command under the lock, passes its exit code, and marks its
// environment so a heavy command inside it doesn't wait on itself.
func TestMainNestsAndPassesTheExitCode(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, LockName)
	if err := Ready(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv(HeldEnv, "")
	out := filepath.Join(dir, "out")
	// The inner `sh` checks the mark the outer Main set, then fails with 3.
	code := Main([]string{"--", "sh", "-c", `printf %s "$` + HeldEnv + `" > ` + out + `; exit 3`}, lock)
	if code != 3 {
		t.Errorf("exit code %d, want 3", code)
	}
	if b, _ := os.ReadFile(out); string(b) != "held" {
		t.Errorf("the command's %s is %q", HeldEnv, b)
	}
	// Held already: a nested Main doesn't wait on the outer's lock.
	release := Take(lock, "outer", 0, &bytes.Buffer{})
	defer release()
	t.Setenv(HeldEnv, "held")
	done := make(chan int)
	go func() { done <- Main([]string{"--", "true"}, lock) }()
	select {
	case c := <-done:
		if c != 0 {
			t.Errorf("nested exit %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a nested heavy command waited on its own lock")
	}
}
