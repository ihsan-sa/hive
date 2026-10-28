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
		test := strings.TrimPrefix(t.Title, flakyTitle)
		if strings.ContainsAny(test, ", ") {
			return // one task for several tests: each still files its own
		}
		k := flakeKey(t.Project, test)
		m.fixes[k] = t.ID
		// The lift is the fix's merge, once: a later save of the same task
		// (a note, a revert) mustn't drop the reds seen since.
		if t.MergeSHA != "" && !m.fixMerged[t.ID] {
			m.fixMerged[t.ID] = true
			if e.At.After(m.lifted[k]) {
				m.lifted[k] = e.At
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
	if e.Decode(&c) != nil || c.OK || c.CantRun != "" {
		return
	}
	if c.Kind != "test" && c.Kind != "retest" && c.Kind != "before" {
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
		m.sighted[k] = append(m.sighted[k], s)
	}
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
	ev = slices.DeleteFunc(slices.Clone(ev), func(s Sighting) bool { return !m.outside(s, task) })
	var why []string
	if i := slices.IndexFunc(ev, func(s Sighting) bool { return s.Main }); i >= 0 {
		why = append(why, "failed on main at "+core.Short(ev[i].SHA))
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

// fileQuarantined gives each quarantined test with no open fix task one,
// and takes back a waiting one whose test is no longer quarantined and was
// taken down by a run that is no sighting (brokenRun) or reads the whole
// tree (core.WholeTree): its quarantine came from reading those as flakes.
func (m *Module) fileQuarantined() {
	q := m.Quarantine("")
	m.withdrawUnsighted(q)
	for _, f := range q {
		if t, ok := m.Work.Get(f.Task); ok && f.Task != "" && !t.Terminal() {
			continue
		}
		title := flakyTitle + f.Test
		if len(m.Work.List(func(t work.Task) bool { return t.Project == f.Project && t.Title == title && !t.Terminal() })) > 0 {
			continue
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
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "flake", "project": f.Project, "sha": f.Test, "text": "quarantined the flaky " + f.Test + " in " + f.Project + ": it " + f.Why})
		m.Work.File(work.Brief{Project: f.Project, From: "improve", Title: title,
			Done:  "The test passes every time on every platform the forge's checks run: it no longer depends on timing, order or shared state, and running it many times (go test -count=50 -run on it) never fails. Once this merges the quarantine on it is lifted, so its next red sends a PR back again.",
			Known: "The test is quarantined (a known flake): a forge check failing only it doesn't send a PR back. It " + f.Why + ". Where it failed:\n" + strings.Join(ev, "\n")})
	}
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
		k := flakeKey(t.Project, strings.TrimPrefix(t.Title, flakyTitle))
		if !unsighted[k] || held[k] {
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
