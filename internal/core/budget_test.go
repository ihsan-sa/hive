package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// budgetHive is a test hive whose memory budget has room for slots turns of
// 250 MB past a 1024 MB reserve, on a machine with plenty free.
func budgetHive(t *testing.T, slots int) (*Core, Config) {
	t.Helper()
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB, cfg.TurnMB, cfg.MemBudgetMB = 1024, 250, 1024+250*slots
	t.Cleanup(func() { endTurns(t, c, cfg) })
	return c, cfg
}

func startAndLaunch(t *testing.T, c *Core, cfg Config, specs ...RunSpec) {
	t.Helper()
	for _, s := range specs {
		if s.Harness == "" {
			s.Harness = "fake"
		}
		if _, err := c.Runs.Start(s); err != nil {
			t.Fatal(err)
		}
		r, _ := c.Runs.Get(s.ID)
		c.Runs.launch(r, cfg)
	}
}

func status(c *Core, id string) (string, string) {
	r, _ := c.Runs.Get(id)
	return r.Status, r.Reason
}

// With the budget full, a sweep waits while a lead's turn starts: every
// role counts against one budget, and a lead is never held by it.
func TestBudgetHoldsASweepWhileALeadStarts(t *testing.T) {
	defer func(p string) { procRoot = p }(procRoot)
	procRoot = fakeProc(t, "8000000", "0.00", "0.00")
	c, cfg := budgetHive(t, 2)
	startAndLaunch(t, c, cfg, RunSpec{ID: "w1", Role: "worker"}, RunSpec{ID: "rv", Role: "reviewer", ReadOnly: true})
	startAndLaunch(t, c, cfg, RunSpec{ID: "sweep", Role: "sweep"}, RunSpec{ID: "lead", Role: "lead", Persistent: true})
	for _, id := range []string{"w1", "rv", "lead"} {
		if st, why := status(c, id); st != StatusRunning {
			t.Errorf("%s: %s %q, want its turn running", id, st, why)
		}
	}
	if st, why := status(c, "sweep"); st == StatusRunning || !strings.HasPrefix(why, "waiting: the Hive's memory budget is full") {
		t.Errorf("sweep with the budget full: %s %q, want it waiting on the budget", st, why)
	}
	b := c.Runs.Budget(cfg)
	if b.Live != 3 || b.Slots != 2 || b.LiveBy["lead"] != 1 || b.WaitBy["sweep"] != 1 {
		t.Errorf("budget %+v, want 3 live of 2 (the lead's counted) and the sweep waiting", b)
	}
	if !strings.Contains(b.String(), "waiting: sweep 1") {
		t.Errorf("budget line %q doesn't name the waiting sweep", b.String())
	}
}

// Held turns go in rank order when room comes: the merge path's reader
// before a sweep that was held first.
func TestBudgetLetsHeldTurnsInByRank(t *testing.T) {
	defer func(p string) { procRoot = p }(procRoot)
	procRoot = fakeProc(t, "8000000", "0.00", "0.00")
	c, cfg := budgetHive(t, 1)
	startAndLaunch(t, c, cfg, RunSpec{ID: "w1", Role: "worker"}, RunSpec{ID: "sweep", Role: "sweep"}, RunSpec{ID: "rv", Role: "reviewer", ReadOnly: true})
	for _, id := range []string{"sweep", "rv"} {
		if st, _ := status(c, id); st == StatusRunning {
			t.Fatalf("%s started past a full budget", id)
		}
	}
	cfg.MemBudgetMB += 250 // room for one more
	for _, id := range []string{"sweep", "rv"} {
		r, _ := c.Runs.Get(id)
		c.Runs.launch(r, cfg)
	}
	if st, why := status(c, "rv"); st != StatusRunning {
		t.Errorf("reviewer with room for one: %s %q, want it running", st, why)
	}
	// The sweep's launch came first, into the room for one, and still waited.
	if st, _ := status(c, "sweep"); st == StatusRunning {
		t.Error("sweep went ahead of the reviewer held after it")
	}
}

// A burst of due turns (a lifted hold, many runs reopened at once) is let in
// a few at a time; a lead's turn isn't one of the few.
func TestBudgetLetsAFewInAtATime(t *testing.T) {
	defer func(p string) { procRoot = p }(procRoot)
	procRoot = fakeProc(t, "8000000", "0.00", "0.00")
	c, cfg := budgetHive(t, 20)
	cfg.TurnBurst = 2
	startAndLaunch(t, c, cfg, RunSpec{ID: "w1", Role: "worker"}, RunSpec{ID: "w2", Role: "worker"}, RunSpec{ID: "w3", Role: "worker"}, RunSpec{ID: "lead", Role: "lead", Persistent: true})
	if st, why := status(c, "w3"); st == StatusRunning || why != "waiting: turns start a few at a time" {
		t.Errorf("third worker in the window: %s %q, want it waiting", st, why)
	}
	if st, _ := status(c, "lead"); st != StatusRunning {
		t.Errorf("lead past the few: %s, want running", st)
	}
	c.Runs.mu.Lock()
	for i := range c.Runs.budget.starts {
		c.Runs.budget.starts[i] = c.Runs.budget.starts[i].Add(-admitWindow)
	}
	c.Runs.mu.Unlock()
	r, _ := c.Runs.Get("w3")
	c.Runs.launch(r, cfg)
	if st, why := status(c, "w3"); st != StatusRunning {
		t.Errorf("third worker after the window: %s %q, want running", st, why)
	}
}

// Step-backs run one at a time across projects.
func TestOneStepBackAtATime(t *testing.T) {
	defer func(p string) { procRoot = p }(procRoot)
	procRoot = fakeProc(t, "8000000", "0.00", "0.00")
	c, cfg := budgetHive(t, 20)
	startAndLaunch(t, c, cfg, RunSpec{ID: "sb1", Role: "stepback", Project: "a"}, RunSpec{ID: "sb2", Role: "stepback", Project: "b"})
	if st, _ := status(c, "sb1"); st != StatusRunning {
		t.Errorf("first step-back: %s, want running", st)
	}
	if st, why := status(c, "sb2"); st == StatusRunning || !strings.HasPrefix(why, "waiting: one step-back at a time") {
		t.Errorf("second step-back: %s %q, want it waiting", st, why)
	}
}

// Free memory holds the budget too: room for a turn past the reserve.
func TestBudgetWaitsOnFreeMemory(t *testing.T) {
	defer func(p string) { procRoot = p }(procRoot)
	procRoot = fakeProc(t, "1200000", "0.00", "0.00") // 1171 MB free: over the floor, not a turn past it
	c, cfg := budgetHive(t, 20)
	startAndLaunch(t, c, cfg, RunSpec{ID: "w1", Role: "worker"})
	if st, why := status(c, "w1"); st == StatusRunning || !strings.Contains(why, "MB free past the 1024 MB reserve") {
		t.Errorf("worker with 147 MB past the reserve: %s %q, want it waiting", st, why)
	}
}

// The limit is the tightest memory.high or memory.max on the daemon's
// cgroup or above it, with memory.current of the one that set it.
func TestCgroupLimit(t *testing.T) {
	defer func(p, s string) { procRoot, sysCgroup = p, s }(procRoot, sysCgroup)
	procRoot, sysCgroup = t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(procRoot, "self"), 0o755)
	os.WriteFile(filepath.Join(procRoot, "self", "cgroup"), []byte("0::/hive.slice/daemon.service\n"), 0o644)
	if _, _, ok := cgroupLimit(); ok {
		t.Error("a limit read with no cgroup files")
	}
	for dir, files := range map[string]map[string]string{
		"hive.slice":                {"memory.high": "10737418240", "memory.max": "max", "memory.current": "8912896000"},
		"hive.slice/daemon.service": {"memory.high": "max", "memory.max": "21474836480", "memory.current": "100"},
	} {
		os.MkdirAll(filepath.Join(sysCgroup, dir), 0o755)
		for f, v := range files {
			os.WriteFile(filepath.Join(sysCgroup, dir, f), []byte(v+"\n"), 0o644)
		}
	}
	lim, used, ok := cgroupLimit()
	if !ok || lim != 10240 || used != 8500 {
		t.Errorf("cgroup limit %v MB, %v used (%v), want 10240 MB, 8500 used", lim, used, ok)
	}
}

// A turn's size is its process tree's RSS, children and all.
func TestTreesRSS(t *testing.T) {
	proc := t.TempDir()
	page := os.Getpagesize()
	for pid, stat := range map[string]string{
		"100": "100 (claude) S 1 100 100 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 PAGES",
		"101": "101 (node (x)) S 100 100 100 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 PAGES",
		"102": "102 (sh) S 101 100 100 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 PAGES",
		"200": "200 (other) S 1 200 200 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 PAGES",
	} {
		os.MkdirAll(filepath.Join(proc, pid), 0o755)
		pages := (100 << 20) / page // 100 MB each
		os.WriteFile(filepath.Join(proc, pid, "stat"), []byte(strings.Replace(stat, "PAGES", strconv.Itoa(pages), 1)), 0o644)
	}
	mb, n := treesRSS(proc, []int{100, 300})
	if n != 1 || mb < 299 || mb > 301 {
		t.Errorf("trees: %v MB over %d roots, want 300 MB over 1", mb, n)
	}
}
