package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// pushHead puts a commit with files on top of the branch's head on the
// forge, past the Hive, as someone with the repo's rights would.
func pushHead(t *testing.T, f *core.Local, branch string, files map[string]string) string {
	t.Helper()
	tmp := t.TempDir()
	must(t, "", "clone", "-q", "-b", branch, f.RepoPath("me/p"), tmp)
	for name, body := range files {
		os.WriteFile(filepath.Join(tmp, name), []byte(body), 0o644)
	}
	must(t, tmp, "add", "-A")
	must(t, tmp, "commit", "-q", "-m", "outside")
	must(t, tmp, "push", "-q", "origin", "HEAD:"+branch)
	return must(t, tmp, "rev-parse", "HEAD")
}

// staleHead is the incident of PR #462: the Hive pushed a newer head (here a
// catch-up with main, or a new diff), but the task kept the one before, on
// which the forge will never report a check.
func staleHead(t *testing.T, newDiff bool) (*Module, *Task, core.Project, *core.Local, string) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	pushMain(t, f, map[string]string{"other.txt": "z"})
	main, _ := m.C.Effects.Sync(p)
	next, err := m.C.Effects.UpdateBranch(p, task.SHA, main, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.C.Effects.Push(p, task.Branch, next); err != nil {
		t.Fatal(err)
	}
	if newDiff {
		next = pushHead(t, f, task.Branch, map[string]string{"more.txt": "w"})
		if err := m.C.Effects.Push(p, task.Branch, next); err != nil { // as if the Hive had pushed it
			t.Fatal(err)
		}
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPending})
	f.SetChecks(p.Repo, next, core.CheckRun{Name: "test", State: core.CheckPass})
	return m, task, p, f, next
}

// The Hive's own newest push is adopted and tested again, and then merges
// on the checks of that head.
func TestMergeAdoptsTheHivesNewerHead(t *testing.T) {
	m, task, p, f, next := staleHead(t, false)
	mergeOnce(t, m, task, p)
	if task.SHA != next || task.Stage != StageTesting || task.Round != 0 || !logHas(task, "adopted it") {
		t.Fatalf("sha %s (head %s), stage %q, reason %q", core.Short(task.SHA), core.Short(next), task.Stage, task.Reason)
	}
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A self project reads every head: an adopted head with the same own diff
// keeps its read, one with a new diff is read again.
func TestMergeAdoptedHeadReadOnlyIfTheDiffChanged(t *testing.T) {
	for _, newDiff := range []bool{false, true} {
		m, task, p, _, next := staleHead(t, newDiff)
		p.Self = true
		main, _ := m.C.Effects.Sync(p)
		ch, _ := m.C.Effects.Classify(p, main, task.SHA)
		if _, err := m.C.Runs.Start(core.RunSpec{ID: "r1", Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": task.SHA, "diff.patch": ch.Diff}}); err != nil {
			t.Fatal(err)
		}
		if err := m.C.Effects.RecordVerdict(core.Verdict{Project: p.Name, SHA: task.SHA, Diff: ch.DiffID, Run: "r1", Verdict: "land"}); err != nil {
			t.Fatal(err)
		}
		mergeOnce(t, m, task, p)
		if task.SHA != next {
			t.Fatalf("not adopted: %s", task.Reason)
		}
		if err := m.test(task, p); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]string{false: StageMerging, true: StageReviewing}[newDiff]; task.Stage != want {
			t.Fatalf("new diff %v: stage %q, want %q", newDiff, task.Stage, want)
		}
	}
}

// A forge still showing an earlier push of the Hive's hasn't caught up: the
// task waits, and keeps its newer head.
func TestMergeWaitsForTheForgeToShowItsHead(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	old := task.SHA
	newer := pushHead(t, f, task.Branch, map[string]string{"x.txt": "1"})
	if err := m.C.Effects.Push(p, task.Branch, newer); err != nil { // as if the Hive had pushed it
		t.Fatal(err)
	}
	task.SHA = newer
	must(t, f.RepoPath("me/p"), "update-ref", "refs/heads/"+task.Branch, old) // the forge lags
	mergeOnce(t, m, task, p)
	if task.SHA != newer || task.Stage != StageMerging || !strings.Contains(task.Reason, "waiting for the forge to show") {
		t.Fatalf("sha %s, stage %q, reason %q", core.Short(task.SHA), task.Stage, task.Reason)
	}
}

// A head the Hive didn't push goes back to the worker, saying it moved.
func TestMergeForeignHeadGoesBack(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	head := pushHead(t, f, task.Branch, map[string]string{"theirs.txt": "q"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || !strings.Contains(task.Findings, "moved to "+core.Short(head)) || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, findings %q", task.Stage, task.Round, task.Findings)
	}
}

// waitedLong backdates the task's wait for its head's first check run past
// the 30 minutes it waits. It goes through the task's saved form, so this file
// builds on a Hive without the wait, where the test then fails (the gate's
// fixes step runs it there).
func waitedLong(t *testing.T, task *Task) {
	t.Helper()
	setJSON(t, task, map[string]any{"no_checks": time.Now().Add(-31 * time.Minute)})
}

// A head on which the forge reports no check run at all waits only so long:
// behind main, it is brought up to date for a head the forge will run on; up
// to date, the lead hears of it once.
func TestMergeNoCheckRunsIsBounded(t *testing.T) {
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nrequired_checks = [\"sandboxed-e2e\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || alerts(m, "main-red") != 0 {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	waitedLong(t, task)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "lead has been told") || alerts(m, "main-red") != 1 {
		t.Fatalf("up to date: stage %q, reason %q, %d alerts", task.Stage, task.Reason, alerts(m, "main-red"))
	}
	old := task.SHA
	pushMain(t, f, map[string]string{"other.txt": "z"})
	waitedLong(t, task)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.SHA == old || task.Stage != StageTesting || task.Round != 0 {
		t.Fatalf("behind main: sha %s, stage %q, reason %q", core.Short(task.SHA), task.Stage, task.Reason)
	}
}

// A task already holding for checks that never came, with its reason
// unchanged from tick to tick, keeps the start of its wait across ticks,
// each of which loads it afresh: else the wait restarts every tick.
func TestMergeNoCheckRunsWaitSurvivesReload(t *testing.T) {
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nrequired_checks = [\"sandboxed-e2e\"]\n"}
	m, task, p, _ := gateRigSeeded(t, false, list, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	setJSON(t, task, map[string]any{"no_checks": nil, "no_checks_on": nil}) // as saved before the wait was bounded
	m.save(task, "")
	reload := func() *Task {
		got, ok := m.Get(task.ID)
		if !ok {
			t.Fatal("task gone")
		}
		m.later = map[string]time.Time{}
		return &got
	}
	again := reload()
	mergeOnce(t, m, again, p)
	again = reload()
	raw, _ := json.Marshal(again)
	var saved map[string]any
	json.Unmarshal(raw, &saved)
	if saved["no_checks_on"] != again.SHA {
		t.Fatalf("the wait's start wasn't saved: %v", saved["no_checks_on"])
	}
	waitedLong(t, again)
	mergeOnce(t, m, again, p)
	if !strings.Contains(again.Reason, "lead has been told") || alerts(m, "main-red") != 1 {
		t.Fatalf("reason %q, %d alerts", again.Reason, alerts(m, "main-red"))
	}
}

// setJSON sets fields of the task's saved form, nil removing one, through
// JSON so that this file builds on a Hive without them.
func setJSON(t *testing.T, task *Task, set map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(task)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	for k, v := range set {
		if v == nil {
			delete(fields, k)
		} else {
			fields[k] = v
		}
	}
	raw, _ = json.Marshal(fields)
	*task = Task{}
	if err := json.Unmarshal(raw, task); err != nil {
		t.Fatal(err)
	}
}
