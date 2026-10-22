package core

import (
	"encoding/json"
	"fmt"
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
	Limit   harness.RateLimit `json:"limit"`
	At      time.Time         `json:"at"`
}

// Runs owns every agent run.
type Runs struct {
	ws      Workspace
	j       *Journal
	sandbox func() (sandbox.Sandbox, error)
	tokens  func(h string) ([]string, error)
	tools   func() []string // host programs to expose (hive binary for the sandbox's OS)
	ops     *Ops

	mu    sync.Mutex
	runs  map[string]*Run
	plans map[string]Plan
	busy  map[string]bool // runs whose tick work is in flight
}

// NewRuns builds the run manager and replays its records.
func NewRuns(ws Workspace, j *Journal, sb func() (sandbox.Sandbox, error), tokens func(string) ([]string, error), tools func() []string) *Runs {
	rs := &Runs{ws: ws, j: j, sandbox: sb, tokens: tokens, tools: tools, runs: map[string]*Run{}, plans: map[string]Plan{}, busy: map[string]bool{}}
	rs.ops = NewOps(rs)
	_ = j.Replay(rs.apply)
	j.Subscribe(func(e Event) {
		rs.mu.Lock()
		rs.apply(e)
		rs.mu.Unlock()
	})
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
}

type turnEnded struct {
	ID          string          `json:"id"`
	Turn        int             `json:"turn"`
	SessionLive bool            `json:"session_live"`
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
		rs.plans[p.Harness] = p
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
		r.Turn, r.Proc, r.Status, r.TurnStarted, r.LastOutput = t.Turn, t.Proc, StatusRunning, e.At, e.At
		if t.Session != "" {
			r.Session = t.Session
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
		r.Status, r.Reason, r.ParkedUntil = t.Status, t.Reason, t.ParkedUntil
		r.Proc = proc.Handle{}
		if t.Result != nil {
			r.Cost += t.Result.CostUSD
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
	if _, err := harness.Get(s.Harness); err != nil {
		return Run{}, err
	}
	for _, d := range []string{rs.ws.RunHost(s.ID), rs.ws.RunRO(s.ID), filepath.Join(rs.ws.RunBox(s.ID), "outbox"), filepath.Join(rs.ws.RunBox(s.ID), "inbox"), rs.ws.RunHome(s.ID)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Run{}, err
		}
	}
	files := map[string]string{"system.md": s.System, "brief.md": s.Brief}
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

// Plans returns the latest usage per harness.
func (rs *Runs) Plans() []Plan {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []Plan
	for _, p := range rs.plans {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Harness < out[j].Harness })
	return out
}

// Send queues a message for a run's next turn.
func (rs *Runs) Send(id, text string) error {
	r, ok := rs.Get(id)
	if !ok {
		return ErrNotFound
	}
	if r.Terminal() && !r.Persistent {
		return fmt.Errorf("run %s has ended (%s)", id, r.Status)
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

// Tick advances every run one step: mailboxes, finished turns, due turns.
func (rs *Runs) Tick() {
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
			rs.step(r)
		}(r)
	}
}

func (rs *Runs) step(r Run) {
	switch r.Status {
	case StatusRunning:
		rs.ops.Drain(r)
		rs.watch(r)
	case StatusQueued:
		rs.launch(r)
	case StatusIdle:
		rs.ops.Drain(r)
		if len(r.Pending) > 0 {
			rs.launch(r)
		}
	case StatusParked:
		if time.Now().After(r.ParkedUntil) {
			rs.launch(r)
		}
	case StatusQuiet:
		rs.ops.Drain(r)
		if len(r.Pending) > 0 {
			rs.launch(r)
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
				tl.SessionLive = true
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
	return tl
}

func (rs *Runs) endTurn(r Run) {
	tl := rs.ReadTurn(r, r.Turn)
	if tl.Limit != nil {
		rs.append("plan.usage", Plan{Harness: r.Harness, Limit: *tl.Limit, At: time.Now().UTC()})
	}
	if raw, err := os.ReadFile(rs.ws.TurnOut(r.ID, r.Turn) + ".egress"); err == nil && len(raw) > 0 {
		hosts := strings.Fields(string(raw))
		sort.Strings(hosts)
		rs.append("run.egress_refused", egressRefused{ID: r.ID, Turn: r.Turn, Hosts: slices.Compact(hosts)})
	}
	te := turnEnded{ID: r.ID, Turn: r.Turn, SessionLive: tl.SessionLive, Result: tl.Result}
	cur, _ := rs.Get(r.ID)
	limited := tl.Result != nil && tl.Result.Limited || (tl.Limit != nil && tl.Limit.Status == "rejected" && (tl.Result == nil || tl.Result.IsError))
	switch {
	case cur.StopAsked:
		te.Status, te.Reason = StatusStopped, "stopped by the Hive"
	case limited:
		until := time.Now().Add(time.Hour)
		if tl.Limit != nil && tl.Limit.ResetsAt.After(time.Now()) {
			until = tl.Limit.ResetsAt.Add(15 * time.Second)
		}
		te.Status, te.ParkedUntil = StatusParked, until
		te.Reason = "usage limit; resumes " + until.Local().Format("Mon 15:04")
		te.Queue = []string{"The usage limit has reset. Continue where you left off."}
	case tl.Result == nil:
		errTail := tail(rs.ws.TurnOut(r.ID, r.Turn)+".err", 400)
		te.Status, te.ParkedUntil = StatusParked, time.Now().Add(backoff(cur.Retries))
		te.Reason = "interrupted; retrying on its checkout" + suffix(errTail)
		te.Queue = []string{"Your previous turn was interrupted. Your checkout and commits are intact. Continue where you left off."}
	case tl.Result.IsError:
		te.Status, te.ParkedUntil = StatusParked, time.Now().Add(backoff(cur.Retries))
		te.Reason = "harness error; retrying" + suffix(Clip(tl.Result.Text, 200))
		te.Queue = []string{"The previous turn hit an error (" + Clip(tl.Result.Text, 200) + "). Continue where you left off."}
	case cur.Finish != nil:
		te.Status = StatusDone
		if cur.Finish.Status == "blocked" {
			te.Status, te.Reason = StatusBlocked, Clip(cur.Finish.Account, 300)
		}
	case r.Schema != "" || r.ReadOnly:
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
func (rs *Runs) launch(r Run) {
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
	promptFile := filepath.Join(rs.ws.RunHost(r.ID), fmt.Sprintf("turn-%03d.prompt", turn))
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: err.Error(), Until: time.Now().Add(time.Minute)})
		return
	}
	spec, err := rs.sandboxSpec(sb, h, r, turn, resume, promptFile)
	if err != nil {
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: err.Error(), Until: time.Now().Add(5 * time.Minute)})
		return
	}
	p, err := sb.Start(spec)
	if err != nil {
		rs.append("run.status", runNote{ID: r.ID, Status: StatusParked, Reason: "start failed: " + err.Error(), Until: time.Now().Add(backoff(r.Retries))})
		return
	}
	rs.append("run.turn_started", turnStarted{ID: r.ID, Session: session, Turn: turn, Proc: p, Delivered: delivered, Resume: resume})
}

// sandboxSpec lays out what the agent sees.
func (rs *Runs) sandboxSpec(sb sandbox.Sandbox, h harness.Harness, r Run, turn int, resume bool, promptFile string) (sandbox.Spec, error) {
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
		mounts = append(mounts, sandbox.Mount{Host: rs.ws.Projects(), Inside: "/hive/projects"})
	}
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
	t := harness.Turn{
		Role: r.Role, SessionID: r.Session, Resume: resume, Model: r.Model, Tools: r.Tools, SystemText: r.System,
		SystemFile: filepath.Join(in(mounts[1]), "system.md"), ReadOnly: r.ReadOnly, RunDir: in(mounts[1]), Schema: r.Schema, Sandboxed: sb.Isolated(),
	}
	if sb.Isolated() {
		t.SystemFile = "/hive/run/system.md"
	}
	env := []string{
		"HIVE_ID=" + r.ID, "HIVE_ROLE=" + r.Role, "HIVE_PROJECT=" + r.Project, "HIVE_TASK=" + r.Task,
		"HIVE_BOX=" + in(mounts[2]), "HIVE_RUN=" + in(mounts[1]), "HIVE_KB=" + in(mounts[4]), "HIVE_MODULES=" + in(mounts[5]),
		"GIT_AUTHOR_NAME=Hive " + r.Role, "GIT_AUTHOR_EMAIL=hive@localhost", "GIT_COMMITTER_NAME=Hive " + r.Role, "GIT_COMMITTER_EMAIL=hive@localhost",
	}
	if !r.Isolated {
		env = append(env, "HIVE_PROJECTS="+in(mounts[6]))
	}
	if sb.Isolated() {
		env = append(sandbox.InsideEnv("/hive/home", true), env...)
		tok, err := rs.tokens(r.Harness)
		if err != nil {
			return sandbox.Spec{}, fmt.Errorf("login for %s: %w", r.Harness, err)
		}
		env = append(env, tok...)
	} else {
		env = append(env, "HIVE_WORKSPACE="+rs.ws.Root)
	}
	env = append(env, h.Env(t)...)
	tools := rs.tools()
	if h.Program() != "hive" {
		tools = append(tools, h.Program())
	}
	argv := append([]string{h.Program()}, h.Args(t)...)
	if !sb.Isolated() && h.Program() == "hive" && len(tools) > 0 {
		argv[0] = tools[0]
	}
	allow := rs.ws.LoadConfig().Egress.For(r.Role)
	if len(allow) > 0 {
		allow = append(append([]string{}, allow...), harness.APIHosts(h)...)
	}
	return sandbox.Spec{
		ID: r.ID, Argv: argv, Env: env, Cwd: in(mounts[0]), Mounts: mounts, Tools: tools,
		Stdin: promptFile, Stdout: rs.ws.TurnOut(r.ID, turn), StopFile: filepath.Join(rs.ws.RunHost(r.ID), "stop"),
		Network: true, Allow: allow, UID: os.Getuid(), GID: os.Getgid(),
	}, nil
}

// Clip shortens s to n runes on one line.
func Clip(s string, n int) string { return harness.Clip(s, n) }
