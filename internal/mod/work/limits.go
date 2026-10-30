package work

import (
	"fmt"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// limitsRead holds a merge that changes the code enforcing the hard limits
// until a warden has read its exact diff (core.LimitsHeld): the one read left
// before a merge. It asks once per head, through Weigh (the risk module),
// and the answer comes back to onLimitsRead. wait says the merge waits.
func (m *Module) limitsRead(t *Task, p core.Project, main string) (wait bool, err error) {
	ch, err := m.C.Effects.Classify(p, main, t.SHA)
	if err != nil {
		return true, err
	}
	why := m.C.Effects.LimitsHeld(p, ch, t.SHA, t.Kind == "revert")
	if why == "" {
		return false, nil
	}
	if t.Checkpoint != "" {
		return true, m.dropCheckpoint(t, p, "it changes the code that enforces the hard limits, which lands only whole, after a warden's read")
	}
	t.setDiff(ch.DiffID)
	reason := "waiting on a warden's read: " + why
	if t.Reason == reason {
		return true, nil // asked already for this head
	}
	t.Reason = reason
	m.save(t, reason)
	a := limitsAsk(t, p, ch)
	if m.Weigh != nil {
		m.Weigh(a)
	} else {
		m.C.Person.Ask(a)
	}
	return true, nil
}

// limitsAsk asks for a read of a change to the limits code, carrying the
// diff of those files so whoever answers sees every line they let land.
func limitsAsk(t *Task, p core.Project, ch core.Change) core.Ask {
	return core.Ask{Kind: core.LimitsRead, Ref: t.ID + "@" + t.SHA, Project: p.Name, From: t.Worker,
		Title: fmt.Sprintf("Land %q? It changes %s, code that enforces the hard limits", t.Label(), strings.Join(ch.Limits, ", ")),
		Detail: "Everything else lands with no read; this code holds the hard limits (money, credentials, private details going out, anything outward in the person's name, deleting a repository or its history). " +
			"Say land (do) unless the change weakens one of them, lets an agent past one, or makes one weaker to check; then decline and say what to fix, which goes back to its worker. PR: " + t.PR.URL,
		Options: []string{"merge", "send back"}, Diff: ch.Diff}
}

// onLimitsRead takes a warden's (or the person's) answer to limitsAsk: land
// approves that head's diff, and the next look merges it; anything else
// sends the change back to its worker with what was said.
func (m *Module) onLimitsRead(a core.Ask) {
	id, sha, _ := strings.Cut(a.Ref, "@")
	t, ok := m.Get(id)
	if !ok || t.Stage != StageMerging || t.SHA != sha {
		return // moved on: a new head asks again
	}
	if a.Answer == "merge" || a.Answer == "yes" {
		m.C.Effects.ApproveDiff(core.LimitsRead, t.Project, sha, t.Diff)
		t.Reason = ""
		m.save(&t, "read: "+core.Clip(strings.TrimSpace("land. "+a.Note), 500))
		return
	}
	p, err := m.project(&t)
	if err != nil {
		return
	}
	t.Reason = ""
	m.sendBack(&t, p, "Your change touches code that enforces the hard limits, and its read said not to land it as it is:\n\n"+strings.TrimSpace(a.Answer+": "+a.Note))
}
