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
)

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
	child := 0
	for i := 0; i < 200 && (child == 0 || !zombie(h.PID)); i++ {
		time.Sleep(20 * time.Millisecond)
		raw, _ := os.ReadFile(pidf)
		child, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	if child == 0 || Alive(h) {
		t.Fatalf("no zombie leader with a child: child %d, alive %v", child, Alive(h))
	}
	defer syscall.Kill(child, syscall.SIGKILL)
	if err := Terminate(h); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200 && running(child); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if running(child) {
		t.Fatal("the zombie leader's child still runs")
	}
}
