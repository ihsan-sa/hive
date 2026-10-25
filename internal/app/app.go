// Package app wires the core and the default modules into a running Hive.
package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	hive "github.com/ihsan-sa/hive-standalone-build"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/feedback"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/forgeread"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/kb"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/models"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/projects"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/risk"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/runner"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/stats"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/system"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/tools"
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
	Runner   *runner.Runner
	Feedback *feedback.Module
	Models   *models.Module
	Stats    *stats.Tally

	seeding  sync.WaitGroup // SeedSelf's copy, which Close waits out
	stopSeed context.CancelFunc
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
	c.Runs.Route = func(r core.Run) []core.Pick { return a.Agents.Route(r.Role, r.Project) }
	a.Chat = chat.New(c, a.Agents)
	a.Work = work.New(c, a.Agents, a.Chat)
	c.Runs.Ops().SetSpawner(a.Work.Spawn)
	a.Improve = improve.New(c, a.Agents, a.Work, a.Chat)
	a.Feedback = feedback.New(c)
	a.Stats = stats.New(c.J)
	a.Improve.Feedback = a.Feedback.Digest
	a.KB = kb.New(c, a.Agents)
	a.System = system.New(c)
	a.Risk = risk.New(c, a.Mods, a.Agents)
	tools.New(c)
	forgeread.New(c)
	a.Work.Steps = a.Risk.Steps
	a.Work.Weigh = a.Risk.Weigh
	a.Work.Gate = runGate(ws)
	a.Projects = projects.New(c, a.Chat, a.Work, a.Improve)
	a.Release = release.New(c)
	a.Models = models.New(c, a.Work)
	a.Agents.Skip = a.Models.Benched
	a.Improve.OnKept = a.Release.AfterKept
	a.Runner = &runner.Runner{C: c, Mods: a.Mods, Agents: a.Agents}
	for _, tick := range []func(){stats.SpendNotice{C: c}.Tick, a.Work.Tick, a.Improve.Tick, a.Improve.TickSweeps, a.Improve.TickStepBacks, a.KB.Tick, a.System.Tick, a.Release.Tick, a.Risk.Tick, a.Projects.Tick, a.moduleJobs, a.Runner.Tick, a.Models.Tick} {
		a.Mods.Builtin(tick)
	}
	c.Person.Handle("release", a.Release.OnPerson)

	a.System.Register(system.Job{Name: "improve.sweep", Fn: func() (string, error) {
		s, err := a.Improve.StartSweep(false)
		if err != nil {
			return err.Error(), nil
		}
		return "started " + s.ID, nil
	}})
	a.System.Register(system.Job{Name: "improve.stepback", Fn: func() (string, error) { return a.Improve.RotateStepBacks(), nil }})
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
	a.Release.PauseMerges = a.Work.PauseMerges
	a.System.Register(system.Job{Name: "hive.update", AtStart: true, Fn: a.Release.AutoUpdate})
	reports := report.New(c)
	a.Improve.Installs = reports.Received
	a.System.Register(system.Job{Name: "report.send", Fn: reports.Send})
	a.System.Register(system.Job{Name: "hive.clean", AtStart: true, Fn: func() (string, error) { return a.Clean(), nil }})
	c.Effects.OnDiskFull = func() { a.System.RunNow("hive.clean") }
	return a, nil
}

// moduleJobs registers the jobs module programs serve (a manifest's
// "schedule"). A due one is journaled as module.scheduled, which the program
// reads off the journal like any hook; the daemon never calls it.
func (a *App) moduleJobs() {
	for _, m := range a.Mods.List() {
		if len(m.Exec) == 0 {
			continue
		}
		name := filepath.Base(m.Dir)
		for job, every := range m.Schedule {
			a.System.RegisterModule(system.Job{Name: job, Default: every, Fn: func() (string, error) {
				a.C.J.MustAppend("module.scheduled", map[string]string{"module": name, "job": job})
				return "asked module " + name, nil
			}})
		}
	}
}

// Clean removes spent checkouts and what checks left behind, then measures
// the Hive's disk use for the dashboard (the hive.clean job).
func (a *App) Clean() string {
	var did []string
	for _, s := range []string{a.Work.PruneCheckouts(), a.C.Effects.Prune()} {
		if s != "" {
			did = append(did, s)
		}
	}
	tidied := 0
	if n := a.Work.TidyGates(func(out string) int {
		if !gate.Untidy(out) {
			return 0
		}
		tidied++
		return gate.Reap(out)
	}); tidied > 0 {
		did = append(did, fmt.Sprintf("tidied %d gate run(s) a dead gate left, ending %d process(es)", tidied, n))
	}
	d := a.C.Effects.MeasureDisk()
	if len(did) == 0 {
		return "nothing to remove; " + d.String()
	}
	return join(did) + "; " + d.String()
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
	a.Mods.Tick()
}

// Run ticks until ctx ends.
func (a *App) Run(ctx context.Context) {
	a.Improve.Recover()
	go a.C.WatchLogins(ctx, time.Minute)
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

// SeedSelf writes the Hive's own source into the knowledge base beside the
// start, with new turns held until it's there, and calls done with how long
// it took. Close stops it and waits for it.
func (a *App) SeedSelf(done func(time.Duration)) {
	a.C.Runs.HoldFor("kb.self", "waiting: the knowledge base takes the Hive's own source")
	ctx, cancel := context.WithCancel(context.Background())
	a.seeding.Add(1)
	a.stopSeed = cancel
	go func() {
		defer a.seeding.Done()
		a.KB.SeedSelf(ctx, hive.Source)
		a.C.Runs.HoldFor("kb.self", "")
		if ctx.Err() == nil {
			done(a.KB.Seeded)
		}
	}()
}

// Close stops module programs and releases the journal.
func (a *App) Close() error {
	if a.stopSeed != nil {
		a.stopSeed()
	}
	a.seeding.Wait()
	a.Runner.Close()
	return a.C.Close()
}
