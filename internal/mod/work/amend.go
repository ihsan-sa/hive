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
// started, it is told instead (message it), and its PR closed with pr_close;
// one whose worker finished blocked without opening a PR has nothing left to
// tell or close, so it can be withdrawn as well (folded into another task, or
// dropped), else no op could end it.

// withdrawn starts the reason of a task its filer took back; it doesn't come
// back with recheck, as new work is a new brief.
const withdrawn = "withdrawn by "

// Withdrawn reports whether t was taken back by its filer or a lead.
func (t Task) Withdrawn() bool {
	return t.Stage == StageStopped && strings.HasPrefix(t.Reason, withdrawn)
}

// endedBare reports whether t's worker finished blocked without a PR.
func (t Task) endedBare() bool { return t.Stage == StageBlocked && t.PR.Number == 0 }

// mayChange checks r may amend or withdraw t: a brief that no worker has
// started on yet (or, to withdraw, one blocked with no PR), filed by r, or by
// anyone but the person when r is the lead.
func (m *Module) mayChange(r core.Run, t Task, verb string) error {
	if err := m.reach(r, t.Project); err != nil {
		return err
	}
	switch {
	case t.Kind != "work":
		return fmt.Errorf("task %s is a %s, not a filed brief", t.ID, t.Kind)
	case t.Stage != StageNew && !t.Queued() && !(verb == "withdrawn" && t.endedBare()):
		return fmt.Errorf("task %s has left the queue (%s), so it can't be %s: message its worker, or close its PR with pr_close (only a blocked task with no PR can still be withdrawn)", t.ID, t.Stage, verb)
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
// stands now. An issue already open gets the amended brief as its title and
// body, and a comment (edited in place on each later amend) saying who
// changed what and why.
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
			err := m.C.Effects.Forge.EditIssue(p.Repo, t.Issue.Number, t.Label(), m.issueBody(&t, p)+"\n\n"+marker(t.ID))
			if err == nil {
				err = m.C.Effects.Forge.StatusComment(p.Repo, t.Issue.Number, "<!-- hive-amended:"+t.ID+" -->", "The brief above was "+note+".")
			}
			if err != nil {
				note += "; the issue couldn't be updated: " + core.Clip(err.Error(), 200)
			}
		}
	}
	m.save(&t, note)
	return t, nil
}

// Withdraw takes back a waiting brief, or one blocked with no PR: the task
// stops, its issue closes with why (and the task that covers it now, into),
// and no worker starts on it again.
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
	when := "before a worker started"
	if t.endedBare() {
		when = "after its worker finished blocked with no PR"
		if t.Worker != "" { // a blocked worker may still wait on a reply: it isn't woken again
			m.C.Runs.Stop(t.Worker, "its brief was "+withdrawn+r.ID+": "+core.Clip(strings.TrimSpace(why), 300))
		}
		t.Checkpoint, t.DraftWhy = "", ""
	}
	reason := withdrawn + r.ID + ": " + core.Clip(strings.TrimSpace(why), 300)
	if by.ID != "" {
		reason += fmt.Sprintf(" (covered by task %s, %q)", by.ID, by.Label())
	}
	t.Stage, t.Reason, t.Held, t.HeldBy, t.Now = StageStopped, reason, false, "", false
	if t.Issue.Number != 0 {
		if p, err := m.project(&t); err == nil {
			text := "Withdrawn by " + r.ID + " " + when + ": " + strings.TrimSpace(why)
			if by.ID != "" && by.Issue.Number != 0 {
				text += fmt.Sprintf("\n\nCovered by #%d.", by.Issue.Number)
			}
			m.comment(p.Repo, t.Issue.Number, text)
			t.IssueOpen = true // closed by the step, retried as a done task's is
		}
	}
	m.save(&t, reason)
	text := fmt.Sprintf("Withdrawn %s: %q (%s)", when, t.Label(), reason)
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

// Retract ends a brief r filed that a worker has already started on, once
// its ask turns out to be void (improve's fix brief for a test whose
// quarantine came from a broken check run): its worker is stopped, an open
// PR closed, and the task withdrawn, so it holds no worker slot and doesn't
// come back with recheck. A task on its way to merge or past it is left be.
func (m *Module) Retract(r core.Run, id, why string) (Task, error) {
	if strings.TrimSpace(why) == "" {
		return Task{}, errors.New("say why with why (it goes on the issue and the task's record)")
	}
	if !m.claim(id) {
		return Task{}, fmt.Errorf("task %s is busy; try again in a moment", id)
	}
	defer m.release(id)
	t, ok := m.Get(id)
	if !ok || m.C.Runs.Walled(r, t.Project) != nil {
		return Task{}, fmt.Errorf("no task %q", id)
	}
	switch {
	case t.Kind != "work" || t.From != r.ID:
		return Task{}, fmt.Errorf("task %s wasn't filed by %s", id, r.ID)
	case t.Stage == StageNew || t.Queued():
		return Task{}, fmt.Errorf("task %s hasn't started; withdraw it", id)
	case t.Stage == StageMerging || t.Stage == StageMerged || t.Stage == StageDone || t.Stage == StageReverting || t.Withdrawn():
		return Task{}, fmt.Errorf("task %s is %s, so it isn't retracted", id, t.Stage)
	}
	why = core.Clip(strings.TrimSpace(why), 300)
	if t.Worker != "" {
		m.C.Runs.Stop(t.Worker, "its brief was "+withdrawn+r.ID+": "+why)
	}
	p, perr := m.project(&t)
	if perr == nil && t.PR.Number != 0 {
		if pr, err := m.C.Effects.Forge.GetPR(p.Repo, t.PR.Number); err == nil && pr.State == "OPEN" {
			m.C.Effects.Forge.ClosePR(p.Repo, pr.Number, "Withdrawn by "+r.ID+": "+why)
		}
	}
	reason := withdrawn + r.ID + " after its worker started: " + why
	t.Stage, t.Reason, t.Held, t.HeldBy, t.Now, t.Checkpoint, t.DraftWhy = StageStopped, reason, false, "", false, "", ""
	if perr == nil && t.Issue.Number != 0 {
		m.comment(p.Repo, t.Issue.Number, "Withdrawn by "+r.ID+" after a worker started on it: "+why)
		t.IssueOpen = true // closed by the step, retried as a done task's is
	}
	m.save(&t, reason)
	m.notify(&t, fmt.Sprintf("Withdrawn after its worker started: %q (%s)", t.Label(), reason))
	return t, nil
}
