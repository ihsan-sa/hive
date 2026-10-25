package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// rerunsPerHead caps the re-runs of the forge's failed checks on one head, so
// a check that fails for real can't be run again and again in place of a fix.
const rerunsPerHead = 2

// rerunGrace is how long after a re-run a failure still read on that head
// waits instead of going back: the forge may not list the new attempt yet.
var rerunGrace = 15 * time.Minute

// forgeFailed opens the findings a red forge check sends back to the worker.
const forgeFailed = "The forge's checks failed on your commit "

func (m *Module) opRerunChecks(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Task string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	t, ok := m.Get(a.Task)
	if a.Task == "" {
		if r.Role != "worker" {
			return nil, errors.New("name the task: --task <id>")
		}
		t, ok = m.taskOf(r.ID)
	}
	if !ok {
		return nil, fmt.Errorf("no task %q", a.Task)
	}
	if r.Role == "worker" && t.Worker != r.ID {
		return nil, fmt.Errorf("task %s isn't yours: a worker re-runs only its own task's checks", t.ID)
	}
	if err := m.C.Runs.Walled(r, t.Project); err != nil {
		return nil, err
	}
	note, err := m.RerunChecks(t.ID, r.ID)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "note": note}, nil
}

// RerunChecks re-runs the forge's failed checks on a task's PR head, at most
// rerunsPerHead times per head, and records it on the task; the merge then
// waits for the new result. A worker sent back only for those checks gets its
// round back, since a flake isn't its fault.
func (m *Module) RerunChecks(id, by string) (string, error) {
	if !m.claim(id) {
		return "", fmt.Errorf("task %s is busy; try again in a moment", id)
	}
	defer m.release(id)
	t, ok := m.Get(id)
	if !ok {
		return "", core.ErrNotFound
	}
	switch {
	case t.Stage == StageMerged || t.Stage == StageDone:
		return "", fmt.Errorf("task %s is %s; nothing to re-run", id, t.Stage)
	case t.PR.Number == 0 || t.SHA == "":
		return "", fmt.Errorf("task %s has no pull request yet; finish first", id)
	case t.Reruns[t.SHA] >= rerunsPerHead:
		return "", fmt.Errorf("the forge's checks on %s were re-run %d times already, the most for one head; a failure that keeps coming back is likely the change's own: read its log and fix it", core.Short(t.SHA), t.Reruns[t.SHA])
	}
	p, err := m.project(&t)
	if err != nil {
		return "", err
	}
	runs, err := m.C.Effects.Forge.Checks(p.Repo, t.SHA)
	if err != nil {
		return "", err
	}
	var failed []core.CheckRun
	for _, c := range runs {
		if c.State == core.CheckFail || c.State == core.CheckCantStart {
			failed = append(failed, c)
		}
	}
	if len(failed) == 0 {
		return "", fmt.Errorf("no forge check has failed on %s, so nothing to re-run", core.Short(t.SHA))
	}
	n, err := m.C.Effects.Forge.RerunFailed(p.Repo, t.SHA)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", fmt.Errorf("the forge has no finished failed run on %s to re-run: one may still be going, or never started (%s), which a re-run doesn't fix; a startup failure is often the workflow file itself, and one awaiting approval is the repo owner's to approve", core.Short(t.SHA), core.CheckNames(failed))
	}
	if t.Reruns == nil {
		t.Reruns = map[string]int{}
	}
	t.Reruns[t.SHA]++
	t.RerunAt = time.Now().UTC()
	// A wait on main belongs to one run of the checks: the re-run's red
	// starts its own, not one already spent.
	t.MainWait, t.MainWaitOn = time.Time{}, ""
	note := fmt.Sprintf("re-ran the forge's failed checks on %s (%s) for %s, %d of %d on this head", core.Short(t.SHA), core.CheckNames(failed), by, t.Reruns[t.SHA], rerunsPerHead)
	if t.Stage == StageWorking && t.Round > 0 && strings.HasPrefix(t.Findings, forgeFailed+core.Short(t.SHA)) {
		t.Round--
		note += "; the round they cost is given back"
	}
	m.save(&t, note)
	return note + ". The merge waits for the new result; finish again without a new commit if the failure was the only finding.", nil
}

// claim marks a task busy for a change from outside its step, waiting for a
// running step to end, so neither saves over the other.
func (m *Module) claim(id string) bool {
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		m.mu.Lock()
		if !m.busy[id] {
			m.busy[id] = true
			m.mu.Unlock()
			return true
		}
		m.mu.Unlock()
	}
	return false
}

func (m *Module) release(id string) {
	m.mu.Lock()
	delete(m.busy, id)
	m.mu.Unlock()
}

// rerunning reports whether t's head had its failed forge checks re-run
// lately enough that a failure still read may be the attempt before.
func rerunning(t *Task) bool {
	return t.Reruns[t.SHA] > 0 && time.Since(t.RerunAt) < rerunGrace
}
