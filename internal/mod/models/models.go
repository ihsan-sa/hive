// Package models is the routing of agents to models: the lead's route op,
// which sets the models a role runs on for the person (a check seat only
// once the person says yes), the models op that shows how the Hive is routed
// and what each agent runs on, and trials, which run one brief's worker on a
// model the person wants to try, on a project of their choosing, and record
// how it went.
package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
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
	Task    string  `json:"task"`
	Model   string  `json:"model"`
	Project string  `json:"project"`
	Title   string  `json:"title"`
	Outcome string  `json:"outcome"` // the task's last stage: merged, done, blocked, stopped
	Reason  string  `json:"reason,omitempty"`
	Turns   int     `json:"turns"`
	Rounds  int     `json:"review_rounds"`
	Minutes int     `json:"minutes"`
	Cost    float64 `json:"cost"`
}

// Module is the models module.
type Module struct {
	C    *core.Core
	Work *work.Module

	mu     sync.Mutex
	trials map[string]Trial // recorded, by task
}

// New registers the ops and the person's answer to a check seat's route.
func New(c *core.Core, w *work.Module) *Module {
	m := &Module{C: c, Work: w, trials: map[string]Trial{}}
	apply := func(e core.Event) {
		if e.Kind != "model.trial" {
			return
		}
		var t Trial
		if e.Decode(&t) == nil {
			m.mu.Lock()
			m.trials[t.Task] = t
			m.mu.Unlock()
		}
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	ops := c.Runs.Ops()
	ops.Register(core.Op{Name: "route", Roles: []string{"lead"}, Help: `{"role": "lead|worker|reviewer|security|warden|tribunal|sweep|fallback", "models": "a,b,c" (in fallback order; empty clears it), "project": "(optional) only this project"} -> sets the models that role runs on in config.json; a check seat (reviewer, security, warden, tribunal) changes only once the person says yes`, Fn: m.opRoute})
	ops.Register(core.Op{Name: "models", Help: `{} -> the named models, the routes, which model each live agent runs on, and the trials and how they went`, Fn: func(r core.Run, _ json.RawMessage) (any, error) { return m.View(r), nil }})
	ops.Register(core.Op{Name: "trial", Roles: []string{"lead"}, Help: `{"model": "a named model or harness:model", "project": "a test project", "title": "...", "done": "..."} -> a brief whose worker runs on that model alone; the checks stay on their own models, and how it went is recorded (models op)`, Fn: m.opTrial})
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
			return nil, fmt.Errorf("a project's lead routes only its own project, %s", r.Project)
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
	if _, err := m.routed(rt); err != nil {
		return nil, err
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
	cfg, err := m.apply(rt)
	if err != nil {
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
		if _, err := cfg.Pick(e); err != nil {
			names := slices.Sorted(maps.Keys(cfg.Models))
			return cfg, fmt.Errorf("%v (named models: %s; or harness:model, such as claude:opus)", err, strings.Join(names, ", "))
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
		return nil, fmt.Errorf("a project's lead tries models only on its own project, %s", r.Project)
	}
	if err := m.C.Runs.Walled(r, b.Project); err != nil {
		return nil, err
	}
	pick, err := m.C.WS.LoadConfig().Pick(b.Model)
	if err != nil {
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

// Tick records each trial that has ended.
func (m *Module) Tick() {
	for _, t := range m.Work.List(func(t work.Task) bool { return t.Model != "" }) {
		switch t.Stage {
		case work.StageMerged, work.StageDone, work.StageBlocked, work.StageStopped:
		default:
			continue
		}
		m.mu.Lock()
		_, done := m.trials[t.ID]
		m.mu.Unlock()
		if done {
			continue
		}
		tr := Trial{Task: t.ID, Model: t.Model, Project: t.Project, Title: t.Title, Outcome: t.Stage, Reason: t.Reason, Rounds: t.Reviews,
			Minutes: int(t.Updated.Sub(t.Created).Minutes())}
		for _, r := range m.C.Runs.List(func(r *core.Run) bool { return r.Task == t.ID && r.Role == "worker" }) {
			tr.Turns += r.Turn
			tr.Cost += r.Cost
		}
		m.C.J.MustAppend("model.trial", tr)
	}
}

// View is what the models op shows.
func (m *Module) View(r core.Run) map[string]any {
	cfg := m.C.WS.LoadConfig()
	named := map[string]string{}
	for name, x := range cfg.Models {
		s := x.Where()
		if x.Key {
			s += ", needs a key"
		}
		named[name] = s
	}
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
	var trials []Trial
	for _, t := range m.trials {
		if m.C.Runs.Walled(r, t.Project) == nil {
			trials = append(trials, t)
		}
	}
	m.mu.Unlock()
	slices.SortFunc(trials, func(a, b Trial) int { return strings.Compare(a.Task, b.Task) })
	var running []string
	for _, t := range m.Work.List(func(t work.Task) bool { return t.Model != "" && m.C.Runs.Walled(r, t.Project) == nil }) {
		m.mu.Lock()
		_, ok := m.trials[t.ID]
		m.mu.Unlock()
		if !ok {
			running = append(running, fmt.Sprintf("%s on %s: %s (%s)", t.ID, t.Model, t.Stage, t.Title))
		}
	}
	return map[string]any{"models": named, "routes": routes, "agents": agents, "trials": trials, "trials_running": running}
}
