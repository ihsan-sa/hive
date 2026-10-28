package work

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A worker starting beside another on the same area (two files of one
// package) is told who that is, with its run id to message, in its system
// prompt, which its reviewers never get.
func TestWorkerIsToldWhoWorksNearby(t *testing.T) {
	m := queueRig(t)
	brief(t, m, "w-one", map[string]any{"title": "Speed up the effects reader", "done": "internal/core/effects.go reads once"})
	tick(m)
	brief(t, m, "w-two", map[string]any{"title": "Runs keep their exit code", "done": "internal/core/runs.go records it"})
	tick(m)
	tasks := m.List(nil)
	if len(tasks) != 2 || tasks[0].Worker == "" || tasks[1].Worker == "" {
		t.Fatalf("both should be at work: %+v", tasks)
	}
	first, second := tasks[0], tasks[1]
	if first.Title != "Speed up the effects reader" {
		first, second = second, first
	}
	run, _ := m.C.Runs.Get(second.Worker)
	if !strings.Contains(run.System, "## Who works nearby") || !strings.Contains(run.System, first.Worker) || !strings.Contains(run.System, "`internal/core/`") {
		t.Fatalf("the second worker wasn't told of the first:\n%s", run.System)
	}
	if strings.Contains(run.Brief, "Who works nearby") {
		t.Fatal("neighbours went in the brief, which reviewers and judges get")
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

// A worker sent back on a conflict is told which tasks merged into main
// since its branch, with their runs to ask; one its branch has already isn't
// named.
func TestConflictNamesWhoMovedMain(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	pushMain(t, f, map[string]string{"feature.go": "package q\n"})
	moved, _ := m.C.Effects.Sync(p)
	m.save(&Task{ID: "t-mover", Project: p.Name, Title: "Rename the feature package", Stage: StageDone, Worker: "w-mover", MergeSHA: moved}, "merged")
	m.save(&Task{ID: "t-old", Project: p.Name, Title: "Something the branch has", Stage: StageDone, Worker: "w-old", MergeSHA: task.SHA}, "merged")
	if err := m.conflict(task, p, task.SHA, errors.New("main moved")); err != nil {
		t.Fatal(err)
	}
	got := task.Resend // sent when the pace has room
	if !strings.Contains(got, "task t-mover, run w-mover") || strings.Contains(got, "t-old") {
		t.Fatalf("conflict message:\n%s", got)
	}
}
