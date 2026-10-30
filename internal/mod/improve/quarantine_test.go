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
// quarantined, with its evidence, and listed on the board, not filed. A
// fix an agent files for it lifts it when it merges.
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

	board := boardOf(m)
	m.boardQuarantined()
	m.boardQuarantined()
	if n := len(m.Work.List(func(t work.Task) bool { return strings.HasPrefix(t.Title, flakyTitle) })); n != 0 {
		t.Fatalf("%d fix tasks filed, want none: the board lists them", n)
	}
	if len(*board) != 1 || !strings.HasPrefix((*board)[0], "hive|quarantined tests|Quarantined in hive: TestModuleLifecycle, TestReportSharing, TestStalls.") || !strings.Contains((*board)[0], "https://ci/3") {
		t.Fatalf("board posts %q, want one listing the three", *board)
	}
	// An agent files the fix, titled for the test.
	fix, _ := m.Work.File(work.Brief{Project: "hive", From: "sweep", Title: flakyTitle + "TestModuleLifecycle", Done: "d", Known: "k"})
	for _, f := range m.Quarantine("hive") {
		if f.Test == "TestModuleLifecycle" && (f.Task != fix.ID || f.TaskStage == "") {
			t.Fatalf("the fix task isn't on its flake: %+v", f)
		}
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

// boardOf records m's board posts as project|where|text. A close with no
// entry open is left out, as problems.Standing does nothing with it.
func boardOf(m *Module) *[]string {
	var posts []string
	open := map[string]bool{}
	m.Board = func(project, where, text string) {
		if text == "" && !open[project] {
			return
		}
		open[project] = text != ""
		posts = append(posts, project+"|"+where+"|"+text)
	}
	return &posts
}

// The board entry is rewritten only when the set of quarantined tests
// changes, and closed once none is left; improve's waiting per-test briefs
// from before are withdrawn, while a started one and an agent's own stay.
func TestQuarantineGoesOnTheBoard(t *testing.T) {
	m := rig(t)
	board := boardOf(m)
	waiting, _ := m.Work.File(work.Brief{Project: "hive", From: "improve", Title: flakyTitle + "TestOld", Done: "d", Known: "k"})
	started, _ := m.Work.File(work.Brief{Project: "hive", From: "improve", Title: flakyTitle + "TestBusy", Done: "d", Known: "k"})
	started.Stage = work.StageWorking
	m.C.J.MustAppend("task.saved", started)
	own, _ := m.Work.File(work.Brief{Project: "hive", From: "sweep", Title: flakyTitle + "TestMine", Done: "d", Known: "k"})
	m.boardQuarantined()
	if len(*board) != 0 {
		t.Fatalf("posted %q with nothing quarantined", *board)
	}
	for id, want := range map[string]bool{waiting.ID: true, started.ID: false, own.ID: false} {
		if got, _ := m.Work.Get(id); got.Withdrawn() != want {
			t.Fatalf("%s (%s) withdrawn %v, want %v", got.Title, got.Stage, got.Withdrawn(), want)
		}
	}
	sight(m, "t1", "https://ci/1", "TestX")
	sight(m, "t2", "https://ci/2", "TestX")
	m.boardQuarantined()
	sight(m, "t3", "https://ci/3", "TestX") // more evidence, the same tests
	m.boardQuarantined()
	sight(m, "t3", "https://ci/3", "TestY")
	sight(m, "t4", "https://ci/4", "TestY")
	m.boardQuarantined()
	if len(*board) != 2 || !strings.Contains((*board)[0], "Quarantined in hive: TestX.") || !strings.Contains((*board)[1], "Quarantined in hive: TestX, TestY.") {
		t.Fatalf("board posts %q", *board)
	}
	old := quarantineWindow
	quarantineWindow = time.Nanosecond
	t.Cleanup(func() { quarantineWindow = old })
	time.Sleep(time.Millisecond)
	m.boardQuarantined()
	m.boardQuarantined()
	if len(*board) != 3 || (*board)[2] != "hive|quarantined tests|" {
		t.Fatalf("board posts %q, want the entry closed once", *board)
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
	board := boardOf(m)
	m.boardQuarantined()
	if len(*board) != 1 || !strings.Contains((*board)[0], "task t1 at headt1, check local test") {
		t.Fatalf("board posts %q", *board)
	}
}

// Once a flaky test's fix merges, a local red on a commit without the fix
// (a head cut before it merged, checked just after) is no sighting: the
// check ran the old test. Reds on commits that have the fix count again.
func TestQuarantineLiftNotUndoneByOldHeads(t *testing.T) {
	m := rig(t)
	p, _ := m.C.Projects.Get("hive")
	if err := m.C.Effects.Clone(p); err != nil {
		t.Fatal(err)
	}
	dir := m.C.WS.Project("hive")
	commit := func(msg string) string {
		t.Helper()
		if out, err := core.Git(dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", msg); err != nil {
			t.Fatal(out, err)
		}
		sha, _ := core.Git(dir, "rev-parse", "HEAD")
		return strings.TrimSpace(sha)
	}
	old1, old2 := commit("t1 before the fix"), commit("t2 before the fix")
	fixed := commit("the fix")
	key := "github.com/x/hive/internal/core::TestStale"
	fix := work.Task{ID: "f", Project: "hive", Title: flakyTitle + key}
	m.C.J.MustAppend("task.saved", fix)
	fix.MergeSHA, fix.Stage = fixed, work.StageMerged
	m.C.J.MustAppend("task.saved", fix)
	time.Sleep(time.Millisecond)
	checked := func(id, sha string) {
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "hive", Title: "work " + id, SHA: sha})
		m.C.J.MustAppend("effect.checked", core.Check{Project: "hive", SHA: sha, Kind: "test", Output: "--- FAIL: TestStale (0.4s)\n", Failed: []string{"TestStale"}, Keys: []string{key}, At: time.Now().UTC()})
	}
	checked("t1", old1)
	checked("t2", old2)
	if q := m.QuarantinedTests("hive", ""); len(q) != 0 {
		t.Fatalf("reds on a commit without the fix quarantined %v", q)
	}
	checked("t3", commit("t3 on the fix"))
	checked("t4", commit("t4 on the fix"))
	if q := m.QuarantinedTests("hive", ""); !slices.Equal(q, []string{key}) {
		t.Fatalf("reds with the fix in quarantined %v", q)
	}
}

// A check run broken by its environment (its folder gone) or failing dozens
// of tests at once is no sighting of any of them: two such runs on unrelated
// heads quarantine nothing and put nothing on the board.
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

	board := boardOf(m)
	m.boardQuarantined()
	if len(*board) != 0 {
		t.Fatalf("broken runs went on the board: %q", *board)
	}
}

// A test in the module's root package reads the whole tree (gofmt, the
// core's size): two branches each with an unformatted file fail it, and
// that is their content, not a flake. It is never quarantined.
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
	board := boardOf(m)
	m.boardQuarantined()
	if len(*board) != 0 {
		t.Fatalf("a whole-tree test went on the board: %q", *board)
	}
}
