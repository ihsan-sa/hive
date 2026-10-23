package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
}

// Finish is an agent's closing account.
type Finish struct {
	Status  string `json:"status"` // done | blocked
	Account string `json:"account"`
	Tested  string `json:"tested,omitempty"`
	Unsure  string `json:"unsure,omitempty"`
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
	Result      string          `json:"result,omitempty"`
	Structured  json.RawMessage `json:"structured,omitempty"`
	Finish      *Finish         `json:"finish,omitempty"`
	Pending     []string        `json:"pending,omitempty"`
	ParkedUntil time.Time       `json:"parked_until,omitempty"`
	Retries     int             `json:"retries"`
	Nudges      int             `json:"nudges"`
	Login       string          `json:"login,omitempty"` // the login its latest turn went on
	Progress    string          `json:"progress,omitempty"`
	ProgressAt  time.Time       `json:"progress_at,omitempty"`
	StopAsked   bool            `json:"stop_asked,omitempty"`
	Waiting     string          `json:"waiting,omitempty"` // what it ended its turn waiting for
	Created     time.Time       `json:"created"`
	Updated     time.Time       `json:"updated"`
	TurnStarted time.Time       `json:"turn_started,omitempty"`
	LastOutput  time.Time       `json:"last_output,omitempty"`
}

// Terminal reports whether the run will take no more turns on its own.
func (r *Run) Terminal() bool {
	switch r.Status {
	case StatusDone, StatusBlocked, StatusStopped:
		return true
	}
	return false
}

// Paths of one run's folders.
func (ws Workspace) RunHost(id string) string { return filepath.Join(ws.Run(id), "host") }
func (ws Workspace) RunRO(id string) string   { return filepath.Join(ws.Run(id), "ro") }
func (ws Workspace) RunBox(id string) string  { return filepath.Join(ws.Run(id), "box") }
func (ws Workspace) RunHome(id string) string { return filepath.Join(ws.Run(id), "home") }

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
}

// Key names the plan's login.
func (p Plan) Key() string {
	if p.Login == "" {
		return HarnessName(p.Harness)
	}
	return p.Login
}

// Parked reports whether the plan is at its usage limit until a reset still ahead.
func (p Plan) Parked(now time.Time) bool {
	return p.Limit.Status == "rejected" && p.Limit.ResetsAt.After(now)
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
	// ModelFor is the model a persistent run's next turn takes, so a lead
	// follows a changed setting; empty keeps the run's own.
	ModelFor func(Run) string

	mu    sync.Mutex
	runs  map[string]*Run
	plans map[string]Plan
	busy  map[string]bool    // runs whose tick work is in flight
	spent map[string]float64 // "2006-01" (UTC) -> what runs cost that month
}

// NewRuns builds the run manager and replays its records.
func NewRuns(ws Workspace, j *Journal, sb func() (sandbox.Sandbox, error), tokens TokenSource, tools func() []string) *Runs {
	rs := &Runs{ws: ws, j: j, sandbox: sb, tokens: tokens, tools: tools, runs: map[string]*Run{}, plans: map[string]Plan{}, busy: map[string]bool{}, spent: map[string]float64{}}
	rs.ops = NewOps(rs)
	_ = j.Replay(rs.apply)
	// A crash between writing a turn's token and starting the turn left it behind.
	stuck := map[string]error{}
	for _, r := range rs.runs {
		if HarnessName(r.Harness) == "codex" && r.Status != StatusRunning {
			if err := rs.dropCodexHome(r.ID); err != nil {
				stuck[r.ID] = err
			}
		}
	}
	j.Subscribe(func(e Event) {
		rs.mu.Lock()
		rs.apply(e)
		rs.mu.Unlock()
	})
	for id, err := range stuck {
		why := "its Codex token could not be deleted: " + err.Error()
		r, _ := rs.Get(id)
		switch {
		case r.Status == StatusBlocked && strings.HasPrefix(r.Reason, "its Codex token could not be deleted"):
			// already said; a restart adds nothing
		case r.Terminal():
			rs.Progress(id, why) // a finished run keeps its status
		default:
			rs.SetStatus(id, StatusBlocked, why)
		}
	}
	return rs
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
	Login     string      `json:"login,omitempty"`
}

type turnEnded struct {
	ID          string          `json:"id"`
	Turn        int             `json:"turn"`
	SessionLive bool            `json:"session_live"`
	Session     string          `json:"session,omitempty"` // the id the harness reported
	Result      *harness.Result `json:"result,omitempty"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason,omitempty"`
	ParkedUntil time.Time       `json:"parked_until,omitempty"`
	Queue       []string        `json:"queue,omitempty"`
}

// egressRefused is the hosts a turn's egress proxy refused.
type egressRefused struct {
	ID    string   `json:"id"`
	Turn  int      `json:"turn"`
	Hosts []string `json:"hosts"`
}

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
	if !strings.HasPrefix(e.Kind, "run.") && e.Kind != "plan.usage" {
		return
	}
	switch e.Kind {
	case "run.created":
		var s RunSpec
		e.Decode(&s)
		rs.runs[s.ID] = &Run{RunSpec: s, Session: NewUUIDFrom(s.ID), Status: StatusQueued, Created: e.At, Updated: e.At}
		return
	case "plan.usage":
		var p Plan
		e.Decode(&p)
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
	switch e.Kind {
	case "run.turn_started":
		var t turnStarted
		e.Decode(&t)
		r.Turn, r.Proc, r.Status, r.TurnStarted, r.LastOutput, r.Login = t.Turn, t.Proc, StatusRunning, e.At, e.At, t.Login
		if t.Session != "" {
			r.Session = t.Session
		}
		if t.Model != "" {
			r.Model = t.Model
		}
		r.Waiting = ""
		if t.Delivered <= len(r.Pending) {
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
		r.Status, r.Reason, r.ParkedUntil = t.Status, t.Reason, t.ParkedUntil
		r.Proc = proc.Handle{}
		if t.Result != nil {
			r.Cost += t.Result.CostUSD
			rs.spent[e.At.UTC().Format("2006-01")] += t.Result.CostUSD
			if !t.Result.IsError {
				r.Result, r.Structured = t.Result.Text, t.Result.Structured
				r.Retries = 0
			}
		}
		if t.Status == StatusParked && (t.Result == nil || (t.Result.IsError && !t.Result.Limited)) {
			r.Retries++
		}
		if len(t.Queue) > 0 {
			r.Pending = append(append([]string{}, t.Queue...), r.Pending...)
		}
		if strings.HasPrefix(t.Reason, "nudged") {
			r.Nudges++
		}
	case "run.queued":
		var n runNote
		e.Decode(&n)
		r.Pending = append(r.Pending, n.Text)
	case "run.delivered":
		var n runNote
		e.Decode(&n)
		if n.N <= len(r.Pending) {
			r.Pending = r.Pending[n.N:]
		}
	case "run.finish":
		var f struct {
			ID     string `json:"id"`
			Finish Finish `json:"finish"`
		}
		e.Decode(&f)
		r.Finish = &f.Finish
	case "run.progress":
		var n runNote
		e.Decode(&n)
		r.Progress, r.ProgressAt = n.Text, e.At
	case "run.status":
		var n runNote
		e.Decode(&n)
		r.Status, r.Reason, r.ParkedUntil = n.Status, n.Reason, n.Until
		if n.Status == StatusQueued || n.Status == StatusIdle {
			r.Nudges = 0
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
		r.Finish, r.StopAsked, r.Nudges = nil, false, 0
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
	for _, d := range []string{rs.ws.RunHost(s.ID), rs.ws.RunRO(s.ID), filepath.Join(rs.ws.RunBox(s.ID), "outbox"), filepath.Join(rs.ws.RunBox(s.ID), "inbox"), rs.ws.RunHome(s.ID)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Run{}, err
		}
	}
	files := map[string]string{"system.md": s.System, "brief.md": s.Brief}
	if s.Schema != "" {
		files["schema.json"] = s.Schema
	}
	for k, v := range s.Inputs {
		files[k] = v
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(rs.ws.RunRO(s.ID), filepath.Base(name)), []byte(content), 0o644); err != nil {
			return Run{}, err
		}
	}
	rs.append("run.created", s)
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
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []Plan
	for _, p := range rs.plans {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// pickLogin chooses the login for a run's next turn: of those not parked at
// their usage limit, the least used, the machine's own first on a tie. A
// bare run whose machine login has no credential file the Hive can read gets
// no token, so the harness's own login on the host still works.
func (rs *Runs) pickLogin(h, home string, bare bool) (Login, []string, error) {
	logins := rs.tokens.Logins(h)
	now := time.Now()
	rs.mu.Lock()
	use := func(l Login) float64 { return rs.plans[l.Key()].Limit.Utilization }
	var open []Login
	for _, l := range logins {
		if !rs.plans[l.Key()].Parked(now) {
			open = append(open, l)
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return use(open[i]) < use(open[j]) })
	rs.mu.Unlock()
	err := fmt.Errorf("every %s login is at its usage limit", HarnessName(h))
	for _, l := range open {
		var env []string
		if env, err = rs.tokens.AgentEnv(l, home); err == nil || bare && l.Name == "" && errors.Is(err, ErrNoLogin) {
			return l, env, nil
		}
	}
	return Login{}, nil, err
}

// freeAt is when a login for harness h next has room: now if one has, else
// the earliest reset.
func (rs *Runs) freeAt(h string) time.Time {
	now := time.Now()
	var at time.Time
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, l := range rs.tokens.Logins(h) {
		p := rs.plans[l.Key()]
		if !p.Parked(now) {
			return now
		}
		if at.IsZero() || p.Limit.ResetsAt.Before(at) {
			at = p.Limit.ResetsAt
		}
	}
	return at.Add(15 * time.Second)
}

// Spent is what runs cost in a month ("2006-01", UTC), from the harnesses' results.
func (rs *Runs) Spent(month string) float64 {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.spent[month]
}

// Takes says why run id can't take a message, or nil if it can.
func (rs *Runs) Takes(id string) error {
	r, ok := rs.Get(id)
	if !ok {
		return ErrNotFound
	}
	if r.Terminal() && !r.Persistent {
		return fmt.Errorf("run %s has ended (%s)", id, r.Status)
	}
	return nil
}

// Send queues a message for a run's next turn.
func (rs *Runs) Send(id, text string) error {
	if err := rs.Takes(id); err != nil {
		return err
	}
	rs.append("run.queued", runNote{ID: id, Text: text})
	return nil
}

// Reopen lets a finished run take more turns (fix rounds go back to the same worker).
func (rs *Runs) Reopen(id, text string) error {
	if _, ok := rs.Get(id); !ok {
		return ErrNotFound
	}
	rs.append("run.reopened", runNote{ID: id})
	rs.append("run.queued", runNote{ID: id, Text: text})
	rs.append("run.status", runNote{ID: id, Status: StatusIdle, Reason: "reopened"})
	return nil
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
func (rs *Runs) Tick(cfg Config) {
	for _, r := range rs.List(func(r *Run) bool { return !r.Terminal() || r.Status == StatusRunning }) {
		rs.mu.Lock()
		if rs.busy[r.ID] {
			rs.mu.Unlock()
			continue
		}
		rs.busy[r.ID] = true
		rs.mu.Unlock()
		go func(r Run) {
			defer func() { rs.mu.Lock(); delete(rs.busy, r.ID); rs.mu.Unlock() }()
			rs.step(r, cfg)
		}(r)
	}
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
	time.Sleep(200 * time.Millisecond) // let the last lines land
	rs.ops.Drain(r)
	rs.endTurn(r)
}

// TurnLog parses a turn's output file.
type TurnLog struct {
	SessionLive bool
	Session     string
	Result      *harness.Result
	Limit       *harness.RateLimit
	Steps       []harness.Event
}

// ReadTurn parses one turn's output.
func (rs *Runs) ReadTurn(r Run, turn int) TurnLog {
	h, _ := harness.Get(r.Harness)
	var tl TurnLog
	_ = ReadJSONL(rs.ws.TurnOut(r.ID, turn), func(line []byte) {
		for _, ev := range h.Parse(line) {
			switch ev.Kind {
			case "init":
				tl.SessionLive, tl.Session = true, ev.SessionID
			case "result":
				if tl.Result != nil && ev.Result != nil { // a second verdict in one turn: trust neither
					ev.Result.Structured = nil
				}
				tl.Result = ev.Result
			case "ratelimit":
				tl.Limit = ev.RateLimit
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
		if r.Schema != "" && tl.Result.Structured == nil && json.Valid([]byte(tl.Result.Text)) {
			tl.Result.Structured = json.RawMessage(tl.Result.Text)
		}
	}
	return tl
}

func (rs *Runs) endTurn(r Run) {
	tl := rs.ReadTurn(r, r.Turn)
	if raw, err := os.ReadFile(rs.ws.TurnOut(r.ID, r.Turn) + ".egress"); err == nil && len(raw) > 0 {
		hosts := strings.Fields(string(raw))
		sort.Strings(hosts)
		rs.append("run.egress_refused", egressRefused{ID: r.ID, Turn: r.Turn, Hosts: slices.Compact(hosts)})
	}
	limited := tl.Result != nil && tl.Result.Limited || (tl.Limit != nil && tl.Limit.Status == "rejected" && (tl.Result == nil || tl.Result.IsError))
	if limited {
		// Park the login until its reset (an hour when the harness didn't say).
		l := harness.RateLimit{}
		if tl.Limit != nil {
			l = *tl.Limit
		}
		l.Status = "rejected"
		if !l.ResetsAt.After(time.Now()) {
			l.ResetsAt = time.Now().Add(time.Hour)
		}
		tl.Limit = &l
	}
	if tl.Limit != nil {
		rs.append("plan.usage", Plan{Harness: HarnessName(r.Harness), Login: r.Login, Limit: *tl.Limit, At: time.Now().UTC()})
	}
	te := turnEnded{ID: r.ID, Turn: r.Turn, SessionLive: tl.SessionLive, Session: tl.Session, Result: tl.Result}
	var dropErr error
	if HarnessName(r.Harness) == "codex" {
		dropErr = rs.dropCodexHome(r.ID)
	}
	cur, _ := rs.Get(r.ID)
	switch {
	case dropErr != nil:
		te.Status, te.Reason = StatusBlocked, "its Codex token could not be deleted: "+dropErr.Error()
	case cur.StopAsked:
		te.Status, te.Reason = StatusStopped, "stopped by the Hive"
	case limited:
		// Another login with room takes the next turn at once; else it waits for the first reset.
		until := rs.freeAt(r.Harness)
		te.Status, te.ParkedUntil = StatusParked, until
		te.Reason = "usage limit; resumes " + until.Local().Format("Mon 15:04")
		te.Queue = []string{"The usage limit has reset. Continue where you left off."}
		if !until.After(time.Now()) {
			te.Reason = "usage limit; moving to another login"
			te.Queue = []string{"Your last turn hit a usage limit, so your next turn runs on another login. Continue where you left off."}
		}
	case tl.Result == nil:
		errTail := tail(rs.ws.TurnOut(r.ID, r.Turn)+".err", 400)
		te.Status, te.ParkedUntil = StatusParked, time.Now().Add(backoff(cur.Retries))
		te.Reason = "interrupted; retrying on its checkout" + suffix(errTail)
		te.Queue = []string{"Your previous turn was interrupted. Your checkout and commits are intact. Continue where you left off."}
	case tl.Result.Subtype == "error_max_budget_usd":
		te.Status, te.Reason = StatusDone, "spent its budget"
	case tl.Result.IsError:
		te.Status, te.ParkedUntil = StatusParked, time.Now().Add(backoff(cur.Retries))
		te.Reason = "harness error; retrying" + suffix(Clip(tl.Result.Text, 200))
		te.Queue = []string{"The previous turn hit an error (" + Clip(tl.Result.Text, 200) + "). Continue where you left off."}
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
		te.Queue = []string{"You ended your turn without `hive agent finish`. If the work is done, commit and finish with an account. If you can't finish, finish with status blocked and say why."}
	default:
		te.Status, te.Reason = StatusQuiet, "keeps ending turns without finishing; flagged for the lead"
	}
	rs.append("run.turn_ended", te)
	// A spawned helper's account goes back to the agent that spawned it.
	if (te.Status == StatusDone || te.Status == StatusBlocked) && r.Parent != "" {
		account := cur.Result
		if cur.Finish != nil {
			account = fmt.Sprintf("status: %s\n%s\ntested: %s\nunsure: %s", cur.Finish.Status, cur.Finish.Account, cur.Finish.Tested, cur.Finish.Unsure)
		}
		if p, ok := rs.Get(r.Parent); ok && (!p.Terminal() || p.Persistent) {
			rs.Send(p.ID, fmt.Sprintf("[%s %s finished]\n%s", r.Role, r.ID, account))
		}
	}
}

func suffix(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return ""
	}
	return ": " + s
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

// launch starts the run's next turn in its sandbox.
func (rs *Runs) launch(r Run, cfg Config) {
	if why := Strained(cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax); why != "" {
		// The hold keeps the run's status, so a parked run keeps its backoff
		// and a nudged one its count; the live figures after " (" change
		// every tick, so only a new kind of hold is journalled.
		why = "waiting: " + why
		if kind, _, _ := strings.Cut(why, " ("); !strings.HasPrefix(r.Reason, kind) {
			rs.append("run.held", runNote{ID: r.ID, Reason: why})
		}
		return
	}
	sb, err := rs.sandbox()
	if err != nil {
		if r.Status != StatusQueued || r.Reason != err.Error() {
			rs.append("run.status", runNote{ID: r.ID, Status: StatusQueued, Reason: err.Error()})
		}
		return
	}
	h, _ := harness.Get(r.Harness)
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
		if pending != "" && r.Turn == 0 {
			prompt += PendingSep + pending
		}
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Continue."
	}
	r.Session = session
	if rs.ModelFor != nil && r.Persistent {
		if m := rs.ModelFor(r); m != "" {
			r.Model = m
		}
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
	login, tok, err := rs.pickLogin(r.Harness, codexHome, !sb.Isolated())
	if err != nil {
		until := rs.freeAt(r.Harness)
		if !until.After(time.Now()) {
			until = time.Now().Add(5 * time.Minute)
		}
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: "login for " + HarnessName(r.Harness) + ": " + err.Error(), Until: until})
		return
	}
	spec, err := rs.sandboxSpec(cfg, sb, h, r, turn, resume, promptFile, tok, codexHome)
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
	rs.append("run.turn_started", turnStarted{ID: r.ID, Session: session, Turn: turn, Proc: p, Delivered: delivered, Resume: resume, Login: login.Key(), Model: r.Model})
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
		{Host: rs.ws.RunBox(r.ID), Inside: "/hive/box", RW: true},
		home,
		{Host: rs.ws.KB(), Inside: "/hive/kb"},
		{Host: rs.ws.Modules(), Inside: "/hive/modules"},
	}
	if !r.Isolated {
		mounts = append(mounts, sandbox.Mount{Host: rs.ws.Projects(), Inside: "/hive/projects"},
			sandbox.Mount{Host: rs.ws.Hive(), Inside: "/hive/state"})
		// Every agent reads the whole Hive but what grants access: the daemon's
		// API token and the login pool's stored logins.
		logins := filepath.Join(rs.ws.Hive(), "logins")
		os.MkdirAll(logins, 0o700) // a login added mid-turn lands under the cover
		mounts = append(mounts, sandbox.Mount{Host: logins, Inside: "/hive/state/logins", Hide: true})
		if _, err := os.Stat(rs.ws.DaemonFile()); err == nil {
			mounts = append(mounts, sandbox.Mount{Host: rs.ws.DaemonFile(), Inside: "/hive/state/daemon.json", Hide: true})
		}
	}
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
	env = append(env, tok...)
	env = append(env, h.Env(t)...)
	tools := rs.tools()
	if h.Program() != "hive" {
		tools = append(tools, h.Program())
	}
	argv := append([]string{h.Program()}, h.Args(t)...)
	if !sb.Isolated() && h.Program() == "hive" && len(tools) > 0 {
		argv[0] = tools[0]
	}
	allow := cfg.Egress.For(r.Role)
	if allow != nil {
		allow = append(allow, harness.APIHosts(h)...)
	}
	return sandbox.Spec{
		ID: r.ID, Argv: argv, Env: env, Cwd: in(mounts[0]), Mounts: mounts, Tools: tools,
		Stdin: promptFile, Stdout: rs.ws.TurnOut(r.ID, turn), StopFile: filepath.Join(rs.ws.RunHost(r.ID), "stop"),
		Network: true, Allow: allow, UID: os.Getuid(), GID: os.Getgid(),
	}, nil
}

// Walled refuses r reaching project when either side is an isolated project:
// an isolated project is reached only from its own runs, and its runs reach
// no other project. Both come from the core's record of r, never from r.
func (rs *Runs) Walled(r Run, project string) error {
	if project == r.Project || rs.projs == nil {
		return nil
	}
	for _, name := range []string{project, r.Project} {
		if p, ok := rs.projs.Get(name); ok && p.Isolated {
			return fmt.Errorf("project %q is out of reach of %s's agents", project, r.Project)
		}
	}
	return nil
}

// hideIsolated covers what isolated projects other than r's own leave in the
// shared Hive, so r reads none of their code, runs, chats, notes, checks or
// scores. An isolated run sees only the knowledge base of what is shared, so
// it gets just the notes covered. The journal holds every project's events in
// one file, so it is covered whole while any project is isolated. The folders
// are made first so what lands in them mid-turn is covered too; a run or chat
// started mid-turn is not, until that agent's next turn.
func (rs *Runs) hideIsolated(r Run) []sandbox.Mount {
	if rs.projs == nil {
		return nil
	}
	iso := map[string]bool{}
	var out []sandbox.Mount
	var rel []string
	for _, p := range rs.projs.List() {
		if !p.Isolated || p.Name == r.Project {
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
		rel = append(rel, "kb/isolated/"+p.Name, "scores/"+p.Name+".jsonl", "chats/project_"+p.Name+".jsonl")
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
	dir := filepath.Join(rs.ws.RunHost(r.ID), "codex")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	os.Rename(filepath.Join(rs.ws.RunHost(r.ID), "codex-sessions"), filepath.Join(dir, "sessions"))
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
	dir := filepath.Join(rs.ws.RunHost(id), "codex")
	os.Rename(filepath.Join(dir, "sessions"), filepath.Join(rs.ws.RunHost(id), "codex-sessions"))
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
