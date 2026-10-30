package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Search shows the recent tasks like the query in the asker's project, with
// their runs, and none of another project's.
func TestRelatedTasksForSearch(t *testing.T) {
	m := queueRig(t)
	brief(t, m, "w-one", map[string]any{"title": "Speed up the effects reader", "done": "internal/core/effects.go reads once"})
	brief(t, m, "w-two", map[string]any{"title": "Runs keep their exit code", "done": "internal/core/runs.go records it"})
	tick(m)
	var first, second Task
	for _, o := range m.List(nil) {
		if o.Title == "Speed up the effects reader" {
			first = o
		} else {
			second = o
		}
	}
	if first.ID == "" || second.ID == "" {
		t.Fatalf("tasks: %+v", m.List(nil))
	}
	if got := m.Related("p", "effects reader slow"); !strings.Contains(got, first.ID) || strings.Contains(got, second.ID) {
		t.Fatalf("search's related tasks: %q", got)
	}
	if got := m.Related("other", "effects reader slow"); got != "" {
		t.Fatalf("another project's run saw p's tasks: %q", got)
	}
}

// A red forge check whose failing test other tasks met lately says so in
// forge-failures.md, from what the problems module's Seen answers.
func TestForgeFailuresSayWhoMetThemBefore(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	var got []string
	m.Seen = func(project, run, taskID string, keys []string) string {
		got = keys
		return "- `x TestFlaky`: also seen by w-before (task t-0, 2h ago); board p-1\n"
	}
	toMerging(t, m, task, p)
	f.SetCheckLog(p.Repo, "head/1", "--- FAIL: TestFlaky (0.1s)\nFAIL\tx\t0.2s\n##[error]Process completed with exit code 1.\n")
	red := core.CheckRun{Name: "test (ubuntu-latest)", State: core.CheckFail, URL: "head/1"}
	f.SetChecks(p.Repo, task.SHA, red)
	mergeOnce(t, m, task, p)
	task.RerunAt = time.Now().Add(-2 * rerunGrace)
	m.later = map[string]time.Time{}
	f.SetChecks(p.Repo, task.SHA, red)
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	b, err := os.ReadFile(filepath.Join(m.C.WS.RunRO(task.Worker), forgeFailuresFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || !strings.Contains(string(b), "## Seen before") || !strings.Contains(string(b), "w-before") {
		t.Fatalf("keys %v; forge-failures.md:\n%s", got, b)
	}
}
