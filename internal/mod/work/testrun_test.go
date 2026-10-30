package work

import (
	"testing"
)

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
