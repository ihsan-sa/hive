// Package models is the routing of agents to models: the route op,
// which sets the models a role runs on for the person (a check seat only
// once the person says yes), the models op that shows how the Hive is routed
// and what each agent runs on, and trials, which run one brief's worker on a
// model the person wants to try, on a project of their choosing, and record
// how it went.
package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
	"github.com/ihsan-sa/hive-standalone-build/internal/openagent"
)

// Route is one change to the routing: role's models in project ("" for the
// workspace), in fallback order; no models clears it.
type Route struct {
	Role    string   `json:"role"`
	Project string   `json:"project,omitempty"`
	Models  list     `json:"models"`
	By      string   `json:"by,omitempty"`
	From    []string `json:"from,omitempty"` // what it was
}

// Trial is how a model did on one trial brief.
type Trial struct {
	Task    string `json:"task"`
	Model   string `json:"model"`
	Project string `json:"project"`
	Title   string `json:"title"`
	Outcome string `json:"outcome"` // the task's last stage: merged, done, blocked, stopped
	Reason  string `json:"reason,omitempty"`
	// NoFinish: its worker never called finish and was ended for it, so
	// routing sends the model no work until a later trial of it finishes.
	NoFinish bool `json:"doesnt_finish,omitempty"`
	// Hive is why the trial ended on the Hive's own trouble, not the
	// model's (the pipeline gave up on its own steps' errors), so it says
	// nothing of how the model does: routing and the models op don't count
	// it, and it is recorded again if the task comes back and ends.
	Hive    string  `json:"hives_trouble,omitempty"`
	Turns   int     `json:"turns"`
	Rounds  int     `json:"review_rounds"`
	Minutes int     `json:"minutes"`
	Cost    float64 `json:"cost"`
}

// Module is the models module.
type Module struct {
	C    *core.Core
	Work *work.Module

	// Probe asks a named model one real request before a route or trial
	// puts work on it (Probe, by default).
	Probe func(ws core.Workspace, name string, m core.OpenModel) error
	// Paid holds paid OpenRouter models to the month's cap (paid.go).
	Paid *Paid

	mu     sync.Mutex
	trials map[string]Trial // recorded, by task
	order  map[string]int   // each recorded trial's place in the journal, by task
	seq    int              // the last place given
	// demoted is each project's worker picks whose runs keep ending without
	// a merge, by demoteKey, with why: Tick works it out from the tasks.
	demoted map[string]string
	// ledgers is how each project's ledger was last left (ledger.go).
	ledgers map[string]ledgerSeen
}

// demoteAfter is the ended tasks in a row, each blocked, a pick's workers
// start on a project before it stops being that project's first pick; and
// demoteFor how long the latest of them keeps it off the front, so the
// person's order gets another try.
const (
	demoteAfter = 2
	demoteFor   = 7 * 24 * time.Hour
)

func demoteKey(project string, p core.Pick) string { return project + "\x00" + p.String() }

// ErrNoKey is a Probe of a model that needs a key nobody has stored yet.
var ErrNoKey = errors.New("it needs a key, and none is stored yet")

// Probe asks named model m one real request, with its stored key, and says
// why it can't answer; nil when it answers.
func Probe(ws core.Workspace, name string, m core.OpenModel) error {
	key := ""
	if raw, err := os.ReadFile(ws.OpenKey(name)); err == nil {
		key = strings.TrimSpace(string(raw))
	} else if m.Key {
		return fmt.Errorf("%w: `%s model key %s`", ErrNoKey, core.Cmd, name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var price [2]float64
	if m.Price != nil {
		price = [2]float64{m.Price.Input, m.Price.Output}
	}
	return openagent.Probe(ctx, m.Endpoint, m.Model, key, price, m.Anthropic())
}

// probe refuses a pick that can't answer, saying why: a named model gets one
// real request (a stored Down is no reason to refuse, as the person may have
// just fixed it); any other pick is refused only while it is down.
// startable refuses a trial on a pick whose harness can't start on this
// machine: its program isn't installed, or (in a sandbox) no key is stored
// for it. A worker started on it would only end blocked. A route isn't held
// to it: its picks may run on a runner that has the program.
func (m *Module) startable(p core.Pick) error {
	h, err := harness.Get(p.Harness)
	if err != nil {
		return err
	}
	if prog := h.Program(); prog != "hive" && runtime.GOOS != "windows" && !installed(prog) {
		return fmt.Errorf("%s can't start here: its program %q isn't installed on this machine", p, prog)
	}
	s, ok := h.(*harness.Spec)
	if !ok || s.KeyEnv == "" || s.KeyOptional {
		return nil
	}
	if sb, err := m.C.Sandbox(); err == nil && !sb.Isolated() {
		return nil // a bare run uses the harness's own login
	}
	if raw, err := os.ReadFile(m.C.WS.HarnessKey(s.Name())); err != nil || strings.TrimSpace(string(raw)) == "" {
		return fmt.Errorf("%s can't start here: no API key is stored for the %s harness (the person stores one with `%s harness key %s`)", p, s.Name(), core.Cmd, s.Name())
	}
	return nil
}

// installed reports whether prog is on PATH or where the sandbox also looks.
func installed(prog string) bool {
	if _, err := exec.LookPath(prog); err == nil {
		return true
	}
	for _, d := range []string{filepath.Join(os.Getenv("HOME"), ".local", "bin"), "/usr/local/bin"} {
		if _, err := os.Stat(filepath.Join(d, prog)); err == nil {
			return true
		}
	}
	return false
}

func (m *Module) probe(cfg core.Config, p core.Pick) error {
	if p.Harness != "open" {
		if d, ok := downOf(m.C.Runs.Down(), p); ok {
			return fmt.Errorf("%s; %s", d.Line(), d.Fix(cfg))
		}
		return nil
	}
	err := m.Probe(m.C.WS, p.Model, cfg.Models[p.Model])
	if err == nil {
		m.C.Runs.Up(p, "it answered a probe")
		return nil
	}
	d := core.Down{Pick: p, Status: openagent.Status(err), Text: err.Error()}
	return fmt.Errorf("%s can't answer, so nothing was routed to it: %v. %s", p, err, d.Fix(cfg))
}

func downOf(downs []core.Down, p core.Pick) (core.Down, bool) {
	for _, d := range downs {
		if d.Pick.Same(p) {
			return d, true
		}
	}
	return core.Down{}, false
}

// New registers the ops and the person's answer to a check seat's route.
func New(c *core.Core, w *work.Module) *Module {
	m := &Module{C: c, Work: w, trials: map[string]Trial{}, order: map[string]int{}, demoted: map[string]string{}, ledgers: map[string]ledgerSeen{}, Probe: Probe}
	apply := func(e core.Event) {
		if e.Kind != "model.trial" {
			return
		}
		var t Trial
		if e.Decode(&t) == nil {
			if t.Hive == "" { // recorded before the Hive's trouble was told apart
				t.Hive = work.HiveTrouble(work.Task{Stage: t.Outcome, Reason: t.Reason})
			}
			m.mu.Lock()
			m.trials[t.Task] = t
			m.seq++
			m.order[t.Task] = m.seq
			m.mu.Unlock()
		}
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	m.Paid = NewPaid(c.J, c.WS.LoadConfig)
	m.tickDemoted() // so a restart routes as before it, not only from the first tick
	ops := c.Runs.Ops()
	ops.Register(core.Op{Name: "route", Help: `{"role": "lead|worker|reviewer|security|warden|tribunal|sweep|fallback", "models": "a,b,c" (in fallback order; empty clears it), "project": "(optional) only this project"} -> sets the models that role runs on in config.json; a check seat (reviewer, security, warden, tribunal) changes only once the person says yes`, Fn: m.opRoute})
	ops.Register(core.Op{Name: "models", Help: `{} -> the named models, the routes, which model each live agent runs on, and the trials and how they went`, Fn: func(r core.Run, _ json.RawMessage) (any, error) { return m.View(r), nil }})
	ops.Register(core.Op{Name: "trial", Help: `{"model": "a named model or harness:model", "project": "a test project", "title": "...", "done": "..."} -> a brief whose worker runs on that model alone: at its usage limit it waits for the reset, never on another model; the checks stay on their own models, and how it went is recorded (models op). A brief (the brief op) is no trial: its worker runs on the project's route and moves along it at a limit. An agent with a project files trials there only, not from a trial of its own, and not one already open on that model with that title`, Fn: m.opTrial})
	c.Person.Handle("route", m.onPerson)
	return m
}

type list []string

func (l *list) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*l = strings.FieldsFunc(one, func(r rune) bool { return r == ',' || r == ' ' })
		return nil
	}
	return json.Unmarshal(b, (*[]string)(l))
}

func (m *Module) opRoute(r core.Run, args json.RawMessage) (any, error) {
	var rt Route
	if err := core.Bind(args, &rt); err != nil {
		return nil, err
	}
	if r.Project != "" && rt.Project != r.Project {
		if rt.Project != "" {
			return nil, fmt.Errorf("an agent with a project routes only that project, %s", r.Project)
		}
		rt.Project = r.Project
	}
	if rt.Project != "" {
		if _, ok := m.C.Projects.Get(rt.Project); !ok {
			return nil, fmt.Errorf("no project %q", rt.Project)
		}
		if err := m.C.Runs.Walled(r, rt.Project); err != nil {
			return nil, err
		}
	}
	rt.By = r.ID
	was := m.C.WS.LoadConfig().Routed(rt.Role, rt.Project)
	cfg, err := m.routed(rt)
	if err != nil {
		return nil, err
	}
	for _, e := range rt.Models {
		p, _ := cfg.Pick(e)
		if _, down := downOf(m.C.Runs.Down(), p); slices.Contains(was, e) && !down {
			continue // already on the route and not known to fail: its turns are its probe
		}
		if err := m.probe(cfg, p); err != nil {
			return nil, err
		}
	}
	if slices.Contains(core.CheckRoles, rt.Role) {
		raw, _ := json.Marshal(rt)
		where := "every project"
		if rt.Project != "" {
			where = "project " + rt.Project
		}
		to := strings.Join(rt.Models, ", ")
		if to == "" {
			to = "its model setting (no route)"
		}
		a := m.C.Person.Ask(core.Ask{Kind: "route", Title: fmt.Sprintf("Run the %s seat on %s?", rt.Role, to),
			Detail: fmt.Sprintf("The lead asks to route the %s seat in %s to %s, in that order at a usage limit. The %s is one of the Hive's checks, which a weaker model reads less well.", rt.Role, where, to, rt.Role),
			Ref:    string(raw), From: r.ID, Project: rt.Project})
		return map[string]string{"ask": a.ID, "note": "a check seat's route waits for the person's yes in Waiting; tell them so"}, nil
	}
	if cfg, err = m.apply(rt); err != nil {
		return nil, err
	}
	return map[string]any{"routed": rt.Role, "route": pickNames(cfg.Route(rt.Role, rt.Project))}, nil
}

func (m *Module) onPerson(a core.Ask) {
	if a.Answer != "yes" {
		return
	}
	var rt Route
	if json.Unmarshal([]byte(a.Ref), &rt) != nil {
		return
	}
	rt.By = "person (asked by " + rt.By + ")"
	if _, err := m.apply(rt); err != nil && a.From != "" {
		m.C.Runs.Send(a.From, "The person said yes to routing the "+rt.Role+" seat, but it couldn't be set: "+err.Error())
	}
}

// routed is the config with rt applied, refused if the Hive can't run it.
func (m *Module) routed(rt Route) (core.Config, error) {
	if !slices.Contains(core.RouteRoles, rt.Role) {
		return core.Config{}, fmt.Errorf("role %q: route one of %s", rt.Role, strings.Join(core.RouteRoles, ", "))
	}
	cfg, err := m.C.WS.ReadConfig()
	if err != nil {
		return cfg, fmt.Errorf("config.json has to be fixed first: %v", err)
	}
	for _, e := range rt.Models {
		p, err := cfg.Pick(e)
		if err != nil {
			names := slices.Sorted(maps.Keys(cfg.Models))
			return cfg, fmt.Errorf("%v (named models: %s; or harness:model, such as claude:opus)", err, strings.Join(names, ", "))
		}
		if p.Harness == "claude" || p.Harness == "codex" {
			if offered := harness.Models(p.Harness); p.Model != "" && !slices.Contains(offered, p.Model) {
				return cfg, fmt.Errorf("%q: the %s harness offers %s, not %q", e, p.Harness, strings.Join(offered, ", "), p.Model)
			}
		}
	}
	set := func(routes map[string][]string) map[string][]string {
		routes = maps.Clone(routes)
		if routes == nil {
			routes = map[string][]string{}
		}
		if len(rt.Models) == 0 {
			delete(routes, rt.Role)
		} else {
			routes[rt.Role] = slices.Clone([]string(rt.Models))
		}
		return routes
	}
	if rt.Project == "" {
		cfg.Routes = set(cfg.Routes)
	} else {
		cfg.Projects = maps.Clone(cfg.Projects)
		if cfg.Projects == nil {
			cfg.Projects = map[string]core.ProjectSettings{}
		}
		ps := cfg.Projects[rt.Project]
		ps.Routes = set(ps.Routes)
		cfg.Projects[rt.Project] = ps
	}
	return cfg, cfg.Check()
}

// apply saves rt into config.json and journals it.
func (m *Module) apply(rt Route) (core.Config, error) {
	before, _ := m.C.WS.ReadConfig()
	cfg, err := m.routed(rt)
	if err != nil {
		return cfg, err
	}
	rt.From = before.Routes[rt.Role]
	if rt.Project != "" {
		rt.From = before.Projects[rt.Project].Routes[rt.Role]
	}
	if err := m.C.WS.SaveConfig(cfg); err != nil {
		return cfg, err
	}
	m.C.J.MustAppend("config.routed", rt)
	return cfg, nil
}

// Record journals each route that differs between before and after as
// config.routed by who, the way the route op does: a change made in
// Settings is on the record with what it was.
func Record(j *core.Journal, before, after core.Config, by string) {
	where := map[string]bool{"": true}
	for p := range before.Projects {
		where[p] = true
	}
	for p := range after.Projects {
		where[p] = true
	}
	routes := func(c core.Config, p string) map[string][]string {
		if p == "" {
			return c.Routes
		}
		return c.Projects[p].Routes
	}
	for _, p := range slices.Sorted(maps.Keys(where)) {
		was, now := routes(before, p), routes(after, p)
		roles := map[string]bool{}
		for r := range was {
			roles[r] = true
		}
		for r := range now {
			roles[r] = true
		}
		for _, role := range slices.Sorted(maps.Keys(roles)) {
			if !slices.Equal(was[role], now[role]) {
				j.MustAppend("config.routed", Route{Role: role, Project: p, Models: now[role], By: by, From: was[role]})
			}
		}
	}
}

func pickNames(ps []core.Pick) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

func (m *Module) opTrial(r core.Run, args json.RawMessage) (any, error) {
	var b struct {
		Model, Project, Title, Done string
	}
	if err := core.Bind(args, &b); err != nil {
		return nil, err
	}
	if b.Model == "" || b.Project == "" || strings.TrimSpace(b.Title) == "" {
		return nil, errors.New("a trial names the model, the project to try it on (a test project, not real work) and a title")
	}
	if r.Project != "" && b.Project != r.Project {
		return nil, fmt.Errorf("an agent with a project tries models only on that project, %s", r.Project)
	}
	if err := m.C.Runs.Walled(r, b.Project); err != nil {
		return nil, err
	}
	// Any agent files trials (a project made to try models on may have its
	// rounds run by a worker); a trial's own worker files none, so trials
	// don't breed trials, and one already open isn't filed twice.
	if own, ok := m.Work.Get(r.Task); ok && r.Task != "" && own.Model != "" {
		return nil, errors.New("a trial's own worker files no trials: finish this one, and say in the account what to try next")
	}
	title := strings.TrimSpace(b.Title)
	if open := m.Work.List(func(t work.Task) bool {
		return t.Model == b.Model && t.Project == b.Project && t.Title == title && !t.Terminal() && t.Stage != work.StageMerged
	}); len(open) > 0 {
		return nil, fmt.Errorf("task %s is already a trial of %s titled %q; it is recorded in the models op once it ends", open[0].ID, b.Model, title)
	}
	cfg := m.C.WS.LoadConfig()
	pick, err := cfg.Pick(b.Model)
	if err != nil {
		return nil, err
	}
	if err := m.startable(pick); err != nil {
		return nil, err
	}
	if err := m.probe(cfg, pick); err != nil {
		return nil, err
	}
	chat, thread := m.Work.Chat.CurrentThread(r)
	t, err := m.Work.File(work.Brief{Project: b.Project, Title: b.Title, Done: b.Done, From: r.ID, Chat: chat, Thread: thread, Model: b.Model,
		Known: "This is a trial of the model " + pick.String() + ": the worker runs on it alone."})
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "note": "filed as a trial of " + pick.String() + "; how it went shows in the models op once it ends"}, nil
}

// Tick tells the person once of each pick that can't answer, and records
// each trial that has ended: again when one that ended on the Hive's own
// trouble came back and ended otherwise.
func (m *Module) Tick() {
	m.tellDown()
	for _, t := range m.Work.List(func(t work.Task) bool { return t.Model != "" }) {
		switch t.Stage {
		case work.StageMerged, work.StageDone, work.StageBlocked, work.StageStopped:
		default:
			continue
		}
		m.mu.Lock()
		was, done := m.trials[t.ID]
		m.mu.Unlock()
		if done && (was.Hive == "" || was.Outcome == t.Stage && was.Reason == t.Reason) {
			continue
		}
		tr := Trial{Task: t.ID, Model: t.Model, Project: t.Project, Title: t.Title, Outcome: t.Stage, Reason: t.Reason, Rounds: t.Reviews,
			Minutes: int(t.Updated.Sub(t.Created).Minutes())}
		finished := false // a worker that called finish wrote its own reason, whatever its words
		for _, r := range m.C.Runs.List(func(r *core.Run) bool { return r.Task == t.ID && r.Role == "worker" }) {
			tr.Turns += r.Turn
			tr.Cost += r.Cost
			finished = finished || r.Finish != nil
		}
		tr.NoFinish = t.Stage == work.StageBlocked && strings.HasPrefix(t.Reason, core.NoFinish) && !finished
		tr.Hive = work.HiveTrouble(t)
		m.C.J.MustAppend("model.trial", tr)
	}
	m.tickDemoted()
	m.tickLedgers()
}

// tickDemoted works out the picks whose workers keep ending blocked on a
// project: of the ended tasks (not trials) whose first worker started on a
// pick, the latest demoteAfter or more ended blocked with no merge or done
// after them, the latest within demoteFor. A task the person or the Hive
// stopped doesn't count either way.
func (m *Module) tickDemoted() {
	first := map[string]core.Run{} // each task's first worker
	for _, r := range m.C.Runs.List(func(r *core.Run) bool { return r.Role == "worker" && r.Task != "" }) {
		if f, ok := first[r.Task]; !ok || r.Created.Before(f.Created) {
			first[r.Task] = r
		}
	}
	tasks := m.Work.List(func(t work.Task) bool {
		return t.Model == "" && t.Project != "" && first[t.ID].ID != "" &&
			(t.Stage == work.StageMerged || t.Stage == work.StageDone || t.Stage == work.StageBlocked)
	})
	slices.SortFunc(tasks, func(a, b work.Task) int { return a.Updated.Compare(b.Updated) })
	type run struct {
		n    int
		last time.Time
		ids  []string
	}
	streak := map[string]*run{}
	for _, t := range tasks {
		key := demoteKey(t.Project, startPick(first[t.ID]))
		if t.Stage != work.StageBlocked {
			delete(streak, key)
			continue
		}
		s := streak[key]
		if s == nil {
			s = &run{}
			streak[key] = s
		}
		s.n, s.last = s.n+1, t.Updated
		s.ids = append(s.ids, t.ID)
	}
	demoted := map[string]string{}
	for key, s := range streak {
		if s.n >= demoteAfter && time.Since(s.last) < demoteFor {
			demoted[key] = fmt.Sprintf("its last %d worker tasks here ended blocked without a merge (%s)", s.n, strings.Join(s.ids[max(0, len(s.ids)-3):], ", "))
		}
	}
	m.mu.Lock()
	m.demoted = demoted
	m.mu.Unlock()
}

// startPick is the pick a worker's first turn went on: its route's first,
// which Agents.Spec starts it on (a turn's move to another model on the same
// harness rewrites its Model and records no Move), or its own with no route.
func startPick(r core.Run) core.Pick {
	if len(r.Route) > 0 {
		return r.Route[0]
	}
	return core.Pick{Harness: core.HarnessName(r.Harness), Model: r.Model}
}

// Demoted says why p is no longer the first worker pick on project ("" when
// it is not): its workers there keep ending without a merge.
func (m *Module) Demoted(project string, p core.Pick) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.demoted[demoteKey(project, p)]
}

// Benched says whether routing skips p: the latest recorded trial of that
// model didn't finish. One that ended on the Hive's own trouble doesn't count.
func (m *Module) Benched(p core.Pick) bool {
	cfg := m.C.WS.LoadConfig()
	m.mu.Lock()
	defer m.mu.Unlock()
	var last Trial
	at := 0
	for _, t := range m.trials {
		if tp, err := cfg.Pick(t.Model); err == nil && tp.Same(p) && t.Hive == "" && m.order[t.Task] > at {
			last, at = t, m.order[t.Task]
		}
	}
	return last.NoFinish
}

// View is what the models op shows.
func (m *Module) View(r core.Run) map[string]any {
	cfg := m.C.WS.LoadConfig()
	named := named(cfg)
	routes := map[string][]string{}
	for _, role := range core.RouteRoles {
		routes[role] = cfg.Shown(role, r.Project)
	}
	var agents []string
	for _, x := range m.C.Runs.List(func(x *core.Run) bool { return !x.Terminal() }) {
		if m.C.Runs.Walled(r, x.Project) == nil {
			agents = append(agents, fmt.Sprintf("%s (%s): %s:%s, %s", x.ID, x.Role, core.HarnessName(x.Harness), x.Model, x.Status))
		}
	}
	m.mu.Lock()
	var trials, uncounted []Trial
	for _, t := range m.trials {
		switch {
		case m.C.Runs.Walled(r, t.Project) != nil:
		case t.Hive != "":
			uncounted = append(uncounted, t)
		default:
			trials = append(trials, t)
		}
	}
	m.mu.Unlock()
	byTask := func(a, b Trial) int { return strings.Compare(a.Task, b.Task) }
	slices.SortFunc(trials, byTask)
	slices.SortFunc(uncounted, byTask)
	var running []string
	for _, t := range m.Work.List(func(t work.Task) bool { return t.Model != "" && m.C.Runs.Walled(r, t.Project) == nil }) {
		m.mu.Lock()
		_, ok := m.trials[t.ID]
		m.mu.Unlock()
		if !ok {
			running = append(running, fmt.Sprintf("%s on %s: %s (%s)", t.ID, t.Model, t.Stage, t.Title))
		}
	}
	var down []string
	for _, d := range m.C.Runs.Down() {
		down = append(down, downLine(d, cfg))
	}
	var demoted []string
	m.mu.Lock()
	for key, why := range m.demoted {
		project, pick, _ := strings.Cut(key, "\x00")
		if m.C.Runs.Walled(r, project) == nil {
			demoted = append(demoted, fmt.Sprintf("%s on %s goes after the rest of its worker route: %s", pick, project, why))
		}
	}
	m.mu.Unlock()
	slices.Sort(demoted)
	return map[string]any{"models": named, "routes": routes, "agents": agents, "trials": trials, "trials_not_counted": uncounted, "trials_running": running, "unavailable": down, "demoted": demoted}
}

// named is each named model and where it is, as the models op shows them.
func named(cfg core.Config) map[string]string {
	out := map[string]string{}
	for name, x := range cfg.Models {
		s := x.Where()
		if x.Key {
			s += ", needs a key"
		}
		out[name] = s
	}
	return out
}

// downLine is a Down with when it is tried again and the fix.
func downLine(d core.Down, cfg core.Config) string {
	again := "tried again by the next turn routed to it"
	if d.Retry.After(time.Now()) {
		again = "tried again " + d.Retry.Local().Format("Mon 15:04") + " or when its config changes"
	}
	return fmt.Sprintf("%s (since %s; %s). %s", d.Line(), d.Since.Local().Format("Mon 15:04"), again, d.Fix(cfg))
}

// tellDown tells the person, and the lead's chat, once per pick and cause
// that can't answer: its answer as it gave it, and the fix.
func (m *Module) tellDown() {
	cfg := m.C.WS.LoadConfig()
	for _, d := range m.C.Runs.Down() {
		id := d.ToldID()
		if _, told := m.C.Person.Get(id); told {
			continue
		}
		title := d.Pick.String() + " can't answer, so runs skip it"
		detail := fmt.Sprintf("Its answer: %s\n\nFix: %s\n\nRuns routed to it move to the next model on their route with room, or wait for the first reset. It is tried again when its config changes or after an hour, and comes back on its own once it answers.", d.Text, d.Fix(cfg))
		m.C.Person.Tell(core.Ask{ID: id, Ref: id, Title: title, Detail: detail, From: d.Run})
		if m.Work.Chat != nil {
			m.Work.Chat.Notify("lead", "", fmt.Sprintf("%s: %s Fix: %s", title, core.Clip(d.Text, 600), d.Fix(cfg)))
		}
	}
}
