package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A lead answers however busy the machine is: under a memory hold that keeps
// a worker's turn from starting, the lead's turn starts (issue #421). The
// floor asked for is more memory than any machine has, so the hold is real
// on both runs; what differs is who it holds.
func TestLeadTurnStartsUnderStrainThatHoldsAWorker(t *testing.T) {
	if core.Strained(1<<30, 0, 0) == "" {
		t.Skip("no memory figure here to hold a turn on")
	}
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 1<<30, 0, 0, 0
	ws.SaveConfig(cfg)
	c, err := core.Open(ws, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Runs.Hold("the test is over")
		for _, id := range []string{"lead", "worker"} {
			c.Runs.Interrupt(id)
		}
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			c.Tick()
			if len(c.Runs.List(func(r *core.Run) bool { return r.Status == core.StatusRunning })) == 0 {
				break
			}
		}
		c.Close()
	})
	for _, s := range []core.RunSpec{
		{ID: "worker", Role: "worker", Harness: "fake"},
		{ID: "lead", Role: "lead", Harness: "fake", Persistent: true},
	} {
		if _, err := c.Runs.Start(s); err != nil {
			t.Fatal(err)
		}
	}
	var lead, worker core.Run
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		c.Tick()
		lead, _ = c.Runs.Get("lead")
		worker, _ = c.Runs.Get("worker")
		if worker.Reason != "" && lead.Turn > 0 {
			break
		}
	}
	if !strings.HasPrefix(worker.Reason, "waiting: machine low on memory") || worker.Turn != 0 {
		t.Fatalf("the worker was not held by the machine: %s turn %d %q", worker.Status, worker.Turn, worker.Reason)
	}
	if lead.Turn != 1 {
		t.Fatalf("the lead's turn did not start under the hold: %s turn %d %q", lead.Status, lead.Turn, lead.Reason)
	}
}
