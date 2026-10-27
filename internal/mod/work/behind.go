package work

import (
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// behindRun is a check a step queued behind itself rather than wait for a
// check slot: sha the commit it checks, waiters the tasks waiting on it,
// each looked at again at once when it ends, c its result once done.
type behindRun struct {
	sha     string
	waiters map[string]bool
	done    bool
	c       core.Check
}

// runBehind returns the result of the check key (of sha) for task id once it
// is done. Until then it is queued behind the step, once however many tasks
// ask, and ok is false: the task waits where it is and is looked at again
// when it ends. A step never waits for a check slot itself (issues #1699,
// #1703). A result is taken once, by the first waiter to come back; what the
// run recorded (Effects' checks) answers the others.
func (m *Module) runBehind(id, key, sha string, run func() core.Check) (core.Check, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.behind[key]
	if r != nil && r.done {
		delete(m.behind, key)
		return r.c, true
	}
	m.later[id] = time.Now().Add(holdGap) // set before it can end, which clears it
	if r != nil {
		r.waiters[id] = true
		return core.Check{}, false
	}
	if m.behind == nil {
		m.behind = map[string]*behindRun{}
	}
	r = &behindRun{sha: sha, waiters: map[string]bool{id: true}}
	m.behind[key] = r
	m.steps.Add(1)
	go func() {
		defer m.steps.Done()
		c := run()
		m.mu.Lock()
		defer m.mu.Unlock()
		r.c, r.done = c, true
		if len(r.waiters) == 0 && m.behind[key] == r {
			delete(m.behind, key)
		}
		for w := range r.waiters {
			delete(m.later, w)
		}
	}()
	return core.Check{}, false
}

// takeBehind returns the result of the check key once done, and forgets it.
func (m *Module) takeBehind(key string) (core.Check, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.behind[key]
	if r == nil || !r.done {
		return core.Check{}, false
	}
	delete(m.behind, key)
	return r.c, true
}

// leaveBehindLocked takes task id off the checks it waits on, as it leaves
// the stage that queued them; one left with no waiters is forgotten once
// done. The caller holds m.mu.
func (m *Module) leaveBehindLocked(id string) {
	for k, r := range m.behind {
		delete(r.waiters, id)
		if r.done && len(r.waiters) == 0 {
			delete(m.behind, k)
		}
	}
}

// pendingLocked says whether task id waits on a check queued behind a step
// that hasn't ended. The caller holds m.mu.
func (m *Module) pendingLocked(id string) bool {
	return len(m.checkingLocked(id)) > 0
}

// Checking returns the commits whose checks task id waits on behind its
// steps, still queued or running: its head's test (for a head it may have
// left), its head merged with main, main's own test or a retest. The stall
// watcher counts a task whose check waits for a slot as waiting its turn.
func (m *Module) Checking(id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkingLocked(id)
}

func (m *Module) checkingLocked(id string) []string {
	var out []string
	if r, ok := m.testRuns[id]; ok && !r.done {
		out = append(out, r.sha)
	}
	if r, ok := m.mergeRuns[id]; ok && !r.done {
		out = append(out, r.sha)
	}
	for _, r := range m.behind {
		if !r.done && r.waiters[id] {
			out = append(out, r.sha)
		}
	}
	return out
}
