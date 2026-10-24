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
	red := "waiting on main: the forge's checks ci fail on main at a6c44d9d too, so this failure isn't the worker's"
	for i, reason := range []string{red, red, red, red, ""} {
		id := fmt.Sprintf("t%d", i)
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "p", Title: "change " + id, Stage: work.StageMerging, Reason: reason})
		m.mu.Lock()
		m.spells[id] = spell{work.StageMerging, time.Now().Add(-2 * time.Hour)}
		m.mu.Unlock()
	}
	m.Watch()
	m.Watch()
	told := map[string]int{}
	for _, e := range m.C.J.Since(0) {
		var s Stall
		if e.Kind == "stall.saved" && e.Decode(&s) == nil && s.Told {
			told[s.Key]++
		}
	}
	if len(told) != 2 || told["task:t4"] != 1 || told["wait:p:"+work.StageMerging+":"+red] != 1 {
		t.Fatalf("handoffs = %v, want one for the red-main cause and one for t4", told)
	}
	for _, s := range m.Stalls() {
		if strings.HasPrefix(s.Key, "wait:") && !strings.HasPrefix(s.What, "4 tasks (") {
			t.Errorf("the cause's stall reads %q, want it to name its 4 tasks", s.What)
		}
	}
}
