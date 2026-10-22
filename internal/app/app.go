// Package app wires the core and the default modules into a running Hive.
package app

import (
	"context"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/kb"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/projects"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/risk"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/system"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// App is a running Hive.
type App struct {
	C        *core.Core
	Mods     *modules.Registry
	Agents   *agents.Agents
	Chat     *chat.Module
	Work     *work.Module
	Improve  *improve.Module
	KB       *kb.Module
	System   *system.Module
	Risk     *risk.Module
	Projects *projects.Module
	Release  *release.Module
}

// New opens the workspace and builds every module.
func New(ws core.Workspace, forge core.Forge) (*App, error) {
	c, err := core.Open(ws, forge)
	if err != nil {
		return nil, err
	}
	a := &App{C: c}
	a.Mods = modules.New(ws, c.J)
	if err := a.Mods.InstallDefaults(); err != nil {
		return nil, err
	}
	a.Agents = &agents.Agents{C: c, Mod: a.Mods}
	c.Runs.Ops().SetSpawner(a.Agents.Spawner)
	c.Runs.ModelFor = func(r core.Run) string { _, m := a.Agents.Model(r.Role, r.Project); return m }
	a.Chat = chat.New(c, a.Agents)
	a.Work = work.New(c, a.Agents, a.Chat)
	a.Improve = improve.New(c, a.Agents, a.Work, a.Chat)
	a.KB = kb.New(c, a.Agents)
	a.System = system.New(c)
	a.Risk = risk.New(c, a.Mods, a.Agents)
	a.Work.Steps = a.Risk.Steps
	a.Projects = projects.New(c, a.Chat, a.Work, a.Improve)
	a.Release = &release.Module{C: c}
	a.Improve.OnKept = a.Release.AfterKept

	a.System.Register(system.Job{Name: "improve.sweep", Fn: func() (string, error) {
		s, err := a.Improve.StartSweep(false)
		if err != nil {
			return err.Error(), nil
		}
		return "started " + s.ID, nil
	}})
	a.System.Register(system.Job{Name: "kb.file", Fn: func() (string, error) {
		id, err := a.KB.File()
		if err != nil {
			return err.Error(), nil
		}
		return "started " + id, nil
	}})
	a.System.Register(system.Job{Name: "modules.update", AtStart: true, Fn: func() (string, error) {
		changed := a.Mods.Update()
		if len(changed) == 0 {
			return "all modules current", nil
		}
		return "updated " + join(changed), nil
	}})
	a.System.Register(system.Job{Name: "hive.update", AtStart: true, Fn: a.Release.AutoUpdate})
	return a, nil
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

// Tick advances everything one step.
func (a *App) Tick() {
	a.C.Tick()
	a.Work.Tick()
	a.Improve.Tick()
	a.Improve.TickSweeps()
	a.KB.Tick()
	a.System.Tick()
	a.Risk.Tick()
	a.Projects.Tick()
}

// Run ticks until ctx ends.
func (a *App) Run(ctx context.Context) {
	a.Improve.Recover()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		a.Tick()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Close releases the journal.
func (a *App) Close() error { return a.C.Close() }
