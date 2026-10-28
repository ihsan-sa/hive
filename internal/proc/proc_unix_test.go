//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A detached process leads its own session, so whatever kills the starter's
// process group on exit (launchd, a terminal) leaves it running: the daemon's
// handover child depends on it.
func TestStartDetachedLeadsItsOwnSession(t *testing.T) {
	h, err := StartDetached([]string{"sleep", "5"}, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer Kill(h)
	time.Sleep(100 * time.Millisecond)
	if sid, err := unix.Getsid(h.PID); err != nil || sid != h.PID {
		t.Fatalf("session %d, %v: want %d", sid, err, h.PID)
	}
	if pg, _ := syscall.Getpgid(h.PID); pg == syscall.Getpgrp() {
		t.Fatalf("shares the test's process group %d", pg)
	}
}

// A leader that exited unreaped is dead, but Terminate still reaches the
// child it left running in its group.
func TestTerminateReachesAZombieLeadersGroup(t *testing.T) {
	pidf := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command("sh", "-c", "sleep 60 & echo $! > "+pidf)
	detach(cmd, 0)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	h := Handle{PID: cmd.Process.Pid, Started: StartTime(cmd.Process.Pid)}
	// A loaded runner can take seconds to fork the shell and its child, so
	// each step waits on a deadline, not a fixed number of polls.
	child := 0
	ok := waitUntil(30*time.Second, func() bool {
		raw, _ := os.ReadFile(pidf)
		child, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		return child != 0 && zombie(h.PID)
	})
	if !ok || Alive(h) {
		t.Fatalf("no zombie leader with a child: child %d, leader zombie %v, alive %v", child, zombie(h.PID), Alive(h))
	}
	defer syscall.Kill(child, syscall.SIGKILL)
	if err := Terminate(h); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(30*time.Second, func() bool { return !running(child) }) {
		t.Fatal("the zombie leader's child still runs")
	}
}

// waitUntil polls cond until it holds or d passes, and says whether it held.
func waitUntil(d time.Duration, cond func() bool) bool {
	for end := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		if cond() {
			return true
		}
		if time.Now().After(end) {
			return cond()
		}
	}
}
