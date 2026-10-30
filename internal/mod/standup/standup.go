// Package standup is the Hive's all-hands: on its schedule it gathers what
// each project got done since the last one (merged, reverted, stuck, in
// flight) and asks a lead to tell the person in plain words, with a
// show-and-tell for a hard problem solved or a breakthrough. Shared projects
// go to the lead's chat as one standup; a private or isolated project's go to
// its own chat, since its records reach only its own lead.
package standup

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Default is the job's schedule when config.json names none.
const Default = "daily 09:00"

// first is how far back the first standup ever looks.
const first = 24 * time.Hour

// most caps each list, so a busy day can't flood the lead's prompt.
const most = 40

// Module is the standup module.
type Module struct {
	C    *core.Core
	Work *work.Module
	Chat *chat.Module

	mu   sync.Mutex
	last map[string]time.Time // chat -> when its last standup was asked for
}

// New loads when each chat last had a standup.
func New(c *core.Core, w *work.Module, ch *chat.Module) *Module {
	m := &Module{C: c, Work: w, Chat: ch, last: map[string]time.Time{}}
	apply := func(e core.Event) {
		if e.Kind != "standup.asked" {
			return
		}
		var a struct{ Chat string }
		e.Decode(&a)
		m.mu.Lock()
		m.last[a.Chat] = e.At
		m.mu.Unlock()
	}
	c.J.ReplayExcept(apply, "task.saved", "run.created")
	c.J.Subscribe(apply)
	return m
}

// Run is the standup job: one ask per chat with anything to tell.
func (m *Module) Run() (string, error) {
	now := time.Now()
	chats := map[string][]core.Project{}
	for _, p := range m.C.Projects.List() {
		id := "lead"
		if p.Hidden() {
			id = "project:" + p.Name
			m.Chat.EnsureProject(p.Name)
		}
		chats[id] = append(chats[id], p)
	}
	ids := make([]string, 0, len(chats))
	for id := range chats {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var did []string
	for _, id := range ids {
		m.mu.Lock()
		since := m.last[id]
		m.mu.Unlock()
		if since.IsZero() {
			since = now.Add(-first)
		}
		facts, n := m.Facts(chats[id], since)
		if n == 0 {
			continue
		}
		shown := fmt.Sprintf("Standup: what got done since %s.", since.Local().Format("Mon 15:04"))
		if err := m.Chat.Ask(id, "standup", shown, ask+"\n\n"+facts); err != nil {
			did = append(did, id+": "+err.Error())
			continue
		}
		m.C.J.MustAppend("standup.asked", map[string]any{"chat": id, "since": since.UTC(), "tasks": n})
		did = append(did, fmt.Sprintf("%s: %d tasks", id, n))
	}
	if len(did) == 0 {
		return "nothing to tell", nil
	}
	return "asked " + strings.Join(did, "; "), nil
}

const ask = "Write the standup for the person: what each project below got done since the last one, in plain words they can read in a minute or two. The facts are the Hive's; each task's story is in $HIVE_KB/record/task/<id>.md and its worker's account in record/run/<worker>.md. Your reply is posted as the standup; reply exactly NOOP if nothing here is worth their time."

// Facts lists, for each of projects, the tasks merged, reverted, blocked or
// stopped since since, and what is in flight now; n counts the tasks that
// changed, so 0 means nothing to tell.
func (m *Module) Facts(projects []core.Project, since time.Time) (facts string, n int) {
	var b strings.Builder
	for _, p := range projects {
		var merged, reverted, stuck []string
		flight := map[string]int{}
		for _, t := range m.Work.List(func(t work.Task) bool { return t.Project == p.Name }) {
			changed := t.Updated.After(since)
			switch {
			case changed && t.MergeSHA != "" && t.Kind == "revert":
				reverted = append(reverted, line(t, t.Lesson))
			case changed && t.MergeSHA != "" && t.Kind != "record" && (t.Stage == work.StageMerged || t.Stage == work.StageDone):
				merged = append(merged, line(t, aim(t)))
			case changed && (t.Stage == work.StageBlocked || t.Stage == work.StageStopped):
				stuck = append(stuck, line(t, t.Stage+": "+t.Reason))
			case !t.Terminal() && t.Stage != work.StageMerged:
				flight[t.Stage]++
			}
		}
		if len(merged)+len(reverted)+len(stuck) == 0 {
			continue
		}
		n += len(merged) + len(reverted) + len(stuck)
		fmt.Fprintf(&b, "## %s (%s)\n\n", p.Name, p.Repo)
		list(&b, "Merged", merged)
		list(&b, "Reverted", reverted)
		list(&b, "Blocked or stopped", stuck)
		if len(flight) > 0 {
			var parts []string
			for _, s := range []string{work.StageWorking, work.StageLanding, work.StageTesting, work.StageReviewing, work.StageMerging, work.StagePerson, work.StageQueued, work.StageIssued, work.StageNew} {
				if flight[s] > 0 {
					parts = append(parts, fmt.Sprintf("%d %s", flight[s], s))
				}
			}
			fmt.Fprintf(&b, "In flight now: %s\n\n", strings.Join(parts, ", "))
		}
	}
	return b.String(), n
}

// aim is a merged task's fix or gain, and what it fixed.
func aim(t work.Task) string {
	if t.Fix() {
		return "fix: " + t.AimWhy
	}
	return "gain"
}

func line(t work.Task, note string) string {
	s := fmt.Sprintf("%s (task %s", t.Label(), t.ID)
	if t.PR.Number > 0 {
		s += fmt.Sprintf(", PR #%d", t.PR.Number)
	}
	if t.Worker != "" {
		s += ", worker " + t.Worker
	}
	s += ")"
	if note = strings.TrimSpace(note); note != "" {
		s += ": " + core.Clip(note, 200)
	}
	return s
}

func list(b *strings.Builder, name string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "%s (%d):\n", name, len(lines))
	for i, l := range lines {
		if i == most {
			fmt.Fprintf(b, "- and %d more\n", len(lines)-most)
			break
		}
		fmt.Fprintf(b, "- %s\n", l)
	}
	b.WriteString("\n")
}
