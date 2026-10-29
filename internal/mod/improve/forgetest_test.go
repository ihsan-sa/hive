package improve

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A merge of a project whose forge CI runs its tests is measured on the
// forge's answer for the merge commit: waited on while it runs, taken with no
// run here once it passes, and run here as before when it is red.
func TestMergeTestsComeFromTheForge(t *testing.T) {
	m, merge := scored(t)
	defer func(w, p time.Duration) { forgeWait, forgePoll = w, p }(forgeWait, forgePoll)
	forgeWait, forgePoll = time.Hour, time.Hour // the test measures again itself
	forge := m.C.Effects.Forge.(*core.Local)
	const job = "test (ubuntu-latest)"
	// The test here fails, so a pass can only be the forge's.
	cfg := `{"test": "false", "forge_tests": ["` + job + `"], "score": {"command": "sh ./score.sh", "direction": "higher"}}`
	head := func() string {
		t.Helper()
		s, err := core.Git(m.C.WS.Project("p"), "rev-parse", "origin/main")
		if err != nil {
			t.Fatal(s, err)
		}
		return strings.TrimSpace(s)
	}
	before := len(m.Scores("p"))
	merge(map[string]string{".hive/project.json": cfg, "n.txt": "6\n"})
	sha := head()
	if len(m.Scores("p")) != before || m.measured("p", sha) {
		t.Fatalf("measured with the forge's tests not yet reported: %+v", m.Scores("p"))
	}
	forge.SetChecks("me/p", sha, core.CheckRun{Name: job, State: core.CheckPending})
	m.afterMerge(work.Task{ID: "t1", Project: "p", MergeSHA: sha})
	if m.measured("p", sha) {
		t.Fatal("measured while the forge's tests still run")
	}
	forge.SetChecks("me/p", sha, core.CheckRun{Name: job, State: core.CheckPass}, core.CheckRun{Name: "lint", State: core.CheckFail})
	m.afterMerge(work.Task{ID: "t1", Project: "p", MergeSHA: sha})
	all := m.Scores("p")
	last := all[len(all)-1]
	if last.SHA != sha || !last.Kept || !last.TestsOK || !strings.Contains(last.Note, "tests green on the forge") {
		t.Fatalf("the forge's pass should stand for the tests: %+v", last)
	}
	if c, ok := m.C.Effects.GetCheck("p", sha, "test"); !ok || !c.OK || c.Where != "forge" {
		t.Fatalf("the forge's pass isn't the merge's test check: %+v %v", c, ok)
	}

	// Red on the forge: the tests run here, and fail here too.
	merge(map[string]string{"n.txt": "7\n"})
	sha = head()
	forge.SetChecks("me/p", sha, core.CheckRun{Name: job, State: core.CheckFail})
	m.afterMerge(work.Task{ID: "t2", Project: "p", MergeSHA: sha})
	m.judges.Wait()
	all = m.Scores("p")
	if last := all[len(all)-1]; last.SHA != sha || last.TestsOK || last.Kept {
		t.Fatalf("a red on the forge should run the tests here, which fail: %+v", last)
	}
}
