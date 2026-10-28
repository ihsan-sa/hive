// Package budget is the memory budget every agent turn this machine runs
// counts against, of any role, reopened and resumed turns too (issue #1849:
// 27 live turns filled the Hive's 10 GB share while the work module's pace
// said one worker at once). The core asks it before each turn but a lead's
// (core.Runs.Room); the policy is here: the Hive's memory limit less a
// reserve, at a per-turn size learned from the turns' own RSS, the rest
// waiting in rank order (the merge path's readers first, then workers and
// the rest, then sweeps and step-backs), one step-back at a time, and a few
// let in at a time, so neither a lifted hold nor a burst of reopened runs
// starts a crowd of turns in one minute. See docs/priority.md.
package budget

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

const (
	turnMBStart = 250 // the per-turn size before any turn is measured
	turnMBLeast = 100 // a measured size is never taken below this
	// cfg.TurnBurst turns (leads aside) start in any admitWindow, at most.
	admitWindow = 20 * time.Second
	// A held run not asked about for waitStale has left the line.
	waitStale = time.Minute
	// The turns' RSS is read again after sampleEvery.
	sampleEvery = 30 * time.Second
)

// Rank is a role's place in the line: 0 leads (never asked), 1 the merge
// path's readers and wardens, 2 workers and the rest, 3 sweeps, step-backs
// and the knowledge base's filer.
func Rank(role string) int {
	switch role {
	case "lead":
		return 0
	case "reviewer", "security", "warden", "tribunal", core.JudgeRole:
		return 1
	case "sweep", "stepback", "filer":
		return 3
	}
	return 2
}

// Module keeps the line and the learned turn size. Its Admit is the core's
// Runs.Room; Tick reads the machine's memory and samples the turns' RSS.
type Module struct {
	C *core.Core
	// Proc and Cgroup are where /proc and cgroup v2 are read; a test points
	// them at its own.
	Proc, Cgroup string

	mu      sync.Mutex
	mem     Mem               // the memory as last read
	read    bool              // mem has been read
	turnMB  float64           // the learned size of one turn; 0 before any
	sampled time.Time         // when the turns' RSS was last read
	starts  []time.Time       // admissions (leads aside) in the last admitWindow
	waiting map[string]waiter // runs held on the budget, by id
	now     func() time.Time
}

type waiter struct {
	rank        int
	role        string
	since, seen time.Time
}

// New makes the module and hands its Admit to the core.
func New(c *core.Core) *Module {
	m := &Module{C: c, Proc: "/proc", Cgroup: "/sys/fs/cgroup", now: time.Now}
	c.Runs.Room = m.Admit
	return m
}

// Mem is the memory figures an admission weighs, in MB.
type Mem struct {
	AvailMB float64 // MemAvailable
	LimitMB float64 // the Hive's limit
	// RoomMB is what the limit's cgroup has left under it, its reclaimable
	// file pages counted as room; -1 where the limit isn't a cgroup's.
	RoomMB float64
	From   string // where the limit came from
}

// Read reads the memory: the limit is the config's mem_budget_mb, else the
// smallest memory.high or memory.max on this process's cgroup or one above
// it, else MemTotal.
func (m *Module) Read(cfg core.Config) Mem {
	v := Mem{RoomMB: -1}
	if kb, ok := field(m.Proc+"/meminfo", "MemAvailable:"); ok {
		v.AvailMB = kb / 1024
	}
	switch lim, room, ok := m.cgroupLimit(); {
	case cfg.MemBudgetMB > 0:
		// The config's own share: no cgroup's use is weighed against it.
		v.LimitMB, v.From = float64(cfg.MemBudgetMB), "mem_budget_mb"
	case ok:
		v.LimitMB, v.RoomMB, v.From = lim, room, "cgroup"
	default:
		if kb, ok := field(m.Proc+"/meminfo", "MemTotal:"); ok {
			v.LimitMB, v.From = kb/1024, "MemTotal"
		}
	}
	return v
}

// cgroupLimit is the smallest memory.high or memory.max set on this
// process's cgroup v2 or any above it, in MB (a parent's ceiling holds its
// children too), and the room under it in the cgroup that set it: the limit
// less memory.current, with the inactive file pages of memory.stat added
// back, since memory.current counts page cache the kernel reclaims before
// it holds anything at memory.high (a Hive cloning repos and building Go
// keeps its cgroup's use near the limit in cache alone).
func (m *Module) cgroupLimit() (limit, room float64, ok bool) {
	raw, err := os.ReadFile(m.Proc + "/self/cgroup")
	if err != nil {
		return 0, 0, false
	}
	path := ""
	for _, l := range strings.Split(string(raw), "\n") {
		if p, found := strings.CutPrefix(l, "0::"); found {
			path = p
		}
	}
	if path == "" {
		return 0, 0, false
	}
	const mb = 1 << 20
	for d := filepath.Join(m.Cgroup, path); strings.HasPrefix(d, m.Cgroup+string(filepath.Separator)); d = filepath.Dir(d) {
		for _, f := range []string{"memory.high", "memory.max"} {
			n, err := readInt(filepath.Join(d, f))
			if err != nil || (ok && float64(n)/mb >= limit) {
				continue // unset ("max"), not enabled here, or looser than one found
			}
			cur, _ := readInt(filepath.Join(d, "memory.current"))
			cache, _ := field(filepath.Join(d, "memory.stat"), "inactive_file ")
			limit, room, ok = float64(n)/mb, (float64(n)-float64(cur)+cache)/mb, true
		}
	}
	return limit, room, ok
}

func readInt(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}

// field reads the number after the first occurrence of key in a file.
func field(path, key string) (float64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	_, rest, found := strings.Cut(string(raw), key)
	if !found {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.Fields(rest + " x")[0], 64)
	return f, err == nil
}

// turnSize is the size one turn is counted at. m.mu is held.
func (m *Module) turnSize(cfg core.Config) float64 {
	switch {
	case cfg.TurnMB > 0:
		return float64(cfg.TurnMB)
	case m.turnMB > 0:
		return m.turnMB
	}
	return turnMBStart
}

func slots(mem Mem, size float64, reserve int) int {
	return max(int((mem.LimitMB-float64(max(reserve, 0)))/size), 1)
}

// Admit says why run r's turn waits on the budget, or "" when it may
// start, given the turns live or starting here. The core asks it under its
// lock, for every role but a lead's.
func (m *Module) Admit(r core.Run, live []core.Run, cfg core.Config) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if !m.read {
		m.mem, m.read = m.Read(cfg), true
	}
	if r.Role == "stepback" && slices.ContainsFunc(live, func(o core.Run) bool { return o.Role == "stepback" }) {
		// Across projects too; it holds no place in the line meanwhile.
		return "waiting: one step-back at a time (another is running)"
	}
	if m.waiting == nil {
		m.waiting = map[string]waiter{}
	}
	w, ok := m.waiting[r.ID]
	if !ok {
		w = waiter{rank: Rank(r.Role), role: r.Role, since: now}
	}
	w.seen = now
	m.waiting[r.ID] = w
	running := map[string]bool{}
	for _, o := range live {
		running[o.ID] = true
	}
	ahead := 0
	for id, o := range m.waiting {
		if running[id] || now.Sub(o.seen) > waitStale {
			delete(m.waiting, id)
			continue
		}
		if id != r.ID && (o.rank < w.rank || o.rank == w.rank && o.since.Before(w.since)) {
			ahead++
		}
	}
	mem, size, reserve := m.mem, m.turnSize(cfg), float64(max(cfg.MemFloorMB, 0))
	n := slots(mem, size, cfg.MemFloorMB)
	room := mem.AvailMB
	if mem.RoomMB >= 0 && mem.RoomMB < room {
		room = mem.RoomMB
	}
	room -= reserve
	switch {
	case mem.LimitMB > 0 && len(live)+ahead+1 > n:
		return fmt.Sprintf("waiting: the Hive's memory budget is full (%d turns live, %d ahead, room for %d at %.0f MB a turn in %.0f MB)", len(live), ahead, n, size, mem.LimitMB-reserve)
	case mem.AvailMB > 0 && room < size*float64(ahead+1):
		return fmt.Sprintf("waiting: the Hive's memory budget is full (%.0f MB free past the %.0f MB reserve, %d ahead, %.0f MB a turn)", max(room, 0), reserve, ahead, size)
	case ahead > 0:
		return fmt.Sprintf("waiting: the Hive's memory budget is full (%d ahead in line)", ahead)
	}
	m.starts = slices.DeleteFunc(m.starts, func(t time.Time) bool { return now.Sub(t) >= admitWindow })
	if cfg.TurnBurst > 0 && len(m.starts) >= cfg.TurnBurst {
		return "waiting: turns start a few at a time"
	}
	m.starts = append(m.starts, now)
	delete(m.waiting, r.ID)
	return ""
}

// Tick reads the memory for the next admissions, and every sampleEvery
// learns the per-turn size from the RSS of each live turn's process tree.
// Where /proc has no process folders (not Linux) nothing is learned.
func (m *Module) Tick() {
	cfg := m.C.WS.LoadConfig()
	mem := m.Read(cfg)
	m.mu.Lock()
	m.mem, m.read = mem, true
	now := m.now()
	due := now.Sub(m.sampled) >= sampleEvery
	if due {
		m.sampled = now
	}
	m.mu.Unlock()
	if !due {
		return
	}
	var roots []int
	for _, r := range m.C.Runs.List(func(r *core.Run) bool { return r.Status == core.StatusRunning && r.Where == "" && r.Proc.PID > 0 }) {
		roots = append(roots, r.Proc.PID)
	}
	mb, n := treesRSS(m.Proc, roots)
	if n == 0 {
		return
	}
	per := max(mb/float64(n), turnMBLeast)
	m.mu.Lock()
	if m.turnMB == 0 {
		m.turnMB = max(per, turnMBStart)
	} else {
		m.turnMB = 0.7*m.turnMB + 0.3*per
	}
	m.mu.Unlock()
}

// treesRSS sums, in MB, the resident memory of each root's process and all
// its descendants, and says how many roots were found.
func treesRSS(proc string, roots []int) (float64, int) {
	if len(roots) == 0 {
		return 0, 0
	}
	ents, err := os.ReadDir(proc)
	if err != nil {
		return 0, 0
	}
	parent, rss := map[int]int{}, map[int]float64{}
	page := float64(os.Getpagesize()) / (1 << 20)
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proc, e.Name(), "stat"))
		if err != nil {
			continue
		}
		// The command name is in parentheses and may hold spaces.
		i := strings.LastIndexByte(string(raw), ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(raw[i+1:]))
		if len(f) < 22 {
			continue
		}
		pp, _ := strconv.Atoi(f[1])
		pages, _ := strconv.ParseFloat(f[21], 64)
		parent[pid], rss[pid] = pp, pages*page
	}
	in := map[int]bool{}
	found := 0
	for _, r := range roots {
		if _, ok := parent[r]; ok {
			in[r] = true
			found++
		}
	}
	var total float64
	for pid, mb := range rss {
		for p, hops := pid, 0; p > 1 && hops < 64; p, hops = parent[p], hops+1 {
			if in[p] {
				total += mb
				break
			}
		}
	}
	return total, found
}

// State is the budget as the dashboard shows it.
type State struct {
	Live    int            `json:"live"`
	Slots   int            `json:"slots"`
	TurnMB  float64        `json:"turn_mb"`
	LimitMB float64        `json:"limit_mb"`
	From    string         `json:"from"`
	LiveBy  map[string]int `json:"live_by"`
	WaitBy  map[string]int `json:"waiting_by"`
}

// State reads the budget now: turns live here and waiting, by role.
func (m *Module) State(cfg core.Config) State {
	mem := m.Read(cfg)
	live := m.C.Runs.List(func(r *core.Run) bool { return r.Status == core.StatusRunning && r.Where == "" })
	m.mu.Lock()
	defer m.mu.Unlock()
	size := m.turnSize(cfg)
	t := State{Live: len(live), Slots: slots(mem, size, cfg.MemFloorMB), TurnMB: size, LimitMB: mem.LimitMB, From: mem.From, LiveBy: map[string]int{}, WaitBy: map[string]int{}}
	running := map[string]bool{}
	for _, r := range live {
		t.LiveBy[r.Role]++
		running[r.ID] = true
	}
	now := m.now()
	for id, w := range m.waiting {
		if !running[id] && now.Sub(w.seen) <= waitStale {
			t.WaitBy[w.role]++
		}
	}
	return t
}

// String is the budget in one line.
func (t State) String() string {
	part := func(m map[string]int) string {
		var s []string
		for _, k := range slices.Sorted(maps.Keys(m)) {
			s = append(s, fmt.Sprintf("%s %d", k, m[k]))
		}
		if len(s) == 0 {
			return "none"
		}
		return strings.Join(s, ", ")
	}
	return fmt.Sprintf("%d live of %d (%.0f MB a turn in %.0f MB from %s, less mem_floor_mb); live: %s; waiting: %s", t.Live, t.Slots, t.TurnMB, t.LimitMB, t.From, part(t.LiveBy), part(t.WaitBy))
}

// Short is the budget as the control center's agents row shows it.
func (t State) Short() string {
	n := 0
	for _, w := range t.WaitBy {
		n += w
	}
	s := fmt.Sprintf("%d of %d in memory", t.Live, t.Slots)
	if n > 0 {
		s += fmt.Sprintf(", %d waiting", n)
	}
	return s
}
