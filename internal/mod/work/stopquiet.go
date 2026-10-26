package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// opStop ends a quiet worker's run, so a task whose worker keeps ending turns
// without finishing doesn't sit working (holding its run and its task) for a
// day until the person stops it. Only a quiet run: one taking turns, waiting on
// an answer or parked is left alone. The run ends blocked, so its task goes
// blocked on the next tick and is the steerer's to bring back with recheck
// (its commits checked, or back sent to its worker); with fresh it goes at
// once to a fresh worker on the same branch with the first one's notes. A
// message to the ended run doesn't wake it (recheck's back reopens it).
func (m *Module) opStop(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		Task, Why string
		Fresh     bool
	}
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
	if t.Stage != StageWorking || t.Worker == "" {
		return nil, fmt.Errorf("task %s is %s, not working; only a working task's quiet worker is stopped this way", t.ID, t.Stage)
	}
	if t.Worker == r.ID {
		return nil, errors.New("that is your own task: finish it with `hive agent finish --status blocked` instead")
	}
	w, ok := m.C.Runs.Get(t.Worker)
	if !ok {
		return nil, fmt.Errorf("task %s's worker %s has no record", t.ID, t.Worker)
	}
	if w.Status != core.StatusQuiet {
		return nil, fmt.Errorf("worker %s is %s, not quiet; only a run that keeps ending turns without finishing is stopped this way (message it, or wait)", w.ID, w.Status)
	}
	why := core.Clip(core.StoppedQuiet+r.ID+": "+a.Why, 300)
	if err := m.C.Runs.EndQuiet(w.ID, why); err != nil {
		return nil, fmt.Errorf("%v (it took a turn since; message it, or wait)", err)
	}
	note := "its worker " + w.ID + " ended blocked; the task goes blocked on the next tick: bring it back with recheck"
	if a.Fresh {
		if err := m.Retry(t.ID); err != nil {
			return nil, err
		}
		note = "its worker " + w.ID + " ended blocked and the task is queued for a fresh worker on the same branch, with the first one's notes"
	}
	return map[string]string{"task": t.ID, "worker": w.ID, "note": note}, nil
}
