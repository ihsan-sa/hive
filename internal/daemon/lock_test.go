package daemon

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A second daemon on a workspace exits at once, naming the daemon that holds
// it, whatever started that one; once that one ends, a daemon starts there.
func TestSecondDaemonOnAWorkspaceExits(t *testing.T) {
	if root := os.Getenv("HIVE_LOCK_HELPER"); root != "" {
		l, err := lockWorkspace(core.Workspace{Root: root}, 0)
		if err != nil {
			os.Stdout.WriteString("lock: " + err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString("locked\n")
		if os.Getenv("HIVE_LOCK_STOPPING") != "" {
			l.Stopping()
			time.Sleep(time.Second)
			os.Exit(0)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Hive(), 0o755)
	first := exec.Command(os.Args[0], "-test.run", "^TestSecondDaemonOnAWorkspaceExits$")
	first.Env = append(os.Environ(), "HIVE_LOCK_HELPER="+ws.Root)
	out, _ := first.StdoutPipe()
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer first.Process.Kill()
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "locked\n" {
		t.Fatalf("the first daemon didn't take the workspace: %q", line)
	}
	start := time.Now()
	err := Run(ws, 0)
	var held *AlreadyRunning
	if !errors.As(err, &held) || held.By.PID != first.Process.Pid {
		t.Fatalf("a second daemon ran on: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the second daemon took %s to exit", d)
	}
	if msg := err.Error(); !strings.Contains(msg, "pid "+strconv.Itoa(first.Process.Pid)) || !strings.Contains(msg, "started by process") {
		t.Errorf("the message doesn't name the running daemon: %s", msg)
	}
	if core.Exists(ws.DaemonFile()) {
		t.Error("the second daemon wrote daemon.json")
	}
	first.Process.Kill()
	first.Wait()
	l, err := lockWorkspace(ws, 0)
	if err != nil {
		t.Fatalf("the lock outlived its daemon: %v", err)
	}
	l.Release()
}

// A daemon started while the one before it is still shutting down (it no
// longer answers, but holds the workspace) waits for it instead of exiting.
func TestDaemonWaitsOutOneShuttingDown(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Hive(), 0o755)
	first := exec.Command(os.Args[0], "-test.run", "^TestSecondDaemonOnAWorkspaceExits$")
	first.Env = append(os.Environ(), "HIVE_LOCK_HELPER="+ws.Root, "HIVE_LOCK_STOPPING=1")
	out, _ := first.StdoutPipe()
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer first.Process.Kill()
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "locked\n" {
		t.Fatalf("the first daemon didn't take the workspace: %q", line)
	}
	time.Sleep(200 * time.Millisecond) // it marks itself stopping
	l, err := takeWorkspace(ws, 0)
	if err != nil {
		t.Fatalf("a daemon exited while the one before it shut down: %v", err)
	}
	l.Release()
	first.Wait()
}
