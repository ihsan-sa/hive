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
	if _, err := forge.EnsureRepo("me/p", ""); err != nil {
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
	once := filepath.Join(t.TempDir(), "once")
	sha := commit(map[string]string{"t.sh": "[ -e " + once + " ] && exit 0\ntouch " + once + "\necho '--- FAIL: TestJoin (0.09s)'\nexit 1\n"})
	if ms := merge(sha); !ms.Kept || !strings.Contains(ms.Note, "flaky: TestJoin") {
		t.Fatalf("a flake was blamed on the merge: %+v", ms)
	}
	if reverts(m) != 0 || !strings.Contains(flakeAlerts(m), "TestJoin") {
		t.Fatalf("reverts %d, alerts %q", reverts(m), flakeAlerts(m))
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
