package work

import (
	"time"

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

// headTest returns the test check of t's head for the test step. One already
// recorded is used unless t asks for a recheck; otherwise the check is queued
// behind the step and ok is false: the step never waits for a check slot
// itself. Behind a backlog of checks, 20 test steps sat in Slots.Acquire,
// each holding its task, as merge steps did (issue #1699). The task waits in
// testing and is looked at on the next tick once the check ends.
//
// One test per task at a time: one still queued or running, for this head or
// one the task has left, is waited out rather than joined by another, so a
// task that goes round adds nothing to the backlog. Its result is taken once
// done, which clears the recheck; a stale head's is dropped. Runs are kept in
// memory, so after a restart a head with no recorded check, or one asked to
// be checked again, queues once more.
func (m *Module) headTest(t *Task, p core.Project, command string) (c core.Check, ok bool, err error) {
	m.mu.Lock()
	r := m.testRuns[t.ID]
	if r != nil && !r.done {
		m.later[t.ID] = time.Now().Add(holdGap) // until it ends, which looks again at once
		m.mu.Unlock()
		return core.Check{}, false, nil
	}
	delete(m.testRuns, t.ID)
	m.mu.Unlock()
	if r != nil && r.sha == t.SHA {
		t.Recheck = false
		return r.c, true, nil
	}
	if c, ok := m.C.Effects.GetCheck(p.Name, t.SHA, "test"); ok && !t.Recheck {
		return c, true, nil
	}
	if _, err := m.C.Sandbox(); err != nil {
		return core.Check{}, false, err
	}
	m.testHead(t, p, command)
	return core.Check{}, false, nil
}

// testHead queues the test of t's head behind the step.
func (m *Module) testHead(t *Task, p core.Project, command string) {
	r := &testRun{sha: t.SHA}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.testRuns == nil {
		m.testRuns = map[string]*testRun{}
	}
	m.testRuns[t.ID] = r
	m.later[t.ID] = time.Now().Add(holdGap) // set before it can end, which clears it
	m.steps.Add(1)
	go func(id, sha string) {
		defer m.steps.Done()
		c := m.C.Effects.RunCheck(p, sha, "test", command)
		m.mu.Lock()
		r.c, r.done = c, true
		if r.gone && m.testRuns[id] == r {
			delete(m.testRuns, id)
		}
		delete(m.later, id)
		m.mu.Unlock()
		m.wake()
	}(t.ID, t.SHA)
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
