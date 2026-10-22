// Package projects creates projects (a repository the Hive works on, with its
// own goals, chat and score) and answers agents' status questions.
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/demo"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Module is the projects module.
type Module struct {
	C       *core.Core
	Chat    *chat.Module
	Work    *work.Module
	Improve *improve.Module

	mu      sync.Mutex // one project setup at a time
	retry   map[string]time.Time
	lastErr map[string]string
}

// New registers ops.
func New(c *core.Core, ch *chat.Module, w *work.Module, im *improve.Module) *Module {
	m := &Module{C: c, Chat: ch, Work: w, Improve: im, retry: map[string]time.Time{}, lastErr: map[string]string{}}
	ops := c.Runs.Ops()
	ops.Register(core.Op{Name: "create_project", Roles: []string{"lead"}, Help: `{"name","repo","description","goals","isolated"}`, Fn: m.opCreate})
	ops.Register(core.Op{Name: "status", Help: `{} -> projects, work in flight, scores, what waits on the person`, Fn: func(core.Run, json.RawMessage) (any, error) { return m.Status(), nil }})
	return m
}

// Spec describes a project to create.
type Spec struct {
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Description string `json:"description"`
	Goals       string `json:"goals"`
	Isolated    bool   `json:"isolated"`
	Demo        bool   `json:"demo"`
	Self        bool   `json:"self"`
}

func (m *Module) opCreate(r core.Run, args json.RawMessage) (any, error) {
	var s Spec
	if err := core.Bind(args, &s); err != nil {
		return nil, err
	}
	p, created, err := m.Create(s)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "repo": p.Repo, "new_repository": created, "chat": "project:" + p.Name}, nil
}

// Create makes (or adopts) the repository, clones it, sets it up, and opens
// its chat. An empty repository gets a first commit with the Hive's files; an
// existing one gets a setup task whose pull request the person merges. Setup
// that fails part way is finished later by Tick, never started over.
func (m *Module) Create(s Spec) (core.Project, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.C.Effects.Forge
	if s.Demo && s.Name == "" {
		s.Name = "textkit"
	}
	if s.Repo == "" && s.Name == "" {
		return core.Project{}, false, errors.New("give a name or a repository")
	}
	if s.Repo == "" || !strings.Contains(s.Repo, "/") {
		user, err := f.User()
		if err != nil {
			return core.Project{}, false, fmt.Errorf("who are you on %s? %v", f.Name(), err)
		}
		if s.Repo == "" {
			s.Repo = user + "/" + s.Name
		} else {
			s.Repo = user + "/" + s.Repo
		}
	}
	for _, p := range m.C.Projects.List() {
		if p.Repo == s.Repo || (s.Demo && p.Demo) {
			p, err := m.finish(p)
			return p, false, err
		}
	}
	created, err := f.EnsureRepo(s.Repo, core.Clip(s.Description, 300))
	// The demo never adopts a repository with history: take the next free name.
	for i := 2; s.Demo && err == nil && !created && i < 10; i++ {
		if empty, _ := f.IsEmpty(s.Repo); empty {
			break
		}
		owner := strings.SplitN(s.Repo, "/", 2)[0]
		s.Name = fmt.Sprintf("textkit-%d", i)
		s.Repo = owner + "/" + s.Name
		created, err = f.EnsureRepo(s.Repo, core.Clip(s.Description, 300))
	}
	if err != nil {
		return core.Project{}, false, err
	}
	p := core.Project{Name: m.C.Projects.UniqueName(s.Name, s.Repo), Repo: s.Repo, Branch: f.DefaultBranch(s.Repo),
		Description: s.Description, Isolated: s.Isolated, Self: s.Self, Demo: s.Demo, Goals: s.Goals}
	if created {
		p.Branch = "main"
	}
	if p, err = m.C.Projects.Add(p); err != nil {
		return p, created, err
	}
	p, err = m.finish(p)
	return p, created, err
}

// finish clones and sets up a registered project; safe to call again.
func (m *Module) finish(p core.Project) (core.Project, error) {
	if p.Ready {
		return p, nil
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return p, err
	}
	// Records from before the Demo flag existed: the demo said so in its description.
	demoProject := p.Demo || (strings.HasPrefix(p.Name, "textkit") && strings.Contains(p.Description, "the demo"))
	goals := strings.TrimSpace(p.Goals)
	if main == "" {
		files := map[string]string{}
		if demoProject {
			files = demo.Files()
		} else {
			files["README.md"] = "# " + p.Name + "\n\n" + p.Description + "\n"
			files[".hive/goals.md"] = "# Goals\n\n" + orDefault(goals, "(Not set yet. Tell the lead what this project is for.)") + "\n"
			files[".hive/lessons.md"] = "# Lessons\n\nWhat was tried and why it didn't stay. Read before working here.\n\n"
			files[".hive/project.json"] = "{\n  \"test\": \"\"\n}\n"
		}
		sha, err := m.C.Effects.Setup(p, "", files, "Set up the project for the Hive")
		if err != nil {
			return p, err
		}
		if err := m.C.Effects.PushInitial(p, sha); err != nil {
			return p, err
		}
		if _, err := m.C.Effects.Sync(p); err != nil {
			return p, err
		}
	} else if m.C.Effects.ReadFile(p, main, ".hive/goals.md") == "" {
		title := "Set up the Hive files for " + p.Name
		if len(m.Work.List(func(t work.Task) bool { return t.Project == p.Name && t.Title == title })) == 0 {
			m.Work.File(work.Brief{Project: p.Name, From: "hive", Title: title,
				Done:  "`.hive/goals.md` holds the person's goals in their own words; `.hive/project.json` names the test command and, if one fits, a score (command, direction, tolerance); `.hive/lessons.md` exists.",
				Known: "The person's words about this project: " + orDefault(goals, p.Description)})
		}
	}
	p.Ready, p.Demo = true, demoProject
	m.C.Projects.Update(p)
	m.Chat.EnsureProject(p.Name)
	m.C.J.MustAppend("project.ready", map[string]string{"project": p.Name, "repo": p.Repo})
	return p, nil
}

// Tick finishes the setup of projects that didn't complete (a failed clone,
// the network down), retrying every minute and raising one alert per error.
func (m *Module) Tick() {
	for _, p := range m.C.Projects.List() {
		if p.Ready || time.Now().Before(m.retry[p.Name]) || !m.mu.TryLock() {
			continue
		}
		m.retry[p.Name] = time.Now().Add(time.Minute)
		go func(p core.Project) {
			defer m.mu.Unlock()
			if _, err := m.finish(p); err != nil && err.Error() != m.lastErr[p.Name] {
				m.lastErr[p.Name] = err.Error()
				m.C.J.MustAppend("alert.raised", map[string]string{"kind": "project", "project": p.Name, "text": "setting up " + p.Name + " (retrying every minute): " + core.Clip(err.Error(), 300)})
			}
		}(p)
		return
	}
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// ProjectStatus is one project's summary.
type ProjectStatus struct {
	Name     string   `json:"name"`
	Repo     string   `json:"repo"`
	Best     *float64 `json:"best_score,omitempty"`
	InFlight []string `json:"in_flight"`
	Recent   []string `json:"recent"`
}

// Status summarizes the Hive for agents and the shell.
func (m *Module) Status() map[string]any {
	var ps []ProjectStatus
	for _, p := range m.C.Projects.List() {
		st := ProjectStatus{Name: p.Name, Repo: p.Repo}
		if b, ok := m.Improve.Best(p.Name); ok {
			st.Best = &b
		}
		for i, t := range m.Work.List(func(t work.Task) bool { return t.Project == p.Name }) {
			line := fmt.Sprintf("[%s] %s (%s)", t.Stage, t.Title, t.ID)
			if t.PR.URL != "" {
				line += " " + t.PR.URL
			}
			if !t.Terminal() {
				st.InFlight = append(st.InFlight, line)
			} else if i < 8 {
				st.Recent = append(st.Recent, line)
			}
		}
		ps = append(ps, st)
	}
	var waiting []string
	for _, a := range m.C.Person.Open() {
		waiting = append(waiting, a.Title)
	}
	return map[string]any{"projects": ps, "waiting_on_person": waiting}
}
