package work

import (
	"testing"
	"time"
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
