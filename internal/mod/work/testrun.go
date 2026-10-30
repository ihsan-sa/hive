package work

import (
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// testRun is a task's test of its head running behind a step: sha the head it
// tests, c its check once done. gone says the task left testing while it
// ran, so it is forgotten when it ends.
type testRun struct {
	sha        string
	done, gone bool
	c          core.Check
}

// dropTestRunLocked forgets the test of a task that left testing: at once if
// it is done, when it ends if not. The caller holds m.mu.
func (m *Module) dropTestRunLocked(id string) {
	if r, ok := m.testRuns[id]; ok {
		if r.done {
			delete(m.testRuns, id)
		}
		r.gone = true
	}
}
