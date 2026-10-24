package work

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Agents' checkouts under a project's .checkouts are removed once nothing can
// take a turn in them again (they had piled up to 641, 3.1 GB, in a day):
//
//   - a task's worker checkout when the task is merged or done, or has sat
//     stopped or blocked for staleTask (Recheck and a warden's answer bring a
//     worker back in its own checkout until then);
//   - a reviewer's or seat's checkout, and an earlier worker's after Retry,
//     once its run has ended and its task is over or the run is staleRun old;
//   - a run with no task (a spawned reviewer, a step-back), once ended and
//     staleRun old; a lead's (an isolated project's) after staleTask;
//   - a folder no run names, once staleRun old.
//
// A run that hasn't ended keeps its checkout, whatever its task says.
const (
	staleTask = 7 * 24 * time.Hour
	staleRun  = 24 * time.Hour
)

// over reports whether a task can no longer bring its worker back.
func over(t Task, now time.Time) bool {
	return t.Stage == StageMerged || t.Stage == StageDone || t.Terminal() && now.Sub(t.Updated) > staleTask
}

// spent reports whether the checkout named id may go.
func (m *Module) spent(id string, now time.Time, dir string) bool {
	r, ok := m.C.Runs.Get(id)
	if !ok {
		fi, err := os.Stat(dir)
		return err == nil && now.Sub(fi.ModTime()) > staleRun
	}
	if !r.Terminal() {
		return false
	}
	old := now.Sub(r.Updated) > staleRun
	if r.Role == "lead" {
		return now.Sub(r.Updated) > staleTask
	}
	if r.Task == "" {
		return old
	}
	t, ok := m.Get(r.Task)
	if !ok {
		return old
	}
	if t.Worker == id {
		return over(t, now)
	}
	return over(t, now) || old
}

// PruneCheckouts removes every spent checkout of every project, for the
// hive.clean job.
func (m *Module) PruneCheckouts() string {
	now := time.Now()
	n, freed := 0, int64(0)
	for _, p := range m.C.Projects.List() {
		root := m.C.WS.Checkouts(p.Name)
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if !e.IsDir() || !m.spent(e.Name(), now, dir) {
				continue
			}
			size := core.Size(dir)
			if m.C.Effects.RemoveCheckout(p.Name, e.Name()) == nil {
				n, freed = n+1, freed+size
			}
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("removed %d checkout(s), %s", n, core.Bytes(freed))
}

// ended removes a task's spent checkouts as it ends (merged or done).
func (m *Module) ended(t Task) {
	now := time.Now()
	for _, r := range m.C.Runs.List(func(r *core.Run) bool { return r.Task == t.ID }) {
		if dir := filepath.Join(m.C.WS.Checkouts(t.Project), r.ID); core.Exists(dir) && m.spent(r.ID, now, dir) {
			m.C.Effects.RemoveCheckout(t.Project, r.ID)
		}
	}
}
