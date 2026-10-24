// Package improve closes the loop: measure main after every merge, keep or
// revert against the best score the project has kept, and sweep the records
// for the next real gain.
package improve

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	Best    *float64  `json:"best,omitempty"`    // best kept after this measure
	Version string    `json:"version,omitempty"` // the score's version (Effects.ScoreVersion); a new one starts a new baseline
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
	// Feedback lists agents' open feedback for the projects mine admits.
	Feedback func(mine func(project string) bool) string

	mu       sync.Mutex
	scores   map[string][]Measurement
	sweeps   map[string]*Sweep
	queue    chan work.Task
	lastSeen int64 // journal seq at the last sweep start
	retryAt  map[string]time.Time
	spells   map[string]spell     // task id -> its current stage and since when
	took     map[string][]float64 // task stage -> seconds its recent spells took
	stalls   map[string]*Stall

	stepbacks map[string]*StepBack
	busy      map[string]bool
	alerted   map[string]string // project -> the step-back alert last raised
	startMu   sync.Mutex        // one step-back starts at a time
}

// New loads measurements and sweeps.
func New(c *core.Core, a *agents.Agents, w *work.Module, ch *chat.Module) *Module {
	m := &Module{C: c, Agents: a, Work: w, Chat: ch, scores: map[string][]Measurement{}, sweeps: map[string]*Sweep{}, queue: make(chan work.Task, 64), retryAt: map[string]time.Time{},
		spells: map[string]spell{}, took: map[string][]float64{}, stalls: map[string]*Stall{}, stepbacks: map[string]*StepBack{}, busy: map[string]bool{}, alerted: map[string]string{}}
	apply := func(e core.Event) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.applyStall(e)
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
	k, ok := m.kept(project)
	if !ok {
		return 0, false
	}
	return *k.Best, true
}

// kept is the last kept measurement with a best score.
func (m *Module) kept(project string) (Measurement, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.scores[project]
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Kept && ms[i].Best != nil {
			return ms[i], true
		}
	}
	return Measurement{}, false
}

// version is the score's version at sha, or "" with no score.
func (m *Module) version(p core.Project, sha string, cfg core.ProjectConfig) string {
	if cfg.Score == nil {
		return ""
	}
	return m.C.Effects.ScoreVersion(p, sha, *cfg.Score)
}

// versionOf is x's score version, worked out at its commit when x was
// recorded before measurements carried one.
func (m *Module) versionOf(p core.Project, x Measurement) string {
	if x.Version != "" {
		return x.Version
	}
	return m.version(p, x.SHA, m.C.Effects.Config(p, x.SHA))
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
	m.Watch()
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
	ms := Measurement{Project: p.Name, SHA: main, TestsOK: ok, Score: score, Note: "start: " + note, Version: m.version(p, main, cfg)}
	m.warnUnlisted(p, cfg)
	if ok && (score != nil || cfg.Score == nil) {
		ms.Kept, ms.Best = true, score
		m.record(ms)
		return
	}
	m.noStart(p, ms)
}

// noStart records a start with no number and raises the alert. Once it has
// failed more than once at one commit, the project's lead gets it as work,
// once per commit: a retry alone won't fix a command whose output can't be read.
func (m *Module) noStart(p core.Project, ms Measurement) {
	m.record(ms)
	note := strings.TrimPrefix(ms.Note, "start: ")
	text, key := baselineAlert(p.Name, ms.TestsOK, note)
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "baseline", "project": p.Name, "key": key, "text": text})
	n := 0
	for _, x := range m.Scores(p.Name) {
		if x.SHA == ms.SHA && !x.Kept && x.Task == "" {
			n++
		}
	}
	if n < 2 {
		return
	}
	head := "Main of " + p.Name + " at " + core.Short(ms.SHA) + " has failed"
	for _, x := range m.Chat.Messages("project:" + p.Name) {
		if x.From == "improve" && strings.HasPrefix(x.Text, head) {
			return // told already at this commit
		}
	}
	m.Chat.EnsureProject(p.Name)
	m.Chat.Post("project:"+p.Name, "", "improve", head+" to give a starting score more than once, so the keep-or-revert loop can't judge any change. "+
		"Look at the output below, fix the test or score command in .hive/project.json (the score is the last word of the output, or the one line that is a number alone), and ask the person if you can't:\n"+core.Clip(note, 1500))
}

// warnUnlisted raises an alert when a score is set with no evaluator paths:
// only a ./ file its command names is then protected from a candidate, not a
// script that file calls, a test or its data.
func (m *Module) warnUnlisted(p core.Project, cfg core.ProjectConfig) {
	if cfg.Score != nil && len(cfg.Score.Paths) == 0 {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "score-paths", "ref": "score-paths:" + p.Name, "project": p.Name,
			"text": p.Name + "'s score lists no evaluator files in its `paths`, so a change could alter how it's scored; list the files the score reads in .hive/project.json"})
	}
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
	k, hasBest := m.kept(p.Name)
	best := 0.0
	if hasBest {
		best = *k.Best
	}
	ms := Measurement{Project: p.Name, SHA: t.MergeSHA, Task: t.ID, TestsOK: ok, Score: score, Note: note, Version: m.version(p, t.MergeSHA, cfg)}
	if !hasBest || m.versionOf(p, k) != ms.Version {
		m.warnUnlisted(p, cfg)
		if hasBest {
			// The score itself changed, so this merge starts a new baseline.
			hasBest, ms.Note = false, "the score changed, so this starts a new baseline; "+note
		}
	}
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
		case "score.measured", "task.saved", "run.turn_ended", "chat.posted", "feedback.saved":
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
// across every project but the hidden (private or isolated) ones, or from
// project only alone when only is set. A hidden project's records reach only
// its own step-back.
func (m *Module) Digest(since int64, only string) string {
	isolated := map[string]bool{}
	for _, p := range m.C.Projects.List() {
		isolated[p.Name] = p.Hidden()
	}
	mine := func(project string) bool { return project == only || only == "" && !isolated[project] }
	var b strings.Builder
	b.WriteString("# Digest for the improvement sweep\n\n")
	for _, p := range m.C.Projects.List() {
		if !mine(p.Name) {
			continue
		}
		fmt.Fprintf(&b, "## Project %s (%s)\n\n", p.Name, p.Repo)
		main, err := m.C.Effects.Sync(p)
		if err != nil || main == "" {
			main = p.Branch
		}
		goals := m.C.Effects.ReadFile(p, main, ".hive/goals.md")
		if strings.TrimSpace(goals) == "" {
			goals = p.Goals
		}
		fmt.Fprintf(&b, "Goals:\n%s\n\n", core.Clip(strings.TrimSpace(goals), 1500))
		if sc := m.C.Effects.Config(p, main).Score; sc != nil {
			fmt.Fprintf(&b, "Score: `%s`, %s is better.\n\n", sc.Command, sc.Direction)
		} else {
			b.WriteString("Score: none defined.\n\n")
		}
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
	b.WriteString("\n## Stalls the watcher acted on (limit = max(stall_floor_seconds, 3 x p90 of the stage's recent durations))\n\n")
	for i, s := range m.Stalls() {
		if i >= 20 {
			break
		}
		fmt.Fprintf(&b, "- %s %s in %s: limit %.0fs from %d samples; %s\n", s.At.Format("01-02 15:04"), s.What, s.Stage, s.Limit, s.Samples, s.Did)
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
	if m.Feedback != nil {
		if fb := m.Feedback(mine); fb != "" {
			b.WriteString("## Agents' feedback on the Hive, still open\n\n" + fb + "\n")
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

// baselineAlert gives the text and dedup key of a start with no number. A
// missing program is keyed on its plain-words cause; any other failure on the
// whole note (the clipped, one-line check output) with digits blanked, so a
// repeat with fresh timings refreshes one alert while a different failure is a
// new one, even under a long fixed header like pytest's session banner.
func baselineAlert(project string, testsOK bool, note string) (text, key string) {
	kind := "test"
	if testsOK {
		kind = "score"
	}
	if why := MissingTool(project, kind, note); why != "" {
		return why + " Measuring again in 10 minutes.", why
	}
	return "the start of " + project + " has no number yet: " + core.Clip(note, 200) + "; measuring again in 10 minutes", kind + ": " + failureCause(note)
}

var digits = regexp.MustCompile(`\d+`)

// failureCause is a short digest of a failure note with runs of digits
// blanked, so timings and counts don't make a new cause.
func failureCause(note string) string {
	sum := sha256.Sum256([]byte(digits.ReplaceAllString(strings.Join(strings.Fields(note), " "), "#")))
	return hex.EncodeToString(sum[:8])
}

// notFound matches the shell's program-not-found lines (dash "sh: 1: X: not
// found", bash "bash: line 1: X: command not found") and the line Hive's own
// guard prints when a program lookup finds nothing.
var notFound = regexp.MustCompile(`(?m)(?:^|\s)(?:\S*/)?(?:ba|da)?sh: (?:line )?(?:\d+: )?([^\s:]+): (?:command )?not found|hive: missing program: ([^\n]+)`)

// MissingTool says in plain words which program the test or score command
// (kind) could not start, and how to get it, or "" when the output shows no
// such failure. A name starting with "-" means the command began with an empty
// program.
func MissingTool(project, kind, out string) string {
	m := notFound.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	so := "so " + project + "'s tests can't run"
	if kind == "score" {
		so = "so " + project + "'s score can't be measured"
	}
	if tool := m[1] + m[2]; !strings.HasPrefix(tool, "-") {
		return tool + " isn't installed, " + so + ". " + getIt(tool)
	}
	return "the " + kind + " command for " + project + " started with no program (an interpreter variable came out empty, so the interpreter it looks for isn't installed), " + so + ". " + getIt("the interpreter it needs")
}

// getIt says how the person gets a missing program onto the machine: install
// it themselves, or ask the lead, whose agent may be able to install it into
// the Hive's own tools or ask a warden to run the install.
func getIt(tool string) string {
	return "Install " + tool + " on this machine, or ask the lead in chat: an agent may be able to install it for the Hive's checks, or ask a warden to run the install for you."
}
