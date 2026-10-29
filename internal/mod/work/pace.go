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
// grows by one once nothing has strained for a growStep; when the machine is
// strained (memory floor, memory or CPU pressure, load per core: the same
// settings that hold a turn; or IO pressure, which holds it at what runs) or the workers' plan is nearly spent or running
// ahead of its five-hour or weekly window, it pulls back to three quarters
// of what is running, or to its start when nothing runs (the strain isn't
// the Hive's, and from 1 it would take minutes to climb back). Load alone
// never pulls the workers under their start, and brings them back up to it
// a growStep after any other strain clears: on 10-25 a load of 27-46 on 20
// cores came from child Hives and builds beside 2-3 live turns, and cutting
// the workers to 1 or 2 hardly moved it while 51 tasks waited. Nor does load alone hold
// a worker from starting while fewer than the start work (loadHolds), and it
// doesn't hide a spent or ahead plan, which still pulls the workers under
// it. Checks and gates do go under their start for load alone (loadCuts):
// theirs are the builds and child Hives that load the machine; but not for
// a load that is the disk's (onlyDisk), which cutting them didn't lift. Nothing
// running is stopped; past the limit, work waits in order. It
// pulls back at most once a step, so the readings (load is a 1-minute
// average) catch up with the last change before the next, and grows more
// slowly than it pulls back, so a load hovering round its limit doesn't
// swing it every minute (10-25: 30-55 changes an hour, 1,2,1,2,3,4,3,…).
// max_workers and max_checks, when set, are the person's ceilings and where
// the pace starts;
// with no machine reading on, or none to read (no /proc, as on macOS or
// Windows), it stays at its start, as it can't see. With no ceiling set, a
// safety ceiling sized to the machine (safeWorkers, safeChecks) keeps the
// pace from running away while its steps are untuned.

// paceStep is the least time between two changes of a limit.
const paceStep = time.Minute

// growStep is the least time since the last change, and since the last
// strain, before a limit grows.
const growStep = 5 * time.Minute

// planFloor is the share of the plan left below which workers pull back.
const planFloor = 0.1

// aheadSlack is how far ahead of a usage window (five-hour or weekly), as a
// share of the window, the workers' plan may run before workers pull back: a
// burst early in a window is fine, a pace that spends it before its reset is
// not (10-25: three five-hour windows ran out 50 to 100 minutes early, and
// one login's week was 75% gone a day in, the other two's already out).
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
	shown time.Time     // when a strain was last seen
	hard  time.Time     // when a strain other than load alone was last seen
	// loadCuts lets load alone pull the limit under its start, as for
	// checks and gates: their builds and child Hives are what load the
	// machine, unlike the workers' turns.
	loadCuts bool
}

// onlyLoad says whether strain is the load per core alone (core.Loaded's
// reason), which doesn't pull a limit under its start, nor hold a worker
// from starting under it (loadHolds).
func onlyLoad(strain string) bool { return strings.HasPrefix(strain, core.LoadedReason) }

// onlyDisk says whether strain is a load that is tasks waiting on the disk
// (core.DiskReason), which doesn't pull even the checks or gates under their
// start: on 10-27 the checks sat at 1 for 6.9 of 12 hours under a load of
// 30-88 that was IO wait, and the load never fell with them.
func onlyDisk(strain string) bool { return strings.HasPrefix(strain, core.DiskReason) }

// onlyIO says whether strain is IO pressure over its limit (core.IOStrained):
// a saturated disk, read on its own rather than through the load. It holds
// the limit at what runs, so no more turns or checks start while it lasts,
// stops nothing running, and lets the limit grow back as it falls.
func onlyIO(strain string) bool { return strings.HasPrefix(strain, core.IOReason) }

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
	step := cmp.Or(p.step, paceStep)
	switch {
	case strain != "":
		p.shown = now
		soft := onlyLoad(strain) && (!p.loadCuts || onlyDisk(strain))
		if !soft {
			p.hard = now
		}
		if now.Sub(p.at) < step {
			if onlyIO(strain) && p.limit <= running {
				p.why = "held: " + strain
			}
			break
		}
		if soft && p.limit < start && now.Sub(p.at) >= max(step, growStep) && now.Sub(p.hard) >= max(step, growStep) {
			// Pulled under its start by another strain or the plan, it
			// comes back once only load is left: load alone would otherwise
			// keep it there, as each minute of it blocks growth.
			p.limit, p.at, p.why = start, now, "back to the start: only load strains"
			break
		}
		floor := 1
		if soft {
			floor = start
		}
		l := max(floor, min(p.limit, running)*3/4)
		if onlyIO(strain) { // hold at what runs: nothing more starts on the disk
			l = max(1, min(p.limit, running))
		}
		if running == 0 {
			l = min(p.limit, start)
		}
		if l < p.limit {
			p.limit, p.at, p.why = l, now, "pulled back: "+strain
		} else if onlyIO(strain) {
			p.why = "held: " + strain
		}
	case waiting && running >= p.limit && (top == 0 || p.limit < top) &&
		now.Sub(p.at) >= max(step, growStep) && now.Sub(p.shown) >= max(step, growStep):
		p.limit, p.at, p.why = p.limit+1, now, "grew: room on the machine and plan"
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
		why = core.IOStrained(cfg.IOPressureMax, cfg.IOFullMax)
	}
	if why == "" {
		why = core.Loaded(cfg.LoadMax)
	}
	return why, core.Watched(cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, max(cfg.IOPressureMax, cfg.IOFullMax), cfg.LoadMax)
}

// loadHolds says whether strain holds a new worker with working of them at
// work from start: every strain does, but load alone only past the start, as
// the few workers there aren't what loads the machine.
func loadHolds(strain string, working, start int) bool {
	return strain != "" && (!onlyLoad(strain) || working >= start)
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
// logins together, or when any one route's every model is spending a usage
// window more than aheadSlack ahead of the clock, or "". The pull-back is
// read route by route: a free model on one project's route (which has no
// windows, so reads on the clock) left the min over every route at 0, and
// it never fired while Claude's week, all of the Hive's own route, went from
// 26% to 65% in a day at 14% of the week passed (issue #1797).
func (m *Module) planSpent(cfg core.Config) string {
	room, ahead := 0.0, 0.0
	for _, project := range append([]string{""}, slices.Sorted(maps.Keys(cfg.Projects))...) {
		least := -1.0 // the least ahead model on this route
		for _, p := range cfg.Route("worker", project) {
			room = max(room, m.C.Runs.Headroom(p))
			if a := m.C.Runs.Ahead(p); least < 0 || a < least {
				least = a
			}
		}
		ahead = max(ahead, least)
	}
	switch {
	case room < planFloor:
		return fmt.Sprintf("the workers' plan is nearly spent (%.0f%% left)", room*100)
	case ahead > aheadSlack:
		return fmt.Sprintf("the workers' plan is %.0f%% ahead of the clock in a usage window and would run out before it resets", ahead*100)
	}
	return ""
}

// paceWorkers is the workers' limit now, with working of them at work and
// waiting saying briefs wait for one. working leaves out quiet workers (see
// admit): they use no machine or plan, so the pace grows only when the
// active ones fill the limit, and a pull-back under strain starts from them.
func (m *Module) paceWorkers(cfg core.Config, working int, waiting bool) int {
	why, watching := machine(cfg)
	if why == "" || onlyLoad(why) { // load alone stops at the start; the plan doesn't
		why = cmp.Or(m.planSpent(cfg), why)
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

// Check ranks: main's own measurement goes first, then the fix of a red
// main's failing test, then any other fix's, then the rest in the order they
// asked. Main's score and size of 282991d7 waited 2h16m behind PR checks,
// and keep/revert, the release and the next baseline with them (issue
// #1078); a red main's fix waited behind 29 other fixes' checks that failed
// on the same red, and every merge with it (issue #1877). Both go past the
// line's patience: there is one red main at a time, and few fixes of it.
const (
	rankFix    = 1
	rankRedFix = 2
	rankMain   = 3
)

// MeasuringMain marks sha as main's commit being measured on project until
// the returned done is called: its checks go ahead of the line.
func (m *Module) MeasuringMain(project, sha string) (done func()) {
	key := project + "@" + sha
	m.mu.Lock()
	m.measuring[key]++
	m.mu.Unlock()
	m.C.Effects.Slots.Fill()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.measuring[key]--; m.measuring[key] <= 0 {
				delete(m.measuring, key)
			}
		})
	}
}

// RankWhy says why a waiting check of rank r goes ahead of the line, or ""
// for one that keeps its place, for the dashboard and the status op.
func RankWhy(r int) string {
	switch {
	case r >= rankMain:
		return "measures main"
	case r == rankRedFix:
		return "fixes red main"
	case r == rankFix:
		return "a fix"
	}
	return ""
}

// checkRank ranks a check of sha on project: main's being measured, or by
// the task whose head it is.
func (m *Module) checkRank(project, sha, _ string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.measuring[project+"@"+sha] > 0 {
		return rankMain
	}
	for _, t := range m.tasks {
		if t.Project == project && t.SHA == sha && !t.Terminal() {
			if t.Fix() && namesAny(t.Title+"\n"+t.Known+"\n"+t.AimWhy, m.mainFails[project]) {
				return rankRedFix
			}
			if t.Fix() {
				return rankFix
			}
			return 0
		}
	}
	return 0
}

// namesAny says text names one of tests (a subtest by its top test) as a
// whole word.
func namesAny(text string, tests []string) bool {
	for _, n := range tests {
		n, _, _ = strings.Cut(n, "/")
		for rest, at := text, 0; n != ""; {
			i := strings.Index(rest, n)
			if i < 0 {
				break
			}
			start, end := at+i, at+i+len(n)
			if (start == 0 || !isWord(text[start-1])) && (end == len(text) || !isWord(text[end])) {
				return true
			}
			rest, at = text[start+1:], start+1
		}
	}
	return false
}

func isWord(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
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

// pacedRead is the route a review of a change on project goes on while the
// reviewer's plan spends its week more than aheadSlack ahead of the clock,
// and why, or "" when it is on pace. The workers' pace holds new changes;
// this holds what reading each one costs, as reads follow the changes in
// flight: each read goes on the route's cheaper model, sonnet where the
// route is opus. On 10-27 a login's week stood at 48% with a tenth of it
// gone. How many reads a change needs doesn't change: a high class still
// gets its second, separate read, as the warden's and tribunal's prompts
// are high so they never land on one (docs/log.md). A change on the
// security list is read as ever (the caller leaves it out).
func (m *Module) pacedRead(project string) ([]core.Pick, string) {
	route := m.Agents.Route("reviewer", project)
	if len(route) == 0 {
		return nil, ""
	}
	a := m.C.Runs.AheadIn(route[0], "seven_day", "reviewer")
	if a <= aheadSlack {
		return nil, ""
	}
	c := cheaper(route)
	return append([]core.Pick{c}, slices.DeleteFunc(slices.Clone(route), c.Same)...), fmt.Sprintf("the reviewer's plan is %.0f%% ahead of its week", a*100)
}

// cheaper is the cheaper pick for a read on route: a later sonnet or haiku
// in it, else sonnet on the first's harness where that is an opus on
// Claude, else the first.
func cheaper(route []core.Pick) core.Pick {
	first := route[0]
	if !strings.Contains(first.Model, "opus") {
		return first
	}
	for _, p := range route[1:] {
		if strings.Contains(p.Model, "sonnet") || strings.Contains(p.Model, "haiku") {
			return p
		}
	}
	if core.HarnessName(first.Harness) == "claude" {
		first.Model = strings.Replace(first.Model, "opus", "sonnet", 1)
	}
	return first
}
