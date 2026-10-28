package core

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The memory budget: every agent turn this machine runs, of any role, counts
// against one budget, the Hive's memory limit less a reserve, at a per-turn
// size learned from the turns' own RSS. A lead's turn is never held by it
// (it is counted); the others wait in rank order, the merge path's readers
// first, then workers and the rest, then sweeps and step-backs, and are let
// in a few at a time, so neither a lifted hold nor a burst of reopened runs
// starts a crowd of turns in one minute (issue #1849).

// sysCgroup is where cgroup v2 is mounted; a test points it at its own.
var sysCgroup = "/sys/fs/cgroup"

const (
	turnMBStart = 250 // the per-turn size before any turn is measured
	turnMBLeast = 100 // a measured size is never taken below this
	// cfg.TurnBurst turns (leads aside) start in any admitWindow, at most.
	admitWindow = 20 * time.Second
	// A held run not stepped for waitStale has left the line.
	waitStale = time.Minute
	// The turns' RSS is read again after sampleEvery.
	sampleEvery = 30 * time.Second
)

// AdmitRank is a role's place in the line for the budget: 0 leads (never
// held), 1 the merge path's readers and wardens, 2 workers and the rest, 3
// sweeps, step-backs and the knowledge base's filer.
func AdmitRank(role string) int {
	switch role {
	case "lead":
		return 0
	case "reviewer", "security", "warden", "tribunal", JudgeRole:
		return 1
	case "sweep", "stepback", "filer":
		return 3
	}
	return 2
}

// budget is the Runs' admission state, kept under rs.mu.
type budget struct {
	turnMB  float64           // the learned size of one turn; 0 before any
	sampled time.Time         // when the turns' RSS was last read
	starts  []time.Time       // non-lead admissions in the last admitWindow
	waiting map[string]waiter // runs held on the budget, by id
	window  time.Duration     // a test's admitWindow; 0 is admitWindow
}

type waiter struct {
	rank        int
	role        string
	since, seen time.Time
}

// memView is the memory figures one admission reads: MemAvailable, and the
// Hive's limit (the config's mem_budget_mb, else the smallest memory.high or
// memory.max of the daemon's cgroup and those above it, else MemTotal) with
// what its cgroup uses now (0 where no cgroup limit is read).
type memView struct {
	availMB, limitMB, usedMB float64
	from                     string // where the limit came from
}

func readMem(cfg Config) memView {
	var v memView
	if kb, ok := procField(procRoot+"/meminfo", "MemAvailable:"); ok {
		v.availMB = kb / 1024
	}
	if lim, used, ok := cgroupLimit(); ok {
		v.limitMB, v.usedMB, v.from = lim, used, "cgroup"
	} else if kb, ok := procField(procRoot+"/meminfo", "MemTotal:"); ok {
		v.limitMB, v.from = kb/1024, "MemTotal"
	}
	if cfg.MemBudgetMB > 0 {
		v.limitMB, v.from = float64(cfg.MemBudgetMB), "mem_budget_mb"
	}
	return v
}

// cgroupLimit is the smallest memory.high or memory.max set on this
// process's cgroup v2 or any above it, in MB, and memory.current of the one
// that set it: a parent's ceiling holds its children too.
func cgroupLimit() (limit, used float64, ok bool) {
	raw, err := os.ReadFile(procRoot + "/self/cgroup")
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
	for d := filepath.Join(sysCgroup, path); strings.HasPrefix(d, sysCgroup+string(filepath.Separator)); d = filepath.Dir(d) {
		for _, f := range []string{"memory.high", "memory.max"} {
			n, err := readInt(filepath.Join(d, f))
			if err != nil || (ok && float64(n)/(1<<20) >= limit) {
				continue // unset ("max"), not enabled here, or looser than one found
			}
			cur, _ := readInt(filepath.Join(d, "memory.current"))
			limit, used, ok = float64(n)/(1<<20), float64(cur)/(1<<20), true
		}
	}
	return limit, used, ok
}

func readInt(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}

// turnSize is the size one turn is counted at.
func (b *budget) turnSize(cfg Config) float64 {
	switch {
	case cfg.TurnMB > 0:
		return float64(cfg.TurnMB)
	case b.turnMB > 0:
		return b.turnMB
	}
	return turnMBStart
}

// liveHere counts the turns running on this machine and the launches in
// flight, by role. rs.mu is held.
func (rs *Runs) liveHere() (int, map[string]int) {
	by := map[string]int{}
	n := rs.launching
	for id, r := range rs.runs {
		if r.Status == StatusRunning && r.Where == "" {
			by[r.Role]++
			n++
		} else if rs.starting[id] {
			by[r.Role]++
		}
	}
	return n, by
}

// admit says why run r's turn waits on the budget, or "" when it may start.
// rs.mu is held; mem was read just before.
func (rs *Runs) admit(r Run, cfg Config, mem memView, now time.Time) string {
	b := &rs.budget
	if r.Lead() {
		return ""
	}
	if _, by := rs.liveHere(); r.Role == "stepback" && by["stepback"] > 0 {
		// Across projects too; it holds no place in the line meanwhile.
		return "waiting: one step-back at a time (another is running)"
	}
	if b.waiting == nil {
		b.waiting = map[string]waiter{}
	}
	w, ok := b.waiting[r.ID]
	if !ok {
		w = waiter{rank: AdmitRank(r.Role), role: r.Role, since: now}
	}
	w.seen = now
	b.waiting[r.ID] = w
	ahead := 0
	for id, o := range b.waiting {
		cur := rs.runs[id]
		if cur == nil || cur.Terminal() || cur.Status == StatusRunning || now.Sub(o.seen) > waitStale {
			delete(b.waiting, id)
			continue
		}
		if id != r.ID && (o.rank < w.rank || o.rank == w.rank && o.since.Before(w.since)) {
			ahead++
		}
	}
	live, _ := rs.liveHere()
	size := rs.budget.turnSize(cfg)
	reserve := float64(max(cfg.MemFloorMB, 0))
	slots := max(int((mem.limitMB-reserve)/size), 1)
	room := mem.limitMB - mem.usedMB
	if mem.usedMB == 0 || mem.availMB < room {
		room = mem.availMB
	}
	room -= reserve
	need := size * float64(ahead+1)
	switch {
	case mem.limitMB > 0 && live+ahead+1 > slots:
		return fmt.Sprintf("waiting: the Hive's memory budget is full (%d turns live, %d ahead, room for %d at %.0f MB a turn in %.0f MB)", live, ahead, slots, size, mem.limitMB-reserve)
	case mem.availMB > 0 && room < need:
		return fmt.Sprintf("waiting: the Hive's memory budget is full (%.0f MB free past the %.0f MB reserve, %d ahead, %.0f MB a turn)", max(room, 0), reserve, ahead, size)
	case ahead > 0:
		return fmt.Sprintf("waiting: the Hive's memory budget is full (%d ahead in line)", ahead)
	}
	b.starts = slices.DeleteFunc(b.starts, func(t time.Time) bool { return now.Sub(t) >= admitWindow })
	if cfg.TurnBurst > 0 && len(b.starts)+rs.launching-rs.leadLaunching >= cfg.TurnBurst {
		return "waiting: turns start a few at a time"
	}
	delete(b.waiting, r.ID)
	return ""
}

// started counts a non-lead turn's start toward the few at a time.
func (rs *Runs) started(r Run) {
	if r.Lead() {
		return
	}
	rs.mu.Lock()
	rs.budget.starts = append(rs.budget.starts, time.Now())
	rs.mu.Unlock()
}

// sampleTurns learns the per-turn size from the RSS of each live turn's
// process tree on this machine, at most every sampleEvery. Where /proc has
// no process folders (not Linux) nothing is learned.
func (rs *Runs) sampleTurns(now time.Time) {
	rs.mu.Lock()
	if now.Sub(rs.budget.sampled) < sampleEvery {
		rs.mu.Unlock()
		return
	}
	rs.budget.sampled = now
	var roots []int
	for _, r := range rs.runs {
		if r.Status == StatusRunning && r.Where == "" && r.Proc.PID > 0 {
			roots = append(roots, r.Proc.PID)
		}
	}
	rs.mu.Unlock()
	if len(roots) == 0 {
		return
	}
	mb, n := treesRSS(procRoot, roots)
	if n == 0 {
		return
	}
	per := max(mb/float64(n), turnMBLeast)
	rs.mu.Lock()
	if rs.budget.turnMB == 0 {
		rs.budget.turnMB = max(per, turnMBStart)
	} else {
		rs.budget.turnMB = 0.7*rs.budget.turnMB + 0.3*per
	}
	rs.mu.Unlock()
}

// treesRSS sums, in MB, the resident memory of each root's process and all
// its descendants, and says how many roots were found.
func treesRSS(proc string, roots []int) (float64, int) {
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

// TurnBudget is the budget as the dashboard shows it.
type TurnBudget struct {
	Live    int            `json:"live"`
	Slots   int            `json:"slots"`
	TurnMB  float64        `json:"turn_mb"`
	LimitMB float64        `json:"limit_mb"`
	From    string         `json:"from"`
	LiveBy  map[string]int `json:"live_by"`
	WaitBy  map[string]int `json:"waiting_by"`
}

// Budget reads the memory budget now: turns live and waiting by role.
func (rs *Runs) Budget(cfg Config) TurnBudget {
	mem := readMem(cfg)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	live, by := rs.liveHere()
	size := rs.budget.turnSize(cfg)
	t := TurnBudget{Live: live, TurnMB: size, LimitMB: mem.limitMB, From: mem.from, LiveBy: by, WaitBy: map[string]int{}}
	t.Slots = max(int((mem.limitMB-float64(max(cfg.MemFloorMB, 0)))/size), 1)
	now := time.Now()
	for id, w := range rs.budget.waiting {
		if cur := rs.runs[id]; cur != nil && !cur.Terminal() && cur.Status != StatusRunning && now.Sub(w.seen) <= waitStale {
			t.WaitBy[w.role]++
		}
	}
	return t
}

// String is the budget in one line.
func (t TurnBudget) String() string {
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
func (t TurnBudget) Short() string {
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
