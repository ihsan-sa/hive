// Package risk handles requests past an agent's reach. In this version every
// such request is relayed to the person, who acts on it or declines; nothing
// an agent asks for runs outside the sandbox. The warden and tribunal design
// that would weigh these requests is described in docs/risk.md.
package risk

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// Request is an agent's ask for something past its reach.
type Request struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // request | module
	Origin  string    `json:"origin"`
	Project string    `json:"project,omitempty"`
	Want    string    `json:"want"`
	Why     string    `json:"why"`
	Steps   string    `json:"steps,omitempty"`
	Tier    string    `json:"tier,omitempty"`
	Route   string    `json:"route,omitempty"` // core.Route: person | person-first | warden | tribunal
	URL     string    `json:"url,omitempty"`
	Ask     string    `json:"ask"` // the person's queue item
	Outcome string    `json:"outcome,omitempty"`
	Note    string    `json:"note,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Module relays requests to the person.
type Module struct {
	C   *core.Core
	Mod *modules.Registry

	mu   sync.Mutex
	reqs map[string]*Request
}

// New loads requests and registers ops.
func New(c *core.Core, mod *modules.Registry) *Module {
	m := &Module{C: c, Mod: mod, reqs: map[string]*Request{}}
	apply := func(e core.Event) {
		if e.Kind != "risk.saved" {
			return
		}
		var r Request
		e.Decode(&r)
		m.mu.Lock()
		m.reqs[r.ID] = &r
		m.mu.Unlock()
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	ops := c.Runs.Ops()
	ops.Register(core.Op{Name: "warden", Help: `{"want","why","steps","tier":"T1|T2|T3"} -> T1 you weigh yourself; T2/T3 go to the risk path`, Fn: m.opRequest})
	ops.Register(core.Op{Name: "install_module", Roles: []string{"lead"}, Help: `{"url","name"}`, Fn: m.opInstall})
	c.Person.Handle("request", m.onAnswer)
	c.Person.Handle("module", m.onModule)
	return m
}

func (m *Module) save(r *Request) {
	r.Updated = time.Now().UTC()
	m.C.J.MustAppend("risk.saved", r)
}

// List returns requests, newest first.
func (m *Module) List() []Request {
	m.mu.Lock()
	var out []Request
	for _, r := range m.reqs {
		out = append(out, *r)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (m *Module) get(id string) (Request, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reqs[id]
	if !ok {
		return Request{}, false
	}
	return *r, true
}

func (m *Module) opRequest(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Want, Why, Steps, Tier string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Want) == "" {
		return nil, errors.New("say what you need (want)")
	}
	tier := core.Tier(a.Tier)
	route := core.Route(tier, m.C.WS.LoadConfig().Autonomy, core.CeilingHit(a.Want, a.Why, a.Steps))
	if route == "self" {
		return map[string]string{"tier": tier, "route": route, "note": "T1 is inside the Hive: weigh it yourself and get on with it."}, nil
	}
	req := &Request{ID: core.NewID(), Kind: "request", Origin: r.ID, Project: r.Project, Want: a.Want, Why: a.Why, Steps: a.Steps, Tier: tier, Route: route, Created: time.Now().UTC()}
	ask := m.C.Person.Ask(core.Ask{Kind: "request", Ref: req.ID, Project: r.Project, From: r.ID,
		Title:   core.Clip(a.Want, 120),
		Detail:  fmt.Sprintf("An agent (%s, %s) needs something outside its sandbox (%s, route %s).\n\nWhy: %s\n\nIts proposed steps (not run by the Hive):\n%s", r.ID, r.Role, tier, route, a.Why, a.Steps),
		Options: []string{"done", "declined"}})
	req.Ask = ask.ID
	m.save(req)
	m.C.Runs.Wait(r.ID, "the person, on request "+req.ID)
	return map[string]string{"request": req.ID, "note": "sent to the person; their answer arrives as a message. End your turn to wait, or carry on without it."}, nil
}

func (m *Module) onAnswer(a core.Ask) {
	req, ok := m.get(a.Ref)
	if !ok {
		return
	}
	req.Outcome, req.Note = a.Answer, a.Note
	m.save(&req)
	text := fmt.Sprintf("[the person answered your request %q] %s", core.Clip(req.Want, 80), a.Answer)
	if a.Note != "" {
		text += ": " + a.Note
	}
	m.C.Runs.Send(req.Origin, text)
}

func (m *Module) opInstall(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ URL, Name string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(a.URL, "https://") && !strings.HasPrefix(a.URL, "git@") && !strings.HasPrefix(a.URL, "file://") && !core.Exists(a.URL) {
		return nil, errors.New("give a git URL (https://, git@, file://) or a local repository path")
	}
	if a.Name == "" {
		a.Name = modules.NameFromURL(a.URL)
	}
	if _, ok := m.Mod.Get(a.Name); ok {
		return nil, fmt.Errorf("a module named %s is already installed", a.Name)
	}
	req := &Request{ID: core.NewID(), Kind: "module", Origin: r.ID, URL: a.URL, Want: "install module " + a.Name, Created: time.Now().UTC()}
	ask := m.C.Person.Ask(core.Ask{Kind: "module", Ref: req.ID, From: r.ID,
		Title:   "Install module " + a.Name + "?",
		Detail:  "From " + a.URL + ". It brings outside code onto this machine; agents will read and use it. The Hive clones it into .modules/" + a.Name + ".",
		Options: []string{"install", "decline"}})
	req.Ask = ask.ID
	m.save(req)
	return "sent to the person for approval; you'll hear back", nil
}

func (m *Module) onModule(a core.Ask) {
	req, ok := m.get(a.Ref)
	if !ok {
		return
	}
	name := modules.NameFromURL(req.URL)
	if a.Answer != "install" {
		req.Outcome = "declined"
		m.save(&req)
		m.C.Runs.Send(req.Origin, "[the person declined installing module "+name+"]")
		return
	}
	mod, err := m.Mod.Install(req.URL, name)
	if err != nil {
		req.Outcome, req.Note = "failed", err.Error()
		m.save(&req)
		m.C.Runs.Send(req.Origin, "[module "+name+" did not install] "+err.Error())
		return
	}
	req.Outcome = "installed " + mod.Source.Release
	m.save(&req)
	m.C.Runs.Send(req.Origin, fmt.Sprintf("[module %s installed at %s]", name, mod.Source.Release))
}
