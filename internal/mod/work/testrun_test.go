package work

import (
	"slices"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The test step never waits for a check slot: with every slot taken, the
// head's test is queued, the step returns and the task waits in testing;
// a second step queues nothing more, and once a slot frees and the test
// passes, the next tick takes the result and the task goes on.
func TestTestStepNeverBlocks(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"feature.txt": "y"})
	m.C.Effects.Slots.Resize(func() int { return 1 })
	release := m.C.Effects.Slots.Acquire(p.Name, "other", "test")
	step := func() {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- m.test(task, p) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Minute):
			release()
			t.Fatal("the test step waited for a check slot")
		}
	}
	step()
	step()
	if task.Stage != StageTesting {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	if _, waiting := m.C.Effects.Slots.State(); len(waiting) != 1 {
		t.Fatalf("one test should wait for a slot, not %d: %+v", len(waiting), waiting)
	}
	release()
	m.Wait()
	m.mu.Lock()
	_, held := m.later[task.ID]
	m.mu.Unlock()
	if held {
		t.Fatal("a finished test should have its task looked at on the next tick")
	}
	step()
	if task.Stage == StageTesting || task.Stage == StageWorking {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.testRuns) != 0 {
		t.Fatalf("a taken test left behind: %d", len(m.testRuns))
	}
}

// A task that leaves testing while its test waits for a slot has it
// forgotten once it ends.
func TestTestRunForgottenWhenTaskLeaves(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"feature.txt": "y"})
	m.save(task, "")
	m.C.Effects.Slots.Resize(func() int { return 1 })
	release := m.C.Effects.Slots.Acquire(p.Name, "other", "test")
	if err := m.test(task, p); err != nil {
		release()
		t.Fatal(err)
	}
	task.Stage = StageWorking
	m.steer(task, "sent back")
	release()
	m.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.testRuns) != 0 {
		t.Fatalf("the test of a task that left testing is kept: %d", len(m.testRuns))
	}
}

// Main's own test and the rerun of what failed are queued behind the test
// step as the head's test is: with every slot taken, a red head's step
// returns while main's test waits for a slot, a second step queues nothing
// more, and the task says which commits it waits on (the stall watcher's
// slot match); the rerun of its failing tests doesn't hold the step either.
func TestTestStepQueuesMainAndRetest(t *testing.T) {
	m, task, p, _ := gateRig(t, false, map[string]string{"BROKEN": "x"})
	unblock := make(chan bool)
	m.Retest = func(core.Project, string, string, core.Check, bool) bool { return <-unblock }
	step := func() {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- m.test(task, p) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Minute):
			t.Fatal("the test step waited on a check")
		}
	}
	look := func() {
		t.Helper()
		m.Wait()
		m.mu.Lock()
		delete(m.later, task.ID)
		m.mu.Unlock()
	}
	step() // the head's test
	look()
	m.C.Effects.Slots.Resize(func() int { return 1 })
	release := m.C.Effects.Slots.Acquire(p.Name, "other", "test")
	step() // red: main's own test waits for the slot
	step()
	_, waiting := m.C.Effects.Slots.State()
	if len(waiting) != 1 || waiting[0].SHA == task.SHA {
		release()
		t.Fatalf("main's test alone should wait for a slot: %+v", waiting)
	}
	if got := m.Checking(task.ID); !slices.Equal(got, []string{waiting[0].SHA}) {
		release()
		t.Fatalf("the task waits on %v, want main's %s", got, waiting[0].SHA)
	}
	release()
	look()
	step() // main is green: the failing tests run again behind the step
	if got := m.Checking(task.ID); !slices.Equal(got, []string{task.SHA}) {
		t.Fatalf("the task waits on %v, want its head's rerun", got)
	}
	step()
	if task.Stage != StageTesting || task.Recheck {
		t.Fatalf("stage %q, recheck %v", task.Stage, task.Recheck)
	}
	unblock <- true // they pass alone: a flake, so the whole check runs once more
	look()
	step()
	if task.Stage != StageTesting || !task.Recheck || task.Retested != task.SHA {
		t.Fatalf("a flake should run once more: stage %q, recheck %v, retested %q", task.Stage, task.Recheck, task.Retested)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.behind) != 0 {
		t.Fatalf("taken checks left behind: %d", len(m.behind))
	}
}
