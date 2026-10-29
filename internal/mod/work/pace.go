package work

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// How many run at once is fixed, not paced: every agent turn other than a
// lead's takes one of the core's slots (core.Config.TurnSlots, sized once
// from the machine's memory), a task has a worker only while fewer than
// workers() have one, and checks and gates each have their own count. None
// moves with load or pressure: the pace that did (grown each 5 minutes,
// pulled back under strain, a ramp after each hold) was retuned round after
// round and still sat at 1 worker and 1 check with 45 checks waiting on
// 10-29 (docs/log.md). A count is changed at once with the slots op, its why
// on the record. The workers' plan still holds new briefs back while it is
// nearly spent or ahead of its window (planSpent): that is what the plan has
// left, not what the machine reads.

// planFloor is the share of the plan left below which no new brief starts.
const planFloor = 0.1

// aheadSlack is how far ahead of a usage window (five-hour or weekly), as a
// share of the window, the workers' plan may run before new briefs wait: a
// burst early in a window is fine, a pace that spends it before its reset is
// not (10-25: three five-hour windows ran out 50 to 100 minutes early, and
// one login's week was 75% gone a day in, the other two's already out).
const aheadSlack = 0.15

// workers is how many tasks have a worker at once: max_workers, or three
// quarters of the slots, leaving the rest to their reviewers, sweeps and the
// other roles.
func workers(cfg core.Config) int { return cmp.Or(cfg.MaxWorkers, max(1, cfg.TurnSlots()*3/4)) }

// checks is how many checks run at once.
func checks(cfg core.Config) int { return cmp.Or(cfg.MaxChecks, core.DefaultMaxChecks()) }

// steady is a reason without its readings ("plan nearly spent", not the share
// left now), for a reason saved with a task: readings that move each minute
// would re-save it each minute.
func steady(why string) string {
	s, _, _ := strings.Cut(why, " (")
	return s
}

// planSpent says when every model a worker would go on, the workspace's
// route or any project's own, has under planFloor of its plan left, all
// logins together, or when any one route's every model is spending a usage
// window more than aheadSlack ahead of the clock, or "". The hold is
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
		return fmt.Sprintf("the workers' plan is ahead of the clock in a usage window and would run out before it resets (%.0f%% ahead)", ahead*100)
	}
	return ""
}

// checkSlots is how many checks run at once, read at every hand-off.
func (m *Module) checkSlots() int { return checks(m.C.WS.LoadConfig()) }

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

// namesAny says text names one of tests as a whole word: a Go subtest by
// its top test, a pytest id (path::test[params]) by its test or its file.
func namesAny(text string, tests []string) bool {
	for _, id := range tests {
		for _, n := range testNames(id) {
			if namesWord(text, n) {
				return true
			}
		}
	}
	return false
}

// testNames are the words that name a failing test's id.
func testNames(id string) []string {
	where, _, found := strings.Cut(id, "::")
	if !found {
		n, _, _ := strings.Cut(id, "/")
		return []string{n}
	}
	n := id[strings.LastIndex(id, "::")+2:]
	n, _, _ = strings.Cut(n, "[")
	if strings.HasSuffix(where, ".py") {
		return []string{n, path.Base(where)}
	}
	n, _, _ = strings.Cut(n, "/")
	return []string{n}
}

// namesWord says text holds n as a whole word.
func namesWord(text, n string) bool {
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
	return false
}

func isWord(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// Pace says how many workers, checks, recursive gates and turns run at once
// and where each number comes from, for the dashboard and the lead.
func (m *Module) Pace() map[string]string {
	cfg := m.C.WS.LoadConfig()
	here, away := m.C.Runs.InUse()
	from := func(set int, key, def string) string {
		if set > 0 {
			return key + " in config.json"
		}
		return def
	}
	return map[string]string{
		"workers": fmt.Sprintf("%d (%s)", workers(cfg), from(cfg.MaxWorkers, "max_workers", "three quarters of the slots")),
		"checks":  fmt.Sprintf("%d (%s)", checks(cfg), from(cfg.MaxChecks, "max_checks", "one per 6 cores, at least 2")),
		"gates":   fmt.Sprintf("%d (%s)", gateCeiling(cfg), from(cfg.MaxGates, "max_gates", "one per 6 cores")),
		"slots":   fmt.Sprintf("%d (%s); %d in use here, %d away", cfg.TurnSlots(), from(cfg.Slots, "slots", "sized from the machine's memory"), here, away),
	}
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

// slotsChange is the slots op's ask: each count given (a pointer, so 0, the
// default sized to the machine, can be asked for) replaces the config's.
type slotsChange struct {
	Slots   *count `json:"slots"`
	Workers *count `json:"workers"`
	Checks  *count `json:"checks"`
	Why     string `json:"why"`
}

// count is a number given as a number or as text, as the agent CLI passes
// every value.
type count int

func (n *count) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil {
		s = string(b)
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("%s isn't a count", b)
	}
	*n = count(v)
	return nil
}

// opSlots changes how many turns, workers or checks run at once, at once,
// with why on the record (slots.changed): the person wants no rule that
// can't be updated fast (10-28 12:24), so any agent that sees the machine
// under- or over-used changes the count and says why.
func (m *Module) opSlots(r core.Run, args json.RawMessage) (any, error) {
	var a slotsChange
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if a.Why = strings.TrimSpace(a.Why); a.Why == "" {
		return nil, fmt.Errorf("say why, with what you saw (runs waiting, memory, load): it goes on the record")
	}
	cfg, err := m.C.WS.ReadConfig()
	if err != nil {
		return nil, fmt.Errorf("config.json has to be fixed first: %v", err)
	}
	from := map[string]int{"slots": cfg.Slots, "workers": cfg.MaxWorkers, "checks": cfg.MaxChecks}
	to := maps.Clone(from)
	for name, v := range map[string]*count{"slots": a.Slots, "workers": a.Workers, "checks": a.Checks} {
		if v == nil {
			continue
		}
		if *v < 0 {
			return nil, fmt.Errorf("%s %d: give a count, or 0 for the default sized to the machine", name, *v)
		}
		to[name] = int(*v)
	}
	if maps.Equal(from, to) {
		return nil, fmt.Errorf("give slots, workers or checks different from now (%v)", from)
	}
	cfg.Slots, cfg.MaxWorkers, cfg.MaxChecks = to["slots"], to["workers"], to["checks"]
	if err := m.C.WS.SaveConfig(cfg); err != nil {
		return nil, err
	}
	m.C.J.MustAppend("slots.changed", map[string]any{"by": r.ID, "why": core.Clip(a.Why, 500), "from": from, "to": to})
	m.C.Effects.Slots.Fill()
	return m.Pace(), nil
}
