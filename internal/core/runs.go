package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Run statuses.
const (
	StatusQueued  = "queued"  // waiting for a sandbox or a slot
	StatusRunning = "running" // a turn is in progress
	StatusIdle    = "idle"    // between turns, waiting for a message (leads)
	StatusParked  = "parked"  // waiting for a usage reset, a retry, or the API
	StatusQuiet   = "quiet"   // ended turns without finishing; flagged, kept
	StatusDone    = "done"
	StatusBlocked = "blocked" // stopped and said why
	StatusStopped = "stopped" // the Hive stopped it on purpose
)

// RunSpec is what a run is asked to do and what it has been granted.
type RunSpec struct {
	ID         string            `json:"id"`
	Role       string            `json:"role"`
	Name       string            `json:"name,omitempty"`
	Project    string            `json:"project,omitempty"`
	Task       string            `json:"task,omitempty"`
	Parent     string            `json:"parent,omitempty"`
	Checkout   string            `json:"checkout"`
	ReadOnly   bool              `json:"read_only,omitempty"`
	Harness    string            `json:"harness"`
	Model      string            `json:"model,omitempty"`
	Schema     string            `json:"schema,omitempty"`
	System     string            `json:"system"`
	Brief      string            `json:"brief"`
	Persistent bool              `json:"persistent,omitempty"` // leads idle between turns instead of finishing
	Isolated   bool              `json:"isolated,omitempty"`   // no view of other projects
	Modules    []string          `json:"modules,omitempty"`    // modules an isolated run may see; others see all
	Tools      []string          `json:"tools,omitempty"`      // explicit harness tool list
	Inputs     map[string]string `json:"inputs,omitempty"`     // name -> content, placed read-only in /hive/run
	OneTurn    bool              `json:"one_turn,omitempty"`   // done when a turn ends; nothing is added to its prompts, and it may only finish and report progress
	Budget     float64           `json:"budget,omitempty"`     // USD the whole run may spend; 0 is no cap
	// Route is the harnesses and models the run may go on, in order
	// (Config.Route): when its own are at their usage limit it moves to the
	// first of these with room. Empty stays and waits.
	Route []Pick `json:"route,omitempty"`
}

// Finish is an agent's closing account.
type Finish struct {
	Status  string `json:"status"` // done | blocked
	Account string `json:"account"`
	Tested  string `json:"tested,omitempty"`
	Unsure  string `json:"unsure,omitempty"`
	Printed bool   `json:"printed,omitempty"` // the agent printed it as its answer and the Hive ran it
}

// Run is a live or past agent.
type Run struct {
	RunSpec
	Session     string          `json:"session"`
	SessionLive bool            `json:"session_live"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason,omitempty"`
	Turn        int             `json:"turn"`
	Proc        proc.Handle     `json:"proc"`
	Cost        float64         `json:"cost"`
	Context     int             `json:"context,omitempty"` // tokens in its session at its last reply
	Result      string          `json:"result,omitempty"`
	Structured  json.RawMessage `json:"structured,omitempty"`
	Finish      *Finish         `json:"finish,omitempty"`
	Pending     []string        `json:"pending,omitempty"`
	Given       []string        `json:"-"` // the messages its current or last turn was handed
	ParkedUntil time.Time       `json:"parked_until,omitempty"`
	ParkedSince time.Time       `json:"parked_since,omitempty"` // when it last went from another status to parked
	Retries     int             `json:"retries"`
	Offline     string          `json:"offline,omitempty"` // its last turn couldn't reach its service, and why
	Nudges      int             `json:"nudges"`
	Head        string          `json:"head,omitempty"`  // its checkout's HEAD log (size, time) at its last turn's end
	Still       int             `json:"still,omitempty"` // turns in a row that ended with that log unchanged
	Login       string          `json:"login,omitempty"` // the login its latest turn went on
	Where       string          `json:"where,omitempty"` // the runner its latest turn went to; empty is this machine
	Progress    string          `json:"progress,omitempty"`
	ProgressAt  time.Time       `json:"progress_at,omitempty"`
	StopAsked   bool            `json:"stop_asked,omitempty"`
	Waiting     string          `json:"waiting,omitempty"`  // what it ended its turn waiting for
	Reopened    bool            `json:"reopened,omitempty"` // reopened while its turn ran: the turn's finish doesn't end it
	Returned    bool            `json:"returned,omitempty"` // reopened after its turn ended, with no turn since: Admit may hold it
	Created     time.Time       `json:"created"`
	Updated     time.Time       `json:"updated"`
	TurnStarted time.Time       `json:"turn_started,omitempty"`
	LastOutput  time.Time       `json:"last_output,omitempty"`
	Moves       []Move          `json:"moves,omitempty"`   // each change of harness, so a past turn is read as it was written
	Stalled     []string        `json:"stalled,omitempty"` // picks its turns kept ending on without finishing, so its route moved it past them
	SessionCost SessionSpend    `json:"session_cost"`
}

// SessionSpend turns the cost a harness reports into what each turn spent.
// Claude Code's total_cost_usd is its session's running total, and a resumed
// session carries it on, so every result line repeats all earlier turns'
// spend; summing them counted a long resumed session (the lead's) many times
// over. It holds the session and its total at the last result line.
type SessionSpend struct {
	Session string  `json:"session,omitempty"`
	Total   float64 `json:"total,omitempty"`
}

// Turn is what a turn ending with this session's running total spent: the
// rise since the last result line in the same session, or all of it in a new
// session (or one whose total started again).
func (s *SessionSpend) Turn(session string, total float64) float64 {
	spent := total
	if session == s.Session && total >= s.Total {
		spent = total - s.Total
	}
	s.Session, s.Total = session, total
	return spent
}

// Move is a run going from one harness to another at a turn.
type Move struct {
	Turn int    `json:"turn"`
	From string `json:"from"`
	To   string `json:"to"`
}

// HarnessAt is the harness the run's turn ran on.
func (r *Run) HarnessAt(turn int) string {
	h := r.Harness
	for i := len(r.Moves) - 1; i >= 0 && turn < r.Moves[i].Turn; i-- {
		h = r.Moves[i].From
	}
	return h
}

// Finishes reports whether the run ends only on its own `hive agent finish`
// (a worker, a helper), not on a structured answer, one turn or never.
func (r Run) Finishes() bool {
	return !r.Persistent && !r.ReadOnly && !r.OneTurn && r.Schema == ""
}

// Terminal reports whether the run will take no more turns on its own.
func (r *Run) Terminal() bool {
	switch r.Status {
	case StatusDone, StatusBlocked, StatusStopped:
		return true
	}
	return false
}

// Lead reports a lead run: the Hive-wide lead or a project's. A lead
// answers the person however busy the machine is: its turn starts ahead of
// every other run's, never waits for pressure, and runs at a higher
// scheduling priority.
func (r *Run) Lead() bool { return r.Role == "lead" }

// Nice is the Unix nice value the run's processes get (higher is lower
// priority; a process can only go higher): the Hive-wide lead at the
// daemon's own, a project's lead just under it, and every other role well
// under both, so leads keep the machine when workers fill it. Checks and
// gate runs go under all of them (CheckNice).
func (r *Run) Nice() int {
	switch {
	case r.Role == "lead" && r.Project == "":
		return 0
	case r.Role == "lead":
		return 2
	}
	return 10
}

// CheckNice is the nice value checks and gate runs get: under every run.
const CheckNice = 15

// LeadMemFloorMB is the one hold a lead's turn has: below this much
// available memory its harness would be killed before it answered, so it
// waits (saying why once, as run.held) rather than crash. Pressure never
// holds a lead, and mem_floor_mb above this is for the other roles.
const LeadMemFloorMB = 256

// Paths of one run's folders.
func (ws Workspace) RunHost(id string) string { return filepath.Join(ws.Run(id), "host") }
func (ws Workspace) RunRO(id string) string   { return filepath.Join(ws.Run(id), "ro") }
func (ws Workspace) RunBox(id string) string  { return filepath.Join(ws.Run(id), "box") }
func (ws Workspace) RunHome(id string) string { return filepath.Join(ws.RunPrivate(id), "home") }

// RunPrivate holds what of a run grants access: its home and CODEX_HOME.
func (ws Workspace) RunPrivate(id string) string { return filepath.Join(ws.Private(), "runs", id) }

// TurnOut is the streamed output of one turn.
func (ws Workspace) TurnOut(id string, turn int) string {
	return filepath.Join(ws.RunHost(id), fmt.Sprintf("turn-%03d.jsonl", turn))
}

// Plan is the latest usage a harness reported for a login.
type Plan struct {
	Harness string            `json:"harness"`
	Login   string            `json:"login,omitempty"` // Login.Key; empty is the machine's own
	Limit   harness.RateLimit `json:"limit"`
	At      time.Time         `json:"at"`
	// Lifted says its five-hour stop doesn't hold: another login of its pick
	// keeps the person's tenth (lift). Set only on the copies Runs hands out
	// (Plans carries it to the control center), never on a journaled one.
	Lifted bool `json:"lifted,omitempty"`
	// Stops are the usage stops it is held to (Config.UsageStops), set like
	// Lifted on the copies Runs hands out; with none it runs to its limit.
	Stops *UsageStops `json:"stops,omitempty"`
	// Wait is the park a limit whose named reset had already passed got;
	// the next such limit in a row doubles it, up to the hour.
	Wait time.Duration `json:"wait,omitempty"`
}

// Key names the plan's login.
func (p Plan) Key() string {
	if p.Login == "" {
		return HarnessName(p.Harness)
	}
	return p.Login
}

// UsageStops are the shares of a plan login's windows, in percent, at which
// it stops taking turns until that window resets, short of the plan's own
// limit; 0 runs it to the limit. They are the person's to set (config.json's
// usage_stops): the person asked (10-27) for "some mechanism that enables
// setting usage cutoffs, not a hard 90% rule for all users".
type UsageStops struct {
	// Week holds every login back at this share of its weekly window, and
	// every run but the checks and leads CheckReserve before it.
	Week float64 `json:"week,omitempty"`
	// FiveHour holds back one login of a pick, the one with the most of its
	// five hours left, while the others run to their limit (lift): the rest
	// of that one's window stays the person's, to use the plan themselves.
	FiveHour float64 `json:"five_hour,omitempty"`
}

// OldUsageStops are the stops every install had before they were settings,
// which a config from then keeps: 95% of the week (the person, 10-25:
// "pause at 95%" until the week resets) and 90% of the five hours (10-26:
// "90% of the only 5h limit avail", the last tenth theirs). They were this
// Hive's own (10-27: "only a thing for THIS hive"), so a new install has
// none.
var OldUsageStops = UsageStops{Week: 95, FiveHour: 90}

func (u UsageStops) check() error {
	for _, v := range []float64{u.Week, u.FiveHour} {
		if v < 0 || v > 100 {
			return fmt.Errorf("usage_stops: %v isn't a percent from 0 (no stop) to 100", v)
		}
	}
	return nil
}

// CheckReserve is the share of a week below its stop kept for the checks
// and leads (Reserved): every other run stops at the week's stop less
// CheckReserve, so a change already written can still be read, judged and
// merged, and the stop stays the ceiling for all. On 10-26 a gate's keep
// waited over 8 hours for its judge, parked at 95% of every login's week,
// and main didn't move for 12 (issue #983). The five-hour stop has no
// reserve: what is left above it is the person's.
const CheckReserve = 0.05

// Reserved says whether role's runs may spend the reserve: the checks
// (CheckRoles), and the leads, which answer however busy the machine is
// (docs/priority.md) and stopped at the week's stop before the reserve.
func Reserved(role string) bool { return role == "lead" || slices.Contains(CheckRoles, role) }

// stop is a window a login stops at short of its limit, and where; at less
// reserve for a run that isn't Reserved; one that may be lifted holds only
// for the last login with room (lift).
type stop struct {
	window   string
	at       float64
	reserve  float64
	name     string
	liftable bool
}

// stops are the windows u stops a login at.
func (u *UsageStops) stops() []stop {
	var out []stop
	if u == nil {
		return nil
	}
	if u.Week > 0 {
		out = append(out, stop{"seven_day", u.Week / 100, min(CheckReserve, u.Week/100), "week", false})
	}
	if u.FiveHour > 0 {
		out = append(out, stop{"five_hour", u.FiveHour / 100, 0, "five-hour window", true})
	}
	return out
}

// Parked reports whether the plan is at its usage limit, or at one of its
// Stops (the week's less CheckReserve) for a run that isn't Reserved, until
// a reset still ahead.
func (p Plan) Parked(now time.Time) bool { return !p.ParkedUntil(now).IsZero() }

// ParkedUntil is when a parked plan (Parked) takes turns again, the latest
// of its limit's reset and those of the windows at their stop; zero when it
// isn't parked.
func (p Plan) ParkedUntil(now time.Time) time.Time { return p.parkedFor(now, false) }

// parkedFor is ParkedUntil for a run that may spend the reserve (Reserved)
// or not: the reserve runs on to the week's stop.
func (p Plan) parkedFor(now time.Time, reserve bool) time.Time {
	var at time.Time
	if p.Limit.Status == "rejected" && p.Limit.ResetsAt.After(now) {
		at = p.Limit.ResetsAt
	}
	if w, _, ok := p.stopped(now, reserve); ok && w.ResetsAt.After(at) {
		at = w.ResetsAt
	}
	return at
}

// stopped is the window of p at its stop that resets last, and its name.
func (p Plan) stopped(now time.Time, reserve bool) (harness.Window, string, bool) {
	var out harness.Window
	var name string
	for _, s := range p.Stops.stops() {
		if s.liftable && p.Lifted {
			continue
		}
		at := s.at
		if !reserve {
			at -= s.reserve
		}
		if w, ok := p.window(s.window, now); ok && w.Utilization >= at && w.ResetsAt.After(out.ResetsAt) {
			out, name = w, s.window
		}
	}
	return out, name, name != ""
}

// stale reports whether p, a reading short of a rejection taken while old is
// parked, was taken before the park: it parks the login for less time, and
// the window that parked old (one at its stop, or its limit's) hasn't reset
// since. A stop is measured against its own window's reset, not the one the
// reading's top level carries.
func (p Plan) stale(old Plan, now time.Time) bool {
	until := old.ParkedUntil(now)
	if !p.ParkedUntil(now).Before(until) {
		return false
	}
	if w, k, ok := old.stopped(now, false); ok && w.ResetsAt.Equal(until) {
		pw, ok := p.window(k, now)
		return !ok || !pw.ResetsAt.After(until)
	}
	return !p.Limit.ResetsAt.After(until)
}

// Standing is the login's state in plain words (RateLimit.Standing), or
// that it has stopped at one of its stops until that window resets.
func (p Plan) Standing(now time.Time) string {
	if w, k, ok := p.stopped(now, false); ok && !(p.Limit.Status == "rejected" && p.Limit.ResetsAt.After(now)) {
		for _, s := range p.Stops.stops() {
			if s.window != k {
				continue
			}
			if w.Utilization < s.at {
				return fmt.Sprintf("paused at %.0f%% of its %s until it resets; the checks and leads run on to %.0f%%", (s.at-s.reserve)*100, s.name, s.at*100)
			}
			return fmt.Sprintf("paused at %.0f%% of its %s until it resets", s.at*100, s.name)
		}
	}
	return p.Limit.Standing(now)
}

// window is the plan's window k while it runs at now: from the windows, or
// the top level when that is the window it reports.
func (p Plan) window(k string, now time.Time) (harness.Window, bool) {
	w, ok := p.Limit.Windows[k]
	if !ok && p.Limit.Window == k {
		w, ok = harness.Window{Utilization: p.Limit.Utilization, ResetsAt: p.Limit.ResetsAt}, true
	}
	return w, ok && w.ResetsAt.After(now)
}

// Used is how much of its plan the login has used: the fullest of its usage
// windows still running at now. Claude reports the windows' figures apart
// from the top-level one, which it leaves at 0, and a window whose reset has
// passed has started again from nothing.
func (p Plan) Used(now time.Time) float64 {
	var u float64
	if p.Limit.ResetsAt.After(now) {
		u = p.Limit.Utilization
	}
	for _, w := range p.Limit.Windows {
		if w.ResetsAt.After(now) {
			u = max(u, w.Utilization)
		}
	}
	return u
}

// windowLength is how long a usage window runs, 0 for one the Hive doesn't
// know: the five hours, and the week with its per-model windows
// (seven_day_opus). The week is paced too: on 10-25 it stood at 75% a day
// after its reset, and two logins sat at their weekly limit for days.
func windowLength(k string) time.Duration {
	switch {
	case strings.HasPrefix(k, "five_hour"):
		return 5 * time.Hour
	case strings.HasPrefix(k, "seven_day"):
		return 7 * 24 * time.Hour
	}
	return 0
}

// Ahead is how far the login has used its plan ahead of the clock: the most
// a paced window's use is beyond the share of it that has passed, 0 when on
// or behind it. The share is of each window's own length, so the same slack
// is 45 minutes of a five-hour window and about a day of the week. A window spent at this rate runs out before it resets and
// the Hive sits idle until it does.
func (p Plan) Ahead(now time.Time) float64 { return p.AheadIn(now, "") }

// AheadIn is Ahead over the windows whose name starts with window
// ("seven_day" for the week, "" for every paced one).
func (p Plan) AheadIn(now time.Time, window string) float64 {
	a := 0.0
	for k, w := range p.Limit.Windows {
		n := windowLength(k)
		if n == 0 || !strings.HasPrefix(k, window) || !w.ResetsAt.After(now) {
			continue
		}
		passed := 1 - float64(w.ResetsAt.Sub(now))/float64(n)
		a = max(a, w.Utilization-passed)
	}
	return a
}

// TokenSource lists a harness's logins and hands out a login's short-lived credential.
type TokenSource interface {
	Logins(h string) []Login
	AgentEnv(l Login, home string) ([]string, error)
}

// Runs owns every agent run.
type Runs struct {
	ws      Workspace
	j       *Journal
	sandbox func() (sandbox.Sandbox, error)
	tokens  TokenSource
	tools   func() []string // host programs to expose (hive binary for the sandbox's OS)
	ops     *Ops
	projs   *Projects // for which projects' records to hide; nil hides none
	// Route is the picks a persistent run's next turn follows, so a lead
	// follows a changed setting; empty keeps the run's own and its Route.
	Route func(Run) []Pick
	// Executor hands a turn this machine is too strained to start to a
	// module that runs it elsewhere: the sandbox to start it in and the
	// runner that has room, or nil when no module has room now.
	Executor func() (sandbox.Sandbox, string)
	// Admit says why a run's next turn waits on a module, or "": the work
	// module's pace for a worker sent back to its task (Returned).
	Admit func(Run) string

	mu      sync.Mutex
	runs    map[string]*Run
	plans   map[string]Plan
	busy    map[string]bool    // runs whose tick work is in flight
	spent   map[string]float64 // "2006-01" (UTC) -> what runs cost that month
	holds   map[string]string  // why no new turn starts (a restart waiting for a quiet moment), by who holds them
	down    map[string]Down    // picks that can't answer, by Pick.String
	strikes map[string]strike  // each pick's straight turns ending in the same error
	cfg     Config             // the config of the latest tick, which a Down is fingerprinted against
	usage   UsageStops         // the config's usage stops, which the plans it hands out are held to
	// proven is the picks (Pick.String) some run has finished done on, every
	// turn of it on that pick: the only ones a run that must finish falls
	// back to (next). ranOn is the pick each run's turns ran on, "" once they
	// ran on more than one, so a run that did its work on opus and finished on
	// a fallback proves neither.
	proven map[string]bool
	ranOn  map[string]string
	// launching counts launches past the hold whose turn isn't journalled
	// yet; leadLaunching those of leads, which the other roles' wait for.
	launching, leadLaunching int
	starting                 map[string]bool // runs with a launch in flight
	deferred                 map[string]int  // ticks a run's launch has waited behind leads', in a row
	passing                  bool            // a tick's pass over the other runs is waiting for the leads' steps
	leadWait                 time.Duration   // how long the other runs wait for the leads' steps (0: 5s)
	self                     proc.Handle     // this daemon, as a turn's claimant (claim.go)
	backedOff                map[string]int  // the turn each run last backed off from for another daemon's claim, said once
	turnLive                 map[string]int  // the turn each run last waited on its own last turn for, said once
	waking                   sync.Mutex      // one wake at a time, so a run is woken once
	// heldAt is when a turn last waited on a strained machine, rampLast
	// when the ramp after it last let one start, and rampedAt when it last
	// held one back (ramp).
	heldAt, rampLast, rampedAt time.Time
}

// rampStep is the least time between two turns starting after a strain hold
// (ramp), so the readings see each before the next: on 10-28, the hold
// lifting started every run it had held at once, and the Hive's slice ran to
// 42 claude, 87 link and 40 compile processes, load near 400 and an OOM kill.
var rampStep = 30 * time.Second

// Holding marks a strain hold now, outside launch (a brief the work module
// held on one), so the turns after it ramp up.
func (rs *Runs) Holding() {
	rs.mu.Lock()
	rs.heldAt = time.Now()
	rs.mu.Unlock()
}

// ramp says whether a turn starting now waits for the ramp: after a strain
// hold, turns start one a rampStep until none has waited on the ramp for two.
func (rs *Runs) ramp(now time.Time) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if now.Sub(rs.heldAt) > 2*rampStep && now.Sub(rs.rampedAt) > 2*rampStep {
		return false
	}
	if now.Sub(rs.rampLast) < rampStep {
		rs.rampedAt = now
		return true
	}
	rs.rampLast = now
	return false
}

// Hold keeps new turns from starting, saying why; "" lets them start again.
func (rs *Runs) Hold(why string) { rs.HoldFor("restart", why) }

// HoldFor is Hold for one holder among several; turns start once none holds them.
func (rs *Runs) HoldFor(who, why string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if why == "" {
		delete(rs.holds, who)
		return
	}
	if rs.holds == nil {
		rs.holds = map[string]string{}
	}
	rs.holds[who] = why
}

// Holds says why no new turn starts, one reason per holder in holder order;
// none when turns start.
func (rs *Runs) Holds() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	who := slices.Sorted(maps.Keys(rs.holds))
	why := make([]string, 0, len(who))
	for _, w := range who {
		why = append(why, rs.holds[w])
	}
	return why
}

// Live counts the turns whose process is running, and those starting.
func (rs *Runs) Live() int {
	n := len(rs.List(func(r *Run) bool { return r.Status == StatusRunning && proc.Alive(r.Proc) }))
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return n + rs.launching
}

// codexTokenStuck opens the note on a run whose Codex token folder would not delete.
const codexTokenStuck = "its Codex token could not be deleted: "

// NewRuns builds the run manager and replays its records.
func NewRuns(ws Workspace, j *Journal, sb func() (sandbox.Sandbox, error), tokens TokenSource, tools func() []string) *Runs {
	rs := &Runs{ws: ws, j: j, sandbox: sb, tokens: tokens, tools: tools, runs: map[string]*Run{}, plans: map[string]Plan{}, busy: map[string]bool{}, spent: map[string]float64{}, down: map[string]Down{}, strikes: map[string]strike{}, self: selfHandle(), backedOff: map[string]int{}, turnLive: map[string]int{}, proven: map[string]bool{}, ranOn: map[string]string{}}
	rs.ops = NewOps(rs)
	rs.usage = ws.LoadConfig().UsageStops
	_ = j.Replay(rs.apply)
	j.Subscribe(func(e Event) {
		rs.mu.Lock()
		rs.apply(e)
		rs.mu.Unlock()
	})
	return rs
}

// Migrate is the daemon's start-up sweep of what grants access, run once
// before it publishes its token. Versions before .hive/private kept each run's
// home and Codex home in the readable runs/<id>/, the turn specs (carrying the
// turn's token) beside the turn's output, and daemon.json in .hive; those move
// under Private or go. A turn the earlier daemon started mounts .hive with no
// cover over private/, so every live turn is ended and waited out first (as
// Stop ends one) and nothing moves while one is alive. A Codex token a crash
// left between writing it and the turn's start goes too. An error names what
// is still readable; the daemon does not start agents until it is gone.
func (rs *Runs) Migrate() error {
	// Any old-layout path means an earlier daemon ran here, so its turns
	// may still be alive with no cover over private/.
	if old := rs.legacyPaths(); len(old) > 0 {
		if err := rs.endOldTurns(); err != nil {
			return err
		}
		for from, to := range old {
			if err := move(from, to); err != nil {
				return fmt.Errorf("%s is still readable to every agent: %w", from, err)
			}
		}
	}
	// A token that cannot be deleted is reported on its run, not dropped.
	for _, r := range rs.List(nil) {
		if HarnessName(r.Harness) != "codex" || r.Status == StatusRunning {
			continue
		}
		if err := rs.dropCodexHome(r.ID); err != nil {
			why := codexTokenStuck + err.Error()
			switch {
			case r.Status == StatusBlocked && strings.HasPrefix(r.Reason, codexTokenStuck):
				// already blocked for it; a restart adds nothing
			case r.Terminal(): // Terminal counts a blocked run too
				// a finished or already blocked run keeps its status and reason,
				// and gets the note once
				if !strings.HasPrefix(r.Progress, codexTokenStuck) {
					rs.Progress(r.ID, why)
				}
			default:
				rs.SetStatus(r.ID, StatusBlocked, why)
			}
		}
	}
	return nil
}

// legacyPaths maps what an earlier version left in the readable tree to where
// it goes under Private; "" means it goes. A Codex home whose place is taken
// goes too: it is one turn's copy, and that turn has ended.
func (rs *Runs) legacyPaths() map[string]string {
	old := map[string]string{}
	if p := filepath.Join(rs.ws.Hive(), "daemon.json"); Exists(p) {
		old[p] = ""
	}
	dirs, _ := os.ReadDir(rs.ws.Runs())
	for _, d := range dirs {
		id, host := d.Name(), rs.ws.RunHost(d.Name())
		for _, from := range []string{filepath.Join(rs.ws.Run(id), "home"), filepath.Join(host, "codex"), filepath.Join(host, "codex-sessions")} {
			if !Exists(from) {
				continue
			}
			to := filepath.Join(rs.ws.RunPrivate(id), filepath.Base(from))
			if filepath.Base(from) == "codex" && Exists(to) {
				to = ""
			}
			old[from] = to
		}
		specs, _ := filepath.Glob(filepath.Join(host, "*.sandbox.json"))
		for _, f := range specs {
			old[f] = ""
		}
	}
	return old
}

// move renames from to to; a to that is taken keeps its place and from goes
// beside it under a fresh name, so nothing is lost and nothing stays. "" removes from.
func move(from, to string) error {
	if to == "" {
		return removeTree(from)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	if Exists(to) {
		id := NewID() // its random tail, not the date, keeps the name fresh
		to += ".old-" + id[strings.LastIndex(id, "-")+1:]
	}
	return os.Rename(from, to)
}

// endOldTurns ends every turn alive from before this start and waits for it
// to go, as Stop does: a terminate, then a kill.
func (rs *Runs) endOldTurns() error {
	for _, r := range rs.List(func(r *Run) bool { return r.Status == StatusRunning && proc.Alive(r.Proc) }) {
		proc.Terminate(r.Proc)
		for i := 0; proc.Alive(r.Proc); i++ {
			switch i {
			case 50:
				proc.Kill(r.Proc)
			case 100:
				return fmt.Errorf("run %s's turn from before the update is still running (pid %d)", r.ID, r.Proc.PID)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil
}

// Ops returns the mailbox operation registry.
func (rs *Runs) Ops() *Ops { return rs.ops }

type turnStarted struct {
	ID        string      `json:"id"`
	Session   string      `json:"session"`
	Turn      int         `json:"turn"`
	Proc      proc.Handle `json:"proc"`
	Delivered int         `json:"delivered"`
	Resume    bool        `json:"resume"`
	Model     string      `json:"model,omitempty"`
	Harness   string      `json:"harness,omitempty"`
	Login     string      `json:"login,omitempty"`
	Where     string      `json:"where,omitempty"` // the executor module's runner it went to; empty is this machine
}

type turnEnded struct {
	ID          string          `json:"id"`
	Turn        int             `json:"turn"`
	SessionLive bool            `json:"session_live"`
	Context     int             `json:"context,omitempty"`
	Session     string          `json:"session,omitempty"` // the id the harness reported
	Result      *harness.Result `json:"result,omitempty"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason,omitempty"`
	ParkedUntil time.Time       `json:"parked_until,omitempty"`
	Queue       []string        `json:"queue,omitempty"`
	Offline     string          `json:"offline,omitempty"`     // why the harness couldn't reach its service, in one plain line
	Unavailable string          `json:"unavailable,omitempty"` // the pick it ran on can't answer (Down), so it moved or waits without a retry's backoff
	Head        string          `json:"head,omitempty"`        // the checkout's HEAD log at the turn's end
	Stalled     string          `json:"stalled,omitempty"`     // the pick it kept ending turns on without finishing, which it moves past
}

// egressRefused is the hosts a turn's egress proxy refused, and each
// destination with why.
type egressRefused struct {
	ID      string          `json:"id"`
	Turn    int             `json:"turn"`
	Hosts   []string        `json:"hosts"`
	Refused []egressRefusal `json:"refused,omitempty"`
}

type egressRefusal struct {
	Dest string `json:"dest"` // host:port as asked
	Why  string `json:"why"`
}

// refusalWhy says in a line why the proxy refused dest, and for a service on
// this machine how it opens.
func refusalWhy(dest, code string) string {
	switch code {
	case sandbox.RefusedUnlisted:
		return "not on this run's egress list (egress in .hive/config.json)"
	case sandbox.RefusedLocal:
		return "a service on this machine or a private network, closed to sandboxes; a loopback port opens only on the person's grant: " +
			"ask a warden for `" + Cmd + " egress grant " + dest + "`"
	}
	return "refused by the egress proxy"
}

// readRefusals reads a turn's .egress file: a line per refusal, "host:port
// why" (a bare host from a proxy before reasons were kept).
func readRefusals(raw string) egressRefused {
	var out egressRefused
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		dest, code := f[0], ""
		if len(f) > 1 {
			code = f[1]
		}
		host := dest
		if h, _, err := net.SplitHostPort(dest); err == nil {
			host = h
		}
		out.Hosts = append(out.Hosts, host)
		if !seen[dest] {
			seen[dest] = true
			out.Refused = append(out.Refused, egressRefusal{Dest: dest, Why: refusalWhy(dest, code)})
		}
	}
	sort.Strings(out.Hosts)
	out.Hosts = slices.Compact(out.Hosts)
	sort.Slice(out.Refused, func(i, j int) bool { return out.Refused[i].Dest < out.Refused[j].Dest })
	return out
}

// harnessGone starts the reason of a run parked because no installed module
// declares its harness any more; after harnessGoneTries such parks (about
// 15 minutes) the run is blocked rather than tried again forever.
const harnessGone, harnessGoneTries = "harness missing:", 3

type runNote struct {
	ID     string    `json:"id"`
	Text   string    `json:"text,omitempty"`
	Status string    `json:"status,omitempty"`
	Reason string    `json:"reason,omitempty"`
	Until  time.Time `json:"until,omitempty"`
	N      int       `json:"n,omitempty"`
}

// apply folds one event into the in-memory state. Called on replay and on append.
func (rs *Runs) apply(e Event) {
	if !strings.HasPrefix(e.Kind, "run.") && e.Kind != "plan.usage" && e.Kind != "model.down" && e.Kind != "model.up" {
		return
	}
	switch e.Kind {
	case "model.down", "model.up":
		rs.applyDown(e)
		return
	case "run.created":
		var c createdSpec
		e.Decode(&c)
		s := c.RunSpec
		rs.load(&s, c.Stored)
		rs.runs[s.ID] = &Run{RunSpec: s, Session: NewUUIDFrom(s.ID), Status: StatusQueued, Created: e.At, Updated: e.At}
		return
	case "plan.usage":
		var p Plan
		e.Decode(&p)
		p.Lifted = false // lift sets it on copies, never here
		// Readings journaled before the parse fix carry 0 at the top level
		// and the real number only under the window.
		if w, ok := p.Limit.Windows[p.Limit.Window]; ok && p.Limit.Utilization == 0 {
			p.Limit.Utilization = w.Utilization
		}
		// A turn that started before the limit can end after it with a
		// reading taken before it: that doesn't lift the park, or the next
		// turn starts only to be turned away (10-25, 10:41).
		if old := rs.held(rs.plans[p.Key()]); old.Parked(e.At) && p.Limit.Status != "rejected" && rs.held(p).stale(old, e.At) {
			return
		}
		rs.plans[p.Key()] = p
		return
	}
	var id struct {
		ID string `json:"id"`
	}
	e.Decode(&id)
	r := rs.runs[id.ID]
	if r == nil {
		return
	}
	r.Updated = e.At
	was := r.Status
	defer func() {
		if r.Status != StatusParked {
			r.ParkedSince = time.Time{}
		} else if was != StatusParked {
			r.ParkedSince = e.At
		}
	}()
	switch e.Kind {
	case "run.turn_started":
		var t turnStarted
		e.Decode(&t)
		r.Turn, r.Proc, r.Status, r.TurnStarted, r.LastOutput, r.Login, r.Where = t.Turn, t.Proc, StatusRunning, e.At, e.At, t.Login, t.Where
		if t.Session != "" {
			r.Session = t.Session
		}
		if t.Model != "" {
			r.Model = t.Model
		}
		if t.Harness != "" && HarnessName(t.Harness) != HarnessName(r.Harness) {
			r.Moves = append(r.Moves, Move{Turn: t.Turn, From: r.Harness, To: t.Harness})
			r.Harness = t.Harness
		}
		if !t.Resume {
			r.SessionLive = false // a new session, live once the harness says it began
		}
		on := Pick{Harness: HarnessName(r.Harness), Model: r.Model}.String()
		if prev, ok := rs.ranOn[r.ID]; ok && prev != on {
			on = ""
		}
		rs.ranOn[r.ID] = on
		r.Waiting, r.Reopened, r.Returned = "", false, false
		r.Given = nil
		if t.Delivered <= len(r.Pending) {
			r.Given = append(r.Given, r.Pending[:t.Delivered]...)
			r.Pending = r.Pending[t.Delivered:]
		}
		r.ParkedUntil = time.Time{}
	case "run.turn_ended":
		var t turnEnded
		e.Decode(&t)
		r.SessionLive = r.SessionLive || t.SessionLive
		if t.Session != "" {
			r.Session = t.Session
		}
		r.Status, r.Reason, r.ParkedUntil, r.Offline = t.Status, t.Reason, t.ParkedUntil, t.Offline
		r.Proc = proc.Handle{}
		rs.strikeTurn(Pick{Harness: HarnessName(r.Harness), Model: r.Model}, r.ID, t.Result)
		if t.Context > 0 {
			r.Context = t.Context
		}
		if t.Result != nil {
			spent := r.SessionCost.Turn(r.Session, t.Result.CostUSD)
			r.Cost += spent
			rs.spent[e.At.UTC().Format("2006-01")] += spent
			if !t.Result.IsError {
				r.Result, r.Structured = t.Result.Text, t.Result.Structured
				r.Retries = 0
			}
		}
		if t.Status == StatusParked && t.Unavailable == "" && (t.Result == nil || (t.Result.IsError && !t.Result.Limited)) {
			r.Retries++
		}
		if len(t.Queue) > 0 {
			r.Pending = append(append([]string{}, t.Queue...), r.Pending...)
		}
		if strings.HasPrefix(t.Reason, "nudged") {
			r.Nudges++
		}
		// A parked turn (a limit, an interruption, an error) didn't end
		// without finishing, so only a new commit counts from it.
		if t.Head != r.Head {
			r.Head, r.Still = t.Head, 0
		} else if t.Status != StatusParked {
			r.Still++
		}
		if t.Stalled != "" {
			// The next stop gets its own nudges before it too is passed.
			r.Stalled = append(r.Stalled, t.Stalled)
			r.Nudges, r.Still = 0, 0
		}
	case "run.queued":
		var n runNote
		e.Decode(&n)
		r.Pending = append(r.Pending, n.Text)
	case "run.delivered":
		var n runNote
		e.Decode(&n)
		if n.N <= len(r.Pending) {
			r.Given = append(r.Given, r.Pending[:n.N]...)
			r.Pending = r.Pending[n.N:]
		}
	case "run.finish":
		var f struct {
			ID      string `json:"id"`
			Finish  Finish `json:"finish"`
			Printed bool   `json:"printed"` // where the first printed finishes said so
		}
		e.Decode(&f)
		f.Finish.Printed = f.Finish.Printed || f.Printed
		r.Finish = &f.Finish
		on := Pick{Harness: HarnessName(r.Harness), Model: r.Model}.String()
		if prev, ok := rs.ranOn[r.ID]; f.Finish.Status == "done" && (!ok || prev == on) {
			rs.proven[on] = true
		}
	case "run.progress":
		var n runNote
		e.Decode(&n)
		r.Progress, r.ProgressAt = n.Text, e.At
	case "run.status":
		var n runNote
		e.Decode(&n)
		if r.Status == StatusRunning && (n.Status == StatusIdle || n.Status == StatusQueued) {
			// Only its end takes a running turn off running: a reopen that
			// lands mid-turn (a second fix round, 2 s after the first) would
			// let the next turn start beside it (issue #1022). Its message
			// waits in Pending for the turn after.
			break
		}
		r.Status, r.Reason, r.ParkedUntil = n.Status, n.Reason, n.Until
		if strings.HasPrefix(n.Reason, harnessGone) && n.Status == StatusParked {
			r.Retries = n.N
		}
		if n.Status == StatusQueued || n.Status == StatusIdle {
			r.Nudges, r.Still = 0, 0
		}
	case "run.held":
		var n runNote
		e.Decode(&n)
		r.Reason = n.Reason
	case "run.stop":
		r.StopAsked = true
	case "run.waiting":
		var n runNote
		e.Decode(&n)
		r.Waiting = n.Text
	case "run.reopened":
		r.Finish, r.StopAsked, r.Nudges, r.Still = nil, false, 0, 0
		r.Reopened = r.Status == StatusRunning
		r.Returned = !r.Reopened
	}
}

// createdSpec is run.created as journaled: a spec whose long texts (system
// prompt, brief, inputs over inlineMax) are left out and named in Stored, read
// back on replay from the run's ro folder, where Start wrote the same text.
// The journal and the kb's record of it then carry names, not every brief,
// diff and system prompt again.
type createdSpec struct {
	RunSpec
	Stored []string `json:"stored,omitempty"` // "system", "brief" or "inputs/<name>"
}

const inlineMax = 1024

// journaled is s with each long text that has a file of its own in ro left
// out. A text whose file another overwrites (a brief under an input named
// brief.md) or shares a name with stays inline.
func journaled(s RunSpec) createdSpec {
	files := s.files()
	names := map[string]int{}
	for k := range files {
		names[filepath.Base(k)]++
	}
	own := func(key, text string) bool {
		v, ok := files[key]
		return ok && v == text && len(text) > inlineMax && names[filepath.Base(key)] == 1
	}
	c := createdSpec{RunSpec: s}
	if own("system.md", s.System) {
		c.System, c.Stored = "", append(c.Stored, "system")
	}
	if own("brief.md", s.Brief) {
		c.Brief, c.Stored = "", append(c.Stored, "brief")
	}
	c.Inputs = nil
	for k, v := range s.Inputs {
		if own(k, v) {
			c.Stored = append(c.Stored, "inputs/"+k)
			continue
		}
		if c.Inputs == nil {
			c.Inputs = map[string]string{}
		}
		c.Inputs[k] = v
	}
	sort.Strings(c.Stored)
	return c
}

// files is what Start writes into the run's ro folder, by name.
func (s RunSpec) files() map[string]string {
	files := map[string]string{"system.md": s.System, "brief.md": s.Brief}
	if s.Schema != "" {
		files["schema.json"] = s.Schema
	}
	maps.Copy(files, s.Inputs)
	return files
}

// load reads back into s the texts journaled lets out of run.created.
func (rs *Runs) load(s *RunSpec, stored []string) {
	read := func(name string) string {
		raw, _ := os.ReadFile(filepath.Join(rs.ws.RunRO(s.ID), filepath.Base(name)))
		return string(raw)
	}
	for _, f := range stored {
		switch k, input := strings.CutPrefix(f, "inputs/"); {
		case input:
			if s.Inputs == nil {
				s.Inputs = map[string]string{}
			}
			s.Inputs[k] = read(k)
		case f == "system":
			s.System = read("system.md")
		case f == "brief":
			s.Brief = read("brief.md")
		}
	}
}

func (rs *Runs) append(kind string, data any) { rs.j.MustAppend(kind, data) }

// NewUUIDFrom makes a session uuid for a run (random; the id is only a label).
func NewUUIDFrom(string) string { return NewUUID() }

// Start records a new run; the next tick launches its first turn.
func (rs *Runs) Start(s RunSpec) (Run, error) {
	if s.ID == "" {
		s.ID = NewID()
	}
	if s.Harness == "" {
		s.Harness = "claude"
	}
	h, err := harness.Get(s.Harness)
	if err != nil {
		return Run{}, err
	}
	if err := harness.Capped(h, s.Budget); err != nil {
		return Run{}, err
	}
	if HarnessName(s.Harness) == "open" {
		m := rs.ws.LoadConfig().Models[s.Model]
		if err := harness.OpenRouterPaid(m.Endpoint, m.Model); err != nil {
			return Run{}, fmt.Errorf("a run won't start on %s: %v", s.Model, err)
		}
		if err := m.Holds(s.Budget); err != nil {
			return Run{}, fmt.Errorf("a run with a budget won't start on %s: %v", s.Model, err)
		}
	}
	for _, d := range []string{rs.ws.RunHost(s.ID), rs.ws.RunRO(s.ID), filepath.Join(rs.ws.RunBox(s.ID), "outbox"), filepath.Join(rs.ws.RunBox(s.ID), "inbox"), rs.ws.RunHome(s.ID)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Run{}, err
		}
	}
	for name, content := range s.files() {
		if err := os.WriteFile(filepath.Join(rs.ws.RunRO(s.ID), filepath.Base(name)), []byte(content), 0o644); err != nil {
			return Run{}, err
		}
	}
	rs.append("run.created", journaled(s))
	r, _ := rs.Get(s.ID)
	return r, nil
}

// Get returns a copy of a run.
func (rs *Runs) Get(id string) (Run, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, ok := rs.runs[id]
	if !ok {
		return Run{}, false
	}
	return *r, true
}

// Find resolves an address: a run id, a name ("lead", "lead:demo"), or an id prefix.
func (rs *Runs) Find(addr string) (Run, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if r, ok := rs.runs[addr]; ok {
		return *r, true
	}
	var best *Run
	for _, r := range rs.runs {
		if (r.Name == addr || strings.HasPrefix(r.ID, addr) || strings.HasSuffix(r.ID, addr)) && (best == nil || r.Created.After(best.Created)) {
			best = r
		}
	}
	if best == nil {
		return Run{}, false
	}
	return *best, true
}

// List returns runs, newest first, filtered by fn (nil = all).
func (rs *Runs) List(fn func(*Run) bool) []Run {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []Run
	for _, r := range rs.runs {
		if fn == nil || fn(r) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Plans returns the latest usage per login.
func (rs *Runs) Plans() []Plan {
	now := time.Now()
	rs.mu.Lock()
	picks := map[string][]Plan{}
	for _, p := range rs.plans {
		pick := HarnessName(p.Harness)
		if pick == "open" { // each model is its own pick
			pick = p.Key()
		}
		picks[pick] = append(picks[pick], rs.held(p))
	}
	rs.mu.Unlock()
	var out []Plan
	for _, ps := range picks {
		var ks []string
		for _, p := range ps {
			ks = append(ks, p.Key())
		}
		out = append(out, lift(ps, now, false, rs.limited(ks, now))...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// lift sets Lifted on every one of plans, the logins of one pick, but the
// one with the most of its five hours left (the first by Key on a tie) of
// those with room short of the stops that lift, for a run that may spend
// the reserve (Reserved) or not; one limited (Runs.limited) has no room. So
// the others run to their limit while that one stops at its five-hour stop and
// keeps the rest of it for the person.
func lift(plans []Plan, now time.Time, reserve bool, limited map[string]time.Time) []Plan {
	keep, left := -1, 0.0
	for i, p := range plans {
		p.Lifted = true
		if limited[p.Key()].After(now) || !p.parkedFor(now, reserve).IsZero() {
			continue
		}
		w, _ := p.window("five_hour", now)
		if l := 1 - w.Utilization; keep < 0 || l > left || l == left && p.Key() < plans[keep].Key() {
			keep, left = i, l
		}
	}
	for i := range plans {
		plans[i].Lifted = i != keep
	}
	return plans
}

// held is p held to the config's usage stops; rs.mu is held.
func (rs *Runs) held(p Plan) Plan {
	u := rs.usage
	p.Stops = &u
	return p
}

// plansOf are the plans of logins, lifted (lift); rs.mu is held.
func (rs *Runs) plansOf(logins []Login, now time.Time, reserve bool, limited map[string]time.Time) []Plan {
	var out []Plan
	for _, l := range logins {
		p, ok := rs.plans[l.Key()]
		if !ok {
			p = Plan{Harness: l.Harness, Login: l.Key()}
		}
		out = append(out, rs.held(p))
	}
	return lift(out, now, reserve, limited)
}

// pickLogin chooses the login for a run's next turn: of those not parked at
// their usage limit, the least used (Plan.Used), the machine's own first on a
// tie; a check (Reserved) takes the reserve up to the week's stop too. A
// bare run whose machine login has no credential file the Hive can read gets
// no token, so the harness's own login on the host still works.
func (rs *Runs) pickLogin(h, model, home string, bare, reserve bool) (Login, []string, error) {
	logins := rs.logins(h, model)
	now := time.Now()
	limited := rs.limited(keys(logins), now)
	rs.mu.Lock()
	use := func(l Login) float64 { return rs.plans[l.Key()].Used(now) }
	var open []Login
	for i, p := range rs.plansOf(logins, now, reserve, limited) {
		if l := logins[i]; !limited[l.Key()].After(now) && p.parkedFor(now, reserve).IsZero() {
			open = append(open, l)
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return use(open[i]) < use(open[j]) })
	usage := rs.usage
	rs.mu.Unlock()
	var at []string
	for _, s := range usage.stops() {
		if !reserve {
			s.at -= s.reserve
		}
		at = append(at, fmt.Sprintf("%.0f%% of its %s", s.at*100, strings.TrimSuffix(s.name, " window")))
	}
	limit := fmt.Errorf("every %s login is at its usage limit", HarnessName(h))
	if len(at) > 0 {
		limit = fmt.Errorf("every %s login is at its usage limit or its stop (%s)", HarnessName(h), strings.Join(at, ", "))
	}
	err := limit
	for _, l := range open {
		env, e := rs.tokens.AgentEnv(l, home)
		if e == nil || bare && l.Name == "" && (errors.Is(e, ErrNoLogin) || errors.Is(e, ErrNoKey)) {
			return l, env, nil
		}
		err = e
	}
	if len(open) < len(logins) && err != limit {
		// Some are at their limit and the rest can't be used: the limit is
		// why nothing runs, not an added login with no credential.
		err = fmt.Errorf("%w; %s can't be used either: %v", limit, strings.Join(keys(open), ", "), err)
	}
	return Login{}, nil, err
}

// limited is when each of the logins keys names whose renewal was turned
// away by its usage limit takes turns again: that parks it too, as a turn
// on it would only be turned away the same. Not under rs.mu.
func (rs *Runs) limited(keys []string, now time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	if renewing, ok := rs.tokens.(interface{ LimitedUntil(string) time.Time }); ok {
		for _, k := range keys {
			if at := renewing.LimitedUntil(k); at.After(now) {
				out[k] = at
			}
		}
	}
	return out
}

// keys names the logins.
func keys(ls []Login) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Key())
	}
	return out
}

// Headroom is how much of its plan the least used login a turn on p could
// take has left (1 - Plan.Used), 0 when every one is at its usage limit; a
// pick with no logins at all says 1, since nothing is known against it.
func (rs *Runs) Headroom(p Pick) float64 {
	logins := rs.logins(p.Harness, p.Model)
	now := time.Now()
	limited := rs.limited(keys(logins), now)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	room := 0.0
	if len(logins) == 0 {
		room = 1
	}
	for _, pl := range rs.plansOf(logins, now, false, limited) {
		if !pl.Parked(now) && !limited[pl.Key()].After(now) {
			room = max(room, 1-pl.Used(now))
		}
	}
	return room
}

// Ahead is how far ahead of its paced windows the least ahead login a turn
// on p could take is (Plan.Ahead), 0 when one is on pace or nothing is known;
// with every login parked it is 0 too, and Headroom says so.
func (rs *Runs) Ahead(p Pick) float64 { return rs.AheadIn(p, "", "") }

// AheadIn is Ahead over the windows named by window (Plan.AheadIn; the
// week's alone is "seven_day") for a run of role, which a login parked for
// other roles still takes when role may spend the reserve (Reserved).
func (rs *Runs) AheadIn(p Pick, window, role string) float64 {
	logins := rs.logins(p.Harness, p.Model)
	now := time.Now()
	limited := rs.limited(keys(logins), now)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	ahead := -1.0
	reserve := Reserved(role)
	for _, pl := range rs.plansOf(logins, now, reserve, limited) {
		if pl.parkedFor(now, reserve).IsZero() && !limited[pl.Key()].After(now) && (ahead < 0 || pl.AheadIn(now, window) < ahead) {
			ahead = pl.AheadIn(now, window)
		}
	}
	return max(ahead, 0)
}

// logins are the logins a turn on harness h and model can go on. A model the
// open harness runs is its own login, with its own limit.
func (rs *Runs) logins(h, model string) []Login {
	if HarnessName(h) == "open" {
		return []Login{{Harness: "open", Name: model}}
	}
	return rs.tokens.Logins(h)
}

// freeAt is when a login for harness h and model next has room: now if one
// has, else the earliest reset; a pick that can't answer (Down) waits for
// its retry too. A check (Reserved) has room up to the week's stop.
func (rs *Runs) freeAt(h, model string, reserve bool) time.Time {
	now := time.Now()
	logins := rs.logins(h, model)
	limited := rs.limited(keys(logins), now)
	var at time.Time
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, p := range rs.plansOf(logins, now, reserve, limited) {
		until := p.parkedFor(now, reserve)
		if l := limited[p.Key()]; l.After(until) {
			until = l
		}
		if until.IsZero() {
			at = now
			break
		}
		if at.IsZero() || until.Before(at) {
			at = until
		}
	}
	if at != now {
		at = at.Add(15 * time.Second)
	}
	if d, ok := rs.down[Pick{Harness: HarnessName(h), Model: model}.String()]; ok && d.Retry.After(at) {
		at = d.Retry
	}
	return at
}

// next is the pick r's next turn goes on, and when it has room: the first of
// its picks with room now, else the one that has room soonest. A persistent
// run follows the settings' route; any other stays on its own pick while it
// has room, then moves along its Route. A run that must finish (a worker)
// moves only to a pick some run has finished done on, or back to the first
// of its route: one on a model that has never finished here ended turns
// without finishing while others waited on it (task 135947 on gpt-oss), so it
// waits for its own model's reset instead.
func (rs *Runs) next(r Run) (Pick, time.Time) {
	picks, _ := rs.picks(r)
	best, at := picks[0], time.Time{}
	for _, p := range picks {
		free := rs.freeAt(p.Harness, p.Model, Reserved(r.Role))
		if !free.After(time.Now()) {
			return p, free
		}
		if at.IsZero() || free.Before(at) {
			best, at = p, free
		}
	}
	return best, at
}

// picks is r's picks in the order next tries them, and those of its route
// it won't go on because no run has finished done on them (unproven).
func (rs *Runs) picks(r Run) (picks, unproven []Pick) {
	own := Pick{Harness: HarnessName(r.Harness), Model: r.Model}
	picks = []Pick{own}
	if rs.Route != nil && r.Persistent {
		if route := rs.Route(r); len(route) > 0 {
			picks = route
		}
	}
	specRoute := r.RunSpec.Route
	if rs.Route != nil && !r.Persistent && len(specRoute) > 0 {
		// A route fixed at its start that can't answer any more (its first
		// pick is down) gives way to the settings' route as it is now, so a
		// run parked on a rejected model follows the route changed since.
		if _, down := rs.downFor(specRoute[0]); down {
			if route := rs.Route(r); len(route) > 0 {
				specRoute = route
				picks = append(picks, route...)
			}
		}
	}
	for _, p := range specRoute {
		if !slices.ContainsFunc(picks, p.Same) {
			picks = append(picks, p)
		}
	}
	if r.Finishes() {
		first := own
		if len(specRoute) > 0 {
			first = specRoute[0]
		}
		rs.mu.Lock()
		picks = slices.DeleteFunc(picks, func(p Pick) bool {
			if p.Same(first) || rs.proven[p.String()] {
				return false
			}
			unproven = append(unproven, p)
			return true
		})
		rs.mu.Unlock()
		if !slices.ContainsFunc(picks, first.Same) {
			picks = append([]Pick{first}, picks...)
		}
	}
	// A pick its turns kept ending on without finishing is passed for good,
	// while its route has another.
	if left := slices.DeleteFunc(slices.Clone(picks), func(p Pick) bool { return slices.Contains(r.Stalled, p.String()) }); len(left) > 0 {
		picks = left
	}
	return picks, unproven
}

// onward is the pick a worker whose turns keep ending on ran without
// finishing goes on next, and when it has room; ok is false when its route
// has no other stop to try (each is passed, or no run has finished on it).
// Its last stop is where it stays quiet, or ends blocked, as before: three
// runs on Deck's free models never reached its route's claude:opus.
func (rs *Runs) onward(r Run, ran Pick) (pick Pick, at time.Time, ok bool) {
	if !r.Finishes() {
		return pick, at, false
	}
	r.Stalled = append(slices.Clone(r.Stalled), ran.String())
	pick, at = rs.next(r)
	return pick, at, !slices.Contains(r.Stalled, pick.String())
}

// Spent is what runs cost in a month ("2006-01", UTC), from the harnesses' results.
func (rs *Runs) Spent(month string) float64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.spent[month]
}

// Takes says why run id can't take a message, or nil if it can.
func (rs *Runs) Takes(id string) error {
	_, err := rs.taker(id)
	return err
}

func (rs *Runs) taker(id string) (Run, error) {
	r, ok := rs.Get(id)
	if !ok {
		return r, ErrNotFound
	}
	if r.Terminal() && !r.Persistent {
		return r, fmt.Errorf("run %s has ended (%s)", id, r.Status)
	}
	return r, nil
}

// Send queues a message for a run's next turn.
func (rs *Runs) Send(id, text string) error {
	r, err := rs.taker(id)
	if err != nil {
		return err
	}
	rs.append("run.queued", runNote{ID: id, Text: text})
	// A turn that ended blocked after the check, before the message landed,
	// never saw it; wake the run for it, as endTurn wakes one whose message
	// came in first.
	if !r.Terminal() {
		rs.wake(id)
	}
	return nil
}

// wake gives a run that ended blocked with a message waiting (often the
// answer it was blocked on) the turn for it, rather than leaving it on a run
// that takes no turns. One blocked for its Codex token stays blocked.
// It is safe to call at any time: it reads the run afresh, so a caller's
// earlier, stale look (Send's live check, endTurn's read) can't wake the
// wrong run; on anything but a blocked run with a message waiting it does
// nothing.
func (rs *Runs) wake(id string) {
	rs.waking.Lock() // Send and endTurn can both see it; it's woken once
	defer rs.waking.Unlock()
	r, ok := rs.Get(id)
	if !ok || r.Status != StatusBlocked || len(r.Pending) == 0 || strings.HasPrefix(r.Reason, codexTokenStuck) || strings.HasPrefix(r.Reason, StoppedQuiet) {
		return
	}
	rs.append("run.reopened", runNote{ID: id})
	rs.append("run.status", runNote{ID: id, Status: StatusIdle, Reason: "woken by a message"})
}

// Reopen lets a finished run take more turns (fix rounds go back to the same worker).
// A Codex run whose token folder still won't delete stays blocked for it.
// The same text again while a turn runs on it, handed or waiting, is
// already being worked on: it takes no further turn (issue #1157), unless
// the run was asked to stop, which the caller still hears.
func (rs *Runs) Reopen(id, text string) error {
	r, ok := rs.Get(id)
	if !ok {
		return ErrNotFound
	}
	if r.Status == StatusStopped || r.StopAsked {
		return fmt.Errorf("run %s was stopped on purpose (%s); it stays stopped", id, r.Reason)
	}
	if r.Status == StatusRunning && (slices.Contains(r.Given, text) || slices.Contains(r.Pending, text)) {
		return nil
	}
	if err := rs.tokenStuck(r); err != nil {
		return err
	}
	rs.append("run.reopened", runNote{ID: id})
	rs.append("run.queued", runNote{ID: id, Text: text})
	rs.append("run.status", runNote{ID: id, Status: StatusIdle, Reason: "reopened"})
	return nil
}

// tokenStuck checks again that a Codex run's token folder is gone; if it still
// won't delete, the run is blocked for it (once) and the error says why.
func (rs *Runs) tokenStuck(r Run) error {
	if HarnessName(r.Harness) != "codex" || r.Status == StatusRunning {
		return nil
	}
	err := rs.dropCodexHome(r.ID)
	if err == nil {
		return nil
	}
	why := codexTokenStuck + err.Error()
	if r.Status != StatusBlocked || !strings.HasPrefix(r.Reason, codexTokenStuck) {
		rs.SetStatus(r.ID, StatusBlocked, why)
	}
	return fmt.Errorf("run %s stays blocked: %s", r.ID, why)
}

// Wait marks a run as waiting for something (a warden, a helper); when its
// turn ends it idles until a message arrives instead of being nudged.
func (rs *Runs) Wait(id, what string) { rs.append("run.waiting", runNote{ID: id, Text: what}) }

// Progress records the agent's own account of where it is.
func (rs *Runs) Progress(id, text string) {
	rs.append("run.progress", runNote{ID: id, Text: Clip(text, 400)})
}

// SetStatus records a status change made by a module (e.g. handing a quiet run on).
func (rs *Runs) SetStatus(id, status, reason string) {
	rs.append("run.status", runNote{ID: id, Status: status, Reason: reason})
}

// StoppedQuiet opens the reason of a quiet run an agent ended with EndQuiet.
const StoppedQuiet = "stopped while quiet by "

// EndQuiet ends a quiet or parked run blocked with reason (StoppedQuiet
// first), only if it is still so and no turn of it is starting: a message that woke it
// since the caller looked keeps it going. A message later doesn't wake it;
// Reopen still does.
func (rs *Runs) EndQuiet(id, reason string) error {
	rs.mu.Lock()
	cur := rs.runs[id]
	switch {
	case cur == nil:
		rs.mu.Unlock()
		return ErrNotFound
	case (cur.Status != StatusQuiet && cur.Status != StatusParked) || rs.starting[id]:
		rs.mu.Unlock()
		return fmt.Errorf("run %s is %s, not quiet or parked", id, cur.Status)
	}
	if rs.starting == nil {
		rs.starting = map[string]bool{}
	}
	rs.starting[id] = true // launch refuses it meanwhile
	rs.mu.Unlock()
	rs.append("run.status", runNote{ID: id, Status: StatusBlocked, Reason: reason})
	rs.mu.Lock()
	delete(rs.starting, id)
	rs.mu.Unlock()
	return nil
}

// Stop ends a run on purpose; it will not be retried.
func (rs *Runs) Stop(id, reason string) {
	r, ok := rs.Get(id)
	if !ok || r.Terminal() {
		return
	}
	rs.append("run.stop", runNote{ID: id, Reason: reason})
	os.WriteFile(filepath.Join(rs.ws.RunHost(id), "stop"), []byte(reason), 0o644)
	if r.Status == StatusRunning {
		proc.Terminate(r.Proc)
		go func(h proc.Handle) { time.Sleep(10 * time.Second); proc.Kill(h) }(r.Proc)
		return // the turn's end records the stop
	}
	rs.append("run.status", runNote{ID: id, Status: StatusStopped, Reason: reason})
}

// Interrupt ends a running turn's process. The turn ends like any interrupted
// one and is retried on the same checkout, so nothing is started twice.
func (rs *Runs) Interrupt(id string) bool {
	r, ok := rs.Get(id)
	return ok && r.Status == StatusRunning && proc.Alive(r.Proc) && proc.Kill(r.Proc) == nil
}

// Tick advances every run one step: mailboxes, finished turns, due turns.
// Leads go first, the Hive-wide lead ahead of the projects', and the other
// runs step once every lead's step is done (or a few seconds in, should one
// hang), so a lead's due turn starts ahead of any queued worker or review.
// Nothing here waits on the caller's loop: the wait runs on its own, and the
// other runs are listed after it, as they are then, since a tick that came
// meanwhile may have stepped them already.
func (rs *Runs) Tick(cfg Config) {
	rs.tickDown(cfg)
	live := rs.List(func(r *Run) bool { return r.Lead() && (!r.Terminal() || r.Status == StatusRunning) })
	slices.SortStableFunc(live, func(a, b Run) int { return a.Nice() - b.Nice() })
	var wg sync.WaitGroup
	for _, r := range live {
		rs.stepAsync(r, cfg, &wg)
	}
	// One pass over the other runs waits at a time: while a tick's is still
	// waiting for the leads' steps, the ticks that come meanwhile skip
	// theirs, rather than pile up a waiting goroutine each.
	rs.mu.Lock()
	if rs.passing {
		rs.mu.Unlock()
		return
	}
	rs.passing = true
	rs.mu.Unlock()
	go func() {
		defer func() { rs.mu.Lock(); rs.passing = false; rs.mu.Unlock() }()
		leads := make(chan struct{})
		go func() { wg.Wait(); close(leads) }()
		select {
		case <-leads:
		case <-time.After(rs.leadWaitOr()):
			// The busy map keeps a run from being stepped twice, so this only
			// says, in daemon.log, which lead's step is slow. It is said once
			// a stall: a later tick can't claim the hung lead, so its pass
			// has no lead to wait for and goes at once.
			rs.mu.Lock()
			var slow []string
			for _, r := range live {
				if rs.busy[r.ID] {
					slow = append(slow, r.ID)
				}
			}
			rs.mu.Unlock()
			log.Printf("runs: a lead's step is still going after %s (%s); the other runs step now", rs.leadWaitOr(), strings.Join(slow, ", "))
		}
		for _, r := range rs.List(func(r *Run) bool { return !r.Lead() && (!r.Terminal() || r.Status == StatusRunning) }) {
			rs.stepAsync(r, cfg, nil)
		}
		// A run that ended without launching leaves no deferral count behind.
		rs.mu.Lock()
		for id := range rs.deferred {
			if r := rs.runs[id]; r == nil || r.Terminal() {
				delete(rs.deferred, id)
			}
		}
		rs.mu.Unlock()
	}()
}

// leadWaitOr is how long a tick's pass over the other runs waits for the
// leads' steps before it goes ahead of a slow one.
func (rs *Runs) leadWaitOr() time.Duration {
	if rs.leadWait > 0 {
		return rs.leadWait
	}
	return 5 * time.Second
}

// stepAsync steps a listed run in its own goroutine once it claims it
// (claim: marked busy for one step, read as it is now), so a step that
// waited (behind the leads, or for a slow one) never acts on a status a
// later tick's step has moved on from, starting a turn twice or ending one
// twice.
func (rs *Runs) stepAsync(r Run, cfg Config, wg *sync.WaitGroup) {
	r, ok := rs.claim(r)
	if !ok {
		return
	}
	if wg != nil {
		wg.Add(1)
	}
	go func() {
		defer func() {
			rs.mu.Lock()
			delete(rs.busy, r.ID)
			rs.mu.Unlock()
			if wg != nil {
				wg.Done()
			}
		}()
		rs.step(r, cfg)
	}()
}

// claim marks a listed run busy for one step and returns it as it is now:
// the list can predate a step that ended since (a turn just started).
func (rs *Runs) claim(r Run) (Run, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.busy[r.ID] {
		return r, false
	}
	rs.busy[r.ID] = true
	if cur := rs.runs[r.ID]; cur != nil {
		r = *cur
	}
	return r, true
}

func (rs *Runs) step(r Run, cfg Config) {
	switch r.Status {
	case StatusRunning:
		rs.ops.Drain(r)
		rs.watch(r)
	case StatusQueued:
		rs.launch(r, cfg)
	case StatusIdle:
		rs.ops.Drain(r)
		if len(r.Pending) > 0 {
			rs.launch(r, cfg)
		}
	case StatusParked:
		if time.Now().After(r.ParkedUntil) {
			rs.launch(r, cfg)
		}
	case StatusQuiet:
		rs.ops.Drain(r)
		if len(r.Pending) > 0 {
			rs.launch(r, cfg)
		}
	}
}

// watch checks a running turn; when its process has gone, the turn ends.
func (rs *Runs) watch(r Run) {
	out := rs.ws.TurnOut(r.ID, r.Turn)
	if fi, err := os.Stat(out); err == nil && fi.ModTime().After(r.LastOutput.Add(30*time.Second)) {
		rs.mu.Lock()
		if x := rs.runs[r.ID]; x != nil {
			x.LastOutput = fi.ModTime()
		}
		rs.mu.Unlock()
	}
	if proc.Alive(r.Proc) {
		return
	}
	settled(out, 50*time.Millisecond, 200*time.Millisecond) // let the last lines land
	// A copy of the run older than now ends nothing: its turn ended already,
	// and ending it again would clear the live turn after it.
	if cur, ok := rs.Get(r.ID); !ok || cur.Status != StatusRunning || cur.Turn != r.Turn {
		return
	}
	rs.ops.Drain(r)
	rs.endTurn(r)
}

// settled waits until the file at path has stopped growing for quiet, and at
// most most in all.
func settled(path string, quiet, most time.Duration) {
	size := func() int64 {
		if fi, err := os.Stat(path); err == nil {
			return fi.Size()
		}
		return -1
	}
	settle(size, time.Now, time.Sleep, quiet, most)
}

// settle is settled's loop over a size, a clock and a sleep, so a test can
// drive it without the wall clock.
func settle(size func() int64, now func() time.Time, sleep func(time.Duration), quiet, most time.Duration) {
	end := now().Add(most)
	last, still := size(), now()
	for now().Before(end) {
		sleep(10 * time.Millisecond)
		if n := size(); n != last {
			last, still = n, now()
		} else if now().Sub(still) >= quiet {
			return
		}
	}
}

// TurnLog parses a turn's output file.
type TurnLog struct {
	SessionLive bool
	Context     int
	Session     string
	Result      *harness.Result
	Limit       *harness.RateLimit
	Steps       []harness.Event
}

// ReadTurn parses one turn's output.
func (rs *Runs) ReadTurn(r Run, turn int) TurnLog {
	var tl TurnLog
	h, err := harness.Get(r.HarnessAt(turn))
	if err != nil { // a module's harness since removed: nothing to read the turn with
		return tl
	}
	var used harness.Tokens // a harness that reports each step's tokens (usage events)
	twice := false          // a second verdict in one turn: trust neither, nor rebuild one from the text
	_ = ReadJSONL(rs.ws.TurnOut(r.ID, turn), func(line []byte) {
		for _, ev := range h.Parse(line) {
			switch ev.Kind {
			case "init":
				tl.SessionLive, tl.Session = true, ev.SessionID
			case "result":
				if tl.Result != nil && ev.Result != nil {
					ev.Result.Structured, twice = nil, true
				}
				tl.Result = ev.Result
			case "ratelimit":
				tl.Limit = ev.RateLimit
			case "context":
				tl.Context = ev.Tokens
			case "usage":
				if ev.Usage != nil {
					used.Add(*ev.Usage)
				}
			}
			if ev.Kind == "text" || ev.Kind == "tool" {
				tl.Steps = append(tl.Steps, ev)
			}
		}
	})
	// A harness whose result carries no text (Codex) answers with its last message.
	if tl.Result != nil && !tl.Result.IsError && tl.Result.Text == "" {
		for _, s := range tl.Steps {
			if s.Kind == "text" {
				tl.Result.Text = s.Text
			}
		}
		if r.Schema != "" && tl.Result.Structured == nil && !twice && json.Valid([]byte(tl.Result.Text)) {
			tl.Result.Structured = json.RawMessage(tl.Result.Text)
		}
	}
	if tl.Result != nil && tl.Result.Tokens == nil && used != (harness.Tokens{}) {
		tl.Result.Tokens = &used
	}
	// A module's harness has no schema flag; its answer is the result's text.
	if tl.Result != nil && !tl.Result.IsError && r.Schema != "" && tl.Result.Structured == nil && !twice && harness.AnswersInText(h) {
		if a := answerJSON(tl.Result.Text); a != nil {
			tl.Result.Structured = a
		}
	}
	return tl
}

// answerJSON is the JSON object a reply ends with, inside a code fence or
// not, or nil.
func answerJSON(text string) json.RawMessage {
	t := strings.TrimSpace(text)
	if body, ok := strings.CutSuffix(t, "```"); ok {
		if i := strings.LastIndex(body, "```"); i >= 0 {
			t = strings.TrimSpace(strings.TrimPrefix(body[i+3:], "json"))
		}
	}
	for i := 0; i < len(t); i++ {
		if t[i] == '{' && (i == 0 || t[i-1] == '\n') && json.Valid([]byte(t[i:])) {
			return json.RawMessage(t[i:])
		}
	}
	return nil
}

func (rs *Runs) endTurn(r Run) {
	tl := rs.ReadTurn(r, r.Turn)
	if HarnessName(r.Harness) == "codex" {
		// Codex keeps its usage readings in its session file, not its output.
		l, n := rs.codexUsage(r, tl.Session)
		if tl.Limit == nil {
			tl.Limit = l
		}
		if tl.Context == 0 {
			tl.Context = n
		}
	}
	if raw, err := os.ReadFile(rs.ws.TurnOut(r.ID, r.Turn) + ".egress"); err == nil && len(raw) > 0 {
		ev := readRefusals(string(raw))
		ev.ID, ev.Turn = r.ID, r.Turn
		rs.append("run.egress_refused", ev)
	}
	limited := tl.Result != nil && tl.Result.Limited || (tl.Limit != nil && tl.Limit.Status == "rejected" && (tl.Result == nil || tl.Result.IsError))
	var wait time.Duration
	if limited {
		// Park the login until its reset (an hour when the harness didn't
		// say). A reset it named that has passed by the turn's end is a
		// limit that is over, not one with no reset: it waits a minute, so a
		// harness repeating a stale reset doesn't spin, not the hour that
		// parked the fake agent's three-second limit on a slow runner (PR #800).
		// One that keeps repeating it waits twice as long each time, up to
		// the hour (#1018).
		l := harness.RateLimit{}
		if tl.Limit != nil {
			l = *tl.Limit
		}
		l.Status = "rejected"
		if l.ResetsAt.IsZero() {
			l.ResetsAt = time.Now().Add(time.Hour)
		} else if !l.ResetsAt.After(time.Now()) {
			wait = time.Minute
			rs.mu.Lock()
			if prev := rs.plans[Plan{Harness: HarnessName(r.Harness), Login: r.Login}.Key()]; prev.Wait > 0 && prev.Limit.Status == "rejected" {
				wait = min(2*prev.Wait, time.Hour)
			}
			rs.mu.Unlock()
			l.ResetsAt = time.Now().Add(wait)
		}
		tl.Limit = &l
	}
	if p := rs.ws.LoadConfig().Models[r.Model].Price; HarnessName(r.Harness) == "open" && p != nil && tl.Result != nil && tl.Result.Tokens != nil {
		// The harness counts no vendor's prices; a priced model's turn costs
		// its tokens at the price the person set.
		res := *tl.Result
		res.CostUSD = p.Cost(*res.Tokens)
		tl.Result = &res
	}
	if tl.Limit != nil {
		rs.append("plan.usage", Plan{Harness: HarnessName(r.Harness), Login: r.Login, Limit: *tl.Limit, At: time.Now().UTC(), Wait: wait})
	}
	te := turnEnded{ID: r.ID, Turn: r.Turn, SessionLive: tl.SessionLive, Session: tl.Session, Context: tl.Context, Result: tl.Result, Head: headMark(r.Checkout)}
	var dropErr error
	if HarnessName(r.Harness) == "codex" {
		dropErr = rs.dropCodexHome(r.ID)
	}
	if HarnessName(r.Harness) == "open" {
		rs.dropOpenKey(r.ID)
	}
	cur, _ := rs.Get(r.ID)
	finishes := r.Role == "worker" && !r.Persistent && r.Schema == "" && !r.ReadOnly && !r.OneTurn
	if f, ok := printedFinish(r, tl.Result); ok && finishes && cur.Finish == nil && !cur.StopAsked && !limited && dropErr == nil {
		// A worker wrote its finish as its answer instead of running it: the
		// Hive runs it on its behalf, with the same flags and the same checks.
		// Only workers: a lead or chat agent showing the command is explaining.
		rs.append("run.finish", struct {
			ID      string `json:"id"`
			Finish  Finish `json:"finish"`
			Printed bool   `json:"printed"`
		}{r.ID, f, true})
		cur.Finish = &f
	}
	// A pick that answers is back; one whose error says it can't answer
	// is skipped by every run until it can (Down).
	ran := Pick{Harness: HarnessName(r.Harness), Model: r.Model}
	var down *Down
	// A 401 through Claude Code (an Anthropic-compatible model) comes back as
	// its apiKeyHelper failing: it asks the one-shot helper again and gets
	// nothing. The first such turn retries with the key handed over afresh;
	// a second in a row is a key the endpoint won't take, and marks the
	// model down as any 401 does, so the run moves on its route.
	var keyRefused bool
	if tl.Result != nil && !limited {
		turns, runs := rs.strikesWith(ran, r.ID, tl.Result)
		keyRefused = tl.Result.IsError && tl.Result.APIStatus == 401 && ran.Harness == "open" && rs.anthropicModel(r.Model) && turns == 1
		if !tl.Result.IsError {
			rs.Up(ran, "it answered")
		} else if !keyRefused && cantAnswer(ran, tl.Result, Unreachable(service(r.Harness), tl.Result.Text), turns, runs) {
			d := rs.markDown(r, ran, tl.Result)
			down = &d
		}
	}
	switch {
	case dropErr != nil:
		te.Status, te.Reason = StatusBlocked, codexTokenStuck+dropErr.Error()
	case cur.StopAsked:
		te.Status, te.Reason = StatusStopped, "stopped by the Hive"
	case limited:
		// Another login with room takes the next turn at once, or else the
		// next model on its route; else it waits for the first reset.
		pick, until := rs.next(r)
		te.Status, te.ParkedUntil = StatusParked, until
		te.Reason = "usage limit; resumes " + until.Local().Format("Mon 15:04")
		if _, unproven := rs.picks(r); len(unproven) > 0 {
			// Say why it didn't fall back, so the person knows a trial
			// on the model would let it.
			names := make([]string, len(unproven))
			for i, p := range unproven {
				names[i] = p.String()
			}
			te.Reason += " (no worker has finished on " + strings.Join(names, ", ") + " yet, so it doesn't fall back there)"
		}
		te.Queue = []string{"The usage limit has reset. Continue where you left off."}
		switch {
		case until.After(time.Now()):
		case !pick.Same(Pick{Harness: HarnessName(r.Harness), Model: r.Model}):
			te.Reason = "usage limit; moving to " + pick.String()
			te.Queue = nil // launch tells it why it moved
		default:
			te.Reason = "usage limit; moving to another login"
			te.Queue = []string{"Your last turn hit a usage limit, so your next turn runs on another login. Continue where you left off."}
		}
	case tl.Result == nil:
		// Under WSL, sandbox-exec's own early failures go to .wsl.
		out := rs.ws.TurnOut(r.ID, r.Turn)
		errTail := strings.TrimSpace(tail(out+".err", 400) + "\n" + tail(out+".wsl", 400))
		te.Offline = Unreachable(service(r.Harness), errTail)
		te.Status, te.ParkedUntil = StatusParked, time.Now().Add(backoff(cur.Retries))
		te.Reason = "interrupted; retrying on its checkout" + suffix(errTail)
		te.Queue = []string{"Your previous turn was interrupted. Your checkout and commits are intact. Continue where you left off."}
	case tl.Result.Subtype == "error_max_budget_usd":
		te.Status, te.Reason = StatusDone, "spent its budget"
	case keyRefused:
		te.Status, te.ParkedUntil = StatusParked, time.Now().Add(backoff(cur.Retries))
		te.Reason = fmt.Sprintf("the endpoint refused %s's key mid-turn, and a turn is handed the key only once; the next turn gets it afresh (if this repeats, store it again with `%s model key %s`)", r.Model, Cmd, r.Model)
		te.Queue = []string{"Your previous turn was cut off when the model's endpoint refused its key. Your checkout and commits are intact. Continue where you left off."}
	case down != nil:
		// As at a usage limit: the next pick with room takes the next turn
		// at once, else it waits for the first reset, with no backoff.
		pick, until := rs.next(r)
		te.Status, te.ParkedUntil, te.Unavailable = StatusParked, until, ran.String()
		te.Reason = ran.String() + cantAnswerWhy + "resumes " + until.Local().Format("Mon 15:04")
		te.Queue = []string{ran.String() + " couldn't answer (" + Clip(down.Text, 200) + "), so your turn waited for a model that can. Continue where you left off."}
		if !until.After(time.Now()) {
			te.Reason = ran.String() + cantAnswerWhy + "moving to " + pick.String()
			te.Queue = nil // launch tells it why it moved
		}
	case tl.Result.IsError:
		te.Status, te.ParkedUntil = StatusParked, time.Now().Add(backoff(cur.Retries))
		te.Reason = "harness error; retrying" + suffix(Clip(tl.Result.Text, 200))
		te.Offline = Unreachable(service(r.Harness), tl.Result.Text)
		te.Queue = []string{"The previous turn hit an error (" + Clip(tl.Result.Text, 200) + "). Continue where you left off."}
	case cur.Finish != nil && cur.Reopened && len(cur.Pending) > 0:
		// Reopened mid-turn, it stays running (issue #1022) and may then
		// finish on what it was told before: the next turn hands it the rest.
		// A blocked finish too, not ended blocked: the unread message may be
		// what unblocks it.
		te.Status, te.Reason = StatusIdle, reopenedMidTurn
	case cur.Finish != nil:
		te.Status = StatusDone
		if cur.Finish.Status == "blocked" {
			te.Status, te.Reason = StatusBlocked, Clip(cur.Finish.Account, 300)
		}
	case r.Schema != "" || r.ReadOnly || r.OneTurn:
		te.Status = StatusDone
	case r.Persistent:
		te.Status = StatusIdle
	case cur.Waiting != "":
		te.Status, te.Reason = StatusIdle, "waiting: "+cur.Waiting
	case cur.Nudges < 2:
		te.Status, te.Reason = StatusIdle, "nudged: ended a turn without finishing"
		te.Queue = []string{printedNudge(r, tl.Result.Text)}
	default:
		pick, at, onward := rs.onward(r, ran)
		switch {
		case onward:
			// Its nudges didn't get it to finish here: the next stop on its
			// route takes the next turn, rather than it going quiet or ending.
			te.Status, te.ParkedUntil, te.Stalled = StatusParked, at, ran.String()
			te.Reason = ran.String() + " keeps ending turns without finishing; moving to " + pick.String()
			if at.After(time.Now()) {
				te.Reason += ", which resumes " + at.Local().Format("Mon 15:04")
			}
			if pick.Harness == ran.Harness && ran.Harness != "open" {
				// The same harness resumes its session, so launch says nothing of the move.
				te.Queue = []string{fmt.Sprintf("Your turns on %s kept ending without finishing, so you now run on %s. Continue where you left off, and when the work is done (or can't be), end with `%s agent finish`.", ran, pick, Cmd)}
			}
		case te.Head != "" && te.Head == cur.Head && cur.Still+1 >= StillTurns:
			// Its nudges changed nothing: it can't follow the finish protocol,
			// so it ends and frees its slot instead of holding it quiet.
			te.Status, te.Reason = StatusBlocked, NoFinish+": it was nudged twice to call `hive agent finish`, and the turns after made no new commit and ended without it"
		default:
			te.Status, te.Reason = StatusQuiet, "keeps ending turns without finishing; flagged for the lead"
		}
	}
	if te.Offline != "" {
		// Retrying costs no usage while it's down, so it checks back every few minutes at most.
		te.Reason = te.Offline + "; retrying"
		te.ParkedUntil = time.Now().Add(min(backoff(cur.Retries), 5*time.Minute))
	}
	rs.append("run.turn_ended", te)
	if te.Reason == reopenedMidTurn {
		rs.append("run.reopened", runNote{ID: r.ID}) // the finish it made before the message isn't its last
	}
	// A message that came in while it finished blocked wakes it. The run is
	// read again, after the turn's end is recorded: one that landed since cur
	// was read is not in cur, and its Send saw the run still running.
	if te.Status == StatusBlocked {
		rs.wake(r.ID)
	}
	// A spawned helper's account goes back to the agent that spawned it.
	if (te.Status == StatusDone || te.Status == StatusBlocked) && r.Parent != "" {
		account := cur.Result
		if cur.Finish != nil {
			account = fmt.Sprintf("status: %s\n%s\ntested: %s\nunsure: %s", cur.Finish.Status, cur.Finish.Account, cur.Finish.Tested, cur.Finish.Unsure)
		}
		if r.Task != "" && te.Status == StatusDone {
			account += "\nwhere its commits go: their own pull request, task " + r.Task + "; you hear when it merges or stops"
		}
		if p, ok := rs.Get(r.Parent); ok && (!p.Terminal() || p.Persistent) {
			rs.Send(p.ID, fmt.Sprintf("[%s %s finished]\n%s", r.Role, r.ID, account))
		}
	}
}

// reopenedMidTurn is why a run that finished in a turn it was reopened during takes another.
const reopenedMidTurn = "reopened during its turn"

// NoFinish begins the reason a run ends blocked for never calling finish.
const NoFinish = "doesn't finish"

// StillTurns is how many turns in a row, the ones answering its two nudges,
// may end without finishing and without a new commit before a run ends
// blocked; one that commits stays quiet for the lead.
const StillTurns = 2

// headMark is the commit a checkout's HEAD points at, which only a new
// commit (or a checkout) changes; it is read without running git there, and
// is "" when it can't be read, so a run whose commits can't be seen stays
// quiet rather than ending blocked.
func headMark(checkout string) string {
	if checkout == "" {
		return ""
	}
	dir := filepath.Join(checkout, ".git")
	if b, err := SafeRead(dir, 4096); err == nil { // a worktree's .git is a file naming its git dir
		if d, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: "); ok {
			dir = d
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(checkout, dir)
			}
		}
	}
	head, err := SafeRead(filepath.Join(dir, "HEAD"), 4096)
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: ")
	if !ok {
		return hashOnly(ref) // detached: the hash itself
	}
	if !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "..") {
		return ""
	}
	common := dir
	if b, err := SafeRead(filepath.Join(dir, "commondir"), 4096); err == nil {
		if common = strings.TrimSpace(string(b)); !filepath.IsAbs(common) {
			common = filepath.Join(dir, common)
		}
	}
	for _, d := range []string{dir, common} {
		if b, err := SafeRead(filepath.Join(d, filepath.FromSlash(ref)), 4096); err == nil {
			return hashOnly(strings.TrimSpace(string(b)))
		}
	}
	packed, _ := SafeRead(filepath.Join(common, "packed-refs"), 16<<20)
	for _, line := range strings.Split(string(packed), "\n") {
		if hash, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref {
			return hashOnly(hash)
		}
	}
	return ""
}

// hashOnly is s when it is a commit hash and "" otherwise, so what a
// checkout's files say never goes on the record as anything but a hash.
func hashOnly(s string) string {
	if len(s) != 40 && len(s) != 64 {
		return ""
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return ""
		}
	}
	return s
}

func suffix(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	return ": " + s
}

// Unreachable says in one plain line why service couldn't be reached, read
// from an error's text, or "" when the text isn't about reaching it.
func Unreachable(service, text string) string {
	t := strings.ToLower(text)
	has := func(words ...string) bool {
		return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(t, w) })
	}
	switch {
	case has("token has expired", "/login", "auth login", "not logged in", "invalid api key", "authentication_error", "bad credentials"):
		return service + " login expired"
	case has("enotfound", "eai_again", "getaddrinfo", "no such host", "could not resolve", "network is unreachable", "enetunreach"):
		return "can't reach " + service + ": no internet connection"
	case has("connection error", "error connecting", "error sending request", "stream disconnected", "econnrefused", "econnreset", "etimedout", "connection refused", "connection reset", "i/o timeout"):
		return "can't reach " + service + ": the connection failed"
	}
	return ""
}

// service names what a harness talks to.
func service(h string) string {
	switch HarnessName(h) {
	case "codex":
		return "Codex"
	case "open":
		return "the model server"
	}
	if s, err := harness.Get(h); err == nil {
		if s, ok := s.(*harness.Spec); ok {
			return s.Display()
		}
	}
	return "Claude"
}

func backoff(n int) time.Duration {
	d := 30 * time.Second << min(n, 6)
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

func tail(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) > n {
		raw = raw[len(raw)-n:]
	}
	return Clip(string(raw), n)
}

// PendingSep joins messages that queued during a turn into the next prompt.
const PendingSep = "\n\n---\n\n"

// holdTurn journals why r's turn waits, keeping its status, so a parked run
// keeps its backoff and a nudged one its count; the live figures after " ("
// change every tick, so only a new kind of hold is journalled.
func (rs *Runs) holdTurn(r Run, why string) {
	if kind, _, _ := strings.Cut(why, " ("); !strings.HasPrefix(r.Reason, kind) {
		rs.append("run.held", runNote{ID: r.ID, Reason: why})
	}
}

// launch starts the run's next turn in its sandbox.
func (rs *Runs) launch(r Run, cfg Config) {
	rs.mu.Lock()
	// A turn starts at most once: not over a live one, nor from a copy of
	// the run older than its last turn or status (one ended meanwhile stays
	// ended), nor beside another launch of it.
	if cur := rs.runs[r.ID]; len(rs.holds) > 0 || cur == nil || cur.Status == StatusRunning || cur.Status != r.Status || cur.Turn != r.Turn || rs.starting[r.ID] {
		rs.mu.Unlock()
		return
	}
	if !r.Lead() && rs.leadLaunching > 0 {
		// A lead's turn starts first; this one is due again next tick. Leads
		// launch when a message comes, so this rarely repeats; one that does
		// (leads launching back to back, tick after tick) is said in daemon.log.
		if rs.deferred == nil {
			rs.deferred = map[string]int{}
		}
		rs.deferred[r.ID]++
		if n := rs.deferred[r.ID]; n == 10 || n%100 == 0 {
			log.Printf("runs: %s %s has waited %d ticks for leads' launches to finish", r.Role, r.ID, n)
		}
		rs.mu.Unlock()
		return
	}
	delete(rs.deferred, r.ID)
	if rs.starting == nil {
		rs.starting = map[string]bool{}
	}
	rs.starting[r.ID] = true
	rs.launching++
	if r.Lead() {
		rs.leadLaunching++
	}
	rs.mu.Unlock()
	defer func() {
		rs.mu.Lock()
		rs.launching--
		if r.Lead() {
			rs.leadLaunching--
		}
		delete(rs.starting, r.ID)
		rs.mu.Unlock()
	}()
	floor, memMax, cpuMax := cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax
	if r.Lead() {
		// A lead answers however busy the machine is: no pressure hold, and
		// only the floor under which its turn could not run at all.
		floor, memMax, cpuMax = min(floor, LeadMemFloorMB), 0, 0
	}
	// A turn this machine is too strained for goes to an executor module
	// with room, or waits.
	var elsewhere sandbox.Sandbox
	where := ""
	why := Strained(floor, memMax, cpuMax)
	if why != "" {
		elsewhere, where = rs.executor()
	}
	if why != "" && elsewhere == nil {
		if !r.Lead() {
			rs.Holding()
		}
		rs.holdTurn(r, "waiting: "+why)
		return
	}
	if rs.Admit != nil {
		if why := rs.Admit(r); why != "" {
			rs.holdTurn(r, why)
			return
		}
	}
	if !r.Lead() && elsewhere == nil && rs.ramp(time.Now()) {
		rs.holdTurn(r, "waiting: starting one turn at a time after the machine was strained")
		return
	}
	sb, err := rs.sandbox()
	if err != nil {
		if r.Status != StatusQueued || r.Reason != err.Error() {
			rs.append("run.status", runNote{ID: r.ID, Status: StatusQueued, Reason: err.Error()})
		}
		return
	}
	if elsewhere != nil {
		sb = elsewhere
	}
	// A run whose own harness and model are at their limit moves to the
	// first pick with room; one changing harness, or open model, can't
	// carry its session over, so it starts afresh on its brief.
	from := Pick{Harness: HarnessName(r.Harness), Model: r.Model}
	pick, _ := rs.next(r)
	moved := ""
	if !pick.Same(from) {
		if pick.Harness != from.Harness || pick.Harness == "open" {
			r.SessionLive = false
			why := "the settings route you there"
			if slices.Contains(r.Stalled, from.String()) {
				why = "your turns on " + from.String() + " kept ending without finishing; when the work is done (or can't be), end with `" + Cmd + " agent finish`"
			} else if d, ok := rs.downFor(from); ok {
				why = from.String() + " can't answer (" + Clip(d.Text, 200) + ")"
			} else if rs.freeAt(from.Harness, from.Model, Reserved(r.Role)).After(time.Now()) {
				why = from.String() + " is at its usage limit"
			}
			moved = fmt.Sprintf("You now run on %s: %s. This is a fresh session on your brief above; your checkout, commits and notes are as you left them, so look at them (git log, $HIVE_BOX/notes.md) and continue.", pick, why)
		}
		r.Harness, r.Model = pick.Harness, pick.Model
	}
	h, err := harness.Get(r.Harness)
	if err != nil { // a module's harness since removed: wait a while for it to come back, then stop and say so
		if r.Retries >= harnessGoneTries {
			rs.SetStatus(r.ID, StatusBlocked, fmt.Sprintf("%s %s, tried %d times: install the module that declares it again, or set another harness, and reopen the run", harnessGone, err, r.Retries+1))
			return
		}
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: harnessGone + " " + err.Error(), Until: time.Now().Add(5 * time.Minute), N: r.Retries + 1})
		return
	}
	if HarnessName(r.Harness) == "open" {
		m := cfg.Models[r.Model]
		if err := harness.OpenRouterPaid(m.Endpoint, m.Model); err != nil {
			rs.SetStatus(r.ID, StatusBlocked, fmt.Sprintf("no turn on %s: %v; route the run to a free model and reopen it", r.Model, err))
			return
		}
		h = m.Harness() // Claude Code for a model on an Anthropic-compatible API
	}
	turn := r.Turn + 1
	// A session that never came up is started fresh under a new id; one that
	// did is resumed with whatever is waiting for it.
	resume := r.SessionLive
	session := r.Session
	delivered := len(r.Pending)
	pending := strings.Join(r.Pending, PendingSep)
	prompt := pending
	if !resume {
		if r.Turn > 0 {
			session = NewUUID()
		}
		prompt = r.Brief
		if pending != "" && (r.Turn == 0 || moved != "") {
			prompt += PendingSep + pending
		}
		if moved != "" {
			prompt += PendingSep + moved
		}
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Continue."
	}
	r.Session = session
	// One process per turn: another daemon on this workspace may have started it.
	took, err := rs.claimTurn(r.ID, turn)
	if err != nil {
		// A wait on this daemon's own live turn is kept apart, so it doesn't
		// use up the once-per-daemon alert a second daemon's claim raises.
		seen := rs.backedOff
		if errors.Is(err, errTurnLive) {
			seen = rs.turnLive
		}
		rs.mu.Lock()
		said, first := seen[r.ID] == turn, len(seen) == 0
		seen[r.ID] = turn
		rs.mu.Unlock()
		if !said {
			fmt.Printf("hive: %s's turn %d isn't started here: %v\n", r.ID, turn, err)
		}
		if errors.Is(err, errTurnLive) {
			if !said { // not a second daemon: something took this run off running mid-turn
				rs.append("alert.raised", map[string]string{"kind": "daemon", "ref": "turn-live-" + r.ID, "text": fmt.Sprintf("%s's turn %d was about to start while %v; it waits for that turn to end.", r.ID, turn, err)})
			}
			return
		}
		if first { // once per daemon, so a second daemon shows in the record and on the dashboard
			rs.append("alert.raised", map[string]string{"kind": "daemon", "ref": "turn-claim", "text": fmt.Sprintf("This daemon (pid %d) backed off %s's turn %d: %v. Two daemons may be running on this workspace.", os.Getpid(), r.ID, turn, err)})
		}
		return
	}
	if took != "" {
		fmt.Printf("hive: %s's turn %d is started again here: %s\n", r.ID, turn, took)
		rs.append("alert.raised", map[string]string{"kind": "daemon", "ref": "turn-takeover-" + r.ID, "text": fmt.Sprintf("%s's turn %d is started again: %s before journalling its start.", r.ID, turn, took)})
	}
	promptFile := filepath.Join(rs.ws.RunHost(r.ID), fmt.Sprintf("turn-%03d.prompt", turn))
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: err.Error(), Until: time.Now().Add(time.Minute)})
		return
	}
	codexHome, started := "", false
	if HarnessName(r.Harness) == "codex" {
		if codexHome, err = rs.freshCodexHome(r); err != nil {
			rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: err.Error(), Until: time.Now().Add(time.Minute)})
			return
		}
		defer func() {
			if !started { // no turn will end to delete the token
				rs.dropCodexHome(r.ID)
			}
		}()
	}
	login, tok, err := rs.pickLogin(r.Harness, r.Model, codexHome, !sb.Isolated(), Reserved(r.Role))
	if err != nil {
		until := rs.freeAt(r.Harness, r.Model, Reserved(r.Role))
		if !until.After(time.Now()) {
			until = time.Now().Add(5 * time.Minute)
		}
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: "login for " + HarnessName(r.Harness) + ": " + err.Error(), Until: until})
		return
	}
	spec, err := rs.sandboxSpec(cfg, sb, h, r, turn, resume, promptFile, tok, codexHome)
	defer func() {
		if !started && HarnessName(r.Harness) == "open" {
			rs.dropOpenKey(r.ID) // no turn will end to delete the key
		}
	}()
	if errors.Is(err, harness.ErrArgTooLong) { // the same prompt would fail every time
		rs.SetStatus(r.ID, StatusBlocked, err.Error())
		return
	}
	if err != nil {
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: err.Error(), Until: time.Now().Add(5 * time.Minute)})
		return
	}
	p, err := sb.Start(spec)
	if err != nil {
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: "start failed: " + err.Error(), Until: time.Now().Add(backoff(r.Retries))})
		return
	}
	started = true
	rs.claimed(r.ID, turn, p)
	rs.append("run.turn_started", turnStarted{ID: r.ID, Session: session, Turn: turn, Proc: p, Delivered: delivered, Resume: resume, Login: login.Key(), Model: r.Model, Harness: r.Harness, Where: where})
}

// Elsewhere says whether an executor module has room for a turn now.
func (rs *Runs) Elsewhere() bool {
	ex, _ := rs.executor()
	return ex != nil
}

// executor returns an executor module with room for a turn this machine is
// too strained to start, and the runner it named; nil when there is none or
// the local sandbox isn't one whose spec another machine can run.
func (rs *Runs) executor() (sandbox.Sandbox, string) {
	if rs.Executor == nil {
		return nil, ""
	}
	if sb, err := rs.sandbox(); err != nil || !sb.Isolated() {
		return nil, ""
	}
	return rs.Executor()
}

// RunPath is where a run's agent sees its run dir ($HIVE_RUN).
func (rs *Runs) RunPath(id string) string {
	m := sandbox.Mount{Host: rs.ws.RunRO(id), Inside: "/hive/run"}
	if sb, err := rs.sandbox(); err == nil {
		return sandbox.InsidePath(sb, m)
	}
	return m.Host
}

// sandboxSpec lays out what the agent sees.
func (rs *Runs) sandboxSpec(cfg Config, sb sandbox.Sandbox, h harness.Harness, r Run, turn int, resume bool, promptFile string, tok []string, codexHome string) (sandbox.Spec, error) {
	home := sandbox.Mount{Host: rs.ws.RunHome(r.ID), Inside: "/hive/home", RW: true}
	mounts := []sandbox.Mount{
		{Host: r.Checkout, Inside: "/work", RW: !r.ReadOnly},
		{Host: rs.ws.RunRO(r.ID), Inside: "/hive/run"},
		// A read-only turn on a harness that can run commands then (Codex, a
		// module's) gets no mailbox to send through, as Claude's, with read
		// tools only, has none (harness.ReadOnlyTools).
		{Host: rs.ws.RunBox(r.ID), Inside: "/hive/box", RW: !(r.ReadOnly && !harness.ReadOnlyTools(h))},
		home,
		{Host: rs.ws.KB(), Inside: "/hive/kb"},
		{Host: rs.ws.Modules(), Inside: "/hive/modules"},
	}
	if !r.Isolated {
		mounts = append(mounts, sandbox.Mount{Host: rs.ws.Projects(), Inside: "/hive/projects"},
			sandbox.Mount{Host: rs.ws.Hive(), Inside: "/hive/state"})
		// Every agent reads the whole Hive but what grants access: the login
		// pool's stored logins, module programs' homes and logs (where a door
		// module keeps its own service login), and Private (the daemon's token,
		// every run's home and CODEX_HOME). All are folders, so what lands in
		// them mid-turn lands under the cover.
		for _, d := range rs.ws.Covered() {
			os.MkdirAll(d, 0o700)
			mounts = append(mounts, sandbox.Mount{Host: d, Inside: "/hive/state/" + filepath.Base(d), Hide: true})
		}
	}
	mounts = append(mounts, sandbox.Mount{Host: rs.ws.Tools(), Inside: sandbox.ToolsInside})
	mounts = append(mounts, rs.hideIsolated(r)...)
	if r.Isolated && sb.Isolated() {
		// Only granted modules are visible: mount each over an empty modules dir,
		// which holds their mount points, since bwrap can't make them inside a
		// read-only bind. No grants leaves it empty.
		empty := filepath.Join(rs.ws.RunHost(r.ID), "modules")
		os.MkdirAll(empty, 0o755)
		mounts[5].Host = empty
		for _, m := range r.Modules {
			os.MkdirAll(filepath.Join(empty, m), 0o755)
			mounts = append(mounts, sandbox.Mount{Host: filepath.Join(rs.ws.Modules(), m), Inside: "/hive/modules/" + m})
		}
	}
	in := func(m sandbox.Mount) string { return sandbox.InsidePath(sb, m) }
	cfgDir := ""
	if codexHome != "" {
		m := sandbox.Mount{Host: codexHome, Inside: "/hive/codex", RW: true}
		mounts, cfgDir = append(mounts, m), in(m)
	}
	t := harness.Turn{
		Role: r.Role, SessionID: r.Session, Resume: resume, Model: r.Model, Tools: r.Tools, SystemText: r.System,
		SystemFile: filepath.Join(in(mounts[1]), "system.md"), ReadOnly: r.ReadOnly, RunDir: in(mounts[1]), Schema: r.Schema, Sandboxed: sb.Isolated(),
		MaxOutput: cfg.MaxOutputTokens, ConfigDir: cfgDir,
	}
	if r.Budget > 0 {
		// Claude Code counts --max-budget-usd against this process's spend
		// only, not the running total a resumed session carries on.
		t.MaxBudgetUSD = max(r.Budget-r.Cost, 0.01)
	}
	if sb.Isolated() {
		t.SystemFile = "/hive/run/system.md"
	}
	t.Writable = []string{in(mounts[2]), in(home)}
	if r.Schema != "" {
		t.SchemaFile = filepath.Join(in(mounts[1]), "schema.json")
		if sb.Isolated() {
			t.SchemaFile = "/hive/run/schema.json"
		}
	}
	env := []string{
		"HIVE_ID=" + r.ID, "HIVE_ROLE=" + r.Role, "HIVE_PROJECT=" + r.Project, "HIVE_TASK=" + r.Task,
		"HIVE_BOX=" + in(mounts[2]), "HIVE_RUN=" + in(mounts[1]), "HIVE_KB=" + in(mounts[4]), "HIVE_MODULES=" + in(mounts[5]),
		"GIT_AUTHOR_NAME=Hive " + r.Role, "GIT_AUTHOR_EMAIL=hive@localhost", "GIT_COMMITTER_NAME=Hive " + r.Role, "GIT_COMMITTER_EMAIL=hive@localhost",
	}
	if !r.Isolated {
		env = append(env, "HIVE_PROJECTS="+in(mounts[6]), "HIVE_STATE="+in(mounts[7]))
	}
	if sb.Isolated() {
		env = append(sandbox.InsideEnv("/hive/home", true), env...)
	} else {
		env = append(env, "HIVE_WORKSPACE="+rs.ws.Root)
	}
	if HarnessName(r.Harness) == "open" {
		// The key goes in a file the open agent deletes before any tool runs,
		// never in its environment, which its commands could read from /proc.
		keyIn := filepath.Join(in(home), ".hive-open", "key")
		if sb.Isolated() {
			keyIn = in(home) + "/.hive-open/key"
		}
		var err error
		if tok, err = rs.openKey(r.ID, tok, keyIn); err != nil {
			return sandbox.Spec{}, err
		}
		if m := cfg.Models[r.Model]; m.Anthropic() {
			// Claude Code runs it: on the endpoint's id, with a key helper
			// that prints the key file once and deletes it.
			t.Model, t.Endpoint, t.Context = m.Model, m.Endpoint, m.Context
			if slices.Contains(tok, "HIVE_OPEN_KEY_FILE="+keyIn) {
				hive := "hive"
				if tools := rs.tools(); !sb.Isolated() && len(tools) > 0 {
					hive = tools[0]
				}
				t.KeyCommand = shellWord(hive, sb.Isolated()) + " open-key " + shellWord(keyIn, sb.Isolated())
			}
			// Its own config folder, so a Claude login in the HOME of a bare
			// run is never found: none can reach the vendor's endpoint.
			if err := realDir(filepath.Join(rs.ws.RunHome(r.ID), ".claude-open")); err != nil {
				return sandbox.Spec{}, err
			}
			t.ConfigDir = in(home) + "/.claude-open"
			if !sb.Isolated() {
				t.ConfigDir = filepath.Join(in(home), ".claude-open")
			}
			tok = nil // the endpoint and id go in Claude Code's own settings
		} else {
			env = append(env, "HIVE_OPEN_HOME="+in(home))
		}
	}
	// PWD is the checkout, not the daemon's own folder a bare run inherits:
	// some harnesses (OpenCode) work in $PWD rather than their working dir.
	env = append(env, "PWD="+in(mounts[0]))
	env = append(env, tok...)
	env = append(env, h.Env(t)...)
	tools := rs.tools()
	if h.Program() != "hive" {
		tools = append(tools, h.Program())
	}
	stdin := promptFile
	if p, ok := h.(harness.Prompter); ok {
		// It is sent its own prompt (the system prompt ahead of it, say), on
		// stdin or in its args.
		raw, err := os.ReadFile(promptFile)
		if err != nil {
			return sandbox.Spec{}, err
		}
		t.Prompt = string(raw)
		sent := p.Prompt(t)
		if p.PromptArg() {
			sent = ""
		}
		stdin = strings.TrimSuffix(promptFile, ".prompt") + ".stdin"
		if err := os.WriteFile(stdin, []byte(sent), 0o644); err != nil {
			return sandbox.Spec{}, err
		}
	}
	argv := append([]string{h.Program()}, h.Args(t)...)
	if !sb.Isolated() && h.Program() == "hive" && len(tools) > 0 {
		argv[0] = tools[0]
	}
	if err := harness.CheckArgs(h.Name(), argv); err != nil {
		return sandbox.Spec{}, err
	}
	allow := cfg.Egress.For(r.Role)
	if allow != nil {
		allow = append(allow, harness.APIHosts(h)...)
		if host := cfg.openHost(r.Model); HarnessName(r.Harness) == "open" && host != "" {
			allow = append(allow, host)
		}
	}
	return sandbox.Spec{
		ID: r.ID, Argv: argv, Env: env, Cwd: in(mounts[0]), Mounts: mounts, Tools: tools,
		Stdin: stdin, Stdout: rs.ws.TurnOut(r.ID, turn), StopFile: filepath.Join(rs.ws.RunHost(r.ID), "stop"),
		Network: true, Allow: allow, Local: cfg.Egress.LocalAddrs(), Guard: rs.ws.Guarded(), Cover: rs.ws.Covered(), UID: os.Getuid(), GID: os.Getgid(),
		Nice: r.Nice(),
	}, nil
}

// Walled refuses r reaching project when either side is hidden (private or
// isolated): a hidden project is reached only from its own runs, and its runs
// reach no other project, so nothing of it is carried out in a brief or message. Both come from the core's record of r, never from r.
func (rs *Runs) Walled(r Run, project string) error {
	if project == r.Project || rs.projs == nil {
		return nil
	}
	for _, name := range []string{project, r.Project} {
		if p, ok := rs.projs.Get(name); ok && p.Hidden() {
			return fmt.Errorf("project %q is out of reach of %s's agents", project, r.Project)
		}
	}
	return nil
}

// hideIsolated covers what hidden projects (private or isolated) other than
// r's own leave in the shared Hive, so r reads none of their code, runs,
// chats, notes, checks or scores. An isolated run sees only the knowledge base
// of what is shared, so it gets just the notes covered; a private run sees the
// rest of the shared Hive as a shared one does. The journal holds every
// project's events in one file, so it is covered whole while any other project
// is hidden. The folders are made first so what lands in them mid-turn is
// covered too; a run or chat started mid-turn is not, until that agent's next
// turn.
func (rs *Runs) hideIsolated(r Run) []sandbox.Mount {
	if rs.projs == nil {
		return nil
	}
	iso := map[string]bool{}
	var out []sandbox.Mount
	var rel []string
	for _, p := range rs.projs.List() {
		if !p.Hidden() || p.Name == r.Project {
			continue
		}
		iso[p.Name] = true
		kb := filepath.Join(rs.ws.KB(), "isolated", p.Name)
		rs.ws.IsolatedKB(p.Name) // never made through a link
		out = append(out, sandbox.Mount{Host: kb, Inside: "/hive/kb/isolated/" + p.Name, Hide: true})
		if r.Isolated {
			continue
		}
		if Exists(rs.ws.Project(p.Name)) { // no clone yet: nothing to hide, and no mount point
			out = append(out, sandbox.Mount{Host: rs.ws.Project(p.Name), Inside: "/hive/projects/" + p.Name, Hide: true})
		}
		for _, d := range []string{"checks", "gates", "risk"} {
			os.MkdirAll(filepath.Join(rs.ws.Hive(), d, p.Name), 0o755)
			rel = append(rel, d+"/"+p.Name)
		}
		// A private project's lead works in its leads/ folder (an isolated one's in a checkout).
		os.MkdirAll(filepath.Join(rs.ws.Hive(), "leads", "project-"+p.Name), 0o755)
		rel = append(rel, "kb/isolated/"+p.Name, "scores/"+p.Name+".jsonl", "chats/project_"+p.Name+".jsonl", "leads/project-"+p.Name)
	}
	if len(iso) > 0 { // the kb's record is filed from the journal, so it is covered whole too
		rec := filepath.Join(rs.ws.KB(), "record")
		os.MkdirAll(rec, 0o755)
		out = append(out, sandbox.Mount{Host: rec, Inside: "/hive/kb/record", Hide: true})
		if !r.Isolated {
			rel = append(rel, "kb/record")
		}
	}
	if len(iso) == 0 || r.Isolated {
		return out
	}
	rel = append(rel, "events.jsonl")
	rs.mu.Lock()
	for id, x := range rs.runs {
		if iso[x.Project] {
			rel = append(rel, "runs/"+id, "chats/agent_"+id+".jsonl", "scratch/"+id)
		}
	}
	rs.mu.Unlock()
	for _, f := range rel {
		host := filepath.Join(rs.ws.Hive(), filepath.FromSlash(f))
		if Exists(host) {
			out = append(out, sandbox.Mount{Host: host, Inside: "/hive/state/" + f, Hide: true})
		}
	}
	return out
}

// freshCodexHome makes the turn's CODEX_HOME: a new folder on the host side
// the agent never had, so nothing it planted can steer the host's writes.
// Only the sessions from earlier turns, which resume needs, move into it.
// The Hive's instructions go in its config.toml rather than on the command
// line, which has a size cap.
func (rs *Runs) freshCodexHome(r Run) (string, error) {
	if err := rs.dropCodexHome(r.ID); err != nil {
		return "", err
	}
	dir := filepath.Join(rs.ws.RunPrivate(r.ID), "codex")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	os.Rename(filepath.Join(rs.ws.RunPrivate(r.ID), "codex-sessions"), filepath.Join(dir, "sessions"))
	if r.System == "" {
		return dir, nil
	}
	// A JSON string is a valid TOML basic string once DEL, which JSON leaves raw, is escaped.
	s, _ := json.Marshal(r.System)
	s = bytes.ReplaceAll(s, []byte("\x7f"), []byte(`\u007f`))
	return dir, os.WriteFile(filepath.Join(dir, "config.toml"), []byte("developer_instructions = "+string(s)+"\n"), 0o600)
}

// dropCodexHome deletes the turn's CODEX_HOME, and with it the access token,
// keeping only its sessions for the next turn's resume. Folders the agent
// locked are opened first; links are never followed.
func (rs *Runs) dropCodexHome(id string) error {
	dir := filepath.Join(rs.ws.RunPrivate(id), "codex")
	os.Rename(filepath.Join(dir, "sessions"), filepath.Join(rs.ws.RunPrivate(id), "codex-sessions"))
	return removeTree(dir)
}

// codexUsage is the plan usage and context Codex recorded during r's turn
// in session's rollout file (the newest rollout when the session isn't
// known), which the agent could write, so it is read as such.
func (rs *Runs) codexUsage(r Run, session string) (*harness.RateLimit, int) {
	newest, at := "", time.Time{}
	filepath.WalkDir(filepath.Join(rs.ws.RunPrivate(r.ID), "codex", "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") ||
			session != "" && !strings.Contains(d.Name(), session) {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(at) {
			newest, at = p, fi.ModTime()
		}
		return nil
	})
	if newest == "" {
		return nil, 0
	}
	raw, err := SafeReadTail(newest, 16<<20)
	if err != nil {
		return nil, 0
	}
	return harness.Codex{}.Usage(raw, r.TurnStarted)
}

// openKeyFile is where a turn on the open harness finds its model's key.
func (rs *Runs) openKeyFile(id string) string {
	return filepath.Join(rs.ws.RunHome(id), ".hive-open", "key")
}

// openKey moves a model key out of tok into the run's key file, which the
// agent sees at keyIn; with no key it leaves no file.
func (rs *Runs) openKey(id string, tok []string, keyIn string) ([]string, error) {
	var out []string
	key := ""
	for _, kv := range tok {
		if k, ok := strings.CutPrefix(kv, "HIVE_OPEN_KEY="); ok {
			key = k
		} else {
			out = append(out, kv)
		}
	}
	rs.dropOpenKey(id)
	if key == "" {
		return out, nil
	}
	if err := rs.openKeyDir(id); err != nil {
		return nil, err
	}
	// O_EXCL never follows a link planted at the file's own name.
	f, err := os.OpenFile(rs.openKeyFile(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, err = f.Write([]byte(key))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		rs.dropOpenKey(id)
		return nil, err
	}
	return append(out, "HIVE_OPEN_KEY_FILE="+keyIn), nil
}

// openKeyDir makes the key file's folder in the run's home.
func (rs *Runs) openKeyDir(id string) error { return realDir(filepath.Dir(rs.openKeyFile(id))) }

// realDir makes a folder in a run's home, which the agent can write: a link
// (or file) an earlier turn left in its place is removed, never followed, so
// the daemon writes nothing elsewhere and the harness reads nothing from
// elsewhere.
func realDir(dir string) error {
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		os.Remove(dir) // the link itself
	}
	os.MkdirAll(filepath.Dir(dir), 0o755) // the run's home, the daemon's own
	os.Mkdir(dir, 0o700)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a real folder", dir)
	}
	return nil
}

// shellWord quotes s as one word for the shell Claude Code runs its key
// helper with: POSIX quoting, so a path holding $ or ` is never expanded;
// on bare Windows, cmd's double quotes (a Windows path holds no ").
func shellWord(s string, isolated bool) string {
	if runtime.GOOS == "windows" && !isolated {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// dropOpenKey deletes a key file a turn left, read or not, and only inside a
// real folder: through a link it would delete another file.
func (rs *Runs) dropOpenKey(id string) {
	f := rs.openKeyFile(id)
	if fi, err := os.Lstat(filepath.Dir(f)); err == nil && fi.IsDir() {
		os.Remove(f) // a link at the file's name goes, not what it points to
	}
}

// removeTree deletes a folder an agent may have locked, opening it first;
// links are never followed.
func removeTree(dir string) error {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// Clip shortens s to n runes on one line.
func Clip(s string, n int) string { return harness.Clip(s, n) }
