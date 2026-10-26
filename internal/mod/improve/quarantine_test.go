package improve

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

func sight(m *Module, task, url string, tests ...string) {
	m.C.J.MustAppend("forge.tests_failed", map[string]any{"project": "hive", "task": task, "branch": "hive/" + task, "sha": "abc" + task, "check": "test (windows-latest)", "url": url, "tests": tests})
}

// A test red on one branch alone is that branch's, even when it passed on a
// re-run there; red on a second, unrelated branch or on main, it is
// quarantined, with its evidence, and gets one fix task. The fix's merge
// lifts it.
func TestQuarantine(t *testing.T) {
	m := rig(t)
	sight(m, "t1", "https://ci/1", "TestModuleLifecycle", "TestMine")
	sight(m, "t1", "https://ci/1", "TestModuleLifecycle") // the same run read again
	sight(m, "t1", "https://ci/2", "TestModuleLifecycle") // the same branch, another attempt
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
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
	// A pass on t3's own re-run is no quarantine: t3's code may have made it.
	if !slices.Equal(names, []string{"TestModuleLifecycle", "TestReportSharing"}) || len(m.Quarantine("other")) != 0 {
		t.Fatalf("quarantined %v", names)
	}
	if len(q[0].Evidence) != 3 || !strings.Contains(q[0].Why, "failed on 2 unrelated branches (tasks t1, t2)") || !strings.Contains(q[1].Why, "failed on main") {
		t.Fatalf("quarantine %+v", q)
	}
	// Seen on another, unrelated branch too, it is.
	sight(m, "t6", "https://ci/7", "TestStalls")
	if !slices.Contains(m.QuarantinedTests("hive", ""), "TestStalls") {
		t.Fatal("a flake on two branches not quarantined")
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
	if slices.Contains(m.QuarantinedTests("hive", ""), "TestModuleLifecycle") {
		t.Fatal("a fixed test stays quarantined")
	}
	time.Sleep(time.Millisecond)
	sight(m, "t4", "https://ci/5", "TestModuleLifecycle")
	if slices.Contains(m.QuarantinedTests("hive", ""), "TestModuleLifecycle") {
		t.Fatal("one red after the fix quarantined it again")
	}
	// A later save of the merged fix (a note) lifts nothing more: the red
	// since still counts, and a second one quarantines it again.
	fix.Log = append(fix.Log, work.Entry{Text: "measured"})
	m.C.J.MustAppend("task.saved", fix)
	sight(m, "t5", "https://ci/6", "TestModuleLifecycle")
	if !slices.Contains(m.QuarantinedTests("hive", ""), "TestModuleLifecycle") {
		t.Fatal("a later save of the fix dropped the reds seen since its merge")
	}
}

// A task and its own revert, or two tasks on the same head, are one
// change: their reds aren't two unrelated branches.
func TestQuarantineNeedsUnrelatedBranches(t *testing.T) {
	m := rig(t)
	m.C.J.MustAppend("task.saved", work.Task{ID: "f1", Project: "hive", Title: "revert", RevertOf: "t1"})
	sight(m, "t1", "https://ci/1", "TestX")
	sight(m, "f1", "https://ci/2", "TestX")
	m.C.J.MustAppend("forge.tests_failed", map[string]any{"project": "hive", "task": "t2", "sha": "abct1", "check": "c", "url": "https://ci/3", "tests": []string{"TestX"}})
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("one change's reds quarantined %v", q)
	}
	sight(m, "t3", "https://ci/4", "TestX")
	if q := m.QuarantinedTests("hive", ""); !slices.Equal(q, []string{"TestX"}) {
		t.Fatalf("quarantined %v", q)
	}
}

// Reds older than the window don't count.
func TestQuarantineAgesOut(t *testing.T) {
	m := rig(t)
	sight(m, "t1", "https://ci/1", "TestOld")
	sight(m, "t2", "https://ci/2", "TestOld")
	if len(m.QuarantinedTests("hive", "")) != 1 {
		t.Fatal("not quarantined")
	}
	old := quarantineWindow
	quarantineWindow = time.Nanosecond
	t.Cleanup(func() { quarantineWindow = old })
	time.Sleep(time.Millisecond)
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("aged-out reds quarantined %v", q)
	}
	m.mu.Lock()
	n := len(m.sighted)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d emptied keys kept", n)
	}
}

// A PR is excused only on evidence from outside its task: its own reds, its
// re-runs' passes and its reverts' reds don't count for it.
func TestQuarantineNotOnItsOwnEvidence(t *testing.T) {
	m := rig(t)
	m.C.J.MustAppend("task.saved", work.Task{ID: "f1", Project: "hive", Title: "revert", RevertOf: "t1"})
	sight(m, "t1", "https://ci/1", "pkg::TestX")
	m.C.J.MustAppend("forge.flaked", map[string]any{"project": "hive", "task": "t1", "sha": "abct1", "checks": []string{"test"}, "tests": []string{"pkg::TestX"}})
	sight(m, "f1", "https://ci/2", "pkg::TestX")
	sight(m, "t2", "https://ci/3", "pkg::TestX")
	if q := m.QuarantinedTests("hive", "t1"); len(q) != 0 {
		t.Fatalf("t1's own evidence quarantined %v for it", q)
	}
	if q := m.QuarantinedTests("hive", "t2"); len(q) != 0 {
		t.Fatalf("t2 excused on one other change's reds: %v", q)
	}
	sight(m, "t3", "https://ci/4", "pkg::TestX")
	for _, task := range []string{"t1", "t2", "t3", ""} {
		if q := m.QuarantinedTests("hive", task); !slices.Equal(q, []string{"pkg::TestX"}) {
			t.Fatalf("for %q: quarantined %v", task, q)
		}
	}
}

// The Hive's own test check is evidence too: its reds on two unrelated
// tasks' heads, or one on main, quarantine a test as the forge's do. A red
// on a commit that is neither (a gate's candidate) is no sighting, and a
// record from before checks named packages is read from its output.
func TestQuarantineFromLocalChecks(t *testing.T) {
	m := rig(t)
	out := "--- FAIL: TestTick (0.2s)\nFAIL\nFAIL\tgithub.com/x/hive/internal/daemon\t3.1s\nok  \tgithub.com/x/hive/internal/core\t1.0s\n"
	key := "github.com/x/hive/internal/daemon::TestTick"
	checked := func(sha, kind string, keys ...string) {
		m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: sha, Kind: kind, Output: out, Failed: []string{"TestTick"}, Keys: keys, At: time.Now().UTC()})
	}
	for _, id := range []string{"t1", "t2"} {
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "hive", Title: "work " + id, SHA: "head" + id})
	}
	checked("headt1", "test", key)
	checked("headt1", "retest", key)
	checked("candidate", "test", key)
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("one task's and a candidate's reds quarantined %v", q)
	}
	checked("headt2", "test") // recorded before checks named packages
	q := m.Quarantine("hive")
	if len(q) != 1 || q[0].Test != key || !strings.Contains(q[0].Why, "failed on 2 unrelated branches (tasks t1, t2)") || q[0].Evidence[0].Check != "local test" {
		t.Fatalf("quarantine %+v", q)
	}
	if q := m.QuarantinedTests("hive", "t1"); len(q) != 0 {
		t.Fatalf("t1 excused on one other change's reds: %v", q)
	}

	m.C.J.MustAppend("effect.merged", map[string]string{"project": "hive", "merge_sha": "main1"})
	other := "github.com/x/hive/internal/core::TestOther"
	checked("main1", "test", other)
	if !slices.Contains(m.QuarantinedTests("hive", "t1"), other) {
		t.Fatal("a local red on main not quarantined")
	}
	m.fileQuarantined()
	fix := m.Work.List(func(t work.Task) bool { return t.Title == flakyTitle+key })
	if len(fix) != 1 || !strings.Contains(fix[0].Known, "task t1 at headt1, check local test") {
		t.Fatalf("fix tasks %+v", fix)
	}
}
