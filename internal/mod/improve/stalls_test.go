package improve

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
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
// wait is still judged on its own: the watcher retries it from its branch's
// head first (TestStuckTaskIsRetriedOnceBeforeTheLead).
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
	if len(told) != 1 || told[waitKey("p", work.StageMerging, "waiting on main")] != 1 {
		t.Fatalf("handoffs = %v, want one for the red-main cause and t4 retried", told)
	}
	for _, s := range m.Stalls() {
		if strings.HasPrefix(s.Key, "wait:") && !strings.HasPrefix(s.What, "4 tasks (") {
			t.Errorf("the cause's stall reads %q, want it to name its 4 tasks", s.What)
		}
	}
}

// A task stuck on its way to the merge is retried from its branch's head by
// the watcher itself, as the lead's recheck would, and kept out of the
// stage's history; only if it sticks again, in any stage, does the lead hear.
func TestStuckTaskIsRetriedOnceBeforeTheLead(t *testing.T) {
	m := rig(t)
	stick := func(stage string) {
		m.mu.Lock()
		m.spells["t0"] = spell{stage: stage, since: time.Now().Add(-2 * time.Hour)}
		m.mu.Unlock()
	}
	m.C.J.MustAppend("task.saved", work.Task{ID: "t0", Project: "p", Title: "change t0", Stage: work.StageTesting})
	stick(work.StageTesting)
	m.Watch()
	if tk, _ := m.Work.Get("t0"); tk.Stage != work.StageLanding || !strings.Contains(tk.Log[len(tk.Log)-1].Text, "the stall watcher") {
		t.Fatalf("task after the first watch: %s %+v, want it retried into landing by the watcher", tk.Stage, tk.Log)
	}
	s := m.Stalls()
	if len(s) != 1 || s[0].Told || !s[0].Rechecked {
		t.Fatalf("stalls = %+v, want one retried and not handed off", s)
	}
	m.mu.Lock()
	n := len(m.took[work.StageTesting])
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("testing history has %d spells, want the retried one left out", n)
	}
	stick(work.StageLanding)
	m.Watch() // it moved on from testing
	m.Watch()
	s = m.Stalls()
	if len(s) != 1 || !s[0].Told || s[0].Stage != work.StageLanding || !strings.Contains(s[0].Did, "once already") {
		t.Fatalf("stalls = %+v, want it handed to the lead once stuck again", s)
	}
	if tk, _ := m.Work.Get("t0"); tk.Stage != work.StageLanding || strings.Count(fmt.Sprint(tk.Log), "the stall watcher") != 1 {
		t.Errorf("task after it stuck again: %s %+v, want it retried only once", tk.Stage, tk.Log)
	}
}

// A task handed to the lead stuck in a stage, that moves and then sticks in
// that stage again, is left with the lead: the lead hears "Stuck" once per
// task and stage, not every spell.
func TestStuckAgainInTheSameStageIsHandedOnce(t *testing.T) {
	m := rig(t)
	stuck := func() (n int) {
		for _, msg := range m.Chat.Messages("lead") {
			if strings.HasPrefix(msg.Text, "Stuck: ") {
				n++
			}
		}
		return
	}
	stick := func(stage string) {
		m.C.J.MustAppend("task.saved", work.Task{ID: "t0", Project: "p", Title: "change t0", Stage: stage})
		m.mu.Lock()
		m.spells["t0"] = spell{stage: stage, since: time.Now().Add(-2 * time.Hour)}
		m.mu.Unlock()
		m.Watch()
		m.Watch()
	}
	stick(work.StageTesting) // retried into landing by the watcher
	stick(work.StageLanding)
	if n := stuck(); n != 1 {
		t.Fatalf("Stuck notices after it stuck in landing: %d, want 1", n)
	}
	stick(work.StageMerging)
	if n := stuck(); n != 2 {
		t.Fatalf("Stuck notices after it stuck in merging: %d, want 2 (a new stage is news)", n)
	}
	stick(work.StageLanding)
	stick(work.StageMerging)
	if n := stuck(); n != 2 {
		t.Fatalf("Stuck notices after it stuck again in landing and merging: %d, want still 2", n)
	}
	if s := m.Stalls(); len(s) != 1 || !s[0].Told || !strings.HasPrefix(s[0].Did, "left it with the lead") {
		t.Fatalf("stalls = %+v, want the task left with the lead", s)
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

// The lead told a task is stuck is told the op that retries it.
func TestRetryHint(t *testing.T) {
	if h := retryHint("task:abc"); !strings.Contains(h, "hive agent recheck --task abc") {
		t.Fatalf("task hint: %q", h)
	}
	if h := retryHint("run:w1"); h != "" {
		t.Fatalf("a run has no recheck: %q", h)
	}
	if h := retryHint("checks@2125f74e"); !strings.Contains(h, "recheck --task <id>") {
		t.Fatalf("shared wait hint: %q", h)
	}
}

// A merged task's change is on main: the watcher leaves it be, however long
// it sits there, as a recheck of it is refused ("task is merged").
func TestMergedTaskIsNotAStall(t *testing.T) {
	m := rig(t)
	m.C.J.MustAppend("task.saved", work.Task{ID: "t0", Project: "p", Title: "change t0", Stage: work.StageMerged, MergeSHA: "abc"})
	m.mu.Lock()
	m.spells["t0"] = spell{stage: work.StageMerged, since: time.Now().Add(-2 * time.Hour)}
	m.mu.Unlock()
	m.Watch()
	m.Watch()
	if s := m.Stalls(); len(s) != 0 {
		t.Fatalf("stalls = %+v, want none for a merged task", s)
	}
}

// A merge step hands its merge to be measured without waiting for room in
// the queue: a full one held the step, so the task sat in merged for good.
func TestEnqueueDoesNotWaitForRoom(t *testing.T) {
	m := &Module{queue: make(chan work.Task)} // nothing reads it: as full as it gets
	done := make(chan struct{})
	go func() { m.enqueue(work.Task{ID: "t0"}); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("enqueue waited for room in a full queue")
	}
	select {
	case got := <-m.queue:
		if got.ID != "t0" {
			t.Fatalf("queued %+v, want t0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the merge never reached the queue")
	}
}

// A check that has just ended leaves the line before its step saves the
// task on, so a watch in between sees a testing task neither waiting nor
// running: it is timed from the check's end, not its long wait in the line,
// and isn't retried (which threw away its green test and started read).
func TestJustEndedCheckIsNoStall(t *testing.T) {
	m := rig(t)
	m.C.J.MustAppend("task.saved", work.Task{ID: "t0", Project: "p", Title: "change t0", Stage: work.StageTesting, SHA: "40496a88"})
	m.mu.Lock()
	m.spells["t0"] = spell{stage: work.StageTesting, since: time.Now().Add(-7 * time.Hour)}
	m.mu.Unlock()
	m.C.J.MustAppend("effect.checked", core.Check{Project: "p", SHA: "40496a88", Kind: "test", OK: true})
	m.Watch()
	if s := m.Stalls(); len(s) != 0 {
		t.Fatalf("stalls = %+v, want none for a task whose test just ended", s)
	}
	if tk, _ := m.Work.Get("t0"); tk.Stage != work.StageTesting {
		t.Fatalf("task is %s, want it left in testing", tk.Stage)
	}
}

// A worker the pace holds before its turn (the ramp after a strain, issue
// #2022) is waiting its turn, not stuck: its task isn't retried or handed to
// the lead, while a working task whose worker isn't held still is a stall.
func TestRunHeldByThePaceIsNoStall(t *testing.T) {
	m := rig(t)
	for _, id := range []string{"0", "1"} {
		m.C.J.MustAppend("run.created", core.RunSpec{ID: "w" + id, Role: "worker", Project: "p", Harness: "fake"})
		m.C.J.MustAppend("task.saved", work.Task{ID: "t" + id, Project: "p", Title: "change t" + id, Stage: work.StageWorking, Worker: "w" + id})
	}
	m.C.J.MustAppend("run.held", map[string]any{"id": "w0", "reason": "waiting: starting one turn at a time after the machine was strained"})
	m.mu.Lock()
	for _, id := range []string{"t0", "t1"} {
		m.spells[id] = spell{stage: work.StageWorking, since: time.Now().Add(-48 * time.Hour)}
	}
	m.mu.Unlock()
	m.Watch()
	s := m.Stalls()
	if len(s) != 1 || !strings.Contains(fmt.Sprint(s), "t1") || strings.Contains(fmt.Sprint(s), "t0") {
		t.Fatalf("stalls = %+v, want only t1's", s)
	}
}
