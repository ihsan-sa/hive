//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// The helper this test runs as a check: as "check" it starts itself again as
// "child" and waits; the child writes its pid and sleeps past the timeout.
func init() {
	switch os.Getenv("HIVE_GROUP_HELPER") {
	case "check":
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), "HIVE_GROUP_HELPER=child")
		c.Start()
		time.Sleep(time.Minute)
		os.Exit(0)
	case "child":
		os.WriteFile(os.Getenv("HIVE_GROUP_PID"), []byte(strconv.Itoa(os.Getpid())), 0o644)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

// A check that times out takes what it started with it.
func TestTimedOutCheckEndsItsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	env := append(os.Environ(), "HIVE_GROUP_HELPER=check", "HIVE_GROUP_PID="+pidFile)
	if _, err := runSync([]string{os.Args[0]}, env, "", "", "", 5*time.Second, nil); err != ErrTimeout {
		t.Fatalf("runSync: %v, want a timeout", err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the check's child never started: %v", err)
	}
	pid, _ := strconv.Atoi(string(raw))
	h := proc.Handle{PID: pid, Started: proc.StartTime(pid)}
	for end := time.Now().Add(5 * time.Second); proc.Alive(h); {
		if time.Now().After(end) {
			proc.Kill(h)
			t.Fatalf("the timed-out check's child (pid %d) still runs", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
