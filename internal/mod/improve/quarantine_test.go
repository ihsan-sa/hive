package improve

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

func sight(m *Module, task, url string, tests ...string) {
	m.C.J.MustAppend("forge.tests_failed", map[string]any{"project": "hive", "task": task, "branch": "hive/" + task, "sha": "abc" + task, "check": "test (windows-latest)", "url": url, "tests": tests})
}

// A test red on one branch alone is that branch's; red on a second,
// unrelated branch, on main, or passing when run again, it is quarantined,
// with its evidence, and gets one fix task. The fix's merge lifts it.
func TestQuarantine(t *testing.T) {
	m := rig(t)
	sight(m, "t1", "https://ci/1", "TestModuleLifecycle", "TestMine")
	sight(m, "t1", "https://ci/1", "TestModuleLifecycle") // the same run read again
	sight(m, "t1", "https://ci/2", "TestModuleLifecycle") // the same branch, another attempt
	if q := m.QuarantinedTests("hive"); len(q) != 0 {
		t.Fatalf("one branch's red quarantined %v", q)
	}
	sight(m, "t2", "https://ci/3", "TestModuleLifecycle")
	m.C.J.MustAppend("forge.tests_failed", map[string]any{"project": "hive", "main": true, "sha": "main1", "check": "test (macos-latest)", "url": "https://ci/4", "tests": []string{"TestReportSharing"}})
	m.C.J.MustAppend("forge.flaked", map[string]any{"project": "hive", "task": "t3", "sha": "head3", "checks": []string{"test"}, "tests": []string{"TestStalls"}})
	q := m.Quarantine("hive")
	var names []string
	for _, f := range q {
		names = append(names, f.Test)
	}
	if !slices.Equal(names, []string{"TestModuleLifecycle", "TestReportSharing", "TestStalls"}) || len(m.Quarantine("other")) != 0 {
		t.Fatalf("quarantined %v", names)
	}
	if len(q[0].Evidence) != 3 || !strings.Contains(q[0].Why, "failed on 2 unrelated branches (tasks t1, t2)") || !strings.Contains(q[1].Why, "failed on main") || !strings.Contains(q[2].Why, "passed when run again") {
		t.Fatalf("quarantine %+v", q)
	}

	m.fileQuarantined()
	m.fileQuarantined()
	fixes := m.Work.List(func(t work.Task) bool { return strings.HasPrefix(t.Title, flakyTitle) })
	if len(fixes) != 3 {
		t.Fatalf("%d fix tasks, want one per test", len(fixes))
	}
	q = m.Quarantine("hive")
	var fix work.Task
	for _, f := range q {
		if f.Task == "" || f.TaskStage == "" {
			t.Fatalf("%s has no fix task: %+v", f.Test, f)
		}
		if f.Test == "TestModuleLifecycle" {
			fix, _ = m.Work.Get(f.Task)
		}
	}
	if !strings.Contains(fix.Known, "https://ci/3") || fix.Title != "Fix the flaky TestModuleLifecycle" {
		t.Fatalf("fix task %q: %q", fix.Title, fix.Known)
	}
	raised := 0
	for _, e := range m.C.J.Since(0) {
		var x struct{ Kind, Text string }
		if e.Kind == "alert.raised" && e.Decode(&x) == nil && x.Kind == "flake" && strings.HasPrefix(x.Text, "quarantined the flaky ") {
			raised++
		}
	}
	if raised != 3 {
		t.Fatalf("%d quarantine alerts, want 3", raised)
	}

	// The fix merges: the quarantine on it is lifted, and only a red after
	// that counts again.
	fix.MergeSHA, fix.Stage = "fixed", work.StageMerged
	m.C.J.MustAppend("task.saved", fix)
	if slices.Contains(m.QuarantinedTests("hive"), "TestModuleLifecycle") {
		t.Fatal("a fixed test stays quarantined")
	}
	time.Sleep(time.Millisecond)
	sight(m, "t4", "https://ci/5", "TestModuleLifecycle")
	if slices.Contains(m.QuarantinedTests("hive"), "TestModuleLifecycle") {
		t.Fatal("one red after the fix quarantined it again")
	}
}

// Reds older than the window don't count.
func TestQuarantineAgesOut(t *testing.T) {
	m := rig(t)
	sight(m, "t1", "https://ci/1", "TestOld")
	sight(m, "t2", "https://ci/2", "TestOld")
	if len(m.QuarantinedTests("hive")) != 1 {
		t.Fatal("not quarantined")
	}
	old := quarantineWindow
	quarantineWindow = time.Nanosecond
	t.Cleanup(func() { quarantineWindow = old })
	time.Sleep(time.Millisecond)
	if q := m.QuarantinedTests("hive"); len(q) != 0 {
		t.Fatalf("aged-out reds quarantined %v", q)
	}
}
