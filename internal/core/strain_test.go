package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// The budget holds a turn once the memory left can't take it beside the ones
// launching: in the Hive's cgroup when it has a limit, else on the machine,
// and never with the budget off or nothing to read.
func TestMemBudget(t *testing.T) {
	proc, root := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(body), 0o644)
	}
	if why := memBudget(proc, root, DefaultTurnMB, 0); why != "" {
		t.Fatalf("held with nothing to read: %q", why)
	}
	// No cgroup limit: the machine's 1000 MB, 600 MB of it available.
	write(proc+"/self/cgroup", "0::/hive.service\n")
	write(root+"/hive.service/memory.max", "max\n")
	write(proc+"/meminfo", "MemTotal:       1024000 kB\nMemFree:  1 kB\nMemAvailable:    614400 kB\n")
	if why := memBudget(proc, root, 240, 1); why != "" {
		t.Fatalf("two turns of 240 MB fit in 600 MB: %q", why)
	}
	if why := memBudget(proc, root, 240, 2); !strings.HasPrefix(why, MemWait) {
		t.Fatalf("a third didn't wait: %q", why)
	}
	if why := memBudget(proc, root, 0, 99); why != "" {
		t.Fatalf("the budget off held: %q", why)
	}
	// A cgroup limit of 1000 MB, 900 MB used of which 400 MB is cache.
	write(root+"/hive.service/memory.max", "1048576000\n")
	write(root+"/hive.service/memory.current", "943718400\n")
	write(root+"/hive.service/memory.stat", "anon 1\nactive_file 5\ninactive_file 419430400\n")
	if why := memBudget(proc, root, 240, 1); why != "" {
		t.Fatalf("two turns fit in the cgroup's 500 MB left: %q", why)
	}
	if why := memBudget(proc, root, 240, 2); !strings.Contains(why, "500 of 1000 MB in use") {
		t.Fatalf("a third didn't wait on the cgroup: %q", why)
	}
	// The daemon in its unit's leaf, which has no limit of its own: the
	// unit's holds.
	write(proc+"/self/cgroup", "0::/hive.service/daemon\n")
	write(root+"/hive.service/daemon/memory.max", "max\n")
	write(root+"/hive.service/daemon/memory.current", "1\n")
	if why := memBudget(proc, root, 240, 2); !strings.Contains(why, "500 of 1000 MB in use") {
		t.Fatalf("the leaf didn't wait on its unit's limit: %q", why)
	}
}

// The CPU is full at a 1-minute load of a CPU each, and never when the load
// can't be read.
func TestCPUFull(t *testing.T) {
	proc := t.TempDir()
	if why := cpuFull(proc, 20); why != "" {
		t.Fatalf("full with nothing to read: %q", why)
	}
	os.WriteFile(proc+"/loadavg", []byte("19.50 30.00 40.00 3/900 123\n"), 0o644)
	if why := cpuFull(proc, 20); why != "" {
		t.Fatalf("load 19.5 on 20 CPUs is full: %q", why)
	}
	os.WriteFile(proc+"/loadavg", []byte("109.12 101.00 104.00 50/3000 123\n"), 0o644)
	if why := cpuFull(proc, 20); why != "load 109 on 20 CPUs" {
		t.Fatalf("load 109 on 20 CPUs: %q", why)
	}
}

// A measure of main runs its checks over every other run's turns, and while
// it runs on a full CPU a turn that may wait doesn't start; once it ends, or
// with turn_mb 0, they start again.
func TestMeasuringMainYieldsTheCPU(t *testing.T) {
	c, _ := testHive(t)
	proc := t.TempDir()
	old := procRoot
	procRoot = proc
	t.Cleanup(func() { procRoot = old })
	os.WriteFile(proc+"/loadavg", []byte(fmt.Sprintf("%d 0 0 1/1 1\n", 5*runtime.NumCPU())), 0o644)

	if n := c.Effects.niceFor("demo", "abc"); n != CheckNice {
		t.Fatalf("a check before any measure runs at %d", n)
	}
	if why := c.Runs.Yielding(240); why != "" {
		t.Fatalf("held with no measure: %q", why)
	}
	done := c.Effects.Measuring("demo", "abc1234567")
	again := c.Effects.Measuring("demo", "abc1234567") // a nested measure of the same commit
	if n := c.Effects.niceFor("demo", "abc1234567"); n != MeasureNice {
		t.Fatalf("main's check runs at %d, not %d", n, MeasureNice)
	}
	if n := c.Effects.niceFor("demo", "def"); n != CheckNice {
		t.Fatalf("another commit's check runs at %d", n)
	}
	why := c.Runs.Yielding(240)
	if !strings.HasPrefix(why, MeasureWait) || !strings.Contains(why, "demo main at abc12345") {
		t.Fatalf("a full CPU didn't hold turns for the measure: %q", why)
	}
	if why := c.Runs.Yielding(0); why != "" {
		t.Fatalf("turn_mb 0 held: %q", why)
	}
	os.WriteFile(proc+"/loadavg", []byte("0.5 0 0 1/1 1\n"), 0o644)
	if why := c.Runs.Yielding(240); why != "" {
		t.Fatalf("held with CPU free: %q", why)
	}
	os.WriteFile(proc+"/loadavg", []byte(fmt.Sprintf("%d 0 0 1/1 1\n", 5*runtime.NumCPU())), 0o644)
	done()
	done() // a second call does nothing
	if n := c.Effects.niceFor("demo", "abc1234567"); n != MeasureNice || c.Runs.Yielding(240) == "" {
		t.Fatalf("the nested measure's end let go of the outer one")
	}
	again()
	if n := c.Effects.niceFor("demo", "abc1234567"); n != CheckNice {
		t.Fatalf("a check after the measure runs at %d", n)
	}
	if why := c.Runs.Yielding(240); why != "" {
		t.Fatalf("held after the measure: %q", why)
	}
}

// While main is measured on a full CPU, a worker's turn waits and says why,
// and a lead's and a reviewer's start.
func TestMeasureHoldsAWorkersTurnNotALeads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the agent")
	}
	c, _ := testHive(t)
	proc := t.TempDir()
	old := procRoot
	procRoot = proc
	t.Cleanup(func() { procRoot = old })
	os.WriteFile(proc+"/loadavg", []byte(fmt.Sprintf("%d 0 0 1/1 1\n", 5*runtime.NumCPU())), 0o644)
	rs := c.Runs
	var n atomic.Int32
	rs.sandbox = func() (sandbox.Sandbox, error) { return claimSB{n: &n, agent: sleeper(t)}, nil }
	cfg := rs.ws.LoadConfig()
	cfg.TurnMB = 1 // pacing on; a megabyte always fits
	defer c.Effects.Measuring("demo", "abc1234567")()
	w, err := rs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "fake", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rs.launch(w, cfg)
	if w, _ = rs.Get("w1"); n.Load() != 0 || w.Status == StatusRunning || !strings.HasPrefix(w.Reason, MeasureWait) {
		t.Fatalf("a worker's turn during main's measure: %d started, status %s, reason %q", n.Load(), w.Status, w.Reason)
	}
	l, err := rs.Start(RunSpec{ID: "l1", Role: "lead", Harness: "fake", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rs.launch(l, cfg)
	if n.Load() != 1 {
		t.Fatalf("a lead's turn waited for main's measure")
	}
	// A reviewer's read, a security seat's too, doesn't wait on a measure:
	// a merge waits on it, and the measure may be another project's.
	r, err := rs.Start(RunSpec{ID: "s-x-security-0", Role: "reviewer", Harness: "fake", Checkout: t.TempDir(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	rs.launch(r, cfg)
	if r, _ = rs.Get(r.ID); n.Load() != 2 || strings.HasPrefix(r.Reason, MeasureWait) {
		t.Fatalf("a security read waited for main's measure: %d started, reason %q", n.Load(), r.Reason)
	}
}

// The disk counts as saturated while IO pressure's full avg60 is over its
// limit, and never with the limit off or nothing to read.
func TestDiskBusy(t *testing.T) {
	proc := t.TempDir()
	if why := diskBusy(proc, DefaultDiskFullMax); why != "" {
		t.Fatalf("held with nothing to read: %q", why)
	}
	os.MkdirAll(proc+"/pressure", 0o755)
	os.WriteFile(proc+"/pressure/io", []byte("some avg10=92.82 avg60=92.17 avg300=92.28 total=1\nfull avg10=59.03 avg60=62.13 avg300=59.26 total=1\n"), 0o644)
	if why := diskBusy(proc, 40); !strings.HasPrefix(why, DiskWait) || !strings.Contains(why, "62%") {
		t.Fatalf("a saturated disk didn't hold: %q", why)
	}
	if why := diskBusy(proc, 70); why != "" {
		t.Fatalf("held under the limit: %q", why)
	}
	if why := diskBusy(proc, 0); why != "" {
		t.Fatalf("the limit off held: %q", why)
	}
}
