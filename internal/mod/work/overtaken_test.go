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

// A queued task stopped while its step starts the worker ends with no worker
// running: before the start the step starts none, and after it the stop ends
// the run its copy didn't name. A push or hold comment on a stopped copy
// doesn't go out either.
func TestStopStandsOverAStartingWorker(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	queued := Task{ID: "q1", Project: "p", Title: "tune the feature", Kind: "work", Stage: StageQueued}
	m.save(&queued, "")
	step, _ := m.Get(queued.ID)
	if err := m.Stop(queued.ID, "stopped by the person"); err != nil {
		t.Fatal(err)
	}
	if err := m.startWorker(&step, p); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.C.Runs.Get(workerID(step)); ok {
		t.Fatal("a worker started for a task stopped before its start")
	}

	other := Task{ID: "q2", Project: "p", Title: "tune it again", Kind: "work", Stage: StageQueued}
	m.save(&other, "")
	stale, _ := m.Get(other.ID) // the person's copy, from before the step saved its worker
	if err := m.startWorker(&other, p); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.tasks[other.ID] = &stale // as if the step's save hadn't landed yet
	m.mu.Unlock()
	if err := m.Stop(other.ID, "stopped by the person"); err != nil {
		t.Fatal(err)
	}
	if r, ok := m.C.Runs.Get(workerID(other)); !ok || !r.Terminal() {
		t.Fatalf("the worker its stop's copy didn't name runs on: %+v", r)
	}

	merging, _ := m.Get(task.ID)
	if err := m.Stop(task.ID, "stopped by the person"); err != nil {
		t.Fatal(err)
	}
	before := len(f.Comments(p.Repo, task.PR.Number))
	m.hold(&merging, p, "waiting on something new")
	if err := m.takeMain(&merging, p, "0123456789012345678901234567890123456789", "main moved"); err != nil {
		t.Fatal(err)
	}
	if len(f.Comments(p.Repo, task.PR.Number)) != before {
		t.Fatal("a stopped step told the PR of a hold")
	}
	if got, _ := m.Get(task.ID); got.Stage != StageStopped {
		t.Fatalf("stage %q", got.Stage)
	}
}
