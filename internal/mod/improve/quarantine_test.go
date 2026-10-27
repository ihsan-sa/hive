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
// tasks' heads, or a flake on main, quarantine a test as the forge's do. A
// red on a commit that is neither (a gate's candidate) is no sighting. On
// main a red counts only once its retest passed: one that stays red is a
// merge that broke the test, not a flake, and main just before a merge is
// never retested.
func TestQuarantineFromLocalChecks(t *testing.T) {
	m := rig(t)
	key := "github.com/x/hive/internal/daemon::TestTick"
	checked := func(sha, kind string, ok bool, keys ...string) {
		m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: sha, Kind: kind, OK: ok, Failed: []string{"TestTick"}, Keys: keys, At: time.Now().UTC()})
	}
	for _, id := range []string{"t1", "t2"} {
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "hive", Title: "work " + id, SHA: "head" + id})
	}
	checked("headt1", "test", false, key)
	checked("headt1", "retest", false, key)
	checked("candidate", "test", false, key)
	checked("headt2", "test", false) // no test placed in its package: no key to sight
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("one task's and a candidate's reds quarantined %v", q)
	}
	checked("headt2", "test", false, key)
	q := m.Quarantine("hive")
	if len(q) != 1 || q[0].Test != key || !strings.Contains(q[0].Why, "failed on 2 unrelated branches (tasks t1, t2)") || q[0].Evidence[0].Check != "local test" {
		t.Fatalf("quarantine %+v", q)
	}
	if q := m.QuarantinedTests("hive", "t1"); len(q) != 0 {
		t.Fatalf("t1 excused on one other change's reds: %v", q)
	}

	m.C.J.MustAppend("effect.merged", map[string]string{"project": "hive", "merge_sha": "main1"})
	m.C.J.MustAppend("effect.merged", map[string]string{"project": "hive", "merge_sha": "main2"})
	broken := "github.com/x/hive/internal/core::TestBroken"
	checked("main1", "test", false, broken)
	checked("main1", "before", false, broken)
	if slices.Contains(m.QuarantinedTests("hive", "t1"), broken) {
		t.Fatal("a local red on main quarantined before its retest answered")
	}
	checked("main1", "retest", false, broken)
	checked("main1", "retest", true) // a later pass is no answer to a red already dropped
	if slices.Contains(m.QuarantinedTests("hive", "t1"), broken) {
		t.Fatal("a merge that broke a test (red again on retest) quarantined it")
	}
	flaky := "github.com/x/hive/internal/core::TestOther"
	checked("main2", "test", false, flaky)
	checked("main2", "retest", true)
	if !slices.Contains(m.QuarantinedTests("hive", "t1"), flaky) {
		t.Fatal("a local flake on main not quarantined")
	}
	m.fileQuarantined()
	fix := m.Work.List(func(t work.Task) bool { return t.Title == flakyTitle+key })
	if len(fix) != 1 || !strings.Contains(fix[0].Known, "task t1 at headt1, check local test") {
		t.Fatalf("fix tasks %+v", fix)
	}
	fix = m.Work.List(func(t work.Task) bool { return t.Title == flakyTitle+flaky })
	if len(fix) != 1 || !strings.Contains(fix[0].Known, "main at main2, check local test, passed when run again") {
		t.Fatalf("fix tasks %+v", fix)
	}
}

// Two tasks with the same head (one caught up to the other's branch): a
// local red there is sighted for the first by id, and is still the other's
// own, so neither is excused on it, nor do they make two branches.
func TestLocalSightingOnASharedHead(t *testing.T) {
	m := rig(t)
	key := "github.com/x/hive/internal/daemon::TestTick"
	m.C.J.MustAppend("task.saved", work.Task{ID: "t1", Project: "hive", Title: "work t1", SHA: "shared"})
	m.C.J.MustAppend("task.saved", work.Task{ID: "t2", Project: "hive", Title: "work t2", SHA: "shared"})
	m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: "shared", Kind: "test", Failed: []string{"TestTick"}, Keys: []string{key}, At: time.Now().UTC()})
	m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: "shared", Kind: "retest", Failed: []string{"TestTick"}, Keys: []string{key}, At: time.Now().UTC()})
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("one shared head's reds quarantined %v", q)
	}
	m.C.J.MustAppend("task.saved", work.Task{ID: "t3", Project: "hive", Title: "work t3", SHA: "head3"})
	m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: "head3", Kind: "test", Failed: []string{"TestTick"}, Keys: []string{key}, At: time.Now().UTC()})
	for _, task := range []string{"t1", "t2", "t3"} {
		if q := m.QuarantinedTests("hive", task); len(q) != 0 {
			t.Fatalf("%s excused on reds that are its own or one other branch's: %v", task, q)
		}
	}
	if q := m.Quarantine("hive"); len(q) != 1 || q[0].Evidence[0].Task != "t1" {
		t.Fatalf("quarantine %+v", q)
	}
}

// A check run broken by its environment (its folder gone) or failing dozens
// of tests at once is no sighting of any of them: two such runs on unrelated
// heads quarantine nothing and file no fix briefs, and a waiting fix brief
// filed on such runs before is taken back.
func TestQuarantineSkipsBrokenRuns(t *testing.T) {
	m := rig(t)
	pkg := "github.com/x/hive/internal/core::"
	key := pkg + "TestPrefetch"
	gone := "--- FAIL: TestPrefetch (0.01s)\n    disk_test.go:318: go env: exit status 1 cannot determine current directory: getwd: no such file or directory\nFAIL\n"
	var many []string
	for i := range 12 {
		many = append(many, pkg+"TestMany"+string(rune('A'+i)))
	}
	for _, id := range []string{"t1", "t2"} {
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "hive", Title: "work " + id, SHA: "head" + id})
		m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: "head" + id, Kind: "test", Output: gone, Failed: []string{"TestPrefetch"}, Keys: []string{key}, At: time.Now().UTC()})
		m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: "head" + id, Kind: "retest", Output: "FAIL\n", Keys: many, At: time.Now().UTC()})
		sight(m, id, "https://ci/"+id, "TestA", "TestB", "TestC", "TestD", "TestE", "TestF", "TestG", "TestH", "TestI", "TestJ", "TestK")
	}
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("broken runs quarantined %v", q)
	}
	m.C.J.MustAppend("effect.merged", map[string]string{"project": "hive", "merge_sha": "main1"})
	m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: "main1", Kind: "test", Output: "fatal: this operation must be run in a work tree\n", Keys: []string{pkg + "TestMain"}, At: time.Now().UTC()})
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("a broken run on main quarantined %v", q)
	}

	stale, _ := m.Work.File(work.Brief{Project: "hive", From: "improve", Title: flakyTitle + key, Done: "d", Known: "k"})
	kept, _ := m.Work.File(work.Brief{Project: "hive", From: "improve", Title: flakyTitle + pkg + "TestOther", Done: "d", Known: "k"})
	m.fileQuarantined()
	if n := len(m.Work.List(func(t work.Task) bool { return strings.HasPrefix(t.Title, flakyTitle) })); n != 2 {
		t.Fatalf("%d fix tasks, want only the two filed by hand", n)
	}
	if got, _ := m.Work.Get(stale.ID); !got.Withdrawn() {
		t.Fatalf("the brief filed on broken runs is %s (%s), want withdrawn", got.Stage, got.Reason)
	}
	if got, _ := m.Work.Get(kept.ID); got.Terminal() {
		t.Fatalf("a brief for a test no broken run took down was taken back: %s", got.Reason)
	}
}

// A test in the module's root package reads the whole tree (gofmt, the
// core's size): two branches each with an unformatted file fail it, and
// that is their content, not a flake. It is never quarantined, and a
// waiting fix brief filed for it is taken back.
func TestQuarantineSkipsWholeTreeTests(t *testing.T) {
	m := rig(t)
	m.modules["hive"] = "github.com/x/hive"
	key := "github.com/x/hive::TestGofmt"
	out := "--- FAIL: TestGofmt (0.3s)\n    gofmt_test.go:37: internal/mod/work/work.go is not gofmt-clean\nFAIL\nFAIL\tgithub.com/x/hive\t0.5s\n"
	for _, id := range []string{"t1", "t2"} {
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "hive", Title: "work " + id, SHA: "head" + id})
		m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: "head" + id, Kind: "test", Output: out, Failed: []string{"TestGofmt"}, Keys: []string{key}, At: time.Now().UTC()})
		sight(m, id, "https://ci/"+id, key)
	}
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("a whole-tree test quarantined: %v", q)
	}
	stale, _ := m.Work.File(work.Brief{Project: "hive", From: "improve", Title: flakyTitle + key, Done: "d", Known: "k"})
	m.fileQuarantined()
	if got, _ := m.Work.Get(stale.ID); !got.Withdrawn() {
		t.Fatalf("the brief for a whole-tree test is %s, want withdrawn", got.Stage)
	}
}
