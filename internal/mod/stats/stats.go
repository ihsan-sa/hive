// Package stats keeps the dashboard's figures: what runs spent today and this
// month, by project and role; what happened in the last hour and day; and
// all-time totals of agents, agent-hours, tokens, spend, merges, reverts and
// releases. Every figure comes from the journal, folded in once as each event
// is appended (a running tally), so a state poll never rescans it.
package stats

import (
	"regexp"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// Spend is what runs cost over a span, in total and split two ways.
type Spend struct {
	Total    float64            `json:"total"`
	Projects map[string]float64 `json:"projects,omitempty"` // "" is the workspace's own runs
	Roles    map[string]float64 `json:"roles,omitempty"`
}

func (s *Spend) add(project, role string, usd float64) {
	if s.Projects == nil {
		s.Projects, s.Roles = map[string]float64{}, map[string]float64{}
	}
	s.Total += usd
	s.Projects[project] += usd
	s.Roles[role] += usd
}

func (s Spend) clone() Spend {
	c := Spend{Total: s.Total, Projects: map[string]float64{}, Roles: map[string]float64{}}
	for k, v := range s.Projects {
		c.Projects[k] = v
	}
	for k, v := range s.Roles {
		c.Roles[k] = v
	}
	return c
}

// Activity is what happened over a recent span.
type Activity struct {
	Messages int `json:"messages"` // chat posts by the Hive's agents and messages delivered to an agent
	Turns    int `json:"turns"`
	Started  int `json:"started"`  // runs
	Finished int `json:"finished"` // runs that ended: done, blocked or stopped
	Merged   int `json:"merged"`   // PRs, reverts included
	Reverted int `json:"reverted"` // reverts merged
	Peer     int `json:"peer"`     // messages from one worker to another
	Helped   int `json:"helped"`   // runs named in a finish account's Helped-by line
}

// Model is one model's share of the all-time totals.
type Model struct {
	Turns  int            `json:"turns"`
	Tokens harness.Tokens `json:"tokens"`
	Spend  float64        `json:"spend"`
}

// AllTime is every run the journal holds.
type AllTime struct {
	Since      time.Time        `json:"since,omitzero"` // the journal's first event
	Runs       map[string]int   `json:"runs"`           // agents run, by role
	AgentHours float64          `json:"agent_hours"`    // time spent in turns
	Turns      int              `json:"turns"`
	Tokens     harness.Tokens   `json:"tokens"`
	Spend      float64          `json:"spend"`
	Models     map[string]Model `json:"models"`
	Merged     int              `json:"merged"`
	Reverted   int              `json:"reverted"`
	Releases   int              `json:"releases"`
	// Peer is worker-to-worker messages; Met the pairs of workers the Hive
	// introduced on files both briefs name (work.met), Talked those of them
	// that messaged each other; Helped the runs named in Helped-by lines.
	Peer   int `json:"peer"`
	Met    int `json:"met"`
	Talked int `json:"talked"`
	Helped int `json:"helped"`
}

// View is the figures as of one moment.
type View struct {
	Today Spend    `json:"today"` // the local calendar day
	Month Spend    `json:"month"` // the UTC month monthly_usd is weighed in
	Hour  Activity `json:"hour"`
	Day   Activity `json:"day"` // the last 24 hours
	All   AllTime  `json:"all"`
}

// The kinds of a recent point.
const (
	kMessage = iota
	kTurn
	kStarted
	kFinished
	kMerged
	kReverted
	kPeer
	kHelped
)

type point struct {
	at   time.Time
	kind int8
}

type run struct {
	role, project, model string
	session              string
	spend                core.SessionSpend
	turnAt               time.Time // the open turn's start
	ended                bool
}

// Tally folds journal events into the figures.
type Tally struct {
	Loc *time.Location // the person's day; time.Local when nil

	mu       sync.Mutex
	runs     map[string]*run
	stages   map[string]string // task -> its last stage
	released map[string]bool   // project + tag
	met      map[string]bool   // pair of runs introduced -> whether they talked since
	days     map[string]*Spend // "2006-01-02" in Loc
	months   map[string]*Spend // "2006-01" in UTC
	recent   []point           // the last 24 hours before the newest event
	all      AllTime
}

// NewTally is an empty tally.
func NewTally() *Tally {
	return &Tally{runs: map[string]*run{}, stages: map[string]string{}, released: map[string]bool{}, met: map[string]bool{},
		days: map[string]*Spend{}, months: map[string]*Spend{}, all: AllTime{Runs: map[string]int{}, Models: map[string]Model{}}}
}

// New is the tally of j: its record so far, then each event as it's appended.
func New(j *core.Journal) *Tally {
	t := NewTally()
	j.Replay(t.Apply)
	j.Subscribe(t.Apply)
	return t
}

func (t *Tally) loc() *time.Location {
	if t.Loc != nil {
		return t.Loc
	}
	return time.Local
}

// Apply folds one event in.
func (t *Tally) Apply(e core.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.all.Since.IsZero() {
		t.all.Since = e.At
	}
	switch e.Kind {
	case "run.created":
		var s core.RunSpec
		if e.Decode(&s) != nil || s.ID == "" || t.runs[s.ID] != nil {
			return
		}
		t.runs[s.ID] = &run{role: s.Role, project: s.Project, model: or(s.Model, core.HarnessName(s.Harness))}
		t.all.Runs[s.Role]++
		t.point(e.At, kStarted)
	case "run.turn_started":
		var s struct{ ID, Session, Model string }
		e.Decode(&s)
		if r := t.runs[s.ID]; r != nil {
			r.turnAt = e.At
			if s.Session != "" {
				r.session = s.Session
			}
			if s.Model != "" {
				r.model = s.Model
			}
		}
	case "run.turn_ended":
		var s struct {
			ID, Session, Status string
			Result              *harness.Result
		}
		e.Decode(&s)
		r := t.runs[s.ID]
		if r == nil {
			return
		}
		if s.Session != "" {
			r.session = s.Session
		}
		if !r.turnAt.IsZero() {
			t.all.AgentHours += e.At.Sub(r.turnAt).Hours()
			r.turnAt = time.Time{}
		}
		t.all.Turns++
		t.point(e.At, kTurn)
		mu := t.all.Models[r.model]
		mu.Turns++
		if s.Result != nil {
			// The harness reports its session's running total: a turn
			// spent the rise since the last one (core.SessionSpend).
			usd := r.spend.Turn(r.session, s.Result.CostUSD)
			t.all.Spend += usd
			mu.Spend += usd
			t.spend(t.days, e.At.In(t.loc()).Format("2006-01-02"), r, usd)
			t.spend(t.months, e.At.UTC().Format("2006-01"), r, usd)
			if s.Result.Tokens != nil {
				t.all.Tokens.Add(*s.Result.Tokens)
				mu.Tokens.Add(*s.Result.Tokens)
			}
		}
		t.all.Models[r.model] = mu
		t.status(e.At, r, s.Status)
	case "run.status":
		var s struct{ ID, Status string }
		e.Decode(&s)
		if r := t.runs[s.ID]; r != nil {
			t.status(e.At, r, s.Status)
		}
	case "run.reopened":
		var s struct{ ID string }
		e.Decode(&s)
		if r := t.runs[s.ID]; r != nil {
			r.ended = false
		}
	case "run.queued":
		t.point(e.At, kMessage)
	case "msg.sent":
		var s struct{ From, To string }
		e.Decode(&s)
		if f, to := t.runs[s.From], t.runs[s.To]; f != nil && to != nil && f.role == "worker" && to.role == "worker" {
			t.all.Peer++
			t.point(e.At, kPeer)
			if talked, ok := t.met[pair(s.From, s.To)]; ok && !talked {
				t.met[pair(s.From, s.To)] = true
				t.all.Talked++
			}
		}
	case "work.met":
		var s struct {
			Run  string
			With []struct{ Run string }
		}
		e.Decode(&s)
		for _, w := range s.With {
			if _, ok := t.met[pair(s.Run, w.Run)]; !ok && w.Run != "" {
				t.met[pair(s.Run, w.Run)] = false
				t.all.Met++
			}
		}
	case "run.finish":
		var s struct{ Finish core.Finish }
		e.Decode(&s)
		for _, m := range helpedBy.FindAllStringSubmatch(s.Finish.Account, -1) {
			n := len(runName.FindAllString(m[1], -1))
			t.all.Helped += n
			for range n {
				t.point(e.At, kHelped)
			}
		}
	case "chat.posted":
		var s struct{ From string }
		e.Decode(&s)
		if s.From != "person" {
			t.point(e.At, kMessage)
		}
	case "task.saved":
		var s struct{ ID, Kind, Stage string }
		if e.Decode(&s) != nil || s.ID == "" {
			return
		}
		prev := t.stages[s.ID]
		t.stages[s.ID] = s.Stage
		if s.Stage != "merged" || prev == "merged" {
			return
		}
		t.all.Merged++
		t.point(e.At, kMerged)
		if s.Kind == "revert" {
			t.all.Reverted++
			t.point(e.At, kReverted)
		}
	case "hive.released":
		var s struct{ Project, Tag string }
		e.Decode(&s)
		if k := s.Project + " " + s.Tag; !t.released[k] {
			t.released[k] = true
			t.all.Releases++
		}
	}
}

func (t *Tally) spend(m map[string]*Spend, key string, r *run, usd float64) {
	s := m[key]
	if s == nil {
		s = &Spend{}
		m[key] = s
	}
	s.add(r.project, r.role, usd)
}

func (t *Tally) status(at time.Time, r *run, status string) {
	switch status {
	case core.StatusDone, core.StatusBlocked, core.StatusStopped:
		if !r.ended {
			r.ended = true
			t.point(at, kFinished)
		}
	case "":
	default:
		r.ended = false
	}
}

// point keeps one recent happening and drops those older than a day.
func (t *Tally) point(at time.Time, kind int8) {
	t.recent = append(t.recent, point{at, kind})
	cut := at.Add(-24 * time.Hour)
	i := 0
	for i < len(t.recent) && t.recent[i].at.Before(cut) {
		i++
	}
	if i > len(t.recent)/2 { // copy down now and then, not every time
		t.recent = append([]point{}, t.recent[i:]...)
	}
}

// View is the figures as of now.
func (t *Tally) View(now time.Time) View {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := View{All: t.all}
	v.All.Runs, v.All.Models = map[string]int{}, map[string]Model{}
	for k, n := range t.all.Runs {
		v.All.Runs[k] = n
	}
	for k, mu := range t.all.Models {
		v.All.Models[k] = mu
	}
	if s := t.days[now.In(t.loc()).Format("2006-01-02")]; s != nil {
		v.Today = s.clone()
	}
	if s := t.months[now.UTC().Format("2006-01")]; s != nil {
		v.Month = s.clone()
	}
	hour, day := now.Add(-time.Hour), now.Add(-24*time.Hour)
	for _, p := range t.recent {
		if p.at.After(day) && !p.at.After(now) {
			count(&v.Day, p.kind)
			if p.at.After(hour) {
				count(&v.Hour, p.kind)
			}
		}
	}
	return v
}

func count(a *Activity, kind int8) {
	switch kind {
	case kMessage:
		a.Messages++
	case kTurn:
		a.Turns++
	case kStarted:
		a.Started++
	case kFinished:
		a.Finished++
	case kMerged:
		a.Merged++
	case kReverted:
		a.Reverted++
	case kPeer:
		a.Peer++
	case kHelped:
		a.Helped++
	}
}

// helpedBy is an account's credit line, open source's Reported-by: the
// runs whose help changed the work.
var helpedBy = regexp.MustCompile(`(?im)^[\s*-]*helped[- ]by:\s*(.+)$`)

// runName is a run's id in a Helped-by line.
var runName = regexp.MustCompile(`\b[a-z]+(?:-[a-z]+)*(?:-[0-9a-f]{6,})+(?:-[0-9]+)?\b`)

// pair is the key of two runs, in either order.
func pair(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + " " + b
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
