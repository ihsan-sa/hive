// Package improve closes the loop: measure main after every merge, keep or
// revert against the best score the project has kept, and sweep the records
// for the next real gain.
package improve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	Project string   `json:"project"`
	SHA     string   `json:"sha"`
	Task    string   `json:"task,omitempty"`
	TestsOK bool     `json:"tests_ok"`
	Score   *float64 `json:"score,omitempty"`
	Kept    bool     `json:"kept"`
	// NoResult says the check couldn't start (a tool it calls is missing), so
	// this is neither kept nor a failure, and main is measured again later.
	NoResult bool      `json:"no_result,omitempty"`
	Best     *float64  `json:"best,omitempty"`    // best kept after this measure
	Version  string    `json:"version,omitempty"` // the score's version (Effects.ScoreVersion); a new one starts a new baseline
	Note     string    `json:"note"`
	At       time.Time `json:"at"`
	// Size is what the project's size command printed ("name value" lines),
	// for the Hive itself the core's size (docs/core.md): shown, never judged.
	Size map[string]float64 `json:"size,omitempty"`
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
	// Installs is the section on what installs sent the developer (the report
	// module), on the developer's own Hive; empty elsewhere.
	Installs func() string

	mu       sync.Mutex
	scores   map[string][]Measurement
	sweeps   map[string]*Sweep
	queue    chan work.Task
	lastSeen int64 // journal seq at the last sweep start
	retryAt  map[string]time.Time
	pending  map[string]held      // project -> its newest merge waiting on a missing tool
	spells   map[string]spell     // task id -> its current stage and since when
	took     map[string][]float64 // task stage -> seconds its recent spells took
	stalls   map[string]*Stall
	fails    map[string][]core.Check // project -> recent red test checks that name their failing tests
	heads    map[string][]string     // task id -> every head it had
	merged   map[string]time.Time    // merge sha -> when it merged

	stepbacks map[string]*StepBack
	busy      map[string]bool
	alerted   map[string]string // project -> the step-back alert last raised
	startMu   sync.Mutex        // one step-back starts at a time
}

// New loads measurements and sweeps.
func New(c *core.Core, a *agents.Agents, w *work.Module, ch *chat.Module) *Module {
	m := &Module{C: c, Agents: a, Work: w, Chat: ch, scores: map[string][]Measurement{}, sweeps: map[string]*Sweep{}, queue: make(chan work.Task, 64), retryAt: map[string]time.Time{}, pending: map[string]held{},
		spells: map[string]spell{}, took: map[string][]float64{}, stalls: map[string]*Stall{}, fails: map[string][]core.Check{}, heads: map[string][]string{}, merged: map[string]time.Time{}, stepbacks: map[string]*StepBack{}, busy: map[string]bool{}, alerted: map[string]string{}}
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
		case "effect.checked":
			var c core.Check
			if e.Decode(&c) == nil && c.Kind == "test" && !c.OK && len(c.Failed) > 0 && time.Since(c.At) < flakeWindow {
				fs := append(m.fails[c.Project], c)
				for len(fs) > 0 && time.Since(fs[0].At) > flakeWindow {
					fs = fs[1:]
				}
				m.fails[c.Project] = fs
			}
		case "effect.merged":
			var x struct {
				MergeSHA string `json:"merge_sha"`
			}
			if e.Decode(&x) == nil && x.MergeSHA != "" && time.Since(e.At) < flakeWindow {
				m.merged[x.MergeSHA] = e.At
			}
		case "task.saved":
			var t work.Task
			if e.Decode(&t) == nil {
				for _, sha := range []string{t.SHA, t.Checkpoint} {
					if sha != "" && !slices.Contains(m.heads[t.ID], sha) {
						m.heads[t.ID] = append(m.heads[t.ID], sha)
					}
				}
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
	work.Measured = m.measured
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
		if ms.SHA == sha && !ms.NoResult && (ms.Kept || ms.Task != "") {
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

// measure runs tests and the score on sha in clean checkouts. cant is the
// check that couldn't start for want of a tool (its CantRun set), which is no
// result either way; a check that did run clears the alert for its kind.
func (m *Module) measure(p core.Project, sha string) (ok bool, score *float64, note string, cant core.Check) {
	cfg := m.C.Effects.Config(p, sha)
	tc := m.C.Effects.RunCheck(p, sha, "test", cfg.Test)
	if tc.CantRun != "" {
		return false, nil, "tests " + tc.CantRun + ": " + core.Clip(tc.Output, 300), tc
	}
	m.Work.RanCheck(p.Name, "test")
	flaky := "" // a red that passed when what failed ran again
	if !tc.OK {
		sum, flake, rc := m.redTests(p, sha, cfg.Test, tc)
		if rc.CantRun != "" {
			rc.Kind = "test"
			return false, nil, "tests red, then " + rc.CantRun + " when what failed ran again: " + core.Clip(rc.Output, 300), rc
		}
		if !flake {
			return false, nil, "tests red: " + sum, cant
		}
		flaky = "tests flaky (red, then green when what failed ran again; filed to fix): " + sum + "; "
	}
	if cfg.Score == nil {
		if flaky != "" {
			return true, nil, flaky + "no score configured", cant
		}
		return true, nil, "tests green (no score configured)", cant
	}
	sc := m.C.Effects.RunCheck(p, sha, "score", cfg.Score.Command)
	if sc.CantRun != "" {
		return false, nil, "score " + sc.CantRun + ": " + core.Clip(sc.Output, 300), sc
	}
	m.Work.RanCheck(p.Name, "score")
	if !sc.OK || sc.Score == nil {
		return true, nil, flaky + "score did not produce a number: " + core.Clip(sc.Output, 600), cant
	}
	return true, sc.Score, flaky + fmt.Sprintf("score %g", *sc.Score), cant
}

// SelfSize is the Hive's own size command, run for a self project whose
// project.json names none while the script is in its tree.
const SelfSize = "sh tests/core-size.sh"

// size runs the project's size command (project.json "size") on sha in a
// clean checkout and reads each "name value" line it prints. It is nil when
// there is no command or it fails: a size is shown, never judged.
func (m *Module) size(p core.Project, sha string) map[string]float64 {
	var pc struct{ Size string }
	json.Unmarshal([]byte(m.C.Effects.ReadFile(p, sha, ".hive/project.json")), &pc)
	cmd := strings.TrimSpace(pc.Size)
	if cmd == "" && p.Self && m.C.Effects.ReadFile(p, sha, "tests/core-size.sh") != "" {
		cmd = SelfSize
	}
	if cmd == "" {
		return nil
	}
	c := m.C.Effects.RunCheck(p, sha, "size", cmd)
	if !c.OK || c.Log == "" {
		return nil
	}
	out, err := core.SafeRead(c.Log, 1<<20) // the full log: a check's Output is clipped to one line
	if err != nil {
		return nil
	}
	return sizes(string(out))
}

// sizes reads "name value" lines; any other line is left out.
func sizes(out string) map[string]float64 {
	var got map[string]float64
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if got == nil {
			got = map[string]float64{}
		}
		got[f[0]] = v
	}
	return got
}

// Tick measures the starting point of projects that have none yet. A red
// start is measured again until there is a number.
func (m *Module) Tick() {
	m.Watch()
	if _, err := m.C.Sandbox(); err != nil {
		return
	}
	m.releaseHeld()
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
	ok, score, note, cant := m.measure(p, main)
	ms := Measurement{Project: p.Name, SHA: main, TestsOK: ok, Score: score, Note: "start: " + note, Version: m.version(p, main, cfg), NoResult: cant.CantRun != ""}
	if ms.NoResult {
		m.noResult(p, ms, cant.Kind) // Tick measures again in 10 minutes
		return
	}
	m.warnUnlisted(p, cfg)
	ms.Size = m.size(p, main)
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
		if x.SHA == ms.SHA && !x.Kept && !x.NoResult && x.Task == "" {
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

// noResult records a measure whose check couldn't start, once per commit, and
// says which tool is missing, once while the project's alert is open. Nothing
// is kept or reverted over it.
func (m *Module) noResult(p core.Project, ms Measurement, kind string) {
	for _, x := range m.Scores(p.Name) {
		if x.SHA == ms.SHA && x.NoResult && x.Note == ms.Note {
			return // recorded and raised already at this commit
		}
	}
	m.record(ms)
	m.Work.RaiseCantRun(p.Name, kind,
		"main of "+p.Name+" at "+core.Short(ms.SHA)+" has no result: "+core.Clip(strings.TrimPrefix(ms.Note, "start: "), 200)+". That is a fault in the environment, not a verdict on the commit: nothing is reverted, and main is measured again every 10 minutes until it is fixed")
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

// retryNoResult is how long a merge whose check couldn't start waits before
// it is measured again.
var retryNoResult = 10 * time.Minute

// held is a merge waiting to be measured again once its missing tool is there.
type held struct {
	t   work.Task
	due time.Time
}

// hold keeps a merge whose check couldn't start for Tick to measure again in
// retryNoResult. Only the newest such merge of a project waits: once the tool
// is there, measuring one a later merge has superseded would judge a main
// that is gone. After a restart Recover finds them all again (their only
// records are no_result), and they are held here the same way.
func (m *Module) hold(p core.Project, t work.Task) {
	m.mu.Lock()
	old, ok := m.pending[p.Name]
	m.mu.Unlock()
	if ok && old.t.MergeSHA != t.MergeSHA && !m.after(p, t.MergeSHA, old.t.MergeSHA) {
		return // a later merge waits already
	}
	m.mu.Lock()
	m.pending[p.Name] = held{t: t, due: time.Now().Add(retryNoResult)}
	m.mu.Unlock()
}

// releaseHeld queues the held merges whose wait is over.
func (m *Module) releaseHeld() {
	m.mu.Lock()
	var due []work.Task
	for name, h := range m.pending {
		if time.Now().After(h.due) {
			due = append(due, h.t)
			delete(m.pending, name)
		}
	}
	m.mu.Unlock()
	for _, t := range due {
		go func() { m.queue <- t }()
	}
}

// after says merge a came after merge b on p's main.
func (m *Module) after(p core.Project, a, b string) bool {
	if a == b {
		return false
	}
	_, err := core.Git(m.C.WS.Project(p.Name), "merge-base", "--is-ancestor", b, a)
	return err == nil
}

// superseded says a merge that had no result has since been passed by a later
// merge on the same project that was measured, so it isn't measured any more.
func (m *Module) superseded(p core.Project, sha string) bool {
	var noResult bool
	var later []string
	for _, x := range m.Scores(p.Name) {
		switch {
		case x.SHA == sha:
			noResult = noResult || x.NoResult
		case x.Task != "" && !x.NoResult:
			later = append(later, x.SHA)
		}
	}
	return noResult && slices.ContainsFunc(later, func(l string) bool { return m.after(p, l, sha) })
}

// introduced says the merge itself brought in the command that calls the
// missing tool: the same command on main before it didn't name that tool. A
// misspelt tool is then the change's fault, and red, not a wait. A tool
// called from a script the command runs isn't seen, and still waits.
func (m *Module) introduced(p core.Project, sha string, cant core.Check) bool {
	parent, err := core.Git(m.C.WS.Project(p.Name), "rev-parse", "-q", "--verify", sha+"^")
	if err != nil || cant.Tool == "" {
		return false
	}
	command := func(at string) string {
		cfg := m.C.Effects.Config(p, at)
		if cant.Kind != "score" {
			return cfg.Test
		}
		if cfg.Score == nil {
			return ""
		}
		return cfg.Score.Command
	}
	names := func(cmd string) bool {
		return slices.Contains(strings.FieldsFunc(cmd, func(r rune) bool { return strings.ContainsRune(" \t\n;&|()<>`$\"'=", r) }), cant.Tool)
	}
	now, before := command(sha), command(strings.TrimSpace(parent))
	return now != before && names(now) && !names(before)
}

func (m *Module) measureLoop() {
	for t := range m.queue {
		m.afterMerge(t)
	}
}

// Recover re-queues merges whose measurement didn't happen (after a restart).
func (m *Module) Recover() {
	for _, t := range m.Work.List(func(t work.Task) bool { return t.MergeSHA != "" }) {
		if time.Since(t.Updated) >= 7*24*time.Hour {
			continue
		}
		for _, b := range t.Banks { // a checkpoint merged before the latest one
			if b.MergeSHA != t.MergeSHA && !m.measured(t.Project, b.MergeSHA) {
				c := t
				c.MergeSHA = b.MergeSHA
				m.queue <- c
			}
		}
		if !m.measured(t.Project, t.MergeSHA) {
			m.queue <- t
		}
	}
}

// afterMerge measures main at the merge and keeps or reverts.
func (m *Module) afterMerge(t work.Task) {
	p, ok := m.C.Projects.Get(t.Project)
	if !ok || m.measured(p.Name, t.MergeSHA) || m.superseded(p, t.MergeSHA) {
		return
	}
	if _, err := m.C.Effects.Sync(p); err != nil {
		go func() { time.Sleep(time.Minute); m.queue <- t }()
		return
	}
	cfg := m.C.Effects.Config(p, t.MergeSHA)
	ok, score, note, cant := m.measure(p, t.MergeSHA)
	switch {
	case cant.CantRun != "" && m.introduced(p, t.MergeSHA, cant) && cant.Kind == "score":
		ok, score, note = true, nil, "score did not produce a number: this merge's score command calls "+cant.Tool+", which isn't installed where checks run: "+note
	case cant.CantRun != "" && m.introduced(p, t.MergeSHA, cant):
		ok, note = false, "tests red: this merge's test command calls "+cant.Tool+", which isn't installed where checks run: "+note
	case cant.CantRun != "":
		m.noResult(p, Measurement{Project: p.Name, SHA: t.MergeSHA, Task: t.ID, Note: note, Version: m.version(p, t.MergeSHA, cfg), NoResult: true}, cant.Kind)
		m.hold(p, t)
		return
	}
	flake := ""
	if !ok {
		flake = m.seenElsewhere(p, t)
	}
	if !ok && flake == "" && cant.CantRun == "" {
		flake = m.redBefore(p, t, cfg.Test)
	}
	k, hasBest := m.kept(p.Name)
	best := 0.0
	if hasBest {
		best = *k.Best
	}
	ms := Measurement{Project: p.Name, SHA: t.MergeSHA, Task: t.ID, TestsOK: ok, Score: score, Note: note, Version: m.version(p, t.MergeSHA, cfg), Size: m.size(p, t.MergeSHA)}
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
	case !ok && flake != "":
		ms.Note = flake + "; not reverted; " + note
		m.record(ms)
		return
	case !ok:
		reason = "the tests went red after it merged: " + strings.TrimPrefix(note, "tests red: ")
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
	ms.Note = reason
	if ok {
		ms.Note += "; " + note
	}
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

// flakeWindow is how far back a test's failures elsewhere count as a flake.
const flakeWindow = 72 * time.Hour

// seenElsewhere is, for a merge whose red tests stayed red when what failed
// ran again (measure does that), what makes it a flake anyway: every test
// that failed also failed on main before this one merged. Only main's own
// checks count: a candidate's code can print any test name it likes. It
// raises an alert for one; "" means the merge broke them.
func (m *Module) seenElsewhere(p core.Project, t work.Task) string {
	c, ok := m.C.Effects.GetCheck(p.Name, t.MergeSHA, "retest")
	if !ok || c.OK {
		c, _ = m.C.Effects.GetCheck(p.Name, t.MergeSHA, "test")
	}
	where := m.failedBefore(p.Name, t, c.Failed, m.C.Effects.MainLine(p))
	if where == "" {
		return ""
	}
	flake := "flaky: " + strings.Join(c.Failed, ", ") + " failed twice after it merged, but also " + where + ", without this change"
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "flake", "project": p.Name, "sha": t.MergeSHA, "task": t.ID,
		"text": core.Clip(flake, 400) + " (" + p.Name + " at " + core.Short(t.MergeSHA) + "). Not reverted; the test wants steadying."})
	return flake
}

// failedBefore says where every one of names also failed at a commit on
// main that can't carry t's change: checked before t first merged (a
// checkpoint counts), and none of t's own heads or checkpoints.
// It returns "" when any of them failed only here.
func (m *Module) failedBefore(project string, t work.Task, names []string, main map[string]bool) string {
	if len(names) == 0 {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	merged, ok := m.merged[t.MergeSHA]
	if !ok {
		merged = time.Now()
		for _, c := range m.fails[project] {
			if c.SHA == t.MergeSHA && c.At.Before(merged) {
				merged = c.At // measured right after it merged
			}
		}
	}
	// A checkpoint merged earlier is part of the change too: only what was
	// checked before the task's first merge is without it.
	for _, b := range t.Banks {
		if at, ok := m.merged[b.MergeSHA]; ok && at.Before(merged) {
			merged = at
		}
	}
	var where []string
	for _, n := range names {
		found := ""
		for _, c := range m.fails[project] {
			if c.SHA != t.MergeSHA && main[c.SHA] && c.At.Before(merged) && !slices.Contains(m.heads[t.ID], c.SHA) && slices.Contains(c.Failed, n) {
				found = "at " + core.Short(c.SHA)
			}
		}
		if found == "" {
			return ""
		}
		if !slices.Contains(where, found) {
			where = append(where, found)
		}
	}
	return "failed " + strings.Join(where, ", ")
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
			kept := fmt.Sprint(x.Kept)
			if x.NoResult {
				kept = "no-result"
			}
			fmt.Fprintf(&b, "- %s %s score=%s kept=%s %s\n", x.At.Format("01-02 15:04"), core.Short(x.SHA), sc, kept, core.Clip(x.Note, 200))
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
	if m.Installs != nil && only == "" {
		b.WriteString(m.Installs())
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
