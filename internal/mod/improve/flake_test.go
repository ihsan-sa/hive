package improve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// tested is project p on the rig; commit puts files on main and returns the
// commit, and merge measures a commit as task t1's merge.
func tested(t *testing.T) (m *Module, commit func(files map[string]string) string, merge func(sha string) Measurement) {
	t.Helper()
	m = rig(t)
	forge := m.C.Effects.Forge.(*core.Local)
	if _, err := forge.EnsureRepo("me/p", "", false); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := core.Git(src, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatal(out, err)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "main")
	commit = func(files map[string]string) string {
		t.Helper()
		for f, c := range files {
			os.MkdirAll(filepath.Dir(filepath.Join(src, f)), 0o755)
			os.WriteFile(filepath.Join(src, f), []byte(c), 0o644)
		}
		git("add", "-A")
		git("commit", "-qm", "change")
		git("push", "-q", forge.RepoPath("me/p"), "HEAD:main")
		return git("rev-parse", "HEAD")
	}
	commit(map[string]string{".hive/project.json": `{"test": "sh ./t.sh"}`, "t.sh": "true\n"})
	p, err := m.C.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	if err != nil {
		t.Fatal(err)
	}
	merge = func(sha string) Measurement {
		t.Helper()
		m.afterMerge(work.Task{ID: "t1", Project: p.Name, MergeSHA: sha})
		ms := m.Scores("p")
		return ms[len(ms)-1]
	}
	return m, commit, merge
}

func reverts(m *Module) int {
	return len(m.Work.List(func(t work.Task) bool { return t.Kind == "revert" }))
}

func flakeAlerts(m *Module) string {
	var out []string
	for _, e := range m.C.J.Since(0) {
		var a struct{ Kind, Text string }
		if e.Kind == "alert.raised" && e.Decode(&a) == nil && a.Kind == "flake" {
			out = append(out, a.Text)
		}
	}
	return strings.Join(out, "\n")
}

// A test that fails after a merge and passes when run again is a flake: an
// alert, and the merge is kept.
func TestFlakeAfterMergeIsNotReverted(t *testing.T) {
	m, commit, merge := tested(t)
	once := "'" + filepath.ToSlash(filepath.Join(t.TempDir(), "once")) + "'" // a Windows path, quoted, reads in sh too
	sha := commit(map[string]string{"t.sh": "[ -e " + once + " ] && exit 0\ntouch " + once + "\necho '--- FAIL: TestJoin (0.09s)'\nexit 1\n"})
	if ms := merge(sha); !ms.Kept || !strings.Contains(ms.Note, "flaky") || !strings.Contains(ms.Note, "TestJoin") {
		t.Fatalf("a flake was blamed on the merge: %+v", ms)
	}
	if reverts(m) != 0 || !strings.Contains(flakeAlerts(m), "TestJoin") {
		t.Fatalf("reverts %d, alerts %q", reverts(m), flakeAlerts(m))
	}
	if f := m.Work.List(func(t work.Task) bool { return strings.HasPrefix(t.Title, "Fix the flaky") }); len(f) != 1 || !f[0].Fix() {
		t.Fatalf("the flaky test's brief names its failure, so it is a fix (issue #568): %+v", f)
	}
}

// A test that fails twice after a merge and nowhere else is a break, and
// the merge is reverted.
func TestBreakAfterMergeIsReverted(t *testing.T) {
	m, commit, merge := tested(t)
	// It failed before at this change's own head, which carries the change.
	head := commit(map[string]string{"t.sh": "echo '--- FAIL: TestJoin (0.09s)'\nexit 1\n"})
	m.C.J.MustAppend("task.saved", work.Task{ID: "t1", Project: "p", Kind: "work", Stage: "testing", SHA: head})
	p, _ := m.C.Projects.Get("p")
	m.C.Effects.Sync(p)
	if c := m.C.Effects.RunCheck(p, head, "test", "sh ./t.sh"); len(c.Failed) != 1 {
		t.Fatalf("the head's failure: %+v", c)
	}
	sha := commit(map[string]string{"README.md": "merged\n"})
	if ms := merge(sha); ms.Kept || ms.TestsOK {
		t.Fatalf("a break was kept: %+v", ms)
	}
	if reverts(m) != 1 || flakeAlerts(m) != "" {
		t.Fatalf("reverts %d, alerts %q", reverts(m), flakeAlerts(m))
	}
}

// A test that fails twice after a merge but also failed before it merged, at
// a commit without the change, is a flake: an alert, no revert.
func TestFailureSeenElsewhereIsAFlake(t *testing.T) {
	m, commit, merge := tested(t)
	p, _ := m.C.Projects.Get("p")
	before, _ := m.C.Effects.Sync(p)
	if c := m.C.Effects.RunCheck(p, before, "test", "echo '--- FAIL: TestJoin (0.09s)'; exit 1"); c.OK || len(c.Failed) != 1 {
		t.Fatalf("the earlier failure: %+v", c)
	}
	sha := commit(map[string]string{"t.sh": "echo '--- FAIL: TestJoin (0.09s)'\nexit 1\n"})
	if ms := merge(sha); ms.TestsOK || !strings.Contains(ms.Note, "not reverted") {
		t.Fatalf("measured %+v", ms)
	}
	if reverts(m) != 0 || !strings.Contains(flakeAlerts(m), "also failed at") {
		t.Fatalf("reverts %d, alerts %q", reverts(m), flakeAlerts(m))
	}
	// One more failing test that failed nowhere else makes it a break.
	sha = commit(map[string]string{"t.sh": "echo '--- FAIL: TestJoin (0.09s)'\necho '--- FAIL: TestNew (0.01s)'\nexit 1\n"})
	m.afterMerge(work.Task{ID: "t2", Project: "p", MergeSHA: sha})
	if reverts(m) != 1 {
		t.Fatalf("a new failure beside a known flake wasn't reverted: %d", reverts(m))
	}
}

// A task that merged a checkpoint earlier carries its change from then on:
// a failure checked after the checkpoint merged isn't one without the change.
func TestFailureAfterACheckpointIsTheChanges(t *testing.T) {
	m, commit, _ := tested(t)
	p, _ := m.C.Projects.Get("p")
	checkpoint := commit(map[string]string{"t.sh": "echo '--- FAIL: TestJoin (0.09s)'\nexit 1\n"})
	m.C.J.MustAppend("effect.merged", map[string]any{"project": "p", "merge_sha": checkpoint})
	other := commit(map[string]string{"README.md": "another change on top\n"})
	m.C.Effects.Sync(p)
	if c := m.C.Effects.RunCheck(p, other, "test", "sh ./t.sh"); len(c.Failed) != 1 {
		t.Fatalf("the other change's failure: %+v", c)
	}
	sha := commit(map[string]string{"README.md": "the rest of the task\n"})
	m.afterMerge(work.Task{ID: "t1", Project: "p", MergeSHA: sha, Banks: []work.Bank{{MergeSHA: checkpoint}}})
	if reverts(m) != 1 || flakeAlerts(m) != "" {
		t.Fatalf("reverts %d, alerts %q", reverts(m), flakeAlerts(m))
	}
}

// A failure checked at a candidate's head, not on main, is what the
// candidate's code printed: it never makes a failure after a merge a flake.
func TestOnlyMainsChecksMakeAFlake(t *testing.T) {
	m, commit, merge := tested(t)
	forge := m.C.Effects.Forge.(*core.Local)
	side := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := core.Git(side, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatal(out, err)
		}
		return strings.TrimSpace(out)
	}
	git("clone", "-q", forge.RepoPath("me/p"), ".")
	os.WriteFile(filepath.Join(side, "t.sh"), []byte("echo '--- FAIL: TestJoin (0.09s)'\nexit 1\n"), 0o644)
	git("commit", "-qam", "another candidate")
	git("push", "-q", "origin", "HEAD:refs/heads/cand")
	cand := git("rev-parse", "HEAD")
	p, _ := m.C.Projects.Get("p")
	m.C.Effects.Sync(p)
	if c := m.C.Effects.RunCheck(p, cand, "test", "sh ./t.sh"); len(c.Failed) != 1 {
		t.Fatalf("the candidate's failure: %+v", c)
	}
	sha := commit(map[string]string{"t.sh": "echo '--- FAIL: TestJoin (0.09s)'\nexit 1\n"})
	if ms := merge(sha); ms.Kept || ms.TestsOK {
		t.Fatalf("a break was kept on a candidate's say-so: %+v", ms)
	}
	if reverts(m) != 1 || flakeAlerts(m) != "" {
		t.Fatalf("reverts %d, alerts %q", reverts(m), flakeAlerts(m))
	}
}

// A merge closed after main already moved past it and was measured (a pull
// request that landed hours before its task closed) isn't measured again at
// its old code: no score, no alert, no revert, only a note of what covered it.
func TestMergeMainAlreadyHoldsIsNotMeasured(t *testing.T) {
	m, commit, merge := tested(t)
	old := commit(map[string]string{"t.sh": "echo '--- FAIL: TestClock (0.01s)'\nexit 1\n"})
	fixed := commit(map[string]string{"t.sh": "true\n"})
	if ms := merge(fixed); !ms.Kept {
		t.Fatalf("main at the fix: %+v", ms)
	}
	before := len(m.Scores("p"))
	m.afterMerge(work.Task{ID: "t0", Project: "p", MergeSHA: old})
	if n := len(m.Scores("p")); n != before {
		t.Fatalf("a merge main already holds was measured: %+v", m.Scores("p")[n-1])
	}
	if reverts(m) != 0 || flakeAlerts(m) != "" {
		t.Fatalf("reverts %d, alerts %q", reverts(m), flakeAlerts(m))
	}
	var got []Covered
	for _, e := range m.C.J.Since(0) {
		var c Covered
		if e.Kind == "score.covered" && e.Decode(&c) == nil {
			got = append(got, c)
		}
	}
	if len(got) != 1 || got[0].SHA != old || got[0].By != fixed || got[0].Task != "t0" {
		t.Fatalf("covered: %+v", got)
	}
	if !m.measured("p", old) {
		t.Fatal("a covered merge would be queued again after a restart")
	}
	if d := m.Digest(0, ""); !strings.Contains(d, core.Short(old)+" not measured: main already held it when measured at "+core.Short(fixed)) {
		t.Fatalf("the digest doesn't show the covered merge:\n%s", d)
	}
	// A merge main doesn't hold beyond itself is still measured.
	next := commit(map[string]string{"t.sh": "true\n# next\n"})
	if ms := merge(next); ms.SHA != next || !ms.Kept {
		t.Fatalf("a new merge: %+v", ms)
	}
}

// A red after a merge that main already had just before it (a clock-bound
// test, say) isn't the merge's: it is filed to fix, and nothing is reverted.
func TestRedAlreadyOnMainBeforeTheMergeIsNotReverted(t *testing.T) {
	m, commit, merge := tested(t)
	commit(map[string]string{"t.sh": "echo '--- FAIL: TestClock (0.01s)'\nexit 1\n"}) // went red unmeasured
	sha := commit(map[string]string{"README.md": "an unrelated change\n"})
	if ms := merge(sha); ms.Kept || ms.TestsOK || !strings.Contains(ms.Note, "not new at this merge") || !strings.Contains(ms.Note, "not reverted") {
		t.Fatalf("measured %+v", ms)
	}
	if reverts(m) != 0 {
		t.Fatalf("a red main had before the merge was reverted: %d", reverts(m))
	}
	filed := m.Work.List(func(t work.Task) bool { return strings.Contains(t.Title, "TestClock pass on main") })
	if len(filed) != 1 {
		t.Fatalf("the red wasn't filed to fix: %d", len(filed))
	}
	if !filed[0].Fix() || !strings.Contains(filed[0].AimWhy, "TestClock") {
		t.Fatalf("a red main's brief names its failure, so it is a fix (issue #568): %q", filed[0].AimWord())
	}
	// A second red merge on the same red main files it no second time.
	sha = commit(map[string]string{"README.md": "another\n"})
	m.afterMerge(work.Task{ID: "t2", Project: "p", MergeSHA: sha})
	if reverts(m) != 0 || len(m.Work.List(func(t work.Task) bool { return strings.Contains(t.Title, "TestClock pass on main") })) != 1 {
		t.Fatalf("reverts %d on a second merge", reverts(m))
	}
}

// The same failures listed in another order are filed to fix once.
func TestRedOnMainInAnotherOrderIsFiledOnce(t *testing.T) {
	m, commit, merge := tested(t)
	commit(map[string]string{"t.sh": "echo '--- FAIL: TestB (0.01s)'\necho '--- FAIL: TestA (0.01s)'\nexit 1\n"})
	if ms := merge(commit(map[string]string{"README.md": "one\n"})); !strings.Contains(ms.Note, "not new at this merge") {
		t.Fatalf("measured %+v", ms)
	}
	commit(map[string]string{"t.sh": "echo '--- FAIL: TestA (0.01s)'\necho '--- FAIL: TestB (0.01s)'\nexit 1\n"})
	m.afterMerge(work.Task{ID: "t2", Project: "p", MergeSHA: commit(map[string]string{"README.md": "two\n"})})
	filed := m.Work.List(func(t work.Task) bool { return strings.HasSuffix(t.Title, "pass on main again") })
	if len(filed) != 1 || !strings.Contains(filed[0].Title, "TestA, TestB") || reverts(m) != 0 {
		t.Fatalf("filed %d, reverts %d", len(filed), reverts(m))
	}
}

// A vet or compile error prints no test or package line to name it by, only
// "file.go:line:col: message". The merge that brought it is reverted; one
// measured on main while it stays red (a goals-only change) is not.
func TestVetRedOnlyRevertsTheMergeThatBroughtIt(t *testing.T) {
	m, commit, merge := tested(t)
	vet := "echo '# x/e'\necho 'vet: e/a_test.go:225:42: not enough arguments in call to f'\nexit 1\n"
	if ms := merge(commit(map[string]string{"t.sh": vet})); ms.Kept || strings.Contains(ms.Note, "not new at this merge") || reverts(m) != 1 {
		t.Fatalf("the merge that broke vet: %+v, reverts %d", ms, reverts(m))
	}
	for i, f := range []string{".hive/goals.md", "README.md"} {
		sha := commit(map[string]string{f: "only text\n"})
		if ms := merge(sha); ms.Kept || !strings.Contains(ms.Note, "not new at this merge") {
			t.Fatalf("merge %d on a vet-red main: %+v", i, ms)
		}
	}
	if reverts(m) != 1 {
		t.Fatalf("merges measured while main stayed red were reverted: %d", reverts(m))
	}
}

// A merge that adds a failure no test name accounts for (a timeout) beside
// a test main already failed is the merge's break: it is reverted.
func TestUnnamedFailureBesideMainsRedIsReverted(t *testing.T) {
	m, commit, merge := tested(t)
	red := "echo '--- FAIL: TestClock (0.01s)'\necho 'FAIL\tp/a\t0.01s'\n"
	commit(map[string]string{"t.sh": red + "exit 1\n"}) // went red unmeasured
	sha := commit(map[string]string{"t.sh": red + "echo 'panic: test timed out after 2s'\necho 'FAIL\tp/b\t2.01s'\nexit 1\n"})
	if ms := merge(sha); ms.Kept || strings.Contains(ms.Note, "not new at this merge") {
		t.Fatalf("measured %+v", ms)
	}
	if reverts(m) != 1 {
		t.Fatalf("a timeout beside main's red wasn't reverted: %d", reverts(m))
	}
}
