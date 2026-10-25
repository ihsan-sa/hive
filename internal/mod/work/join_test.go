package work

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func brief(t *testing.T, m *Module, from string, b map[string]any) map[string]string {
	t.Helper()
	raw, _ := json.Marshal(b)
	out, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: from, Role: "worker", Project: "p"}}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]string)
}

// Two workers stuck on the same red check file a brief about it a minute
// apart (as #290 and #291 were), one naming no path the queue could hold it
// by: the second joins the first as added context, so one worker takes it.
// A brief filed once that worker has started joins it too, and its worker is
// told. Different work, or work its filer says is apart, is filed on its own.
func TestDuplicateBriefJoinsOpenTask(t *testing.T) {
	m := queueRig(t)
	first := brief(t, m, "w-one", map[string]any{"title": "TestBlockedWorkComesBack times out on the Windows runner",
		"done": "The test passes reliably on windows-latest", "known": "It waited 4 minutes for the tool worker."})
	second := brief(t, m, "w-two", map[string]any{"title": "Windows CI e2e flakes: TestBlockedWorkComesBack never wakes the tool worker",
		"done": "TestBlockedWorkComesBack and TestModuleLifecycle pass reliably on windows-latest", "known": "Run 40078245067: the worker was never woken for turn 3."})
	if second["task"] != first["task"] || second["joined"] != "true" || !strings.Contains(second["note"], "TestBlockedWorkComesBack") {
		t.Fatalf("the second brief didn't join the first: %v / %v", first, second)
	}
	tick(m)
	tasks := m.List(nil)
	if len(tasks) != 1 {
		t.Fatalf("want one task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Stage != StageWorking || task.Worker == "" {
		t.Fatalf("the task didn't start its worker: %s %s", task.Stage, task.Reason)
	}
	if !strings.Contains(task.Known, "Also asked by w-two") || !strings.Contains(task.Known, "Run 40078245067") || !strings.Contains(task.Known, "TestModuleLifecycle") {
		t.Fatalf("the joined brief isn't in the task's context: %q", task.Known)
	}
	run, _ := m.C.Runs.Get(task.Worker)
	if !strings.Contains(run.Brief, "Run 40078245067") {
		t.Fatal("the worker's brief lacks the joined ask")
	}

	third := brief(t, m, "sweep-x", map[string]any{"title": "TestModuleLifecycle fails on Windows", "done": "TestModuleLifecycle passes there"})
	if third["task"] != task.ID {
		t.Fatalf("a brief on a test the working task names didn't join it: %v", third)
	}
	if got, _ := m.Get(task.ID); !got.JoinedBy("sweep-x") || got.JoinedBy("sweep") {
		t.Fatalf("the join isn't on the task's record: %v", got.Log)
	}

	other := brief(t, m, "w-two", map[string]any{"title": "Show the queue on the dashboard", "known": "internal/tui/views.go"})
	apart := brief(t, m, "w-two", map[string]any{"title": "TestBlockedWorkComesBack also needs a Linux leg", "apart": true})
	if other["task"] == task.ID || apart["task"] == task.ID || other["joined"] != "" || apart["joined"] != "" {
		t.Fatalf("separate work joined the task: %v / %v", other, apart)
	}
	if n := len(m.List(nil)); n != 3 {
		t.Fatalf("want three tasks, got %d", n)
	}
}

// What makes two briefs the same ask, on the briefs filed on 2026-10-24.
func TestBriefMatch(t *testing.T) {
	open := Task{Title: "Windows e2e flakes: TestReportSharing and TestModuleLifecycle on a slow runner", Done: "Both tests pass reliably on test (windows-latest)"}
	for _, c := range []struct {
		b    Brief
		same bool
	}{
		{Brief{Title: "e2e tests flake on the slow macOS/Windows runners", Done: "TestReportSharing passes on macOS and Windows"}, true},
		{Brief{Title: "Main's Windows test and the update test are green again", Done: "cmd/hive TestInstallerShareAnswer is fixed", Known: "TestModuleLifecycle passed earlier"}, false},
		{Brief{Title: "windows e2e flakes: testreportsharing and testmodulelifecycle on a slow runner!"}, true},
		{Brief{Title: "Show the queue on the dashboard", Known: "internal/tui/views.go"}, false},
	} {
		if got := match(c.b, open) != ""; got != c.same {
			t.Errorf("%q: same ask %v, want %v", c.b.Title, got, c.same)
		}
	}
	files := Task{Title: "Cap the checks run in internal/core/effects.go"}
	if match(Brief{Title: "Cap how many checks run at once", Known: "see internal/core/effects.go."}, files) == "" {
		t.Error("the same file under a similar title isn't the same ask")
	}
	if match(Brief{Title: "Log every effect", Known: "internal/core/effects.go"}, files) != "" {
		t.Error("the same file under another title is the same ask")
	}
}
