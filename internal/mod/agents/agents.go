// Package agents composes what every agent starts with: the short generic
// orientation plus what it is asked to do, from the agents module on disk.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// Agents builds run specs.
type Agents struct {
	C   *core.Core
	Mod *modules.Registry
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

// Model picks the model for a role in a project.
func (a *Agents) Model(role, project string) (harness, model string) {
	cfg := a.C.WS.LoadConfig()
	harness, model = cfg.Harness, cfg.Model
	if role == "lead" && cfg.LeadModel != "" {
		model = cfg.LeadModel
	}
	if ps, ok := cfg.Projects[project]; ok {
		if ps.Harness != "" {
			harness = ps.Harness
		}
		if ps.Model != "" {
			model = ps.Model
		}
	}
	if role == "reviewer" && cfg.ReviewerModel != "" {
		model = cfg.ReviewerModel
	}
	return harness, model
}

// Spec builds a run spec with the role's system prompt, grants and model.
func (a *Agents) Spec(id, role, project, checkout, brief string) core.RunSpec {
	h, m := a.Model(role, project)
	s := core.RunSpec{ID: id, Role: role, Project: project, Checkout: checkout, Harness: h, Model: m,
		System: a.System(role, project), Brief: brief}
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

// Spawner is the core's spawn hook: a helper gets a fresh checkout of the
// project at main on its own branch (or a scratch dir with no project).
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
	}
	return s, nil
}
