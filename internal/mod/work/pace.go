package work

import (
	"cmp"
	"fmt"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The pace: how many workers, and how many checks, start at once is not a
// fixed number but a limit that sets itself from what the machine and the
// plan carry. While something waits on the limit and nothing is strained, it
// grows by one a step; when the machine is strained (memory floor, memory or
// CPU pressure, load per core: the same settings that hold a turn) or the
// workers' plan is nearly spent, it pulls back to three quarters of what is
// running. Nothing running is stopped; past the limit, work waits in order.
// It changes at most once a step, so the readings (load is a 1-minute
// average) catch up with the last change before the next. max_workers and
// max_checks, when set, are the person's ceilings and where the pace starts;
// with no machine reading on, or none to read (no /proc, as on macOS or
// Windows), it stays at its start, as it can't see.

// paceStep is the least time between two changes of a limit.
const paceStep = time.Minute

// planFloor is the share of the plan left below which workers pull back.
const planFloor = 0.1

// defaultWorkers is where the workers' limit starts when no ceiling is set.
const defaultWorkers = 4

// pace is one self-setting limit.
type pace struct {
	mu    sync.Mutex
	limit int // 0 until first read
	start int // the start it last read: a new one (a ceiling set) starts it afresh
	at    time.Time
	why   string
	step  time.Duration // the least time between two changes; 0 is paceStep
}

// next moves the limit for running things with waiting ones wanting more,
// strain saying what strains the machine or plan ("" for nothing), watching
// whether any machine reading is on, from start under ceiling (0: none).
func (p *pace) next(now time.Time, running int, waiting bool, strain string, watching bool, start, ceiling int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	top := ceiling
	if !watching {
		top = start
	}
	if p.limit == 0 || start != p.start { // a restart starts from what already runs
		p.limit, p.start, p.at, p.why = max(start, min(running, cmp.Or(top, running))), start, now, "the start"
	}
	if now.Sub(p.at) >= cmp.Or(p.step, paceStep) {
		switch {
		case strain != "":
			if l := max(1, min(p.limit, running)*3/4); l < p.limit {
				p.limit, p.at, p.why = l, now, "pulled back: "+strain
			}
		case waiting && running >= p.limit && (top == 0 || p.limit < top):
			p.limit, p.at, p.why = p.limit+1, now, "grew: room on the machine and plan"
		}
	}
	if top > 0 && p.limit > top {
		p.limit, p.why = top, "the ceiling"
	}
	return p.limit
}

// get is the limit, 0 before its first read.
func (p *pace) get() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.limit
}

// String says the limit and why it is there.
func (p *pace) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("%d (%s)", p.limit, p.why)
}

// machine says why the machine is strained, or "", and whether any reading
// is both on and there to read.
func machine(cfg core.Config) (string, bool) {
	why := core.Strained(cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax)
	if why == "" {
		why = core.Loaded(cfg.LoadMax)
	}
	return why, core.Watched(cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax)
}

// planSpent says when every model a worker would go on has under planFloor
// of its plan left, all logins together, or "".
func (m *Module) planSpent(cfg core.Config) string {
	room := 0.0
	for _, p := range cfg.Route("worker", "") {
		room = max(room, m.C.Runs.Headroom(p))
	}
	if room >= planFloor {
		return ""
	}
	return fmt.Sprintf("the workers' plan is nearly spent (%.0f%% left)", room*100)
}

// paceWorkers is the workers' limit now, with working of them at work and
// waiting saying briefs wait for one.
func (m *Module) paceWorkers(cfg core.Config, working int, waiting bool) int {
	why, watching := machine(cfg)
	if why == "" {
		why = m.planSpent(cfg)
	}
	return m.workers.next(time.Now(), working, waiting, why, watching, cmp.Or(cfg.MaxWorkers, defaultWorkers), cfg.MaxWorkers)
}

// checkSlots is the checks' limit for the slots: the pace's, or before its
// first read where it starts.
func (m *Module) checkSlots() int {
	return cmp.Or(m.checks.get(), m.C.WS.LoadConfig().MaxChecks, core.DefaultMaxChecks())
}

// paceChecks moves the checks' limit and starts the checks it now has room for.
func (m *Module) paceChecks() {
	cfg := m.C.WS.LoadConfig()
	running, waiting := m.C.Effects.Slots.State()
	why, watching := machine(cfg)
	m.checks.next(time.Now(), len(running), len(waiting) > 0, why, watching, cmp.Or(cfg.MaxChecks, core.DefaultMaxChecks()), cfg.MaxChecks)
	m.C.Effects.Slots.Fill()
}

// Pace says the workers', checks' and recursive gates' limits now and why, for the dashboard
// and the lead.
func (m *Module) Pace() map[string]string {
	return map[string]string{"workers": m.workers.String(), "checks": m.checks.String(), "gates": m.gates.String()}
}
