package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Landed ends a task whose work is already on main, whatever stage it is in:
// its PR was merged on the forge (by the person, or by a merge the Hive didn't
// see through), or its commits are on main. A task stuck past the queue holds
// back every brief that touches what it touches, so the lead ends it here
// rather than waiting on the person. It records the merge commit and runs
// OnMerged, and the next tick takes it to done, so the briefs behind it start.
// One whose PR is open and whose work isn't on main is refused: that is a
// merge still to happen, not one that happened.
func (m *Module) Landed(id, mergeSHA, by, why string) (Task, error) {
	t, ok := m.Get(id)
	if !ok {
		return Task{}, core.ErrNotFound
	}
	switch t.Stage {
	case StageMerged, StageDone:
		return t, fmt.Errorf("task %s is already %s", id, t.Stage)
	case StageNew:
		return t, fmt.Errorf("task %s has no work yet (%s)", id, t.Stage)
	}
	p, err := m.project(&t)
	if err != nil {
		return t, err
	}
	m.mu.Lock()
	busy := m.busy[id]
	if !busy {
		m.busy[id] = true
	}
	m.mu.Unlock()
	if busy {
		return t, fmt.Errorf("task %s has a step running right now; ask again in a moment", id)
	}
	defer m.release(id)
	t, _ = m.Get(id) // as the last step left it
	merged, how, err := m.landedAt(&t, p, mergeSHA)
	if err != nil {
		return t, err
	}
	past := m.pastCheckpoint(&t, p) // read before the worker stops
	if t.Worker != "" {
		if r, ok := m.C.Runs.Get(t.Worker); ok && !r.Terminal() {
			m.C.Runs.Stop(t.Worker, "its work is already on main")
		}
	}
	if t.Checkpoint != "" { // its PR is the checkpoint's: say what merged and what didn't
		m.save(&t, fmt.Sprintf("checkpoint %s is what merged%s", core.Short(t.Checkpoint), past))
	}
	t.Checkpoint = "" // ended, not banked: its worker is stopped
	m.merged(&t, merged, fmt.Sprintf("ended as landed by %s (%s, %s): %s", by, how, core.Short(merged), core.Clip(why, 300)))
	return t, nil
}

// landedAt finds where t's work is on main: its PR's merge on the forge, or
// mergeSHA (a commit on main's line that holds its commits), or its head
// itself when main has it. It says how it knows.
func (m *Module) landedAt(t *Task, p core.Project, mergeSHA string) (string, string, error) {
	open := false
	if t.PR.Number != 0 {
		pr, err := m.C.Effects.Forge.GetPR(p.Repo, t.PR.Number)
		if err != nil {
			return "", "", err
		}
		if pr.State == "MERGED" && pr.MergeSHA != "" {
			return pr.MergeSHA, fmt.Sprintf("PR #%d merged on the forge", pr.Number), nil
		}
		open = pr.State == "OPEN"
	}
	if t.SHA == "" {
		return "", "", fmt.Errorf("task %s has no PR merged on the forge and no commits to find on main", t.ID)
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return "", "", err
	}
	dir := m.C.WS.Project(p.Name)
	refuse := func() error {
		state := "no PR"
		if t.PR.Number != 0 {
			state = fmt.Sprintf("PR #%d is %s", t.PR.Number, map[bool]string{true: "open", false: "not merged"}[open])
		}
		return fmt.Errorf("task %s isn't landed: %s and its head %s isn't on main %s", t.ID, state, core.Short(t.SHA), core.Short(main))
	}
	if mergeSHA != "" {
		full, err := core.Git(dir, "rev-parse", "--verify", "-q", mergeSHA+"^{commit}")
		if err != nil || !m.C.Effects.MainLine(p)[full] {
			return "", "", fmt.Errorf("%s isn't a commit on main", mergeSHA)
		}
		if !holds(dir, full, t.SHA) {
			return "", "", fmt.Errorf("main at %s doesn't hold task %s's head %s", core.Short(full), t.ID, core.Short(t.SHA))
		}
		return full, "its work is on main at " + core.Short(full), nil
	}
	if _, err := core.Git(dir, "merge-base", "--is-ancestor", t.SHA, main); err == nil {
		return t.SHA, "its head is on main", nil
	}
	if holds(dir, main, t.SHA) { // squashed: main has the change, under another commit
		return "", "", errors.New("main holds its change, squashed under another commit: say which with merge <sha>")
	}
	return "", "", refuse()
}

// pastCheckpoint reads what t's worker committed past the Hive's last take of
// its branch (the checkpoint, or it caught up with main), which ending the task
// leaves off main, and says so for the log.
func (m *Module) pastCheckpoint(t *Task, p core.Project) string {
	if t.Checkpoint == "" || t.Worker == "" {
		return ""
	}
	r, ok := m.C.Runs.Get(t.Worker)
	if !ok || r.Checkout == "" {
		return ""
	}
	base := t.SHA
	if base == "" {
		base = t.Checkpoint
	}
	sha, err := m.C.Effects.Extract(p, r, base)
	switch {
	case errors.Is(err, core.ErrNoChanges):
		return "; the worker had committed nothing past it"
	case err != nil:
		return "; the worker's commits past it couldn't be read: " + core.Clip(err.Error(), 200)
	}
	return fmt.Sprintf("; the worker's commits past it, up to %s, are not on main", core.Short(sha))
}

// holds reports whether the commit at would gain nothing from merging sha in:
// sha's change is already there, squashed or not, or sha changes nothing
// since its merge base with at (an empty own diff).
func holds(dir, at, sha string) bool {
	if _, err := core.Git(dir, "merge-base", "--is-ancestor", sha, at); err == nil {
		return true
	}
	if base, err := core.Git(dir, "merge-base", at, sha); err == nil {
		if _, err := core.Git(dir, "diff", "--quiet", strings.TrimSpace(base), sha); err == nil {
			return true
		}
	}
	tree, err := core.Git(dir, "merge-tree", "--write-tree", at, sha)
	if err != nil {
		return false
	}
	own, err := core.Git(dir, "rev-parse", at+"^{tree}")
	return err == nil && strings.TrimSpace(strings.SplitN(tree, "\n", 2)[0]) == strings.TrimSpace(own)
}

func (m *Module) opLanded(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Task, Merge, Why string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Why) == "" {
		return nil, errors.New("say why with why (it goes on the task's record)")
	}
	t, ok := m.Get(a.Task)
	if !ok {
		return nil, core.ErrNotFound
	}
	if err := m.reach(r, t.Project); err != nil {
		return nil, err
	}
	t, err := m.Landed(a.Task, a.Merge, r.ID, a.Why)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "merge": t.MergeSHA, "note": "ended as merged; the briefs waiting on it start as the queue allows"}, nil
}

// alreadyLanded ends t when its head sha brings nothing main doesn't already
// have (its tree is main's, or merging it into main changes nothing): its
// work landed through another task or by hand. Its PR is closed and its issue
// closed, each naming the main commit that carries the work, and it is done:
// no further test, read, merge or worker round, and not blocked or stopped.
// Nothing is measured, as that commit was measured when it merged. A task
// whose own PR is merged on the forge is left to the merge path, which records
// and measures it. A checkpoint main already holds is dropped and the worker
// keeps going.
func (m *Module) alreadyLanded(t *Task, p core.Project, main, sha string) (bool, error) {
	dir := m.C.WS.Project(p.Name)
	if sha == "" || main == "" || !holds(dir, main, sha) {
		return false, nil
	}
	open := false
	if t.PR.Number != 0 {
		pr, err := m.C.Effects.Forge.GetPR(p.Repo, t.PR.Number)
		if err != nil {
			return true, err
		}
		if pr.State == "MERGED" { // its own merge: the merge path records it, banks a checkpoint and measures it
			return false, nil
		}
		open = pr.State == "OPEN"
	}
	if t.Checkpoint != "" {
		return true, m.dropCheckpoint(t, p, "main at "+core.Short(main)+" already has everything in it")
	}
	at, exact := carrier(dir, main, sha)
	who := at + " carries"
	if !exact {
		who = fmt.Sprintf("%s or an older commit carries", at)
	}
	say := fmt.Sprintf("Already on main: %s this work, so there is nothing left to merge. Closing.", who)
	if open {
		if err := m.C.Effects.Forge.ClosePR(p.Repo, t.PR.Number, say); err != nil {
			return true, err
		}
	}
	if t.Worker != "" {
		if r, ok := m.C.Runs.Get(t.Worker); ok && !r.Terminal() {
			m.C.Runs.Stop(t.Worker, "its work is already on main")
		}
	}
	t.MergeSHA, t.Stage, t.Reason, t.Failures = at, StageDone, "", 0
	t.IssueOpen = t.Issue.Number != 0
	m.save(t, fmt.Sprintf("already landed: main at %s carries its head %s", core.Short(at), core.Short(sha)))
	m.notify(t, fmt.Sprintf("Already on main: %q, carried by %s. %s", t.Label(), core.Short(at), t.PR.URL))
	if t.IssueOpen {
		m.comment(p.Repo, t.Issue.Number, say)
		m.closeIssue(t, p)
	}
	return true, nil
}

// carrierBack is how many commits carrier looks back over on main's line.
var carrierBack = 200

// carrier is the main commit that brought sha's change: the oldest on main's
// first-parent line, going back from main, that still holds it. When the
// oldest it looks back to still holds it, that one is named and exact is
// false: the work may have landed further back. A head that brought main in
// is read as the branch was before (its first parent), so the commit named is
// the one that landed the work, not main's newest.
func carrier(dir, main, sha string) (at string, exact bool) {
	for i := 0; i < 20; i++ {
		prev, err := core.Git(dir, "rev-parse", "-q", "--verify", sha+"^2^{commit}")
		if err != nil || strings.TrimSpace(prev) == "" {
			break
		}
		first, err := core.Git(dir, "rev-parse", "-q", "--verify", sha+"^1^{commit}")
		if err != nil || !holds(dir, main, strings.TrimSpace(first)) {
			break
		}
		sha = strings.TrimSpace(first)
	}
	out, err := core.Git(dir, "rev-list", "--first-parent", "-n", fmt.Sprint(carrierBack+1), main)
	lines := strings.Fields(out)
	if err != nil || len(lines) == 0 {
		return main, false
	}
	at = main
	for i, c := range lines[1:] {
		if !holds(dir, c, sha) {
			return at, true
		}
		if i+1 == carrierBack { // one further back may be the carrier
			return c, false
		}
		at = c
	}
	return at, true // main's line starts here
}

// landedHead is alreadyLanded against main as it is now, for a step that
// hasn't synced main itself. A checkpoint in flight is left to its own steps.
func (m *Module) landedHead(t *Task, p core.Project, sha string) (bool, error) {
	if sha == "" || t.Checkpoint != "" {
		return false, nil
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return false, err
	}
	return m.alreadyLanded(t, p, main, sha)
}
