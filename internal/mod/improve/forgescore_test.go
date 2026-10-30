package improve

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A merge of a project scored on the forge takes its number from the forge's
// score job: waited on while it runs, its first reading starts a baseline of
// its own (a runner's number isn't this machine's), a fall is judged against
// the parent's forge reading with nothing scored here, and a red job falls
// back to the score run here.
func TestMergeScoreComesFromTheForge(t *testing.T) {
	m, merge := scored(t)
	defer func(w, p time.Duration) { forgeWait, forgePoll = w, p }(forgeWait, forgePoll)
	forgeWait, forgePoll = time.Hour, time.Hour // the test measures again itself
	forge := m.C.Effects.Forge.(*core.Local)
	const job = "test (ubuntu-latest)"
	cfg := `{"test": "true", "forge_tests": ["` + job + `"], "score": {"command": "sh ./score.sh", "direction": "higher"}}`
	head := func() string {
		t.Helper()
		s, err := core.Git(m.C.WS.Project("p"), "rev-parse", "origin/main")
		if err != nil {
			t.Fatal(s, err)
		}
		return strings.TrimSpace(s)
	}
	passed := core.CheckRun{Name: job, State: core.CheckPass}
	scoredAs := func(sha, url string, n string) {
		forge.SetChecks("me/p", sha, passed, core.CheckRun{Name: ScoreJob, State: core.CheckPass, URL: url})
		forge.SetCheckLog("me/p", url, "Run cmd=$(python3 ...)\necho \"hive-score: $n\"\nbenchmark ...\n"+n+"\nhive-score: "+n+"\nCleaning up orphan processes\n")
	}

	merge(map[string]string{".hive/project.json": cfg, "n.txt": "6\n"})
	sha := head()
	forge.SetChecks("me/p", sha, passed, core.CheckRun{Name: ScoreJob, State: core.CheckPending})
	m.afterMerge(work.Task{ID: "t1", Project: "p", MergeSHA: sha})
	if m.measured("p", sha) {
		t.Fatal("measured while the forge's score job still runs")
	}
	scoredAs(sha, "u1", "40")
	m.afterMerge(work.Task{ID: "t1", Project: "p", MergeSHA: sha})
	all := m.Scores("p")
	last := all[len(all)-1]
	if last.SHA != sha || !last.Kept || last.Score == nil || *last.Score != 40 || !strings.HasSuffix(last.Version, ForgeVersion) ||
		!strings.Contains(last.Note, "forge's score job") || !strings.Contains(last.Note, "new baseline") {
		t.Fatalf("the forge's 40 should start a baseline of its own: %+v", last)
	}
	if c, ok := m.C.Effects.GetCheck("p", sha, "score"); !ok || c.Where != "forge" || c.Score == nil || *c.Score != 40 {
		t.Fatalf("the forge's reading isn't the merge's score check: %+v %v", c, ok)
	}

	// A fall on the forge is judged against the parent's forge reading; the
	// score here (7, above the best) never runs.
	merge(map[string]string{"n.txt": "7\n"})
	sha = head()
	scoredAs(sha, "u2", "30")
	m.afterMerge(work.Task{ID: "t2", Project: "p", MergeSHA: sha})
	m.judges.Wait()
	all = m.Scores("p")
	if last := all[len(all)-1]; last.SHA != sha || last.Kept || last.Against == nil || *last.Against != 40 || !strings.Contains(last.Note, "both on the forge") {
		t.Fatalf("30 against the parent's 40 on the forge should be a fall: %+v", last)
	}

	// A red score job: the score runs here.
	merge(map[string]string{"n.txt": "8\n"})
	sha = head()
	forge.SetChecks("me/p", sha, passed, core.CheckRun{Name: ScoreJob, State: core.CheckFail})
	m.afterMerge(work.Task{ID: "t3", Project: "p", MergeSHA: sha})
	m.judges.Wait()
	all = m.Scores("p")
	if last := all[len(all)-1]; last.SHA != sha || last.Score == nil || *last.Score != 8 || strings.HasSuffix(last.Version, ForgeVersion) {
		t.Fatalf("a red score job should score here: %+v", last)
	}

	// A commit whose workflow has no score job: once its other checks are
	// done, the score runs here without waiting out forgeWait.
	merge(map[string]string{"n.txt": "9\n"})
	sha = head()
	forge.SetChecks("me/p", sha, passed)
	m.afterMerge(work.Task{ID: "t4", Project: "p", MergeSHA: sha})
	m.judges.Wait()
	all = m.Scores("p")
	if last := all[len(all)-1]; last.SHA != sha || last.Score == nil || *last.Score != 9 {
		t.Fatalf("no score job should score here: %+v", last)
	}
}

// Only main's tip is measured: a merge that a later merge, itself waiting
// to be measured, holds is left for it, and covered once it is measured.
func TestOnlyMainsTipIsMeasured(t *testing.T) {
	m, merge := scored(t)
	merge(map[string]string{"n.txt": "6\n"})
	old := m.Scores("p")[len(m.Scores("p"))-1].SHA
	merge(map[string]string{"n.txt": "7\n"}, work.Task{ID: "hold"}) // measured at once; drop it to stand for a tip not yet measured
	m.mu.Lock()
	tip := m.scores["p"][len(m.scores["p"])-1].SHA
	m.scores["p"] = m.scores["p"][:len(m.scores["p"])-2] // neither measured yet
	m.mu.Unlock()
	m.C.J.MustAppend("task.saved", work.Task{ID: "tip", Project: "p", MergeSHA: tip, Stage: work.StageMerged, Created: time.Now().UTC()})
	m.afterMerge(work.Task{ID: "old", Project: "p", MergeSHA: old})
	if m.measured("p", old) {
		t.Fatal("a merge under main's tip was measured before the tip")
	}
	m.afterMerge(work.Task{ID: "tip", Project: "p", MergeSHA: tip})
	m.afterMerge(work.Task{ID: "old", Project: "p", MergeSHA: old})
	if !m.measured("p", tip) || !m.measured("p", old) {
		t.Fatal("the tip measured should cover the merge under it")
	}
	m.mu.Lock()
	cov := m.covered["p"]
	m.mu.Unlock()
	if len(cov) == 0 || cov[len(cov)-1].SHA != old || cov[len(cov)-1].By != tip {
		t.Fatalf("the merge under the tip should be covered by it: %+v", cov)
	}
}
