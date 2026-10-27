package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Amending and withdrawing: a brief still waiting for a worker (filed, or
// queued with its issue) can be changed in place or taken back by whoever
// filed it, or by the lead, so a wider ask doesn't have to be filed again
// beside the first and the old copy held (issue #405). Once a worker has
// started, it is told instead (message it), and its PR closed with pr_close.

// withdrawn starts the reason of a task its filer took back; it doesn't come
// back with recheck, as new work is a new brief.
const withdrawn = "withdrawn by "

// Withdrawn reports whether t was taken back before a worker started on it.
func (t Task) Withdrawn() bool {
	return t.Stage == StageStopped && strings.HasPrefix(t.Reason, withdrawn)
}

// mayChange checks r may amend or withdraw t: a brief that no worker has
// started on yet, filed by r, or by anyone but the person when r is the lead.
func (m *Module) mayChange(r core.Run, t Task, verb string) error {
	if err := m.reach(r, t.Project); err != nil {
		return err
	}
	switch {
	case t.Kind != "work":
		return fmt.Errorf("task %s is a %s, not a filed brief", t.ID, t.Kind)
	case t.Stage != StageNew && !t.Queued():
		return fmt.Errorf("task %s has left the queue (%s), so it can't be %s: message its worker, or close its PR with pr_close", t.ID, t.Stage, verb)
	case t.From == r.ID:
		return nil
	case r.Role != "lead":
		return fmt.Errorf("task %s was filed by %s; only its filer or the lead can have it %s", t.ID, t.From, verb)
	case t.From == "person":
		return fmt.Errorf("task %s is the person's own ask; only they can have it %s", t.ID, verb)
	}
	return nil
}

// Amend replaces a waiting brief's title, done line or known context with
// those given (an empty one is kept), so its worker starts on the ask as it
// stands now. An issue already open gets the brief as amended in a comment,
// edited in place on each later amend.
func (m *Module) Amend(r core.Run, id, title, done, known, why string) (Task, error) {
	title, done, known = strings.TrimSpace(title), strings.TrimSpace(done), strings.TrimSpace(known)
	if title == "" && done == "" && known == "" {
		return Task{}, errors.New("say what changes: title, done or known (each given replaces the old one)")
	}
	if !m.claim(id) {
		return Task{}, fmt.Errorf("task %s is busy; try again in a moment", id)
	}
	defer m.release(id)
	t, ok := m.Get(id)
	if !ok || m.C.Runs.Walled(r, t.Project) != nil {
		return Task{}, fmt.Errorf("no task %q", id)
	}
	if err := m.mayChange(r, t, "amended"); err != nil {
		return Task{}, err
	}
	var changed []string
	if title != "" && title != t.Title {
		t.Title, changed = title, append(changed, "title")
	}
	if done != "" && done != t.Done {
		t.Done, changed = done, append(changed, "done")
	}
	if known != "" && known != t.Known {
		t.Known, changed = known, append(changed, "known")
	}
	if len(changed) == 0 {
		return t, nil
	}
	note := fmt.Sprintf("amended by %s (%s)", r.ID, strings.Join(changed, ", "))
	if why = strings.TrimSpace(why); why != "" {
		note += ": " + core.Clip(why, 300)
	}
	if t.Issue.Number != 0 {
		if p, err := m.project(&t); err == nil {
			body := fmt.Sprintf("The brief was amended by %s%s; its worker starts on this:\n\n%s", r.ID, suffixed(core.Clip(why, 300)), m.issueBody(&t, p))
			if err := m.C.Effects.Forge.StatusComment(p.Repo, t.Issue.Number, "<!-- hive-amended:"+t.ID+" -->", body); err != nil {
				note += "; the issue couldn't be updated: " + core.Clip(err.Error(), 200)
			}
		}
	}
	m.save(&t, note)
	return t, nil
}

// Withdraw takes back a waiting brief: the task stops, its issue closes with
// why (and the task that covers it now, into), and no worker starts on it.
func (m *Module) Withdraw(r core.Run, id, into, why string) (Task, error) {
	if strings.TrimSpace(why) == "" {
		return Task{}, errors.New("say why with why (it goes on the issue and the task's record)")
	}
	var by Task
	if into != "" {
		o, ok := m.Get(into)
		if !ok || into == id || m.C.Runs.Walled(r, o.Project) != nil {
			return Task{}, fmt.Errorf("no task %q to withdraw into", into)
		}
		by = o
	}
	if !m.claim(id) {
		return Task{}, fmt.Errorf("task %s is busy; try again in a moment", id)
	}
	defer m.release(id)
	t, ok := m.Get(id)
	if !ok || m.C.Runs.Walled(r, t.Project) != nil {
		return Task{}, fmt.Errorf("no task %q", id)
	}
	if err := m.mayChange(r, t, "withdrawn"); err != nil {
		return Task{}, err
	}
	reason := withdrawn + r.ID + ": " + core.Clip(strings.TrimSpace(why), 300)
	if by.ID != "" {
		reason += fmt.Sprintf(" (covered by task %s, %q)", by.ID, by.Label())
	}
	t.Stage, t.Reason, t.Held, t.HeldBy, t.Now = StageStopped, reason, false, "", false
	if t.Issue.Number != 0 {
		if p, err := m.project(&t); err == nil {
			text := "Withdrawn by " + r.ID + " before a worker started on it: " + strings.TrimSpace(why)
			if by.ID != "" && by.Issue.Number != 0 {
				text += fmt.Sprintf("\n\nCovered by #%d.", by.Issue.Number)
			}
			m.comment(p.Repo, t.Issue.Number, text)
			t.IssueOpen = true // closed by the step, retried as a done task's is
		}
	}
	m.save(&t, reason)
	text := fmt.Sprintf("Withdrawn before a worker started: %q (%s)", t.Label(), reason)
	if quietWithdrawer(r) {
		m.post(&t, text)
	} else {
		m.notify(&t, text)
	}
	return t, nil
}

// quietWithdrawer says a withdrawal by r needs no lead's turn: a lead (the
// main one or a project's) decided it, or improve or a step-back took back
// its own brief, with its reason on the task and in the feed.
func quietWithdrawer(r core.Run) bool {
	return r.Role == "lead" || r.Role == "stepback" || r.ID == "improve"
}

func (m *Module) opAmend(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Task, Title, Done, Known, Why string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if a.Task == "" {
		return nil, errors.New("name the task: --task <id>")
	}
	t, err := m.Amend(r, a.Task, a.Title, a.Done, a.Known, a.Why)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "title": t.Label(), "stage": t.Stage, "note": "amended in place; its worker starts on the brief as it stands now"}, nil
}

func (m *Module) opWithdraw(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Task, Into, Why string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if a.Task == "" {
		return nil, errors.New("name the task: --task <id>")
	}
	t, err := m.Withdraw(r, a.Task, a.Into, a.Why)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "stage": t.Stage, "note": t.Reason}, nil
}
