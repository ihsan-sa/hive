package work

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Joining: a brief an agent files whose ask matches an open task that no
// worker has pushed for yet (filed, queued or being worked on) joins that task
// as added context instead of starting a second worker on the same thing. A
// match is a failing test both name in their title or done line,
// a near-identical title, or the same named files under a similar title.
// Pushed tasks aren't joined: their worker is done, so the added ask would be
// lost. The person's asks and the Hive's own (trials) are always
// filed as they come; an agent that means separate work says apart.

var testName = regexp.MustCompile(`\b(?:Test|Benchmark|Fuzz)[A-Z][A-Za-z0-9_]*`)

// match says why brief b is the same ask as task t, or "" when it isn't.
func match(b Brief, t Task) string {
	mine := names(testName, b.Title+"\n"+b.Done)
	for _, n := range names(testName, t.Title+"\n"+t.Done+"\n"+strings.Join(t.Joined, "\n")) {
		if slices.Contains(mine, n) {
			return "both are about " + n
		}
	}
	sim := likeness(b.Title, t.Title)
	if sim >= 0.8 { // "add left-pad" and "add right-pad" share 4 of 6 words and are two asks
		return "the titles are nearly the same"
	}
	if f := files(b.Title + "\n" + b.Done + "\n" + b.Known); sim >= 0.3 && len(f) > 0 && slices.Equal(f, files(t.Title+"\n"+t.Done+"\n"+t.Known)) {
		return "both name " + strings.Join(f, ", ")
	}
	return ""
}

// names are the distinct matches of re in s, sorted.
func names(re *regexp.Regexp, s string) []string {
	out := re.FindAllString(s, -1)
	slices.Sort(out)
	return slices.Compact(out)
}

// files are the file paths (a last part with an extension) a text names.
func files(s string) []string {
	var out []string
	for _, p := range names(pathLike, s) {
		p = strings.TrimRight(strings.TrimPrefix(p, "./"), ".")
		if i := strings.LastIndex(p, "/"); i >= 0 && strings.Contains(p[i+1:], ".") {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// stop are words too common in titles to make two of them alike.
var stop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "from": true, "that": true, "this": true, "into": true, "its": true, "are": true, "not": true, "when": true, "one": true, "all": true, "has": true, "but": true, "out": true, "own": true, "again": true, "instead": true}

// likeness is the share of words two titles have in common (Jaccard).
func likeness(a, b string) float64 {
	words := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			if len(w) > 2 && !stop[w] {
				out[w] = true
			}
		}
		return out
	}
	x, y := words(a), words(b)
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	both := 0
	for w := range x {
		if y[w] {
			both++
		}
	}
	return float64(both) / float64(len(x)+len(y)-both)
}

// Join is one brief that joined a task: who filed it, why it matched and the
// context it added. Chat and Thread are set when a lead filed it, so the
// thread it came from hears how the task ends too.
type Join struct {
	From   string    `json:"from"`
	At     time.Time `json:"at"`
	Why    string    `json:"why"`
	Added  string    `json:"added"`
	Chat   string    `json:"chat,omitempty"`
	Thread string    `json:"thread,omitempty"`
	// Task and Issue are set when the ask was a task of its own batched into
	// this one (batch.go).
	Task  string `json:"task,omitempty"`
	Issue int    `json:"issue,omitempty"`
}

// JoinedBy reports whether a brief run filed joined t. Tasks joined before
// Joins was kept are read from their log.
func (t Task) JoinedBy(run string) bool {
	for _, j := range t.Joins {
		if j.From == run {
			return true
		}
	}
	for _, e := range t.Log {
		if e.Text == joinedNote+run || strings.HasPrefix(e.Text, joinedNote+run+":") {
			return true
		}
	}
	return false
}

const joinedNote = "joined by a brief from "

// fullJoins is how many joined briefs add their whole context to a task's
// Known; later ones add a line each, so a hot task's brief stays readable.
// Every one is still posted whole on the issue.
const fullJoins = 6

// joinable reports whether a new brief may join t: open work on the
// project that no worker has pushed for yet, taken the same way (a gain or
// an unclear brief is never folded into a fix, nor a fix into a gain), and
// not the filer's own task.
func joinable(t Task, b Brief, own string) bool {
	return t.Kind == "work" && t.Project == b.Project && t.ID != own && t.Model == "" && b.fix() == t.Fix() &&
		(t.Stage == StageNew || t.Queued() || t.Stage == StageWorking)
}

// fix says the brief is clearly a fix: it says so and names the failure, as
// File requires of one, or it says no aim and names something broken.
func (b Brief) fix() bool {
	aim, why := b.aim()
	return aim == AimFix && why != ""
}

// join adds b to the oldest open task it matches and returns that task and
// why, or ok false when there is none and b is to be filed on its own. lead
// says a lead filed it, whose thread then follows the task. A task a step
// holds is waited on briefly, and all of them for a few seconds at most, as
// every other agent's brief waits on filing meanwhile.
func (m *Module) join(b Brief, own string, lead bool) (Task, string, bool) {
	open := m.List(func(t Task) bool { return joinable(t, b, own) })
	slices.Reverse(open) // oldest first, so a third brief joins the first
	end := time.Now().Add(4 * time.Second)
	for _, o := range open {
		why := match(b, o)
		if why == "" || m.workerEnded(o) || !m.claimWithin(o.ID, min(2*time.Second, time.Until(end))) {
			continue
		}
		t, ok := m.Get(o.ID) // afresh, now no step holds it
		if !ok || !joinable(t, b, own) || m.workerEnded(t) {
			m.release(o.ID)
			continue
		}
		j := Join{From: b.From, Why: why}
		if lead {
			j.Chat, j.Thread = b.Chat, b.Thread
		}
		added := m.addJoin(&t, j, b.Title, b.Done, b.Known, "joined here since "+why)
		m.save(&t, fmt.Sprintf("%s%s: %s", joinedNote, b.From, why))
		if t.Stage == StageWorking && t.Worker != "" {
			m.C.Runs.Send(t.Worker, "Another brief was filed for the same ask as your task, so it joins yours rather than starting a second worker. Cover it too, or say in your account what you left out:\n\n"+added)
		}
		m.release(o.ID)
		return t, why, true
	}
	return Task{}, "", false
}

// addJoin adds an ask (title, done, known) to t as j, how, for the brief
// and the issue, and returns the text added. The caller saves t.
func (m *Module) addJoin(t *Task, j Join, title, done, known, how string) string {
	added := fmt.Sprintf("Also asked by %s (%s; %s): %s", j.From, time.Now().UTC().Format("15:04Z"), how, core.Clip(title, 300))
	line := added
	if done != "" {
		added += "\nDone looks like: " + core.Clip(done, 1500)
	}
	if known != "" {
		added += "\nKnown: " + core.Clip(known, 3000)
	}
	if len(t.Joins) >= fullJoins {
		line += " (its whole ask is on the task's issue)"
	} else {
		line = added
	}
	j.At, j.Added = time.Now().UTC(), added
	t.Known = strings.TrimSpace(t.Known + "\n\n" + line)
	t.Joined = append(t.Joined, core.Clip(title, 300)+"\n"+core.Clip(done, 1500))
	t.Joins = append(t.Joins, j)
	return added
}

// workerEnded says t's worker has finished its run, so a brief joining it now
// would reach a worker that no longer reads its messages.
func (m *Module) workerEnded(t Task) bool {
	if t.Stage != StageWorking || t.Worker == "" {
		return false
	}
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && r.Terminal()
}

// postJoins puts each joined brief not yet on t's issue there as a comment,
// so the issue shows every ask its work covers. Each carries its own marker,
// so one posted before a restart lost the count is edited, not posted again.
// A comment that fails is tried on the next step.
func (m *Module) postJoins(t *Task, p core.Project) {
	for t.Issue.Number != 0 && t.JoinsPosted < len(t.Joins) {
		n := t.JoinsPosted
		err := m.C.Effects.Forge.StatusComment(p.Repo, t.Issue.Number, fmt.Sprintf("<!-- hive-joined:%s:%d -->", t.ID, n), "Another brief joined this task:\n\n"+t.Joins[n].Added)
		if rl := (*core.RateLimited)(nil); errors.As(err, &rl) {
			m.limited(rl)
		}
		if err != nil {
			return
		}
		t.JoinsPosted++
		m.save(t, fmt.Sprintf("posted the brief %s joined on issue #%d", t.Joins[n].From, t.Issue.Number))
	}
}

// notify tells t's thread, and the thread of each lead whose brief joined
// it, how t went. A helper's spawner hears it too: a lead through its thread,
// any other agent still running as a message, since a worker that split its
// work among helpers has no thread of its own to read.
func (m *Module) notify(t *Task, text string) {
	m.toSpawner(t, text)
	m.notifyThreads(t, text)
}

// notifyThreads is notify without the message to a helper's spawner, for news
// the spawner already has.
func (m *Module) notifyThreads(t *Task, text string) { m.tell(t, text, m.Chat.Notify) }

// post puts how t went in the same threads without waking their leads, for
// an outcome that needs nothing of them (a merge); a helper's spawner still
// hears it. A merge of a module built as a project of its own and not yet
// installed still wakes them: the lead installs it (install_module
// --project, lead.md).
func (m *Module) post(t *Task, text string) {
	if m.moduleToInstall(t) {
		m.notify(t, text)
		return
	}
	m.toSpawner(t, text)
	m.tell(t, text, func(c, th, s string) { m.Chat.Record(c, th, s) })
}

// moduleToInstall says t's head carries a module.json at its root while no
// module of its project's name is installed.
func (m *Module) moduleToInstall(t *Task) bool {
	if t.SHA == "" || core.Exists(filepath.Join(m.C.WS.Modules(), t.Project)) {
		return false
	}
	_, err := core.Git(m.C.WS.Project(t.Project), "cat-file", "-e", t.SHA+":module.json")
	return err == nil
}

// toSpawner tells a helper's spawner, when it isn't a lead and still runs.
func (m *Module) toSpawner(t *Task, text string) {
	if t.Kind == "helper" {
		if r, ok := m.C.Runs.Get(t.From); ok && r.Role != "lead" && (!r.Terminal() || r.Persistent) {
			m.C.Runs.Send(r.ID, fmt.Sprintf("[your helper's task %s] %s", t.ID, text))
		}
	}
}

// tell sends text to t's thread and each joined lead's thread once.
func (m *Module) tell(t *Task, text string, send func(chat, thread, text string)) {
	send(t.Chat, t.Thread, text)
	seen := map[[2]string]bool{{t.Chat, t.Thread}: true}
	for _, j := range t.Joins {
		if k := [2]string{j.Chat, j.Thread}; j.Thread != "" && !seen[k] {
			seen[k] = true
			send(j.Chat, j.Thread, text)
		}
	}
}

// AddKnown adds text to task id's Known, from a module rather than a brief
// (improve's dip while an earlier one is looked into), and tells its worker
// when one is on it. Only a task a worker will still read takes it: new,
// queued, or working with its worker live. It is false otherwise (gone,
// finished, past its worker in landing, review or merge, where no one would
// read it), or when a step holds it past a few seconds.
func (m *Module) AddKnown(id, from, text string) (Task, bool) {
	if !m.claimWithin(id, 4*time.Second) {
		return Task{}, false
	}
	defer m.release(id)
	t, ok := m.Get(id)
	readable := t.Stage == StageNew || t.Queued() || (t.Stage == StageWorking && !m.workerEnded(t))
	if !ok || !readable {
		return Task{}, false
	}
	t.Known = strings.TrimSpace(t.Known + "\n\n" + text)
	m.save(&t, "context added by "+from)
	if t.Stage == StageWorking && t.Worker != "" {
		m.C.Runs.Send(t.Worker, "More for your task, from "+from+":\n\n"+text)
	}
	return t, true
}
