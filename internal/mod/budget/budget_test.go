package budget

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// rig is a module whose budget has room for slots turns of 250 MB past a
// 1024 MB reserve, on a machine with plenty free, at a clock the test moves.
func rig(slots int) (*Module, core.Config, *time.Time) {
	now := time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC)
	m := &Module{now: func() time.Time { return now }, read: true}
	m.mem = Mem{AvailMB: 8000, LimitMB: float64(1024 + 250*slots), RoomMB: -1, From: "mem_budget_mb"}
	cfg := core.Config{MemFloorMB: 1024, TurnMB: 250}
	return m, cfg, &now
}

func run(id, role string) core.Run { return core.Run{RunSpec: core.RunSpec{ID: id, Role: role}} }

// With the budget full, a sweep waits; every role's live turn counts,
// a lead's too (the core never asks for a lead's own: TestAdmitAsksForAllButLeads).
func TestBudgetHoldsASweepWhenFull(t *testing.T) {
	m, cfg, _ := rig(2)
	live := []core.Run{run("lead", "lead"), run("rv", "reviewer")}
	if why := m.Admit(run("sweep", "sweep"), live, cfg); !strings.HasPrefix(why, "waiting: the Hive's memory budget is full (2 turns live") {
		t.Errorf("sweep with the budget full: %q, want it waiting on the budget", why)
	}
	if why := m.Admit(run("w", "worker"), live[1:], cfg); why != "" {
		t.Errorf("worker with room for one: %q, want it let in", why)
	}
}

// Held turns go in rank order when room comes: the merge path's reader
// before a sweep that was held first.
func TestBudgetLetsHeldTurnsInByRank(t *testing.T) {
	m, cfg, now := rig(1)
	live := []core.Run{run("w1", "worker")}
	for _, r := range []core.Run{run("sweep", "sweep"), run("rv", "reviewer")} {
		if why := m.Admit(r, live, cfg); why == "" {
			t.Fatalf("%s let in past a full budget", r.ID)
		}
		*now = now.Add(time.Second)
	}
	m.mem.LimitMB += 250 // room for one more
	if why := m.Admit(run("sweep", "sweep"), live, cfg); !strings.Contains(why, "1 ahead") {
		t.Errorf("sweep behind the reviewer: %q, want it waiting with 1 ahead", why)
	}
	if why := m.Admit(run("rv", "reviewer"), live, cfg); why != "" {
		t.Errorf("reviewer with room for one: %q, want it let in", why)
	}
	// A held run not asked about for a minute has left the line.
	*now = now.Add(2 * waitStale)
	m.mem.LimitMB += 250
	if why := m.Admit(run("w2", "worker"), []core.Run{run("w1", "worker"), run("rv", "reviewer")}, cfg); why != "" {
		t.Errorf("worker behind a sweep that left the line: %q, want it let in", why)
	}
}

// A burst of due turns (a lifted hold, many runs reopened at once) is let in
// a few at a time.
func TestBudgetLetsAFewInAtATime(t *testing.T) {
	m, cfg, now := rig(20)
	cfg.TurnBurst = 2
	for i, want := range []string{"", "", "waiting: turns start a few at a time"} {
		if why := m.Admit(run("w"+strconv.Itoa(i), "worker"), nil, cfg); why != want {
			t.Errorf("worker %d in the window: %q, want %q", i, why, want)
		}
	}
	*now = now.Add(admitWindow)
	if why := m.Admit(run("w2", "worker"), nil, cfg); why != "" {
		t.Errorf("third worker after the window: %q, want it let in", why)
	}
}

// Step-backs run one at a time across projects.
func TestOneStepBackAtATime(t *testing.T) {
	m, cfg, _ := rig(20)
	sb := run("sb1", "stepback")
	sb.Project = "a"
	if why := m.Admit(run("sb2", "stepback"), []core.Run{sb}, cfg); !strings.HasPrefix(why, "waiting: one step-back at a time") {
		t.Errorf("second step-back: %q, want it waiting", why)
	}
	if len(m.waiting) != 0 {
		t.Error("a step-back waiting on another holds a place in the line")
	}
}

// Free memory holds the budget too: room for a turn past the reserve.
func TestBudgetWaitsOnFreeMemory(t *testing.T) {
	m, cfg, _ := rig(20)
	m.mem.AvailMB = 1171
	if why := m.Admit(run("w1", "worker"), nil, cfg); !strings.Contains(why, "MB free past the 1024 MB reserve") {
		t.Errorf("worker with 147 MB past the reserve: %q, want it waiting", why)
	}
}

// cgroupRig writes a daemon cgroup under a 10 GB memory.high whose use is
// current bytes, inactiveFile of them reclaimable page cache.
func cgroupRig(t *testing.T, current, inactiveFile string) *Module {
	m := &Module{Proc: t.TempDir(), Cgroup: t.TempDir(), now: time.Now}
	os.MkdirAll(filepath.Join(m.Proc, "self"), 0o755)
	os.WriteFile(filepath.Join(m.Proc, "self", "cgroup"), []byte("0::/hive.slice/daemon.service\n"), 0o644)
	os.WriteFile(filepath.Join(m.Proc, "meminfo"), []byte("MemTotal:       32000000 kB\nMemFree:          100000 kB\nMemAvailable:   16000000 kB\n"), 0o644)
	for dir, files := range map[string]map[string]string{
		"hive.slice":                {"memory.high": "10737418240", "memory.max": "max", "memory.current": current, "memory.stat": "anon 1\nfile 9\ninactive_file " + inactiveFile + "\nactive_file 0\n"},
		"hive.slice/daemon.service": {"memory.high": "max", "memory.max": "21474836480", "memory.current": "100", "memory.stat": "inactive_file 0\n"},
	} {
		os.MkdirAll(filepath.Join(m.Cgroup, dir), 0o755)
		for f, v := range files {
			os.WriteFile(filepath.Join(m.Cgroup, dir, f), []byte(v+"\n"), 0o644)
		}
	}
	return m
}

// The limit is the tightest memory.high or memory.max on the daemon's
// cgroup or above it, and the room under it what that cgroup uses less
// what it could reclaim.
func TestCgroupLimit(t *testing.T) {
	m := cgroupRig(t, "8912896000", "0")
	if mem := m.Read(core.Config{}); mem.From != "cgroup" || mem.LimitMB != 10240 || mem.RoomMB != 1740 {
		t.Errorf("cgroup read %+v, want a 10240 MB limit with 1740 MB room", mem)
	}
	// The config's share is weighed on its own, never against a cgroup's use.
	if mem := m.Read(core.Config{MemBudgetMB: 6000}); mem.LimitMB != 6000 || mem.RoomMB != -1 {
		t.Errorf("config share read %+v, want 6000 MB and no cgroup room", mem)
	}
	m.Proc = t.TempDir()
	if mem := m.Read(core.Config{}); mem.From == "cgroup" {
		t.Error("a cgroup limit read with no cgroup file")
	}
}

// A cgroup at its memory.high in page cache (git clones, Go builds) still
// has room: the reclaimable file pages are room, so the merge path's
// readers and the workers don't wait on cache forever.
func TestCgroupFullOfCacheHasRoom(t *testing.T) {
	m := cgroupRig(t, "10700000000", "8000000000") // 10204 MB used, 7629 MB of it inactive file pages
	cfg := core.Config{MemFloorMB: 1024, TurnMB: 250}
	m.mem, m.read, m.now = m.Read(cfg), true, time.Now
	if why := m.Admit(run("rv", "reviewer"), nil, cfg); why != "" {
		t.Errorf("reviewer under a cgroup full of cache: %q, want it let in", why)
	}
	m = cgroupRig(t, "10700000000", "0") // the same use, none of it reclaimable
	m.mem, m.read = m.Read(cfg), true
	if why := m.Admit(run("rv", "reviewer"), nil, cfg); !strings.Contains(why, "MB free past the 1024 MB reserve") {
		t.Errorf("reviewer under a cgroup really full: %q, want it waiting", why)
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

// The status line names turns live and waiting by role.
func TestStateLine(t *testing.T) {
	s := State{Live: 3, Slots: 2, TurnMB: 250, LimitMB: 1524, From: "mem_budget_mb", LiveBy: map[string]int{"lead": 1, "worker": 2}, WaitBy: map[string]int{"sweep": 1}}
	if got := s.String(); !strings.Contains(got, "live: lead 1, worker 2; waiting: sweep 1") {
		t.Errorf("line %q", got)
	}
	if got := s.Short(); got != "3 of 2 in memory, 1 waiting" {
		t.Errorf("short %q", got)
	}
}
