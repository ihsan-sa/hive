package work

import (
	"cmp"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The pace: how many workers, and how many checks, start at once is not a
// fixed number but a limit that sets itself from what the machine and the
// plan carry. While something waits on the limit and nothing is strained, it
// grows by one a step; when the machine is strained (memory floor, memory or
// CPU pressure, load per core: the same settings that hold a turn) or the
// workers' plan is nearly spent or running ahead of its five-hour window,
// it pulls back to three quarters of what is running, or to its start when
// nothing runs (the strain isn't the Hive's, and from 1 it would take
// minutes to climb back). Nothing running is stopped; past the limit, work
// waits in order. It changes at most once a step, so the readings (load is
// a 1-minute average) catch up with the last change before the next.
// max_workers and max_checks, when set, are the person's ceilings and where
// the pace starts;
// with no machine reading on, or none to read (no /proc, as on macOS or
// Windows), it stays at its start, as it can't see. With no ceiling set, a
// safety ceiling sized to the machine (safeWorkers, safeChecks) keeps the
// pace from running away while its steps are untuned.

// paceStep is the least time between two changes of a limit.
const paceStep = time.Minute

// planFloor is the share of the plan left below which workers pull back.
const planFloor = 0.1

// aheadSlack is how far ahead of its five-hour window the workers' plan may
// run before workers pull back: a burst early in a window is fine, a pace
// that spends it an hour before its reset is not (10-25: three windows ran
// out 50 to 100 minutes early, and nothing moved until the reset).
const aheadSlack = 0.15

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
			l := max(1, min(p.limit, running)*3/4)
			if running == 0 {
				l = min(p.limit, start)
			}
			if l < p.limit {
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

// journal records a change of p's limit from before (0: its first read,
// not recorded) as a pace.changed event, so a sweep can tune the steps
// against what the machine and plan did.
func (m *Module) journal(name string, p *pace, before int) {
	p.mu.Lock()
	limit, why := p.limit, p.why
	p.mu.Unlock()
	if before != 0 && limit != before {
		m.C.J.MustAppend("pace.changed", map[string]any{"limit": name, "from": before, "to": limit, "why": why})
	}
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

// steady is a strain without its readings ("machine loaded", not the load
// now), for a reason saved with a task: readings that move each minute would
// re-save it each minute.
func steady(strain string) string {
	s, _, _ := strings.Cut(strain, " (")
	return s
}

// safeWorkers is the workers' ceiling when the person sets none: one a core,
// as a worker's own builds and tests run beside the model's turns.
func safeWorkers() int { return max(defaultWorkers, runtime.NumCPU()) }

// safeChecks is the checks' ceiling when the person sets none: twice where
// they start, as a check keeps several cores busy on its own.
func safeChecks() int { return 2 * core.DefaultMaxChecks() }

// planSpent says when every model a worker would go on, the workspace's
// route or any project's own, has under planFloor of its plan left, all
// logins together, or is spending its five-hour window more than aheadSlack
// ahead of the clock, or "".
func (m *Module) planSpent(cfg core.Config) string {
	room, ahead := 0.0, -1.0
	for _, project := range append([]string{""}, slices.Sorted(maps.Keys(cfg.Projects))...) {
		for _, p := range cfg.Route("worker", project) {
			room = max(room, m.C.Runs.Headroom(p))
			if a := m.C.Runs.Ahead(p); ahead < 0 || a < ahead {
				ahead = a
			}
		}
	}
	switch {
	case room < planFloor:
		return fmt.Sprintf("the workers' plan is nearly spent (%.0f%% left)", room*100)
	case ahead > aheadSlack:
		return fmt.Sprintf("the workers' plan is %.0f%% ahead of its five-hour window and would run out before it resets", ahead*100)
	}
	return ""
}

// paceWorkers is the workers' limit now, with working of them at work and
// waiting saying briefs wait for one. working leaves out quiet workers (see
// admit): they use no machine or plan, so the pace grows only when the
// active ones fill the limit, and a pull-back under strain starts from them.
func (m *Module) paceWorkers(cfg core.Config, working int, waiting bool) int {
	why, watching := machine(cfg)
	if why == "" {
		why = m.planSpent(cfg)
	}
	before := m.workers.get()
	defer m.journal("workers", &m.workers, before)
	return m.workers.next(time.Now(), working, waiting, why, watching, cmp.Or(cfg.MaxWorkers, defaultWorkers), cmp.Or(cfg.MaxWorkers, safeWorkers()))
}

// checkSlots is the checks' limit for the slots: the pace's, or before its
// first read where it starts.
func (m *Module) checkSlots() int {
	return cmp.Or(m.checks.get(), m.C.WS.LoadConfig().MaxChecks, core.DefaultMaxChecks())
}

// Check ranks: a check of a task queued briefs wait on goes first, then a
// fix's, then the rest in the order they asked. The worker on #601 finished
// with ~20 briefs waiting on it to land, and its check sat ~30 minutes
// behind 31 others (issue #838).
const (
	rankFix   = 1
	rankFrees = 2
)

// checkRank ranks a check of sha on project by the task whose head it is.
func (m *Module) checkRank(project, sha, _ string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var mine *Task
	for _, t := range m.tasks {
		if t.Project == project && t.SHA == sha && !t.Terminal() {
			mine = t
			break
		}
	}
	if mine == nil {
		return 0
	}
	for _, t := range m.tasks {
		if t.Queued() && holder(t.Reason) == mine.ID {
			return rankFrees
		}
	}
	if mine.Fix() {
		return rankFix
	}
	return 0
}

// paceChecks moves the checks' limit and starts the checks it now has room for.
func (m *Module) paceChecks() {
	cfg := m.C.WS.LoadConfig()
	running, waiting := m.C.Effects.Slots.State()
	why, watching := machine(cfg)
	before := m.checks.get()
	defer m.journal("checks", &m.checks, before)
	m.checks.next(time.Now(), len(running), len(waiting) > 0, why, watching, cmp.Or(cfg.MaxChecks, core.DefaultMaxChecks()), cmp.Or(cfg.MaxChecks, safeChecks()))
	m.C.Effects.Slots.Fill()
}

// Pace says the workers', checks' and recursive gates' limits now and why, for the dashboard
// and the lead.
func (m *Module) Pace() map[string]string {
	return map[string]string{"workers": m.workers.String(), "checks": m.checks.String(), "gates": m.gates.String()}
}
