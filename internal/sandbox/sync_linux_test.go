package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A synchronous command dies with the process that started it, killed
// outright: a check whose daemon is gone has no one to time it out.
func TestSyncCommandDiesWithItsDaemon(t *testing.T) {
	if os.Getenv("HIVE_SYNC_HELPER") == "1" {
		pidFile := os.Getenv("HIVE_SYNC_PID")
		go runSync([]string{"/bin/sh", "-c", "echo $$ > " + pidFile + "; exec sleep 300"}, nil, "", "", "", time.Hour)
		time.Sleep(time.Hour)
		return
	}
	pidFile := t.TempDir() + "/pid"
	cmd := exec.Command(os.Args[0], "-test.run=^TestSyncCommandDiesWithItsDaemon$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HIVE_SYNC_HELPER=1", "HIVE_SYNC_PID="+pidFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := waitPid(t, pidFile)
	cmd.Process.Kill()
	cmd.Wait()
	gone(t, pid)
}

// What a command leaves running in its group ends when it returns.
func TestSyncCommandLeavesNothingRunning(t *testing.T) {
	pidFile := t.TempDir() + "/pid"
	code, err := runSync([]string{"/bin/sh", "-c", "sleep 300 & echo $! > " + pidFile + "; exit 3"}, nil, "", "", "", time.Minute)
	if err != nil || code != 3 {
		t.Fatalf("code %d, %v", code, err)
	}
	gone(t, waitPid(t, pidFile))
}

func waitPid(t *testing.T, path string) int {
	t.Helper()
	for i := 0; i < 100; i++ {
		raw, _ := os.ReadFile(path)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the command never started")
	return 0
}

// gone waits for pid to end; a zombie (ended, its parent not yet reaped it) counts.
func gone(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil || syscall.Kill(pid, 0) != nil {
			return
		}
		if s := string(raw); strings.HasPrefix(strings.Fields(s[strings.LastIndexByte(s, ')')+1:])[0], "Z") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("pid %d outlived its check", pid)
}
