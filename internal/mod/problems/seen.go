package problems

import (
	"fmt"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Sighting is one task's run meeting a failure, a failing test by its key,
// recorded where the Hive hands the failure back so the next run to meet it
// learns who met it before without asking.
type Sighting struct {
	Key     string    `json:"key"`
	Project string    `json:"project"`
	Run     string    `json:"run"`
	Task    string    `json:"task"`
	At      time.Time `json:"at"`
}

// seenFor is how long a sighting counts: a failure met a fortnight ago is
// history, not a neighbour.
const seenFor = 14 * 24 * time.Hour

// seenMax is the most failures one hand-back is marked with. A check broken
// by its environment fails dozens of tests at once (#9f32ad7); those say
// nothing about each test, and the board mustn't fill with them.
const seenMax = 8

// TestKey is one name for a failing test, whichever way it was read: the
// local tests' "pkg TestX" and a forge log's "pkg::TestX" meet as one.
func TestKey(k string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(k, "::", " ")), " ")
}

func (m *Module) applySeen(s Sighting) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := s.Project + "\x00" + s.Key
	cut := time.Now().Add(-seenFor)
	var kept []Sighting
	for _, o := range m.seen[k] {
		if o.At.After(cut) && o.Task != s.Task {
			kept = append(kept, o)
		}
	}
	if s.At.After(cut) {
		kept = append(kept, s)
	}
	m.seen[k] = kept
}

// sightings are the recent sightings of key in project by tasks besides task,
// oldest first.
func (m *Module) sightings(project, key, task string) []Sighting {
	m.mu.Lock()
	defer m.mu.Unlock()
	cut := time.Now().Add(-seenFor)
	var out []Sighting
	for _, s := range m.seen[project+"\x00"+key] {
		if s.Task != task && s.At.After(cut) {
			out = append(out, s)
		}
	}
	return out
}

// Seen records that run, working task in project, was handed the failing
// tests keys, and returns the lines to hand it with them: for each one
// another task met lately, who met it (to message) and its board item. A
// failure met by a second task goes on the board by itself, posted as from
// the first to meet it; a later one adds to it. "" when none was met before.
func (m *Module) Seen(project, run, task string, keys []string) string {
	seen := map[string]bool{}
	var mine []string
	for _, k := range keys {
		if k = TestKey(k); k != "" && !strings.HasSuffix(k, " !") && !seen[k] {
			seen[k] = true
			mine = append(mine, k)
		}
	}
	if len(mine) == 0 || len(mine) > seenMax {
		return ""
	}
	var b strings.Builder
	for _, k := range mine {
		before := m.sightings(project, k, task)
		m.C.J.Append("problem.seen", Sighting{Key: k, Project: project, Run: run, Task: task, At: time.Now().UTC()})
		if len(before) == 0 {
			continue
		}
		item := m.onBoard(project, k, run, task, before)
		var who []string
		for i := len(before) - 1; i >= 0 && len(who) < 3; i-- {
			s := before[i]
			who = append(who, fmt.Sprintf("%s (task %s, %s ago)", s.Run, s.Task, ago(s.At)))
		}
		more := ""
		if n := len(before) - len(who); n > 0 {
			more = fmt.Sprintf(" and %d more", n)
		}
		fmt.Fprintf(&b, "- `%s`: also seen by %s%s", k, strings.Join(who, ", "), more)
		if item != "" {
			b.WriteString("; board " + item)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "Other runs met these failures lately. Ask them what they found (`hive agent message --to <run>`), and when you find the cause, say it on the board item (`hive agent problem --id <id> --also \"...\"`, or `--solved \"how\"`):\n\n" + b.String()
}

// onBoard is the id of the open board item for failing test key in project,
// posted now if there is none, with run's sighting added to it: "" when it
// can't be saved.
func (m *Module) onBoard(project, key, run, task string, before []Sighting) string {
	for _, p := range m.List(func(p Problem) bool { return p.Open() && p.Project == project && p.Key == key }) {
		if !strings.Contains(p.Text, run) && !containsRun(p.Notes, run) {
			p.Notes = append(p.Notes, Note{At: time.Now().UTC(), Run: run, Role: "worker", Text: "met it too, on task " + task})
			p.Last = time.Now().UTC()
			m.C.J.Append("problem.saved", p)
		}
		return p.ID
	}
	first := before[0]
	var runs []string
	for _, s := range before {
		runs = append(runs, s.Run+" (task "+s.Task+")")
	}
	runs = append(runs, run+" (task "+task+")")
	now := time.Now().UTC()
	p := Problem{ID: "p-" + core.NewID(), At: now, Last: now, Run: first.Run, Role: "worker", Project: project, Key: key, Where: "tests",
		Text: core.Clip("The test `"+key+"` failed for more than one task: "+strings.Join(runs, ", ")+". Put on the board by the Hive when the second met it; whoever finds the cause says so here.", 2000)}
	if _, err := m.C.J.Append("problem.saved", p); err != nil {
		return ""
	}
	return p.ID
}

func containsRun(notes []Note, run string) bool {
	for _, n := range notes {
		if n.Run == run {
			return true
		}
	}
	return false
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
