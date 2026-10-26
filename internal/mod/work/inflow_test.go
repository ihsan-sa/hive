package work

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A project with as many tasks open as the floor (or as it merged in the
// last six hours, if more) takes no new gain from an agent: the brief is
// refused with what to do instead. A fix and the person's ask in their words
// are still filed, and once work merges or the merge rate rises, gains file
// again (#899).
func TestFilingSlowsToWhatMerges(t *testing.T) {
	m := queueRig(t)
	words := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet"}
	for _, w := range words {
		brief(t, m, "lead-1", map[string]any{"title": "Make the " + w + " path quicker", "apart": true})
	}
	try := func(b map[string]any) (map[string]string, error) {
		raw, _ := json.Marshal(b)
		out, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "sweep-1", Role: "sweep", Project: "p"}}, raw)
		if err != nil {
			return nil, err
		}
		return out.(map[string]string), nil
	}
	if _, err := try(map[string]any{"title": "Make the kilo path quicker"}); err == nil || !strings.Contains(err.Error(), "10 tasks open and merged 0") {
		t.Fatalf("a gain was filed past the backlog: %v", err)
	}
	if out, err := try(map[string]any{"title": "TestKilo fails on main", "aim": "fix", "why": "TestKilo fails"}); err != nil || out["task"] == "" {
		t.Fatalf("a fix wasn't filed: %v %v", out, err)
	}
	out, err := try(map[string]any{"title": "Add a lima switch", "asked": "please add a lima switch"})
	if err != nil {
		t.Fatalf("the person's ask wasn't filed: %v", err)
	}
	if got, _ := m.Get(out["task"]); !strings.Contains(got.Known, "The person asked: please add a lima switch") {
		t.Fatalf("the person's words aren't in its known: %q", got.Known)
	}

	// Twelve open now; three merge, which brings the open work under the floor.
	for i, task := range m.List(nil)[:3] {
		task.Stage = StageMerged
		task.MergeSHA = fmt.Sprintf("%040d", i)
		m.save(&task, "merged as "+core.Short(task.MergeSHA))
	}
	if out, err := try(map[string]any{"title": "Make the mike path quicker"}); err != nil || out["task"] == "" {
		t.Fatalf("a gain wasn't filed with nine open and three merged: %v %v", out, err)
	}
	if _, err := try(map[string]any{"title": "Make the november path quicker"}); err == nil {
		t.Fatal("a gain was filed with ten open and three merged")
	}
}

// Merges older than the window don't count toward the rate.
func TestMergedSinceWindow(t *testing.T) {
	now := time.Now()
	task := Task{Log: []Entry{{At: now.Add(-7 * time.Hour), Text: "merged as aaa"}, {At: now.Add(-time.Hour), Text: "checkpoint merged as bbb; the worker keeps going"}, {At: now, Text: "merged on the forge outside the Hive (PR #3) as ccc"}, {At: now, Text: "landed"}}}
	if n := task.mergedSince(now.Add(-inflowWindow)); n != 2 {
		t.Fatalf("want 2 merges in the window, got %d", n)
	}
}
