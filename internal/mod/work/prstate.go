package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Any agent's hand on the state of the Hive's own pull requests: close one
// with a comment, ending its task. Only a PR a
// task opened counts as the Hive's own, and only in a project the agent
// reaches; no agent holds a forge login, so the daemon acts for it.

// ownPR finds the task whose PR an agent names, by task id or by project and
// PR number, refusing a PR no task of the Hive opened.
func (m *Module) ownPR(r core.Run, id, project string, number int) (Task, core.Project, error) {
	t, ok := m.Get(id)
	if id == "" {
		if project == "" {
			project = r.Project
		}
		if number == 0 || project == "" {
			return Task{}, core.Project{}, errors.New("name the task, or the project and the PR number")
		}
		found := m.List(func(c Task) bool { return c.Project == project && c.PR.Number == number })
		for _, c := range found { // one still going comes first
			if !c.Terminal() {
				t, ok = c, true
				break
			}
		}
		if !ok && len(found) > 0 {
			t, ok = found[0], true
		}
		if !ok {
			return Task{}, core.Project{}, fmt.Errorf("PR #%d in %s isn't one the Hive opened for a task, so it isn't the Hive's to change", number, project)
		}
	}
	if !ok {
		return Task{}, core.Project{}, fmt.Errorf("no task %q", id)
	}
	if err := m.reach(r, t.Project); err != nil {
		return Task{}, core.Project{}, err
	}
	if t.PR.Number == 0 {
		return t, core.Project{}, fmt.Errorf("task %s has no pull request", t.ID)
	}
	if number != 0 && number != t.PR.Number {
		return t, core.Project{}, fmt.Errorf("task %s's PR is #%d, not #%d", t.ID, t.PR.Number, number)
	}
	p, err := m.project(&t)
	return t, p, err
}

// openPR reads t's PR from the forge and refuses one that isn't open.
func (m *Module) openPR(t Task, p core.Project) (core.PR, error) {
	pr, err := m.C.Effects.Forge.GetPR(p.Repo, t.PR.Number)
	if err != nil {
		return pr, err
	}
	if pr.State != "OPEN" {
		return pr, fmt.Errorf("PR #%d is %s, not open", pr.Number, strings.ToLower(pr.State))
	}
	return pr, nil
}

// closedByLead starts the reason of a task the lead ended with pr_close. Its
// PR is closed, so recheck doesn't bring it back; new work is a new brief.
const closedByLead = "closed by the lead"

// ClosePR closes a task's PR with a comment and ends the task, its worker
// stopped; with deleteBranch its hive/ branch goes too. A revert the lead no
// longer wants ends this way, rather than waiting on the person.
func (m *Module) ClosePR(id, by, comment string, deleteBranch bool) (string, error) {
	save, done := m.take(id)
	defer done()
	t, _ := m.Get(id)
	switch t.Stage {
	case StageMerged, StageDone:
		return "", fmt.Errorf("task %s is %s; its PR merged", id, t.Stage)
	}
	p, err := m.project(&t)
	if err != nil {
		return "", err
	}
	pr, err := m.C.Effects.Forge.GetPR(p.Repo, t.PR.Number)
	if err != nil {
		return "", err
	}
	note := fmt.Sprintf("PR #%d closed by %s: %s", pr.Number, by, core.Clip(comment, 300))
	switch pr.State {
	case "MERGED":
		return "", fmt.Errorf("PR #%d is merged: end the task with landed", pr.Number)
	case "OPEN":
		if err := m.C.Effects.Forge.ClosePR(p.Repo, pr.Number, comment); err != nil {
			return "", err
		}
	default:
		note = fmt.Sprintf("PR #%d was already closed on the forge; task ended by %s: %s", pr.Number, by, core.Clip(comment, 300))
	}
	if t.Worker != "" {
		if r, ok := m.C.Runs.Get(t.Worker); ok && !r.Terminal() {
			m.C.Runs.Stop(t.Worker, "its pull request was "+closedByLead)
		}
	}
	if deleteBranch {
		switch branch := t.Branch; {
		case core.HiveBranch(branch) != nil:
			note += fmt.Sprintf("; branch %q kept, as only hive/ branches are the Hive's", branch)
		default:
			if err := m.C.Effects.Forge.DeleteBranch(p.Repo, branch); err != nil {
				note += "; its branch couldn't be deleted: " + core.Clip(err.Error(), 200)
			} else {
				note += "; branch " + branch + " deleted"
			}
		}
	}
	shas := []string{t.SHA, t.Checkpoint}
	t.Stage, t.Reason, t.Checkpoint, t.DraftWhy = StageStopped, core.Clip(closedByLead+": "+comment, 300), "", ""
	save(&t, note)
	m.dropChecks(t, shas)
	// A merge question already put to the person is about a PR that is gone
	// now: answered for them, it leaves Waiting (onPersonMerge ignores it, as
	// the task is no longer waiting on the person).
	for _, a := range m.C.Person.Open() {
		if a.Kind == "merge" && strings.HasPrefix(a.Ref, t.ID+"@") {
			m.C.Person.Answer(a.ID, "leave open", "the Hive, not the person: the lead closed the PR")
		}
	}
	m.notify(&t, fmt.Sprintf("Closed without merging: %s %s (%s)", t.Label(), t.PR.URL, core.Clip(comment, 300)))
	return note, nil
}

// dropChecks takes t's checks still waiting in the check line out of it, so
// a closed PR doesn't hold a place there; a commit another live task still
// has keeps its place.
func (m *Module) dropChecks(t Task, shas []string) {
	for _, sha := range shas {
		if sha == "" || len(m.List(func(o Task) bool { return o.ID != t.ID && !o.Terminal() && (o.SHA == sha || o.Checkpoint == sha) })) > 0 {
			continue
		}
		m.C.Effects.Slots.Drop(t.Project, sha)
	}
}

type prArgs struct {
	Task, Project, Why, Comment string
	PR                          prNumber
	DeleteBranch                bool `json:"delete_branch"`
}

// prNumber is a PR number given as a number or as text ("12", "#12"), as the
// agent CLI passes every value.
type prNumber int

func (n *prNumber) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil {
		s = string(b)
	}
	v, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if err != nil {
		return fmt.Errorf("pr %s isn't a PR number", b)
	}
	*n = prNumber(v)
	return nil
}

func (m *Module) prOp(r core.Run, args json.RawMessage, need string, do func(a prArgs, t Task) (string, error)) (any, error) {
	var a prArgs
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	switch {
	case need == "why" && strings.TrimSpace(a.Why) == "":
		return nil, errors.New("say why with why (it goes on the PR and the task's record)")
	case need == "comment" && strings.TrimSpace(a.Comment) == "":
		return nil, errors.New("say why with comment (it is posted on the PR as it closes)")
	}
	t, _, err := m.ownPR(r, a.Task, a.Project, int(a.PR))
	if err != nil {
		return nil, err
	}
	note, err := do(a, t)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "pr": t.PR.URL, "note": note}, nil
}

func (m *Module) opPRClose(r core.Run, args json.RawMessage) (any, error) {
	return m.prOp(r, args, "comment", func(a prArgs, t Task) (string, error) {
		return m.ClosePR(t.ID, r.ID, a.Comment, a.DeleteBranch)
	})
}
