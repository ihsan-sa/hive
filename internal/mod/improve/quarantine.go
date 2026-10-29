package improve

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// quarantineWindow is how far back a forge failure counts toward a test's
// quarantine.
var quarantineWindow = 24 * time.Hour

// maxSightedPerRun is the most tests one red run is taken as sightings of.
// A flake takes down a test or two; a run that fails more was broken by its
// environment (its folder gone, a full disk) or by the change itself, and
// reading it as that many flakes would quarantine every test it took down,
// hiding real regressions, and file a fix brief for each.
const maxSightedPerRun = 10

// brokenRunSigns are output lines that say a run's environment was broken,
// not its tests: no test it failed is a sighting.
var brokenRunSigns = []string{
	"getcwd() failed",
	"cannot determine current directory",
	"must be run in a work tree",
}

// brokenRun reports whether output shows a run broken by its environment.
func brokenRun(output string) bool {
	low := strings.ToLower(output)
	return slices.ContainsFunc(brokenRunSigns, func(s string) bool { return strings.Contains(low, strings.ToLower(s)) })
}

// flakyTitle starts the title of the task that fixes a flaky test.
const flakyTitle = "Fix the flaky "

// A Sighting is one red of a test in the forge's checks or the Hive's own
// test check (Check "local test"): on a PR's head, on main, or on a head
// whose re-run then passed.
type Sighting struct {
	At     time.Time `json:"at"`
	Task   string    `json:"task,omitempty"`
	Branch string    `json:"branch,omitempty"`
	SHA    string    `json:"sha,omitempty"`
	Check  string    `json:"check,omitempty"`
	URL    string    `json:"url,omitempty"`
	Main   bool      `json:"main,omitempty"`
	Rerun  bool      `json:"rerun,omitempty"` // passed when run again
}

// A Flake is a quarantined test, keyed package::test: seen failing in the
// last quarantineWindow on main or on two or more unrelated branches. A pass
// on a re-run is evidence for that branch, never a quarantine on its own: a
// change's own code can fail once and pass the next time. A forge check
// that fails only quarantined tests doesn't send a PR back or hold its
// merge, judged on evidence from outside that PR's task (QuarantinedTests).
// Task fixes it; its merge lifts the quarantine, and only later reds count
// again.
type Flake struct {
	Project   string     `json:"project"`
	Test      string     `json:"test"`
	Why       string     `json:"why"`
	Evidence  []Sighting `json:"evidence"`
	Task      string     `json:"task,omitempty"`
	TaskStage string     `json:"task_stage,omitempty"`
}

func flakeKey(project, test string) string { return project + "\x00" + test }

// applyQuarantine takes in a journal event's sightings and fix tasks; m.mu is held.
func (m *Module) applyQuarantine(e core.Event) {
	switch e.Kind {
	case "forge.tests_failed", "forge.flaked":
		var x struct {
			Project, Task, Branch, SHA, Check, URL string
			Main                                   bool
			Tests                                  []string
		}
		if e.Decode(&x) != nil {
			return
		}
		if len(x.Tests) > maxSightedPerRun {
			m.unsight(x.Project, x.Tests)
			return
		}
		if time.Since(e.At) > quarantineWindow {
			return
		}
		for _, test := range x.Tests {
			k := flakeKey(x.Project, test)
			if x.URL != "" && slices.ContainsFunc(m.sighted[k], func(s Sighting) bool { return s.URL == x.URL }) {
				continue
			}
			m.sighted[k] = append(m.sighted[k], Sighting{At: e.At, Task: x.Task, Branch: x.Branch, SHA: x.SHA, Check: x.Check, URL: x.URL, Main: x.Main, Rerun: e.Kind == "forge.flaked"})
		}
	case "effect.checked":
		m.sightLocal(e)
	case "task.saved":
		var t work.Task
		if e.Decode(&t) != nil {
			return
		}
		if t.RevertOf != "" {
			m.kin[t.ID] = t.RevertOf
		}
		if !strings.HasPrefix(t.Title, flakyTitle) {
			return
		}
		// The lift is the fix's merge, once: a later save of the same task
		// (a note, a revert) mustn't drop the reds seen since.
		lift := t.MergeSHA != "" && !m.fixMerged[t.ID]
		if lift {
			m.fixMerged[t.ID] = true
		}
		for _, test := range flakeTests(t) {
			k := flakeKey(t.Project, test)
			m.fixes[k] = t.ID
			if lift && e.At.After(m.lifted[k]) {
				m.lifted[k], m.liftedBy[k] = e.At, t.MergeSHA
			}
		}
	}
}

// sightLocal takes in a red of the Hive's own test check: on a task's head
// or on main, the same evidence as the forge's, and often the first, since a
// busy machine shows a timing flake before CI does. A check on any other
// commit (a gate's or step-back's candidate) is no sighting, nor is a run
// broken by its environment or one failing more than maxSightedPerRun
// tests; m.mu is held.
func (m *Module) sightLocal(e core.Event) {
	var c core.Check
	if e.Decode(&c) != nil || c.CantRun != "" {
		return
	}
	if c.Kind != "test" && c.Kind != "retest" && c.Kind != "before" {
		return
	}
	if c.OK {
		// A green on main is what makes a red there a flake (flake): the
		// same commit, or main between two reds, passing the test.
		if _, main := m.merged[c.SHA]; (main || c.Kind == "before") && time.Since(c.At) <= quarantineWindow {
			gs := slices.DeleteFunc(m.greens[c.Project], func(g Sighting) bool { return time.Since(g.At) > quarantineWindow })
			m.greens[c.Project] = append(gs, Sighting{At: c.At, SHA: c.SHA, Check: "local " + c.Kind, Main: true})
		}
		return
	}
	keys := c.Keys
	if keys == nil && len(c.Failed) > 0 {
		keys, _ = core.FailedTestKeys(c.FullOutput()) // recorded before checks named packages
	}
	keys = slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return !strings.Contains(k, "::") })
	if len(keys) == 0 {
		return
	}
	// An older record is read only for what it says of the run, so a
	// replay doesn't read every red's full log.
	recent := time.Since(c.At) <= quarantineWindow
	if len(keys) > maxSightedPerRun || brokenRun(c.Output) || (recent && brokenRun(c.FullOutput())) {
		m.unsight(c.Project, keys)
		return
	}
	if !recent {
		return
	}
	s := Sighting{At: c.At, SHA: c.SHA, Check: "local " + c.Kind}
	_, s.Main = m.merged[c.SHA]
	s.Main = s.Main || c.Kind == "before" // main just before a merge
	if !s.Main {
		var ids []string
		for id, hs := range m.heads {
			if slices.Contains(hs, c.SHA) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return
		}
		slices.Sort(ids)
		s.Task = ids[0]
	}
	for _, test := range keys {
		k := flakeKey(c.Project, test)
		if slices.ContainsFunc(m.sighted[k], func(o Sighting) bool { return o.SHA == s.SHA && o.Check == s.Check }) {
			continue
		}
		// A local check runs the commit itself, so a red on one without the
		// fix (a head cut before it merged) says nothing of the fixed test.
		if fix := m.liftedBy[k]; fix != "" && !m.contains(c.Project, c.SHA, fix) {
			continue
		}
		m.sighted[k] = append(m.sighted[k], s)
	}
}

// contains says sha has commit fix in its history; if git can't tell (one
// of them isn't fetched), it does, so a red is never dropped on a guess.
func (m *Module) contains(project, sha, fix string) bool {
	dir := m.C.WS.Project(project)
	if _, err := core.Git(dir, "merge-base", "--is-ancestor", fix, sha); err == nil {
		return true
	}
	for _, c := range []string{sha, fix} {
		if _, err := core.Git(dir, "cat-file", "-e", c+"^{commit}"); err != nil {
			return true
		}
	}
	return false
}

// unsight notes project's tests taken down by a run that is no sighting, so
// a waiting fix brief filed on such a run is taken back; m.mu is held.
func (m *Module) unsight(project string, tests []string) {
	for _, t := range tests {
		m.unsighted[flakeKey(project, t)] = true
	}
}

// related reports whether one task follows up or reverts the other, so
// their branches carry the same change; m.mu is held.
func (m *Module) related(a, b string) bool {
	return m.kin[a] == b || m.kin[b] == a || (m.kin[a] != "" && m.kin[a] == m.kin[b])
}

// outside reports whether sighting s is evidence for task's PR: not from
// task itself, a task related to it, or a head task had. Every sighting is
// for "" (the dashboard's view); m.mu is held.
func (m *Module) outside(s Sighting, task string) bool {
	return task == "" || s.Task == "" || (s.Task != task && !m.related(s.Task, task) && !slices.Contains(m.heads[task], s.SHA))
}

// flake is the quarantine entry for k on evidence from outside task, if its
// sightings make one; m.mu is held.
func (m *Module) flake(k, task string) (Flake, bool) {
	project, test, _ := strings.Cut(k, "\x00")
	if core.WholeTree(test, m.module(project)) {
		// Its reds are the tree's content (a branch's unformatted file), so
		// two branches failing it is no flake; nor is it ever excused.
		delete(m.sighted, k)
		m.unsighted[k] = true
		return Flake{}, false
	}
	var ev []Sighting
	for _, s := range m.sighted[k] {
		if time.Since(s.At) < quarantineWindow && s.At.After(m.lifted[k]) {
			ev = append(ev, s)
		}
	}
	if len(ev) == 0 {
		delete(m.sighted, k) // aged out or fixed: nothing left to keep
		return Flake{}, false
	}
	m.sighted[k] = ev
	ev = slices.DeleteFunc(slices.Clone(ev), func(s Sighting) bool { return !m.outside(s, task) || m.mainRed(project, k, s) })
	var why []string
	if i := slices.IndexFunc(ev, func(s Sighting) bool { return s.Main }); i >= 0 {
		why = append(why, "failed on main at "+core.Short(ev[i].SHA)+" and passed there too")
	}
	// Unrelated branches: two tasks, neither following up or reverting the
	// other, red on different heads, neither of which the other task had.
	var tasks []string
	var heads []string
	for _, s := range ev {
		if s.Task == "" || s.Main || slices.Contains(tasks, s.Task) {
			continue
		}
		if slices.ContainsFunc(tasks, func(o string) bool { return m.related(s.Task, o) || slices.Contains(m.heads[o], s.SHA) }) ||
			slices.ContainsFunc(heads, func(h string) bool { return h == s.SHA || slices.Contains(m.heads[s.Task], h) }) {
			continue
		}
		tasks, heads = append(tasks, s.Task), append(heads, s.SHA)
	}
	if len(tasks) >= 2 {
		why = append(why, fmt.Sprintf("failed on %d unrelated branches (tasks %s)", len(tasks), strings.Join(tasks, ", ")))
	}
	if len(why) == 0 {
		return Flake{}, false
	}
	return Flake{Project: project, Test: test, Why: strings.Join(why, "; ") + " in the last " + quarantineWindow.String(), Evidence: ev, Task: m.fixes[k]}, true
}

// mainRed reports whether red s of test k is main being red on it, not a
// flake: a red on main that main never passed (not at the same commit, a
// re-run's pass, nor between two of its reds on main), so every main reading
// since it is red too, or a branch's red while main was so. One commit broke
// the test, and its fix is a revert or a fix of main, not one flake brief per
// test it took down (failures.go's redBefore files that); m.mu is held.
func (m *Module) mainRed(project, k string, s Sighting) bool {
	var reds []Sighting
	passed := slices.Clone(m.greens[project])
	for _, o := range m.sighted[k] {
		switch {
		case o.Main && o.Rerun:
			passed = append(passed, o)
		case o.Main:
			reds = append(reds, o)
		}
	}
	// flaky says red r on main has a pass to show it isn't main's own.
	flaky := func(r Sighting) bool {
		return slices.ContainsFunc(passed, func(g Sighting) bool {
			return g.SHA == r.SHA || slices.ContainsFunc(reds, func(o Sighting) bool {
				return o.SHA != g.SHA && (r.At.Before(g.At) && g.At.Before(o.At) || o.At.Before(g.At) && g.At.Before(r.At))
			})
		})
	}
	if s.Main {
		return !s.Rerun && !flaky(s)
	}
	// A branch red while main was red on the test, not passing it since.
	return slices.ContainsFunc(reds, func(r Sighting) bool {
		return !flaky(r) && !s.At.Before(r.At) && !slices.ContainsFunc(passed, func(g Sighting) bool { return g.At.After(r.At) && g.At.Before(s.At) })
	})
}

// Quarantine is project's quarantined tests ("" for every project's), by
// project and test, each with its fix task.
func (m *Module) Quarantine(project string) []Flake { return m.quarantine(project, "") }

// module is project's Go module path, read from main's go.mod once; m.mu
// is held.
func (m *Module) module(project string) string {
	mod, ok := m.modules[project]
	if !ok {
		mod = core.GoModule(m.C.Effects.ReadFile(core.Project{Name: project}, "HEAD", "go.mod"))
		m.modules[project] = mod
	}
	return mod
}

// quarantine is Quarantine on evidence from outside task.
func (m *Module) quarantine(project, task string) []Flake {
	m.mu.Lock()
	var out []Flake
	for k := range m.sighted {
		if project != "" && !strings.HasPrefix(k, project+"\x00") {
			continue
		}
		if f, ok := m.flake(k, task); ok {
			out = append(out, f)
		}
	}
	m.mu.Unlock()
	for i := range out {
		if t, ok := m.Work.Get(out[i].Task); ok && out[i].Task != "" {
			out[i].TaskStage = t.Stage
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Project+"\x00"+out[i].Test < out[j].Project+"\x00"+out[j].Test
	})
	return out
}

// QuarantinedTests names project's tests quarantined on evidence from
// outside task, so no task's own reds or re-runs excuse it
// (core.Effects.Quarantined).
func (m *Module) QuarantinedTests(project, task string) []string {
	var names []string
	for _, f := range m.quarantine(project, task) {
		names = append(names, f.Test)
	}
	return names
}

// fileQuarantined gives the quarantined tests no task answers (answered) a
// fix brief, one for the tests quarantined on the same commits' reds, and
// takes back a waiting one whose test is no longer quarantined and was
// taken down by a run that is no sighting (brokenRun) or reads the whole
// tree (core.WholeTree): its quarantine came from reading those as flakes.
func (m *Module) fileQuarantined() {
	q := m.Quarantine("")
	m.withdrawUnsighted(q)
	var groups [][]Flake
	at := map[string]int{}
	for _, f := range q {
		if t, ok := m.Work.Get(f.Task); ok && f.Task != "" && !t.Terminal() {
			continue
		}
		if m.answered(f.Project, f.Test, f.Evidence) {
			continue
		}
		var shas []string
		for _, s := range f.Evidence {
			shas = append(shas, s.SHA)
		}
		slices.Sort(shas)
		key := f.Project + "\x00" + strings.Join(slices.Compact(shas), " ")
		i, ok := at[key]
		if !ok {
			i, at[key] = len(groups), len(groups)
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], f)
	}
	for _, g := range groups {
		m.fileFlakes(g)
	}
}

// fileFlakes files one fix brief for quarantined tests seen red together.
func (m *Module) fileFlakes(g []Flake) {
	f := g[0]
	var names []string
	for _, o := range g {
		names = append(names, o.Test)
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "flake", "project": o.Project, "sha": o.Test, "text": "quarantined the flaky " + o.Test + " in " + o.Project + ": it " + o.Why})
	}
	var ev []string
	for _, s := range f.Evidence {
		where := "task " + s.Task
		if s.Branch != "" {
			where += " (" + s.Branch + ")"
		}
		if s.Main {
			where = "main"
		}
		line := fmt.Sprintf("- %s: %s at %s, check %s", s.At.Format("01-02 15:04"), where, core.Short(s.SHA), s.Check)
		if s.Rerun {
			line += ", passed when run again"
		}
		if s.URL != "" {
			line += " " + s.URL
		}
		ev = append(ev, line)
	}
	known := "The test is quarantined (a known flake): a forge check failing only it doesn't send a PR back. It " + f.Why
	if len(g) > 1 {
		known = "The tests are quarantined (known flakes), seen red together: a forge check failing only them doesn't send a PR back. Each " + f.Why
	}
	m.Work.File(work.Brief{Project: f.Project, From: "improve", Title: core.Clip(flakyTitle+strings.Join(names, ", "), 120),
		Done:  "The test passes every time on every platform the forge's checks run: it no longer depends on timing, order or shared state, and running it many times (" + m.manyTimes(core.Project{Name: f.Project}, "HEAD") + ") never fails. Once this merges the quarantine on it is lifted, so its next red sends a PR back again.",
		Known: known + ". Where it failed:\n" + strings.Join(ev, "\n") + "\n\n" + testsLine + strings.Join(names, ", ")})
}

// manyTimes says how to run a flaky test many times in project p at sha:
// go test's -count on a Go project, its own test command on any other.
func (m *Module) manyTimes(p core.Project, sha string) string {
	test := strings.TrimSpace(m.C.Effects.Config(p, sha).Test)
	if test == "" || strings.Contains(test, "go test") {
		return "go test -count=50 -run on it"
	}
	return "the project's own test command, " + core.Clip(test, 200) + ", narrowed to it"
}

// testsLine starts the line of a flake brief's known context that names its
// tests, which its title, clipped, may not hold.
const testsLine = "Tests: "

// mainAgain frames the title of a red main's fix brief (failures.go's
// redBefore): "Make <tests> pass on main again".
const mainAgain = " pass on main again"

// flakeTests names the tests t was filed for, if it is a flake brief or a
// red main's.
func flakeTests(t work.Task) []string {
	var list string
	switch {
	case strings.HasPrefix(t.Title, flakyTitle):
		list = strings.TrimPrefix(t.Title, flakyTitle)
	case strings.HasPrefix(t.Title, "Make ") && strings.HasSuffix(t.Title, mainAgain):
		list = strings.TrimSuffix(strings.TrimPrefix(t.Title, "Make "), mainAgain)
	default:
		return nil
	}
	for _, l := range strings.Split(t.Known, "\n") {
		if rest, ok := strings.CutPrefix(l, testsLine); ok {
			list += ", " + rest
		}
	}
	var names []string
	for _, n := range strings.Split(list, ", ") {
		if n = strings.TrimSpace(n); n != "" && !strings.HasSuffix(n, "…") {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// sameTest says a and b name one test, either with its package or bare.
func sameTest(a, b string) bool {
	return a == b || strings.HasSuffix(a, "::"+b) || strings.HasSuffix(b, "::"+a)
}

// answered reports whether a task on project already answers test's reds:
// an open flake or red-main brief naming it, among however many tests, or
// one a lead or improve stopped (or its worker left blocked) with no red of
// the test since from a commit it hadn't been seen red on. A withdrawal
// stands until there is new evidence, not until the next pass refiles it.
func (m *Module) answered(project, test string, reds []Sighting) bool {
	return len(m.Work.List(func(t work.Task) bool {
		if t.Project != project || !slices.ContainsFunc(flakeTests(t), func(n string) bool { return sameTest(test, n) }) {
			return false
		}
		if !t.Terminal() {
			return true
		}
		if t.Stage == work.StageDone {
			return false // finished: a red since is the test's again
		}
		return !slices.ContainsFunc(reds, func(s Sighting) bool {
			return s.At.After(t.Updated) && !strings.Contains(t.Known, core.Short(s.SHA)) &&
				!slices.ContainsFunc(reds, func(o Sighting) bool { return o.SHA == s.SHA && !o.At.After(t.Updated) })
		})
	})) > 0
}

// withdrawUnsighted takes back improve's fix briefs for tests not in q that
// a run read as no sighting took down: a waiting one is withdrawn, and one a
// worker already started is retracted, its worker stopped, so it holds no
// worker slot on a test that was never flaky.
func (m *Module) withdrawUnsighted(q []Flake) {
	m.mu.Lock()
	unsighted := maps.Clone(m.unsighted)
	m.mu.Unlock()
	if len(unsighted) == 0 {
		return
	}
	settled := []string{work.StageStopped, work.StageDone, work.StageMerged, work.StageMerging, work.StageReverting, work.StageRecording}
	held := map[string]bool{}
	for _, f := range q {
		held[flakeKey(f.Project, f.Test)] = true
	}
	for _, t := range m.Work.List(func(t work.Task) bool {
		return t.From == "improve" && strings.HasPrefix(t.Title, flakyTitle) && !slices.Contains(settled, t.Stage)
	}) {
		tests := flakeTests(t)
		if len(tests) == 0 || slices.ContainsFunc(tests, func(test string) bool {
			k := flakeKey(t.Project, test)
			return !unsighted[k] || held[k]
		}) {
			continue
		}
		why := "its quarantine came from a check run broken by its environment or failing more than " + fmt.Sprint(maxSightedPerRun) + " tests at once, or it is a test of the whole tree whose reds are a branch's own content, which is no evidence the test is flaky; the test is no longer quarantined"
		r := core.Run{ID: "improve", Project: t.Project}
		if t.Stage == work.StageNew || t.Queued() {
			m.Work.Withdraw(r, t.ID, "", why) // a worker that just started keeps it until the next pass
			continue
		}
		m.Work.Retract(r, t.ID, why) // refused once it is on its way to merge or past it
	}
}
