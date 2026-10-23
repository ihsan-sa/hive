//go:build !windows

package risk

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A step that times out ends with its children, not just sh.
func TestTimeoutEndsTheWholeStep(t *testing.T) {
	g := newRig(t, 2)
	old := ExecTimeout
	ExecTimeout = 300 * time.Millisecond
	t.Cleanup(func() { ExecTimeout = old })
	req := g.request("a slow step", "x", "T2")
	g.decide(req, map[string]string{"decision": "do", "reason": "fine", "script": "sleep 30 & echo $! > pid; wait"})
	if got := g.settle(req.ID, "approved", "running"); got.State != "failed" {
		t.Fatalf("got %+v", got)
	}
	b, _ := os.ReadFile(filepath.Join(g.c.WS.Hive(), "risk", req.Project, req.ID, "pid"))
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if !eventually(func() bool { return gone(pid) }) {
		t.Fatalf("the step's child %d outlived the timeout", pid)
	}
}

// gone is true once pid has exited, counting a zombie that nothing reaps (a
// nested sandbox without a reaping init) as exited.
func gone(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	return err == nil && strings.Contains(string(b), ") Z ")
}
