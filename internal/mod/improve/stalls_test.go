package improve

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// The limit is three times the 90th percentile of the stage's own history,
// never under the floor, which is all there is with no history.
func TestStallLimit(t *testing.T) {
	floor := time.Minute
	for _, c := range []struct {
		took []float64
		want time.Duration
	}{
		{nil, time.Minute},
		{[]float64{5, 10}, time.Minute},
		{[]float64{100, 30, 60}, 300 * time.Second},
		{[]float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 9000}, 300 * time.Second}, // one outlier in twenty doesn't move it
	} {
		if got := StallLimit(c.took, floor); got != c.want {
			t.Errorf("StallLimit(%v) = %s, want %s", c.took, got, c.want)
		}
	}
}

// Tasks held on the same named wait (main is red) are one stall: the lead
// hears about the cause once, not once per task. A task stuck with no named
// wait is still handed off on its own.
func TestSharedWaitIsOneHandoff(t *testing.T) {
	m := rig(t)
	red := "waiting on main: its own tests fail at a6c44d9d, so this failure isn't the worker's"
	for i, reason := range []string{red, red, red, red, ""} {
		id := fmt.Sprintf("t%d", i)
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "p", Title: "change " + id, Stage: work.StageMerging, Reason: reason})
		m.mu.Lock()
		m.spells[id] = spell{stage: work.StageMerging, since: time.Now().Add(-2 * time.Hour)}
		m.mu.Unlock()
	}
	m.Watch()
	m.Watch()
	told := map[string]int{}
	acts := map[string]bool{}
	for _, e := range m.C.J.Since(0) {
		var s Stall
		if e.Kind == "stall.saved" && e.Decode(&s) == nil && s.Told && !acts[s.Key+s.At.String()] {
			acts[s.Key+s.At.String()] = true
			told[s.Key]++
		}
	}
	if len(told) != 2 || told["task:t4"] != 1 || told[waitKey("p", work.StageMerging, "waiting on main")] != 1 {
		t.Fatalf("handoffs = %v, want one for the red-main cause and one for t4", told)
	}
	for _, s := range m.Stalls() {
		if strings.HasPrefix(s.Key, "wait:") && !strings.HasPrefix(s.What, "4 tasks (") {
			t.Errorf("the cause's stall reads %q, want it to name its 4 tasks", s.What)
		}
	}
}

// holdOnMain saves tasks ids held on red main at sha, as if they had been in
// merging for two hours and waiting on main for waited.
func holdOnMain(m *Module, sha string, waited time.Duration, ids ...string) {
	for _, id := range ids {
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "p", Title: "change " + id, Stage: work.StageMerging, Reason: "waiting on main: its own tests fail at " + sha + ", so this failure isn't the worker's"})
		m.mu.Lock()
		sp := m.spells[id]
		sp.since, sp.waitSince = time.Now().Add(-2*time.Hour), time.Now().Add(-waited)
		m.spells[id] = sp
		m.mu.Unlock()
	}
}

// A red main is one cause however main moves: a new main commit doesn't
// start a second group that's handed off again, and the alert names
// whoever waits now, including tasks that joined after the handoff.
func TestRedMainAcrossMainCommitsIsOneHandoff(t *testing.T) {
	m := rig(t)
	holdOnMain(m, "aaaaaaa", 2*time.Hour, "t0", "t1")
	m.Watch()
	key := waitKey("p", work.StageMerging, "waiting on main")
	var told int
	for _, e := range m.C.J.Since(0) {
		var s Stall
		if e.Kind == "stall.saved" && e.Decode(&s) == nil && s.Told {
			told++
		}
	}
	if told != 1 {
		t.Fatalf("handoffs after the first watch = %d, want 1", told)
	}
	holdOnMain(m, "bbbbbbb", time.Minute, "t0", "t1", "t2")
	m.Watch()
	stalls := m.Stalls()
	if len(stalls) != 1 || stalls[0].Key != key || !strings.HasPrefix(stalls[0].What, "3 tasks (") || !strings.Contains(stalls[0].What, "bbbbbbb") {
		t.Fatalf("stalls = %+v, want the one red-main stall naming its 3 tasks at the new main", stalls)
	}
	if s := stalls[0]; s.Tries != 1 {
		t.Errorf("the red-main stall acted %d times, want once", s.Tries)
	}
}

// A group is timed from when its tasks began waiting, not from when they
// entered the stage: a long merge that has only just met red main isn't stuck.
func TestWaitIsTimedFromItsStart(t *testing.T) {
	m := rig(t)
	holdOnMain(m, "aaaaaaa", time.Minute, "t0")
	m.Watch()
	if s := m.Stalls(); len(s) != 0 {
		t.Fatalf("stalls = %+v, want none a minute into the wait", s)
	}
}

// Time spent in a wait the watcher handed off isn't a normal spell of its
// stage, so it doesn't raise the stage's limit.
func TestHandedOffWaitStaysOutOfHistory(t *testing.T) {
	m := rig(t)
	holdOnMain(m, "aaaaaaa", 2*time.Hour, "t0", "t1")
	m.Watch()
	m.C.J.MustAppend("task.saved", work.Task{ID: "t0", Project: "p", Title: "change t0", Stage: work.StageDone})
	m.C.J.MustAppend("task.saved", work.Task{ID: "t2", Project: "p", Title: "change t2", Stage: work.StageMerging})
	m.C.J.MustAppend("task.saved", work.Task{ID: "t2", Project: "p", Title: "change t2", Stage: work.StageDone})
	m.mu.Lock()
	n := len(m.took[work.StageMerging])
	m.mu.Unlock()
	if n != 1 {
		t.Fatalf("merging history has %d spells, want only t2's", n)
	}
}
