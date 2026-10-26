package improve

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A step-back asks whether a project's whole approach is right and what the
// simplest setup is that might beat it, then runs the setup the agent designs
// in a copy of the project and scores it. A win is a fault in the Hive's own
// setup, filed with both scores and what ran.

// Setup is the experiment a step-back designs: one harness session in a copy
// of the project at its start, with System appended to the harness's own
// prompt and Asks sent in turn. Nothing else is added.
type Setup struct {
	Why    string   `json:"why"`
	System string   `json:"system"`
	Asks   []string `json:"asks"`
	Model  string   `json:"model"`
}

// StepBack is one step-back on one project.
type StepBack struct {
	ID      string    `json:"id"`
	Project string    `json:"project"`
	Why     string    `json:"why"`
	Run     string    `json:"run"`           // the agent that steps back
	Exp     string    `json:"exp,omitempty"` // the experiment it designed
	Setup   *Setup    `json:"setup,omitempty"`
	Asked   int       `json:"asked,omitempty"`
	From    string    `json:"from"` // the project's start
	Best    float64   `json:"best"` // the Hive's best kept score when it began
	Since   time.Time `json:"since,omitempty"`
	Budget  float64   `json:"budget,omitempty"` // what the Hive spent on the project since the last step-back
	Score   *float64  `json:"score,omitempty"`
	Status  string    `json:"status"` // thinking | experimenting | scoring | won | lost | none | failed
	Note    string    `json:"note,omitempty"`
	Fault   string    `json:"fault,omitempty"` // the task or person's ask it filed
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

func (s StepBack) active() bool {
	return s.Status == "thinking" || s.Status == "experimenting" || s.Status == "scoring"
}

// StepBacks returns a project's step-backs ("" for all), oldest first.
func (m *Module) StepBacks(project string) []StepBack {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StepBack
	for _, s := range m.stepbacks {
		if project == "" || s.Project == project {
			out = append(out, *s)
		}
	}
	return out
}

func (m *Module) saveStepBack(s StepBack) { m.C.J.MustAppend("stepback.saved", s) }

// start is the project's first kept measure under the score that gave its
// best: the seed every setup starts from.
func (m *Module) start(p core.Project) (Measurement, bool) {
	k, _ := m.kept(p.Name)
	v := m.versionOf(p, k)
	for _, x := range m.Scores(p.Name) {
		if x.Kept && x.Score != nil && m.versionOf(p, x) == v {
			return x, true
		}
	}
	return Measurement{}, false
}

// RotateStepBacks starts a step-back on each project where the Hive merged
// something since its last one (the improve.stepback schedule).
func (m *Module) RotateStepBacks() string {
	var started []string
	for _, p := range m.C.Projects.List() {
		last := m.lastStepBack(p.Name)
		merged := slices.ContainsFunc(m.Scores(p.Name), func(x Measurement) bool { return x.Task != "" && !x.NoResult && x.At.After(last) })
		if merged && m.StartStepBack(p, "it is the regular step-back") == nil {
			started = append(started, p.Name)
		}
	}
	return "stepped back on: " + strings.Join(started, ", ")
}

func (m *Module) lastStepBack(project string) time.Time {
	var t time.Time
	for _, s := range m.StepBacks(project) {
		if s.Started.After(t) {
			t = s.Started
		}
	}
	return t
}

// hiveRuns are the Hive's own runs on a project made after t, newest first.
func (m *Module) hiveRuns(project string, t time.Time) []core.Run {
	return m.C.Runs.List(func(r *core.Run) bool {
		return r.Project == project && !strings.HasPrefix(r.ID, "stepback-") && r.Created.After(t)
	})
}

// plateau counts the merges measured since the best kept score last moved
// whose task was filed after the last step-back began. A task filed before
// it was already in flight and its runs were in that step-back's budget, so
// its merge landing later is not a new plateau.
func (m *Module) plateau(project string) int {
	n, since := 0, m.lastStepBack(project)
	var best *float64
	for _, x := range m.Scores(project) {
		if !x.Kept || x.Best == nil {
			continue
		}
		if best == nil || *x.Best != *best {
			n = 0
		} else if t, ok := m.Work.Get(x.Task); ok && t.Created.After(since) {
			n++
		}
		best = x.Best
	}
	return n
}

// StartStepBack starts the agent that steps back on p, in a copy of p at
// main. Every way it can't start is recorded and said in p's step-back alert.
func (m *Module) StartStepBack(p core.Project, why string) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	for _, s := range m.StepBacks(p.Name) {
		if s.active() {
			return fmt.Errorf("a step-back on %s is already running", p.Name)
		}
	}
	id := "stepback-" + core.NewID()
	s := StepBack{ID: id, Project: p.Name, Why: why, Run: id, Since: m.lastStepBack(p.Name), Status: "thinking", Started: time.Now().UTC()}
	s.Best, _ = m.Best(p.Name)
	for _, r := range m.hiveRuns(p.Name, s.Since) {
		s.Budget += r.Cost
	}
	st, ok := m.start(p)
	if !ok {
		return m.fail(s, "it has no score to beat")
	}
	s.From = st.SHA
	if s.Budget <= 0 {
		s.Status, s.Note, s.Ended = "none", "the Hive has spent nothing on it since the last step-back, so a setup has nothing to spend", time.Now().UTC()
		m.alert(p.Name, "the step-back on "+p.Name+" did not run: "+s.Note)
		m.saveStepBack(s)
		return nil
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return m.fail(s, "could not fetch the project: "+err.Error())
	}
	dir, err := m.C.Effects.NewCheckout(p, id, main)
	if err != nil {
		return m.fail(s, "could not make a copy of the project: "+err.Error())
	}
	cfg := m.C.Effects.Config(p, st.SHA)
	h, model := m.Agents.Model("worker", p.Name)
	better := "higher"
	if cfg.Score != nil && cfg.Score.Direction == "lower" {
		better = "lower"
	}
	brief := fmt.Sprintf(`Step back on project %s: %s.

The person's goal, in their words: %s

Its score (%s is better) started at %g, and the best the Hive has kept is %g. The records are in $HIVE_RUN/digest.md, the project at main is your working directory, and the Hive's own prompts and modules are in $HIVE_MODULES.

Ask two questions: is the whole approach right, and what is the simplest setup that might beat it?

If a setup is worth testing, write it to $HIVE_BOX/experiment.json as {"why": "...", "system": "...", "asks": ["..."], "model": "..."}. The Hive runs it once in a fresh copy of the project at its start, in a worker's sandbox: one %s session, "system" appended to the harness's own system prompt (empty for none), the "asks" sent in turn in that session, on "model" (empty for %s). Nothing else is added, and it can only report on its own copy. It may spend $%.2f, what the Hive has spent on this project since the last step-back. The copy is then scored with the project's score command, and if it beats %g the Hive files a fault against its own setup with both scores.

Write your reasoning to $HIVE_BOX/sweep.md, then finish.`, p.Name, why, p.Goals, better, *st.Score, s.Best, h, model, s.Budget, s.Best)
	spec := m.Agents.Spec(id, "stepback", p.Name, dir, brief)
	only := ""
	if p.Hidden() {
		only = p.Name
	}
	spec.Inputs, spec.Budget = map[string]string{"digest.md": m.Digest(0, only)}, s.Budget
	if _, err := m.C.Runs.Start(spec); err != nil {
		return m.fail(s, "could not start the step-back: "+err.Error())
	}
	m.saveStepBack(s)
	return nil
}

// TickStepBacks starts a step-back where the score has plateaued and moves
// each running one on.
func (m *Module) TickStepBacks() {
	if n := m.C.WS.LoadConfig().PlateauMerges; n > 0 {
		for _, p := range m.C.Projects.List() {
			if k := m.plateau(p.Name); k >= n {
				m.StartStepBack(p, fmt.Sprintf("its best score has not moved in %d merges", k))
			}
		}
	}
	for _, s := range m.StepBacks("") {
		if !s.active() {
			continue
		}
		m.mu.Lock()
		busy := m.busy[s.ID]
		m.busy[s.ID] = true
		m.mu.Unlock()
		if busy {
			continue
		}
		go func() {
			m.advance(s)
			m.mu.Lock()
			delete(m.busy, s.ID)
			m.mu.Unlock()
		}()
	}
}

func (m *Module) advance(s StepBack) {
	run := s.Run
	if s.Status != "thinking" {
		run = s.Exp
	}
	r, ok := m.C.Runs.Get(run)
	if ok && r.Status == core.StatusParked {
		m.alert(s.Project, fmt.Sprintf("the step-back on %s is waiting, and no other starts there until it ends (stop run %s to end it): %s", s.Project, r.ID, r.Reason))
	}
	if !ok || (!r.Terminal() && s.Status != "scoring") {
		return
	}
	if r.Status != core.StatusDone {
		m.fail(s, fmt.Sprintf("the %s ended %s: %s", map[bool]string{true: "step-back", false: "experiment"}[run == s.Run], r.Status, r.Reason))
		return
	}
	switch s.Status {
	case "thinking":
		raw, err := core.SafeRead(filepath.Join(m.C.WS.RunBox(r.ID), "experiment.json"), 1<<20)
		if errors.Is(err, os.ErrNotExist) {
			s.Status, s.Note, s.Ended = "none", "it designed no experiment", time.Now().UTC()
			m.saveStepBack(s)
			return
		}
		var set Setup
		if err := json.Unmarshal(raw, &set); err != nil || len(set.Asks) == 0 {
			m.fail(s, fmt.Sprintf("its experiment.json is not a setup with at least one ask (%v)", err))
			return
		}
		m.experiment(s, set)
	case "experimenting":
		if s.Asked < len(s.Setup.Asks) && r.Cost < s.Budget {
			s.Asked++
			m.saveStepBack(s)
			m.C.Runs.Reopen(r.ID, s.Setup.Asks[s.Asked-1])
			return
		}
		s.Status = "scoring"
		m.saveStepBack(s)
		fallthrough
	case "scoring":
		m.score(s, r)
	}
}

// experiment starts the setup in a fresh copy of the project at its start,
// as a worker in a worker's sandbox, seeing no other project.
func (m *Module) experiment(s StepBack, set Setup) {
	if s.Budget <= 0 { // saved before step-backs had a budget
		m.fail(s, "it has no budget to hold the experiment to")
		return
	}
	p, ok := m.C.Projects.Get(s.Project)
	if !ok {
		m.fail(s, "the project is gone")
		return
	}
	id := s.ID + "-exp"
	dir, err := m.C.Effects.NewCheckout(p, id, s.From)
	if err != nil {
		m.fail(s, "could not make a copy of the project: "+err.Error())
		return
	}
	h, model := m.Agents.Model("worker", p.Name)
	if set.Model != "" {
		model = set.Model
	}
	if _, ok := m.C.Runs.Get(id); !ok {
		spec := core.RunSpec{ID: id, Role: "worker", Name: "experiment", Project: p.Name, Checkout: dir, Harness: h, Model: model,
			System: set.System, Brief: set.Asks[0], Isolated: true, OneTurn: true, Budget: s.Budget}
		if _, err := m.C.Runs.Start(spec); err != nil {
			m.fail(s, "could not start the experiment: "+err.Error())
			return
		}
	}
	s.Exp, s.Setup, s.Asked, s.Status = id, &set, 1, "experimenting"
	m.saveStepBack(s)
}

// score scores what the experiment left with the project's score command and
// files a fault when it beats the Hive's best.
func (m *Module) score(s StepBack, r core.Run) {
	p, ok := m.C.Projects.Get(s.Project)
	if !ok {
		m.fail(s, "the project is gone")
		return
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		m.alert(s.Project, "the step-back on "+s.Project+" is waiting to score: could not fetch the project: "+err.Error())
		return // tried again next tick
	}
	cfg := m.C.Effects.Config(p, main)
	if cfg.Score == nil {
		m.fail(s, "the project no longer has a score command")
		return
	}
	sha, err := m.C.Effects.Snapshot(p, r, s.From)
	if errors.Is(err, core.ErrNoChanges) {
		sha, err = s.From, nil
	}
	if err != nil {
		m.fail(s, "could not take out what the experiment left: "+err.Error())
		return
	}
	ch, err := m.C.Effects.Classify(p, s.From, sha)
	if len(ch.Protected) > 0 {
		err = errors.New("it changed what scores it (" + strings.Join(ch.Protected, ", ") + ")")
	}
	if err != nil {
		m.fail(s, "the experiment's score can't count: "+err.Error())
		return
	}
	if k, _ := m.kept(p.Name); m.versionOf(p, k) != m.C.Effects.ScoreVersion(p, sha, *cfg.Score) {
		m.fail(s, "the score changed since the experiment's start, so its number can't be compared with the Hive's best")
		return
	}
	c := m.C.Effects.RunCheck(p, sha, "score", cfg.Score.Command)
	if !c.OK || c.Score == nil {
		m.fail(s, "the experiment's copy did not score: "+core.Clip(c.Output, 300))
		return
	}
	s.Score, s.Ended = c.Score, time.Now().UTC()
	s.Status, s.Note = "lost", fmt.Sprintf("the setup scored %g against the Hive's best %g", *c.Score, s.Best)
	if !cfg.Score.WithinTolerance(s.Best, *c.Score) {
		s.Status = "won"
		s.Fault = m.fileFault(s, p, r, sha, *cfg.Score)
	}
	m.saveStepBack(s)
}

// transcript is what a run said and did, each step clipped, to about n runes.
func (m *Module) transcript(r core.Run, n int) string {
	var b strings.Builder
	for turn := 1; turn <= r.Turn; turn++ {
		for _, ev := range m.C.Runs.ReadTurn(r, turn).Steps {
			if b.Len() > n {
				return b.String() + "> …\n"
			}
			b.WriteString("> " + core.Clip(ev.Tool+" "+ev.Text, 300) + "\n")
		}
	}
	return b.String()
}

// fileFault files the scores, the count of asks and the run ids against the
// Hive's own source, which becomes an issue on its forge that may be public.
// The setup and the transcripts were written after reading the project, so
// they go only to the task's worker, as a file on the host. A fault on an
// isolated project, or with no Hive source to file against, asks the person
// instead, and that local ask shows the setup.
//
// The commits the setup left (sha, from s.From) go to the worker too, as a
// patch it can apply: when the step-back ran on the Hive itself they are the
// change that already won, so the fault asks for them on main first.
func (m *Module) fileFault(s StepBack, p core.Project, exp core.Run, sha string, sc core.Score) string {
	var ids []string
	var shown []core.Run
	var spent float64
	for i, r := range m.hiveRuns(p.Name, s.Since) {
		spent += r.Cost
		if i < 8 {
			ids, shown = append(ids, r.ID), append(shown, r)
		}
	}
	set, _ := json.MarshalIndent(s.Setup, "", "  ")
	asks, better := 0, "higher"
	if s.Setup != nil {
		asks = len(s.Setup.Asks)
	}
	if sc.Direction == "lower" {
		better = "lower"
	}
	title := fmt.Sprintf("The Hive's own setup lost to a simpler one on %s: %g against %g", p.Name, *s.Score, s.Best)
	report := fmt.Sprintf(`A step-back on %s asked whether the Hive's approach is right and what the simplest setup is that might beat it, and ran its answer: one harness session sent %d ask(s).

From the project's start at %s, the setup scored %g. The best the Hive has kept is %g (%s is better, tolerance %g). The setup cost $%.2f; the Hive's runs on the project since the last step-back cost $%.2f.

The setup ran as %s; the Hive's latest runs on the project were %s.
`, p.Name, asks, core.Short(s.From), *s.Score, s.Best, better, sc.Tolerance, exp.Cost, spent, exp.ID, strings.Join(ids, ", "))
	for _, self := range m.C.Projects.List() {
		if self.Self && !p.Hidden() {
			transcripts := fmt.Sprintf("# The setup\n\n%s\n\n# What the setup did (run %s)\n\n%s\n# What the Hive's runs did, newest first\n\n", set, exp.ID, m.transcript(exp, 6000))
			for _, r := range shown {
				transcripts += fmt.Sprintf("## %s %s ($%.2f)\n\n%s\n", r.Role, r.ID, r.Cost, m.transcript(r, 1500))
			}
			known := report + "\nThe setup, and what each of those runs said and did, is in $HIVE_RUN/transcripts.md. Quote nothing from it in commits, the PR or your finish text: it can hold the project's own text."
			inputs := map[string]string{"transcripts.md": transcripts}
			done := "the prompt or module that held the Hive's agents back is changed, from what the transcripts show the simpler setup did that they didn't"
			if patch, n := m.leftPatch(p, s.From, sha); n > 0 && p.Name == self.Name {
				inputs["commits.patch"] = patch
				known += fmt.Sprintf("\n\nThe setup's %d commit(s), the change that scored %g, are in $HIVE_RUN/commits.patch (git format-patch from %s). They start from the project's start, not main: apply them with `git am -3 $HIVE_RUN/commits.patch` and resolve what main changed since.", n, *s.Score, core.Short(s.From))
				done = "the setup's commits in $HIVE_RUN/commits.patch are forward-ported onto main, keeping what still holds and saying in the account what was left out and why; then " + done
			} else if n > 0 {
				inputs["changes.patch"] = patch
				known += fmt.Sprintf("\n\nWhat the setup changed in its copy (%d commit(s)) is in $HIVE_RUN/changes.patch, to compare with what the Hive's runs did; it is the project's code, so quote none of it either.", n)
			}
			t, err := m.Work.File(work.Brief{Project: self.Name, Title: title, From: s.Run, Aim: work.AimGain, Why: "a simpler setup scored better; this aims to win that back", Known: known, Inputs: inputs, Done: done})
			if err == nil {
				return t.ID
			}
		}
	}
	return m.C.Person.Ask(core.Ask{Kind: "question", Ref: "stepback:" + s.ID, Project: p.Name, Title: title, Detail: report + "\nThe setup:\n\n" + string(set), Options: []string{"ok"}}).ID
}

// leftPatch is the commits from base to sha in p's host clone, as one
// format-patch mbox, and how many there are; none when there are none, git
// can't tell, or the patch is too large to hand over whole.
func (m *Module) leftPatch(p core.Project, base, sha string) (string, int) {
	if base == "" || sha == "" || base == sha {
		return "", 0
	}
	dir := m.C.WS.Project(p.Name)
	count, err := core.Git(dir, "rev-list", "--count", base+".."+sha)
	n, _ := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || n == 0 {
		return "", 0
	}
	patch, err := core.Git(dir, "format-patch", "--stdout", "--binary", "--no-signature", base+".."+sha)
	if err != nil || patch == "" || len(patch) > 1<<20 {
		return "", 0
	}
	return patch + "\n", n
}

// alert says how a project's step-back stands, as one alert that updates.
func (m *Module) alert(project, text string) {
	m.mu.Lock()
	same := m.alerted[project] == text
	m.alerted[project] = text
	m.mu.Unlock()
	if !same {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "stepback", "ref": "stepback:" + project, "project": project, "text": text})
	}
}

// fail ends a step-back and says why in its project's step-back alert.
func (m *Module) fail(s StepBack, why string) error {
	s.Status, s.Note, s.Ended = "failed", why, time.Now().UTC()
	m.alert(s.Project, "the step-back on "+s.Project+" could not finish: "+why) // first, so an ended step-back always shows its alert
	m.saveStepBack(s)
	return errors.New(why)
}
