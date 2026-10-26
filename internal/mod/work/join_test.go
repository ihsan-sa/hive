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
// told. Different work, a gain or an unclear brief (taken as a gain) on the
// same test, or work its filer says is apart, is filed on its own.
func TestDuplicateBriefJoinsOpenTask(t *testing.T) {
	m := queueRig(t)
	first := brief(t, m, "w-one", map[string]any{"title": "TestBlockedWorkComesBack times out on the Windows runner",
		"done": "The test passes reliably on windows-latest", "known": "It waited 4 minutes for the tool worker.", "aim": "fix", "why": "TestBlockedWorkComesBack times out"})
	second := brief(t, m, "w-two", map[string]any{"title": "Windows CI e2e flakes: TestBlockedWorkComesBack never wakes the tool worker",
		"done": "TestBlockedWorkComesBack and TestModuleLifecycle pass reliably on windows-latest", "known": "Run 40078245067: the worker was never woken for turn 3.", "aim": "fix", "why": "TestBlockedWorkComesBack never wakes"})
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

	third := brief(t, m, "sweep-x", map[string]any{"title": "TestModuleLifecycle fails on Windows", "done": "TestModuleLifecycle passes there", "aim": "fix", "why": "TestModuleLifecycle fails"})
	if third["task"] != task.ID {
		t.Fatalf("a brief on a test the working task names didn't join it: %v", third)
	}
	if got, _ := m.Get(task.ID); !got.JoinedBy("sweep-x") || got.JoinedBy("sweep") {
		t.Fatalf("the join isn't on the task's record: %v", got.Log)
	}
	// The issue was opened with w-two's ask in its body; sweep-x's, joined
	// once it was open, goes on it as a comment, once however often it ticks.
	tick(m)
	tick(m)
	forge := m.C.Effects.Forge.(*flakyForge)
	var posted []string
	for _, c := range forge.Comments("me/p", task.Issue.Number) {
		if strings.Contains(c, "Another brief joined") {
			posted = append(posted, c)
		}
	}
	if len(posted) != 1 || !strings.Contains(posted[0], "Also asked by sweep-x") {
		t.Fatalf("want sweep-x's ask on the issue once, got %q", posted)
	}
	if got, _ := m.Get(task.ID); got.JoinsPosted != 2 || len(got.Joins) != 2 {
		t.Fatalf("joins %d, posted %d", len(got.Joins), got.JoinsPosted)
	}
	// The aim is read as File reads it: " Fix " with a failure joins the fix,
	// " Gain" doesn't, and an aim that is neither is refused before it joins.
	if fix := brief(t, m, "w-four", map[string]any{"title": "TestModuleLifecycle hangs", "done": "TestModuleLifecycle passes", "aim": " Fix ", "why": "it hangs"}); fix["task"] != task.ID {
		t.Fatalf("a fix said as \" Fix \" didn't join the fix: %v", fix)
	}
	if g := brief(t, m, "w-four", map[string]any{"title": "TestModuleLifecycle hangs less", "done": "TestModuleLifecycle passes", "aim": " Gain", "why": "it hangs"}); g["task"] == task.ID {
		t.Fatalf("a gain joined the fix: %v", g)
	}
	raw, _ := json.Marshal(map[string]any{"title": "TestModuleLifecycle hangs", "aim": "urgent", "why": "it hangs"})
	if _, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "w-four", Role: "worker", Project: "p"}}, raw); err == nil {
		t.Fatal("an aim that is neither fix nor gain was taken")
	}

	other := brief(t, m, "w-two", map[string]any{"title": "Show the queue on the dashboard", "known": "internal/tui/views.go"})
	apart := brief(t, m, "w-two", map[string]any{"title": "TestBlockedWorkComesBack also needs a Linux leg", "apart": true})
	gain := brief(t, m, "w-two", map[string]any{"title": "TestBlockedWorkComesBack runs in half the time", "done": "TestBlockedWorkComesBack takes 20s", "aim": "gain"})
	unclear := brief(t, m, "w-three", map[string]any{"title": "TestBlockedWorkComesBack logs each wake", "done": "TestBlockedWorkComesBack prints a line per wake"})
	if other["task"] == task.ID || apart["task"] == task.ID || gain["task"] == task.ID || unclear["task"] == task.ID || other["joined"] != "" || apart["joined"] != "" {
		t.Fatalf("separate work joined the fix: %v / %v / %v / %v", other, apart, gain, unclear)
	}
	// The apart brief names no failure, so it is a gain too, and the gain and
	// the unclear brief on its test join it rather than the fix.
	if gain["task"] != apart["task"] || unclear["task"] != apart["task"] {
		t.Fatalf("gains on one test didn't join each other: %v / %v / %v", apart, gain, unclear)
	}
	if n := len(m.List(nil)); n != 4 {
		t.Fatalf("want four tasks, got %d", n)
	}
}

// What makes two briefs the same ask, on the briefs filed on 2026-10-24.
func TestBriefMatch(t *testing.T) {
	pad := Task{Title: "add left-pad FAKE:needpackage"}
	if match(Brief{Title: "add right-pad FAKE:needpackage"}, pad) != "" {
		t.Error("two asks whose short titles differ in one word are the same ask")
	}
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

// A working task whose worker has finished but not yet pushed isn't joined,
// since the worker would never read the added ask; the brief files on its own.
// A lead's brief that joins a task has its thread told how that task ends.
func TestJoinSkipsEndedWorkerAndTellsLeadThread(t *testing.T) {
	m := queueRig(t)
	first := brief(t, m, "w-one", map[string]any{"title": "TestQueueOrder fails on main", "aim": "fix", "why": "TestQueueOrder fails"})
	tick(m)
	task, _ := m.Get(first["task"])
	if task.Stage != StageWorking {
		t.Fatalf("not working: %s %s", task.Stage, task.Reason)
	}
	raw, _ := json.Marshal(map[string]any{"title": "TestQueueOrder is red", "aim": "fix", "why": "TestQueueOrder fails"})
	out, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead", Project: "p"}}, raw)
	if err != nil || out.(map[string]string)["task"] != task.ID {
		t.Fatalf("the lead's brief didn't join: %v %v", out, err)
	}
	got, _ := m.Get(task.ID)
	if len(got.Joins) != 1 || got.Joins[0].From != "lead" {
		t.Fatalf("joins %+v", got.Joins)
	}
	got.Joins[0].Chat, got.Joins[0].Thread = "lead", "t-person" // as a lead's turn in a person's thread sets it
	m.notify(&got, "Merged: TestQueueOrder")
	var heard int
	for _, msg := range m.Chat.Messages("lead") {
		if msg.Thread == "t-person" && msg.Text == "Merged: TestQueueOrder" {
			heard++
		}
	}
	if heard != 1 {
		t.Fatalf("the joined lead's thread heard the outcome %d times", heard)
	}
	m.C.Runs.SetStatus(task.Worker, core.StatusDone, "")
	again := brief(t, m, "w-two", map[string]any{"title": "TestQueueOrder still red", "aim": "fix", "why": "TestQueueOrder fails"})
	if again["task"] == task.ID || again["joined"] != "" {
		t.Fatalf("a brief joined a task whose worker had ended: %v", again)
	}
}
