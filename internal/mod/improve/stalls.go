package improve

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Stall is what the watcher decided about one thing that stopped moving. It
// is journaled with the limit and history it judged by, so the sweep can tune them.
type Stall struct {
	Key     string    `json:"key"` // task:<id> or run:<id>
	What    string    `json:"what"`
	Project string    `json:"project,omitempty"`
	Stage   string    `json:"stage"`
	Since   time.Time `json:"since"`   // when it last moved
	Limit   float64   `json:"limit_s"` // seconds without moving that counted as stuck
	Samples int       `json:"samples"` // past durations of the stage behind the limit
	Tries   int       `json:"tries"`
	Told    bool      `json:"told"` // handed to the lead; the watcher does nothing more
	Did     string    `json:"did"`
	At      time.Time `json:"at"` // last action
	Moved   time.Time `json:"moved,omitempty"`
}

type spell struct {
	stage     string
	since     time.Time
	wait      string    // the named wait it is held on now, as waitCause gives it
	waitSince time.Time // since when it has been held on that wait
	waits     []string  // every named wait it was held on in this spell
}

// waitCause names what a task held on reason waits for, or "" if it names
// no wait. Main's red holds every change that waits on it whatever commit
// main is at, so its waits are one cause however main moves; any other wait
// is one cause while its HoldKey stays (the pending checks it lists finishing
// one by one don't start it again).
func waitCause(reason string) string {
	if !strings.HasPrefix(reason, "waiting ") {
		return ""
	}
	if strings.HasPrefix(reason, "waiting on main:") {
		return "waiting on main"
	}
	return work.HoldKey(reason)
}

// waitKey is the stall key of the wait a task in project and stage shares.
func waitKey(project, stage, cause string) string {
	return "wait:" + project + ":" + stage + ":" + cause
}

// applyStall keeps each task's current stage spell and how long finished
// spells took, from the journal.
func (m *Module) applyStall(e core.Event) {
	switch e.Kind {
	case "task.saved":
		var t work.Task
		e.Decode(&t)
		sp, ok := m.spells[t.ID]
		if ok && sp.stage == t.Stage {
			m.spells[t.ID] = sp.held(t, e.At)
			return
		}
		if ok && !m.actedOn(t, sp) { // a spell the watcher acted on isn't the norm
			m.took[sp.stage] = append(m.took[sp.stage], e.At.Sub(sp.since).Seconds())
			if n := len(m.took[sp.stage]); n > 50 {
				m.took[sp.stage] = m.took[sp.stage][n-50:]
			}
		}
		m.spells[t.ID] = spell{stage: t.Stage, since: e.At}.held(t, e.At)
	case "stall.saved":
		var s Stall
		e.Decode(&s)
		m.stalls[s.Key] = &s
	}
}

// held notes the named wait t is held on as of at.
func (sp spell) held(t work.Task, at time.Time) spell {
	c := waitCause(t.Reason)
	if c == sp.wait {
		return sp
	}
	sp.wait, sp.waitSince = c, at
	if c != "" && !slices.Contains(sp.waits, c) {
		sp.waits = append(sp.waits[:len(sp.waits):len(sp.waits)], c)
	}
	return sp
}

// actedOn reports whether the watcher acted on t's spell sp, on its own or
// through a wait it shared with other tasks: a wait handed off before t
// joined it covers t too, until it moved on.
func (m *Module) actedOn(t work.Task, sp spell) bool {
	if s := m.stalls["task:"+t.ID]; s != nil && s.Stage == sp.stage && !s.At.Before(sp.since) {
		return true
	}
	for _, c := range sp.waits {
		if s := m.stalls[waitKey(t.Project, sp.stage, c)]; s != nil && s.Stage == sp.stage && (!s.At.Before(sp.since) || s.Moved.IsZero() || s.Moved.After(sp.since)) {
			return true
		}
	}
	return false
}

// StallLimit is three times the 90th percentile of a stage's recent
// durations here, never under the floor, which is all there is while history is thin.
func StallLimit(took []float64, floor time.Duration) time.Duration {
	if len(took) == 0 {
		return floor
	}
	s := append([]float64(nil), took...)
	sort.Float64s(s)
	p90 := s[(len(s)*9+9)/10-1]
	return max(floor, time.Duration(3*p90*float64(time.Second)))
}

// Stalls returns the watcher's records, newest first.
func (m *Module) Stalls() []Stall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Stall
	for _, s := range m.stalls {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Watch acts on whatever has gone longer without moving than its stage
// usually takes in this Hive: it restarts a hung turn once, then hands the
// thing to the lead, and keeps one alert per stuck thing up to date.
func (m *Module) Watch() {
	floor := time.Duration(m.C.WS.LoadConfig().StallFloorSeconds) * time.Second
	if floor <= 0 {
		floor = 20 * time.Minute
	}
	now := map[string]string{} // key -> current stage
	// A queued brief and a check waiting for a slot are waiting their turn,
	// not stuck; a check is timed from when it got its slot.
	running, waiting := m.C.Effects.Slots.State()
	// Tasks held on a named wait (main is red, another task's turn) share
	// its cause: the cause is judged once, so it reaches the lead once.
	type group struct {
		stage, project string
		tasks          []work.Task
		since          time.Time
	}
	waits := map[string]*group{}
	var causes []string
	for _, t := range m.Work.List(func(t work.Task) bool { return !t.Terminal() && t.Stage != work.StagePerson }) {
		now["task:"+t.ID] = t.Stage
		// The recursive gate runs one change at a time on its own clock.
		if t.Queued() || strings.HasPrefix(t.Reason, work.GateWait) || t.Stage == work.StageTesting && slices.ContainsFunc(waiting, func(u core.SlotUse) bool { return u.Project == t.Project && u.SHA == t.SHA }) {
			continue
		}
		if c := waitCause(t.Reason); c != "" && t.Stage != work.StageWorking && t.Stage != work.StageReviewing { // a run's own hang is still its own
			key := waitKey(t.Project, t.Stage, c)
			g := waits[key]
			if g == nil {
				g = &group{stage: t.Stage, project: t.Project}
				waits[key] = g
				causes = append(causes, key)
			}
			m.mu.Lock()
			sp := m.spells[t.ID]
			m.mu.Unlock()
			since := sp.since
			if sp.wait == c {
				since = sp.waitSince
			}
			if g.since.IsZero() || since.Before(g.since) {
				g.since = since // the cause has held since its first task began waiting on it
			}
			g.tasks = append(g.tasks, t)
			continue
		}
		run := map[string]string{work.StageWorking: t.Worker, work.StageReviewing: t.Reviewer}[t.Stage]
		m.mu.Lock()
		sp, took := m.spells[t.ID], append([]float64(nil), m.took[t.Stage]...)
		m.mu.Unlock()
		if t.Stage == work.StageTesting {
			for _, u := range running {
				if u.Project == t.Project && u.SHA == t.SHA {
					sp.since = latest(sp.since, u.Since)
				}
			}
		}
		m.judge(Stall{Key: "task:" + t.ID, What: fmt.Sprintf("%q", t.Label()), Project: t.Project, Stage: t.Stage, Since: sp.since}, run, t.Chat, t.Thread, took, floor)
	}
	for _, key := range causes {
		g := waits[key]
		now[key] = g.stage
		sort.Slice(g.tasks, func(i, j int) bool { return g.tasks[i].ID < g.tasks[j].ID }) // the same tasks read the same, so What changes only when they do
		t := g.tasks[0]
		what, chat, thread := fmt.Sprintf("%q", t.Label()), t.Chat, t.Thread
		if len(g.tasks) > 1 {
			var labels []string
			for _, t := range g.tasks {
				labels = append(labels, fmt.Sprintf("%q", t.Label()))
			}
			what, chat, thread = fmt.Sprintf("%d tasks (%s)", len(g.tasks), strings.Join(labels, ", ")), "", ""
		}
		what += ", " + t.Reason + ","
		m.mu.Lock()
		took := append([]float64(nil), m.took[g.stage]...)
		m.mu.Unlock()
		m.judge(Stall{Key: key, What: what, Project: g.project, Stage: g.stage, Since: g.since}, "", chat, thread, took, floor)
	}
	took := map[string][]float64{}
	for _, r := range m.C.Runs.List(func(r *core.Run) bool { return r.Terminal() && r.Task == "" && !r.Persistent }) {
		if len(took[r.Role]) < 50 {
			took[r.Role] = append(took[r.Role], r.Updated.Sub(r.Created).Seconds())
		}
	}
	for _, r := range m.C.Runs.List(func(r *core.Run) bool { return !r.Terminal() && r.Task == "" && !r.Persistent }) {
		now["run:"+r.ID] = r.Role
		m.judge(Stall{Key: "run:" + r.ID, What: "the " + r.Role + " " + r.ID, Project: r.Project, Stage: r.Role, Since: r.Created}, r.ID, "", "", took[r.Role], floor)
	}
	for _, s := range m.Stalls() {
		if s.Moved.IsZero() && now[s.Key] != s.Stage {
			s.Moved = time.Now().UTC()
			m.C.J.MustAppend("stall.saved", s)
			m.alertStall(s)
		}
	}
}

// judge acts on cur if it has gone past its stage's limit without moving.
func (m *Module) judge(cur Stall, run, chat, thread string, took []float64, floor time.Duration) {
	if r, ok := m.C.Runs.Get(run); ok {
		if r.Status == core.StatusParked || r.Status == core.StatusQuiet || r.Waiting != "" {
			return // waiting on a reset, a planned retry or something it asked for; quiet is flagged already
		}
		cur.Since = latest(cur.Since, r.TurnStarted, r.LastOutput)
	}
	s := &cur
	m.mu.Lock()
	if old := m.stalls[cur.Key]; old != nil && old.Stage == cur.Stage && old.Moved.IsZero() {
		c := *old
		s = &c
	}
	m.mu.Unlock()
	if s.Told {
		if s.What != cur.What { // tasks joined or left a shared wait: the alert names who waits now
			s.What = cur.What
			m.C.J.MustAppend("stall.saved", *s)
			m.alertStall(*s)
		}
		return
	}
	limit := StallLimit(took, floor)
	if time.Since(latest(cur.Since, s.At)) < limit {
		return
	}
	s.Tries++
	s.Limit, s.Samples, s.At = limit.Seconds(), len(took), time.Now().UTC()
	did := "restarted its turn"
	if s.Tries > 1 || !m.C.Runs.Interrupt(run) {
		s.Told, did = true, "handed it to the lead"
	}
	if s.Did != "" {
		s.Did += ", then "
	}
	s.Did += did + " at " + s.At.Local().Format("15:04")
	m.C.J.MustAppend("stall.saved", *s)
	text := m.alertStall(*s)
	if s.Told {
		m.Chat.Notify(chat, thread, "Stuck: "+text)
	}
}

// alertStall raises or updates the one alert for s.
func (m *Module) alertStall(s Stall) string {
	text := fmt.Sprintf("%s stopped moving in %s at %s, longer than the %s limit for that stage here (from %d past runs); I %s",
		s.What, s.Stage, s.Since.Local().Format("Mon 15:04"), time.Duration(s.Limit*float64(time.Second)).Round(time.Second), s.Samples, s.Did)
	if !s.Moved.IsZero() {
		text += "; it moved on at " + s.Moved.Local().Format("15:04")
	}
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "stall", "ref": s.Key, "project": s.Project, "text": text})
	return text
}

func latest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}
