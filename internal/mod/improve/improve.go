// Package improve closes the loop: measure main after every merge, keep or
// revert against the best score the project has kept, and sweep the records
// for the next real gain.
package improve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Measurement is one measure of main.
type Measurement struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`
	Task    string    `json:"task,omitempty"`
	TestsOK bool      `json:"tests_ok"`
	Score   *float64  `json:"score,omitempty"`
	Kept    bool      `json:"kept"`
	Best    *float64  `json:"best,omitempty"` // best kept after this measure
	Note    string    `json:"note"`
	At      time.Time `json:"at"`
}

// Sweep is one notice run.
type Sweep struct {
	ID      string    `json:"id"`
	Run     string    `json:"run"`
	Status  string    `json:"status"`
	Report  string    `json:"report,omitempty"`
	Asks    []string  `json:"asks,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

// Module is the self-improvement module.
type Module struct {
	C      *core.Core
	Agents *agents.Agents
	Work   *work.Module
	Chat   *chat.Module

	// OnKept is called after a merge is measured and kept.
	OnKept func(project, sha string)

	mu       sync.Mutex
	scores   map[string][]Measurement
	sweeps   map[string]*Sweep
	queue    chan work.Task
	lastSeen int64 // journal seq at the last sweep start
	retryAt  map[string]time.Time

	stepbacks map[string]*StepBack
	busy      map[string]bool
	alerted   map[string]string // project -> the step-back alert last raised
	startMu   sync.Mutex        // one step-back starts at a time
}

// New loads measurements and sweeps.
func New(c *core.Core, a *agents.Agents, w *work.Module, ch *chat.Module) *Module {
	m := &Module{C: c, Agents: a, Work: w, Chat: ch, scores: map[string][]Measurement{}, sweeps: map[string]*Sweep{}, queue: make(chan work.Task, 64), retryAt: map[string]time.Time{}, stepbacks: map[string]*StepBack{}, busy: map[string]bool{}, alerted: map[string]string{}}
	apply := func(e core.Event) {
		m.mu.Lock()
		defer m.mu.Unlock()
		switch e.Kind {
		case "score.measured":
			var ms Measurement
			e.Decode(&ms)
			m.scores[ms.Project] = append(m.scores[ms.Project], ms)
		case "sweep.saved":
			var s Sweep
			e.Decode(&s)
			m.sweeps[s.ID] = &s
			if s.Status == "running" {
				m.lastSeen = e.Seq
			}
		case "stepback.saved":
			var s StepBack
			e.Decode(&s)
			m.stepbacks[s.ID] = &s
		}
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	w.OnMerged = func(t work.Task) { m.queue <- t }
	work.BestScore = m.Best
	go m.measureLoop()
	return m
}

// Scores returns a project's measurements, oldest first.
func (m *Module) Scores(project string) []Measurement {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Measurement{}, m.scores[project]...)
}

// Best returns the best kept score.
func (m *Module) Best(project string) (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.scores[project]
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Kept && ms[i].Best != nil {
			return *ms[i].Best, true
		}
	}
	return 0, false
}

func (m *Module) measured(project, sha string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ms := range m.scores[project] {
		if ms.SHA == sha && (ms.Kept || ms.Task != "") {
			return true
		}
	}
	return false
}

func (m *Module) hasBaseline(project string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ms := range m.scores[project] {
		if ms.Kept {
			return true
		}
	}
	return false
}

func (m *Module) record(ms Measurement) {
	ms.At = time.Now().UTC()
	m.C.J.MustAppend("score.measured", ms)
	core.AppendJSONL(filepath.Join(m.C.WS.Hive(), "scores", ms.Project+".jsonl"), ms)
}

// measure runs tests and the score on sha in clean checkouts.
func (m *Module) measure(p core.Project, sha string) (bool, *float64, string) {
	cfg := m.C.Effects.Config(p, sha)
	tc := m.C.Effects.RunCheck(p, sha, "test", cfg.Test)
	if !tc.OK {
		return false, nil, "tests red: " + core.Clip(tc.Output, 600)
	}
	if cfg.Score == nil {
		return true, nil, "tests green (no score configured)"
	}
	sc := m.C.Effects.RunCheck(p, sha, "score", cfg.Score.Command)
	if !sc.OK || sc.Score == nil {
		return true, nil, "score did not produce a number: " + core.Clip(sc.Output, 600)
	}
	return true, sc.Score, fmt.Sprintf("score %g", *sc.Score)
}

// Tick measures the starting point of projects that have none yet. A red
// start is measured again until there is a number.
func (m *Module) Tick() {
	if _, err := m.C.Sandbox(); err != nil {
		return
	}
	for _, p := range m.C.Projects.List() {
		if !p.Ready || m.hasBaseline(p.Name) {
			continue
		}
		m.mu.Lock()
		due := time.Now().After(m.retryAt[p.Name])
		if due {
			m.retryAt[p.Name] = time.Now().Add(10 * time.Minute)
		}
		m.mu.Unlock()
		if !due {
			continue
		}
		go m.baseline(p)
	}
}

func (m *Module) baseline(p core.Project) {
	main, err := m.C.Effects.Sync(p)
	if err != nil || main == "" {
		m.mu.Lock()
		m.retryAt[p.Name] = time.Now().Add(30 * time.Second) // nothing to measure yet
		m.mu.Unlock()
		return
	}
	cfg := m.C.Effects.Config(p, main)
	ok, score, note := m.measure(p, main)
	ms := Measurement{Project: p.Name, SHA: main, TestsOK: ok, Score: score, Note: "start: " + note}
	if ok && (score != nil || cfg.Score == nil) {
		ms.Kept, ms.Best = true, score
		m.record(ms)
		return
	}
	m.record(ms)
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "baseline", "project": p.Name, "text": "the start of " + p.Name + " has no number yet: " + core.Clip(note, 200) + "; measuring again in 10 minutes"})
}

func (m *Module) measureLoop() {
	for t := range m.queue {
		m.afterMerge(t)
	}
}

// Recover re-queues merges whose measurement didn't happen (after a restart).
func (m *Module) Recover() {
	for _, t := range m.Work.List(func(t work.Task) bool { return t.MergeSHA != "" }) {
		if !m.measured(t.Project, t.MergeSHA) && time.Since(t.Updated) < 7*24*time.Hour {
			m.queue <- t
		}
	}
}

// afterMerge measures main at the merge and keeps or reverts.
func (m *Module) afterMerge(t work.Task) {
	p, ok := m.C.Projects.Get(t.Project)
	if !ok || m.measured(p.Name, t.MergeSHA) {
		return
	}
	if _, err := m.C.Effects.Sync(p); err != nil {
		go func() { time.Sleep(time.Minute); m.queue <- t }()
		return
	}
	cfg := m.C.Effects.Config(p, t.MergeSHA)
	ok, score, note := m.measure(p, t.MergeSHA)
	best, hasBest := m.Best(p.Name)
	ms := Measurement{Project: p.Name, SHA: t.MergeSHA, Task: t.ID, TestsOK: ok, Score: score, Note: note}
	worse := !ok
	reason := ""
	switch {
	case !ok:
		reason = "the tests went red after it merged"
	case cfg.Score != nil && score == nil:
		worse, reason = true, "the score stopped producing a number"
	case cfg.Score != nil && hasBest && !cfg.Score.WithinTolerance(*score, best):
		worse, reason = true, fmt.Sprintf("the score fell to %g from the best kept %g (tolerance %g)", *score, best, cfg.Score.Tolerance)
	}
	if !worse {
		ms.Kept = true
		if score != nil {
			b := *score
			if hasBest {
				b = cfg.Score.Best(*score, best)
			}
			ms.Best = &b
		} else if hasBest {
			ms.Best = &best
		}
		m.record(ms)
		if m.OnKept != nil {
			m.OnKept(p.Name, t.MergeSHA)
		}
		return
	}
	ms.Note = reason + "; " + note
	m.record(ms)
	if t.Kind == "revert" {
		m.C.Person.Ask(core.Ask{Kind: "question", Ref: "revert-bad:" + t.ID, Project: p.Name,
			Title:  "Main is still worse after a revert in " + p.Name,
			Detail: reason + ". The Hive won't revert a revert on its own.", Options: []string{"ok"}})
		return
	}
	if _, err := m.Work.Revert(t, t.MergeSHA, reason); err != nil {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "revert", "project": p.Name, "text": "could not file a revert: " + err.Error()})
	}
}

// Sweeps returns notice runs, newest first.
func (m *Module) Sweeps() []Sweep {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Sweep
	for _, s := range m.sweeps {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// StartSweep runs the notice agent over a digest of the records. Unless forced,
// it skips when nothing has happened since the last sweep.
func (m *Module) StartSweep(force bool) (Sweep, error) {
	for _, s := range m.Sweeps() {
		if s.Status == "running" {
			return s, nil
		}
	}
	m.mu.Lock()
	since := m.lastSeen
	m.mu.Unlock()
	if !force && !m.anythingNew(since) {
		return Sweep{}, fmt.Errorf("nothing new since the last sweep")
	}
	id := "sweep-" + core.NewID()
	dir, err := m.Agents.Scratch(id)
	if err != nil {
		return Sweep{}, err
	}
	spec := m.Agents.Spec(id, "sweep", "", dir, "Sweep the records in $HIVE_RUN/digest.md for the next real gains, write sweep.md, file asks with evidence, and finish.")
	spec.Inputs = map[string]string{"digest.md": m.Digest(since, "")}
	if _, err := m.C.Runs.Start(spec); err != nil {
		return Sweep{}, err
	}
	s := Sweep{ID: id, Run: id, Status: "running", Started: time.Now().UTC()}
	m.C.J.MustAppend("sweep.saved", s)
	return s, nil
}

func (m *Module) anythingNew(since int64) bool {
	for _, e := range m.C.J.Since(since) {
		switch e.Kind {
		case "score.measured", "task.saved", "run.turn_ended", "chat.posted":
			return true
		}
	}
	return false
}

// TickSweeps finishes sweeps whose run has ended.
func (m *Module) TickSweeps() {
	for _, s := range m.Sweeps() {
		if s.Status != "running" {
			continue
		}
		r, ok := m.C.Runs.Get(s.Run)
		if !ok || !r.Terminal() {
			continue
		}
		report, _ := core.SafeRead(filepath.Join(m.C.WS.RunBox(r.ID), "sweep.md"), 1<<20)
		dir := filepath.Join(m.C.WS.Hive(), "improve", "sweeps")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, s.ID+".md"), report, 0o644)
		for _, t := range m.Work.List(func(t work.Task) bool { return t.From == r.ID }) {
			s.Asks = append(s.Asks, t.ID)
		}
		s.Status, s.Report, s.Ended = r.Status, core.Clip(string(report), 2000), time.Now().UTC()
		m.C.J.MustAppend("sweep.saved", s)
	}
}

// Digest summarizes scores, reverts, failures, chats and notes since seq,
// across every project, or from project only alone when only is set.
func (m *Module) Digest(since int64, only string) string {
	mine := func(project string) bool { return only == "" || project == only }
	var b strings.Builder
	b.WriteString("# Digest for the improvement sweep\n\n")
	for _, p := range m.C.Projects.List() {
		if !mine(p.Name) {
			continue
		}
		fmt.Fprintf(&b, "## Project %s (%s)\n\n", p.Name, p.Repo)
		ms := m.Scores(p.Name)
		if len(ms) > 10 {
			ms = ms[len(ms)-10:]
		}
		if best, ok := m.Best(p.Name); ok {
			fmt.Fprintf(&b, "Best kept score: %g\n\n", best)
		}
		b.WriteString("Recent measurements of main:\n")
		for _, x := range ms {
			sc := "-"
			if x.Score != nil {
				sc = fmt.Sprint(*x.Score)
			}
			fmt.Fprintf(&b, "- %s %s score=%s kept=%v %s\n", x.At.Format("01-02 15:04"), core.Short(x.SHA), sc, x.Kept, core.Clip(x.Note, 200))
		}
		b.WriteString("\nWork:\n")
		for i, t := range m.Work.List(func(t work.Task) bool { return t.Project == p.Name }) {
			if i >= 25 {
				break
			}
			fmt.Fprintf(&b, "- [%s] %s (%s) %s\n", t.Stage, t.Label(), t.ID, core.Clip(t.Reason+" "+t.Lesson, 300))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Runs that stopped, blocked or were flagged\n\n")
	for i, r := range m.C.Runs.List(func(r *core.Run) bool {
		return mine(r.Project) && (r.Status == core.StatusBlocked || r.Status == core.StatusQuiet || r.Status == core.StatusStopped || r.Retries > 0)
	}) {
		if i >= 20 {
			break
		}
		fmt.Fprintf(&b, "- %s %s [%s] %s\n", r.Role, r.ID, r.Status, core.Clip(r.Reason, 300))
	}
	b.WriteString("\n## Agents' notes\n\n")
	for i, r := range m.C.Runs.List(func(r *core.Run) bool { return mine(r.Project) }) {
		if i >= 30 {
			break
		}
		if notes, err := core.SafeRead(filepath.Join(m.C.WS.RunBox(r.ID), "notes.md"), 4000); err == nil && len(strings.TrimSpace(string(notes))) > 0 {
			fmt.Fprintf(&b, "### %s %s\n%s\n\n", r.Role, r.ID, core.Clip(string(notes), 1500))
		}
	}
	b.WriteString("## Recent chat\n\n")
	for _, ch := range m.Chat.Chats() {
		if !mine(ch.Project) {
			continue
		}
		msgs := m.Chat.Messages(ch.ID)
		if len(msgs) > 12 {
			msgs = msgs[len(msgs)-12:]
		}
		for _, msg := range msgs {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", ch.ID, msg.Name, core.Clip(msg.Text, 240))
		}
	}
	return b.String()
}
