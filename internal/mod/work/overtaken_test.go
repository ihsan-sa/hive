package work

import (
	"sync"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// stallForge runs on() at the at'th read of checks, from inside the step that
// asked, and waits for it: a person's stop landing at that point of a step.
type stallForge struct {
	*core.Local
	mu    sync.Mutex
	n, at int
	on    func()
}

func (f *stallForge) Checks(repo, sha string) ([]core.CheckRun, error) {
	f.mu.Lock()
	f.n++
	hit := f.n == f.at
	f.mu.Unlock()
	if hit {
		f.on()
	}
	return f.Local.Checks(repo, sha)
}

// A Tick-started merge step stopped mid-way, at every read of the forge's
// checks it makes (the merge's own last look included), doesn't merge, and the
// person's stop stands.
func TestStopMidMergeStepStands(t *testing.T) {
	run := func(at int) (int, Task, string) {
		m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
		toMerging(t, m, task, p)
		f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass})
		sf := &stallForge{Local: f, at: at}
		sf.on = func() {
			done := make(chan struct{})
			go func() { // the person, beside the step
				defer close(done)
				if err := m.Stop(task.ID, "stopped by the person"); err != nil {
					t.Error(err)
				}
			}()
			<-done
		}
		m.C.Effects.Forge = sf
		m.Tick()
		m.Wait()
		got, _ := m.Get(task.ID)
		return sf.n, got, prState(t, f, task.PR.Number)
	}
	n, got, state := run(0)
	if got.Stage != StageMerged || state != "MERGED" || n == 0 {
		t.Fatalf("unstopped: stage %q, reason %q, PR %s, %d reads of checks", got.Stage, got.Reason, state, n)
	}
	for at := 1; at <= n; at++ {
		_, got, state := run(at)
		if got.Stage != StageStopped || !got.ByPerson || state != "OPEN" {
			t.Fatalf("stopped at read %d of %d: stage %q, by person %v, PR %s", at, n, got.Stage, got.ByPerson, state)
		}
	}
}
