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

// quarantineWindow is how far back a forge failure counts toward a test's
// quarantine.
var quarantineWindow = 24 * time.Hour

// flakyTitle starts the title of the task that fixes a flaky test.
const flakyTitle = "Fix the flaky "

// A Sighting is one red of a test in the forge's checks: on a PR's head, on
// main, or on a head whose re-run then passed.
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

// A Flake is a quarantined test: seen failing in the last quarantineWindow
// on main, on two or more unrelated branches, or on a head where it passed
// when run again. A forge check that fails only quarantined tests doesn't
// send a PR back or hold its merge. Task fixes it; its merge lifts the
// quarantine, and only later reds count again.
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
		if e.Decode(&x) != nil || time.Since(e.At) > quarantineWindow {
			return
		}
		for _, test := range x.Tests {
			k := flakeKey(x.Project, test)
			if x.URL != "" && slices.ContainsFunc(m.sighted[k], func(s Sighting) bool { return s.URL == x.URL }) {
				continue
			}
			m.sighted[k] = append(m.sighted[k], Sighting{At: e.At, Task: x.Task, Branch: x.Branch, SHA: x.SHA, Check: x.Check, URL: x.URL, Main: x.Main, Rerun: e.Kind == "forge.flaked"})
		}
	case "task.saved":
		var t work.Task
		if e.Decode(&t) != nil || !strings.HasPrefix(t.Title, flakyTitle) {
			return
		}
		test := strings.TrimPrefix(t.Title, flakyTitle)
		if strings.ContainsAny(test, ", ") {
			return // one task for several tests: each still files its own
		}
		k := flakeKey(t.Project, test)
		m.fixes[k] = t.ID
		if t.MergeSHA != "" && e.At.After(m.lifted[k]) {
			m.lifted[k] = e.At
		}
	}
}

// flake is the quarantine entry for k, if its sightings make one; m.mu is held.
func (m *Module) flake(k string) (Flake, bool) {
	project, test, _ := strings.Cut(k, "\x00")
	var ev []Sighting
	for _, s := range m.sighted[k] {
		if time.Since(s.At) < quarantineWindow && s.At.After(m.lifted[k]) {
			ev = append(ev, s)
		}
	}
	m.sighted[k] = ev // the rest has aged out or was fixed
	var why []string
	if i := slices.IndexFunc(ev, func(s Sighting) bool { return s.Main }); i >= 0 {
		why = append(why, "failed on main at "+core.Short(ev[i].SHA))
	}
	if i := slices.IndexFunc(ev, func(s Sighting) bool { return s.Rerun }); i >= 0 {
		why = append(why, "passed when run again on "+core.Short(ev[i].SHA)+" (task "+ev[i].Task+")")
	}
	var tasks []string
	for _, s := range ev {
		if s.Task != "" && !s.Main && !slices.Contains(tasks, s.Task) {
			tasks = append(tasks, s.Task)
		}
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
func (m *Module) Quarantine(project string) []Flake {
	m.mu.Lock()
	var out []Flake
	for k := range m.sighted {
		if project != "" && !strings.HasPrefix(k, project+"\x00") {
			continue
		}
		if f, ok := m.flake(k); ok {
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

// QuarantinedTests names project's quarantined tests (core.Effects.Quarantined).
func (m *Module) QuarantinedTests(project string) []string {
	var names []string
	for _, f := range m.Quarantine(project) {
		names = append(names, f.Test)
	}
	return names
}

// fileQuarantined gives each quarantined test with no open fix task one.
func (m *Module) fileQuarantined() {
	for _, f := range m.Quarantine("") {
		if t, ok := m.Work.Get(f.Task); ok && f.Task != "" && !t.Terminal() {
			continue
		}
		title := flakyTitle + f.Test
		if len(m.Work.List(func(t work.Task) bool { return t.Project == f.Project && t.Title == title && !t.Terminal() })) > 0 {
			continue
		}
		var ev []string
		for _, s := range f.Evidence {
			where := "task " + s.Task + " (" + s.Branch + ")"
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
