// Package system keeps schedules and wake-ups: when sweeps, knowledge filing,
// module updates and Hive updates run, and agents' own scheduled wake-ups.
package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Job is a named scheduled action provided by a module.
type Job struct {
	Name    string
	AtStart bool   // run soon after the daemon starts, not only after one interval
	Default string // its schedule when config.json and the defaults name none
	Fn      func() (string, error)
}

// Wakeup is an agent's scheduled message to itself.
type Wakeup struct {
	ID   string    `json:"id"`
	Run  string    `json:"run"`
	At   time.Time `json:"at"`
	Text string    `json:"text"`
	Done bool      `json:"done"`
}

// Module is the system module.
type Module struct {
	C *core.Core

	mu       sync.Mutex
	jobs     map[string]Job
	last     map[string]time.Time
	results  map[string]string
	running  map[string]bool
	wakeups  map[string]*Wakeup
	started  time.Time
	lastTick time.Time
}

// New loads schedule records and registers the schedule op.
func New(c *core.Core) *Module {
	m := &Module{C: c, jobs: map[string]Job{}, last: map[string]time.Time{}, results: map[string]string{}, running: map[string]bool{}, wakeups: map[string]*Wakeup{}, started: time.Now()}
	apply := func(e core.Event) {
		m.mu.Lock()
		defer m.mu.Unlock()
		switch e.Kind {
		case "system.ran":
			var r struct{ Job, Result string }
			e.Decode(&r)
			m.last[r.Job], m.results[r.Job] = e.At, r.Result
		case "system.wakeup":
			var w Wakeup
			e.Decode(&w)
			m.wakeups[w.ID] = &w
		case "system.woke":
			var w Wakeup
			e.Decode(&w)
			if x := m.wakeups[w.ID]; x != nil {
				x.Done = true
			}
		}
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	c.Runs.Ops().Register(core.Op{Name: "schedule", Help: `{"in": "2h" | "at": "RFC3339", "text": "..."}`, Fn: func(r core.Run, args json.RawMessage) (any, error) {
		var a struct{ In, At, Text string }
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
		at, err := when(a.In, a.At)
		if err != nil {
			return nil, err
		}
		w := Wakeup{ID: core.NewID(), Run: r.ID, At: at.UTC(), Text: a.Text}
		c.J.MustAppend("system.wakeup", w)
		c.Runs.Wait(r.ID, "a wake-up at "+at.Local().Format("Mon 15:04"))
		return "scheduled for " + at.Local().Format("Mon 15:04"), nil
	}})
	return m
}

func when(in, at string) (time.Time, error) {
	if in != "" {
		d, err := time.ParseDuration(in)
		if err != nil {
			return time.Time{}, fmt.Errorf("bad duration %q (use e.g. 90m, 2h)", in)
		}
		return time.Now().Add(d), nil
	}
	if at != "" {
		return time.Parse(time.RFC3339, at)
	}
	return time.Time{}, errors.New(`give "in" (e.g. 2h) or "at" (RFC3339)`)
}

// Register adds a job.
func (m *Module) Register(j Job) {
	m.mu.Lock()
	m.jobs[j.Name] = j
	m.mu.Unlock()
}

// Has says whether a job is registered.
func (m *Module) Has(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.jobs[name]
	return ok
}

// Default is a registered job's own default schedule.
func (m *Module) Default(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[name].Default
}

// Schedule is a parsed schedule string.
type Schedule struct {
	Every time.Duration
	Daily string // HH:MM
	Off   bool
}

// Parse reads "every 12h", "daily 04:00" or "off".
func Parse(s string) (Schedule, error) {
	f := strings.Fields(strings.TrimSpace(s))
	switch {
	case len(f) == 1 && f[0] == "off":
		return Schedule{Off: true}, nil
	case len(f) == 2 && f[0] == "every":
		d, err := time.ParseDuration(f[1])
		if err != nil || d < time.Minute {
			return Schedule{}, fmt.Errorf("bad interval %q", f[1])
		}
		return Schedule{Every: d}, nil
	case len(f) == 2 && f[0] == "daily":
		if _, err := time.Parse("15:04", f[1]); err != nil {
			return Schedule{}, fmt.Errorf("bad time %q", f[1])
		}
		return Schedule{Daily: f[1]}, nil
	}
	return Schedule{}, fmt.Errorf("schedule %q: use \"every 12h\", \"daily 04:00\" or \"off\"", s)
}

// Due reports whether a job should run now.
func (s Schedule) Due(now, last, started time.Time, atStart bool) bool {
	if s.Off {
		return false
	}
	if s.Every > 0 {
		if last.IsZero() {
			if atStart {
				return now.Sub(started) > 30*time.Second
			}
			return now.Sub(started) >= s.Every
		}
		return now.Sub(last) >= s.Every
	}
	t, _ := time.Parse("15:04", s.Daily)
	today := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	return !now.Before(today) && last.Before(today)
}

// Tick runs due jobs and wake-ups, and notices sleeps.
func (m *Module) Tick() {
	now := time.Now()
	m.mu.Lock()
	if !m.lastTick.IsZero() && now.Sub(m.lastTick) > 2*time.Minute {
		gap := now.Sub(m.lastTick).Round(time.Second)
		m.mu.Unlock()
		m.C.J.MustAppend("system.resumed", map[string]string{"gap": gap.String()})
		m.mu.Lock()
	}
	m.lastTick = now
	var due []Job
	cfg := m.C.WS.LoadConfig()
	for name, j := range m.jobs {
		s := m.schedule(cfg, j)
		if !m.running[name] && s.Due(now, m.last[name], m.started, j.AtStart) {
			m.running[name] = true
			due = append(due, j)
		}
	}
	var wake []Wakeup
	for _, w := range m.wakeups {
		if !w.Done && !now.Before(w.At) {
			wake = append(wake, *w)
		}
	}
	m.mu.Unlock()
	for _, j := range due {
		go m.run(j)
	}
	for _, w := range wake {
		m.C.Runs.Send(w.Run, "[wake-up you scheduled] "+w.Text)
		m.C.J.MustAppend("system.woke", w)
	}
}

// schedule is a job's schedule: config.json's, else the Hive's default, else
// the job's own.
func (m *Module) schedule(cfg core.Config, j Job) Schedule {
	for _, text := range []string{cfg.Schedules[j.Name], core.DefaultConfig().Schedules[j.Name], j.Default} {
		if s, err := Parse(text); err == nil {
			return s
		}
	}
	return Schedule{Off: true}
}

// RunNow runs a job immediately (from the control center).
func (m *Module) RunNow(name string) error {
	m.mu.Lock()
	j, ok := m.jobs[name]
	busy := m.running[name]
	if ok && !busy {
		m.running[name] = true
	}
	m.mu.Unlock()
	if !ok {
		return core.ErrNotFound
	}
	if busy {
		return errors.New(name + " is already running")
	}
	go m.run(j)
	return nil
}

func (m *Module) run(j Job) {
	res, err := j.Fn()
	if err != nil {
		res = "error: " + err.Error()
	}
	m.C.J.MustAppend("system.ran", map[string]string{"job": j.Name, "result": core.Clip(res, 300)})
	m.mu.Lock()
	m.running[j.Name] = false
	m.mu.Unlock()
}

// JobState is one schedule for the control center.
type JobState struct {
	Name     string    `json:"name"`
	Schedule string    `json:"schedule"`
	Last     time.Time `json:"last"`
	Result   string    `json:"result"`
	Running  bool      `json:"running"`
}

// Jobs lists schedules.
func (m *Module) Jobs() []JobState {
	cfg := m.C.WS.LoadConfig()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []JobState
	for name, j := range m.jobs {
		sched := cfg.Schedules[name]
		if _, err := Parse(sched); err != nil && j.Default != "" {
			sched = j.Default
		}
		out = append(out, JobState{Name: name, Schedule: sched, Last: m.last[name], Result: m.results[name], Running: m.running[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Wakeups lists pending wake-ups.
func (m *Module) Wakeups() []Wakeup {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Wakeup
	for _, w := range m.wakeups {
		if !w.Done {
			out = append(out, *w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
