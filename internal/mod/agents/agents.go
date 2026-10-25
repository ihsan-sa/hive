// Package agents composes what every agent starts with: the short generic
// orientation plus what it is asked to do, from the agents module on disk.
package agents

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// Agents builds run specs.
type Agents struct {
	C   *core.Core
	Mod *modules.Registry
	// Skip, when set, benches a pick from every route (a model whose trial
	// didn't finish).
	Skip func(core.Pick) bool
}

// System is the appended system prompt for a role: orientation, the role,
// and the modules this run may use.
func (a *Agents) System(role, project string) string {
	var b strings.Builder
	b.WriteString(a.Mod.Prompt("orientation.md"))
	b.WriteString("\n\n")
	if p := a.Mod.Prompt(role + ".md"); p != "" {
		b.WriteString(p)
		b.WriteString("\n\n")
	}
	granted := a.Mod.Granted(project)
	if len(granted) > 0 {
		b.WriteString("## Modules you may use\n\n")
		for _, name := range granted {
			if m, ok := a.Mod.Get(name); ok {
				fmt.Fprintf(&b, "- `%s`: %s\n", name, m.Description)
			}
		}
	}
	return b.String()
}

// Model picks the harness and model for a role in a project: the first on
// its route (core.Config.Route).
func (a *Agents) Model(role, project string) (harness, model string) {
	p := a.Route(role, project)[0]
	return p.Harness, p.Model
}

// Route is the picks a run of role in project goes on, in fallback order,
// less those Skip benches while any other is left.
func (a *Agents) Route(role, project string) []core.Pick {
	route := a.C.WS.LoadConfig().Route(role, project)
	if a.Skip == nil {
		return route
	}
	if kept := slices.DeleteFunc(slices.Clone(route), a.Skip); len(kept) > 0 {
		return kept
	}
	return route
}

// Spec builds a run spec with the role's system prompt, grants and model.
func (a *Agents) Spec(id, role, project, checkout, brief string) core.RunSpec {
	route := a.Route(role, project)
	s := core.RunSpec{ID: id, Role: role, Project: project, Checkout: checkout, Harness: route[0].Harness, Model: route[0].Model,
		System: a.System(role, project), Brief: brief, Route: route}
	if p, ok := a.C.Projects.Get(project); ok && p.Isolated {
		s.Isolated = true
		s.Modules = a.Mod.Granted(project)
	}
	return s
}

// Scratch makes an empty working directory for runs with no project checkout.
func (a *Agents) Scratch(id string) (string, error) {
	dir := filepath.Join(a.C.WS.Hive(), "scratch", id)
	return dir, os.MkdirAll(dir, 0o755)
}

// Spawner makes the runs the work module's spawn hook passes on: a reviewer
// gets a fresh checkout of the project at main (a scratch dir with no project).
func (a *Agents) Spawner(parent core.Run, role, project, brief, model string) (core.RunSpec, error) {
	id := core.NewID()
	dir := ""
	if p, ok := a.C.Projects.Get(project); ok {
		base, err := a.C.Effects.Sync(p)
		if err != nil {
			return core.RunSpec{}, err
		}
		dir, err = a.C.Effects.NewCheckout(p, id, base)
		if err != nil {
			return core.RunSpec{}, err
		}
	} else {
		var err error
		if dir, err = a.Scratch(id); err != nil {
			return core.RunSpec{}, err
		}
		project = ""
	}
	s := a.Spec(id, role, project, dir, brief+"\n\n(You are a helper spawned by "+parent.ID+"; your account goes back to it.)")
	if role == "reviewer" {
		s.ReadOnly = true
	}
	if model != "" {
		s.Model = model
		if p, err := helperPick(a.C.WS.LoadConfig(), project, model); err == nil {
			s.Harness, s.Model = p.Harness, p.Model // a named model runs on the open harness
		}
	}
	return s, nil
}

// helperPick is where a helper asked for on model runs in project: a named
// model on the open harness, harness:model as said, and a plain model name
// on the project's harness, not the workspace's.
func helperPick(cfg core.Config, project, model string) (core.Pick, error) {
	p, err := cfg.Pick(model)
	if err == nil && p.Harness != "open" && !strings.Contains(model, ":") {
		p.Harness = core.HarnessName(cmp.Or(cfg.Projects[project].Harness, cfg.Harness))
	}
	return p, err
}
