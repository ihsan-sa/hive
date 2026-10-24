package work

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// checkpointRig is the gate rig on a scored project whose best kept score is
// best, with a still-running worker whose commit scores score.
func checkpointRig(t *testing.T, best float64, score string) (*Module, *Task, core.Project) {
	m, task, p, forge := gateRig(t, false, map[string]string{"score.val": score + "\n"})
	base := must(t, m.C.WS.Project(p.Name), "rev-parse", task.SHA+"^")
	pushMain(t, forge, map[string]string{".hive/project.json": `{"test": "test ! -f BROKEN", "score": {"command": "cat score.val", "direction": "higher"}}`})
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	old := BestScore
	BestScore = func(string) (float64, bool) { return best, true }
	t.Cleanup(func() { BestScore = old })
	task.Stage, task.Base, task.SHA, task.PR, task.Branch = StageWorking, base, "", core.PR{}, "hive/t1-cp"
	return m, task, p
}

// A commit that only matches the best kept score is not a checkpoint: the
// task stays with its worker and nothing is pushed.
func TestCheckpointNeedsAHigherScore(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "5")
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageWorking || task.Checkpoint != "" || task.PR.Number != 0 || !logged(task, "doesn't beat the best kept score 5") {
		t.Fatalf("stage %q, checkpoint %q, PR %d, log %v", task.Stage, task.Checkpoint, task.PR.Number, task.Log)
	}
	// Nothing new in the checkout: the next look doesn't measure again.
	n := len(task.Log)
	m.checkWorker(task, p)
	if len(task.Log) != n {
		t.Fatalf("looked again with no new commit: %v", task.Log[n:])
	}
}

// A higher score opens a checkpoint through the normal gate; a reviewer's
// findings drop it and leave the running worker as it was.
func TestCheckpointHandbackLeavesWorker(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "7")
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageTesting || task.Checkpoint == "" || task.PR.Number == 0 {
		t.Fatalf("stage %q, checkpoint %q, PR %d, log %v", task.Stage, task.Checkpoint, task.PR.Number, task.Log)
	}
	if err := m.test(task, p); err != nil || task.Stage != StageReviewing {
		t.Fatalf("stage %q, err %v", task.Stage, err)
	}
	id := "r-t1-" + core.Short(task.SHA) + "-0"
	if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "reviewer", Project: p.Name, Harness: "fake", ReadOnly: true, Inputs: map[string]string{"sha": task.SHA}}); err != nil {
		t.Fatal(err)
	}
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone,
		"result": harness.Result{Structured: []byte(`{"verdict":"fix","summary":"no","token":"x"}`)}})
	before, _ := m.C.Runs.Get("w1")
	if err := m.review(task, p); err != nil {
		t.Fatal(err)
	}
	after, _ := m.C.Runs.Get("w1")
	if task.Stage != StageWorking || task.Checkpoint != "" || task.Round != 0 || after.Status != before.Status || len(after.Pending) != 0 {
		t.Fatalf("stage %q, checkpoint %q, round %d, worker %s with %v", task.Stage, task.Checkpoint, task.Round, after.Status, after.Pending)
	}
	if !logged(task, "checkpoint not merged: the reviewer asked for fixes") || strings.Contains(task.Reason, "fix") {
		t.Fatalf("log %v, reason %q", task.Log, task.Reason)
	}
}

// A dropped checkpoint's commit is not weighed again when the reflog moves
// with no new commit, and a worker sent back after its finish gets none.
func TestCheckpointWeighsACommitOnce(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "7")
	if err := m.checkWorker(task, p); err != nil || task.Checkpoint == "" {
		t.Fatalf("checkpoint %q, err %v", task.Checkpoint, err)
	}
	pr := task.PR.Number
	m.dropCheckpoint(task, p, "it conflicts with main")
	task.Seen = "" // the reflog moved
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageWorking || task.Checkpoint != "" || task.PR.Number != pr {
		t.Fatalf("weighed again: stage %q, checkpoint %q, PR %d", task.Stage, task.Checkpoint, task.PR.Number)
	}

	m, task, p = checkpointRig(t, 5, "7")
	task.Round = 1
	if err := m.checkWorker(task, p); err != nil || task.Checkpoint != "" || task.PR.Number != 0 {
		t.Fatalf("a sent-back worker got a checkpoint %q (PR %d, err %v)", task.Checkpoint, task.PR.Number, err)
	}
}

// A conflict found before a checkpoint's pull request opens comments on no PR.
func TestCheckpointDropWithoutPR(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "7")
	task.Checkpoint = "x"
	if err := m.dropCheckpoint(task, p, "it conflicts with main"); err != nil || task.Checkpoint != "" {
		t.Fatalf("checkpoint %q, err %v", task.Checkpoint, err)
	}
}

// A revert of a checkpoint's merge names that checkpoint's pull request, not
// the task's current one.
func TestCheckpointPROf(t *testing.T) {
	task := Task{PR: core.PR{Number: 9}, Banks: []Bank{{PR: core.PR{Number: 4}, MergeSHA: "a"}, {PR: core.PR{Number: 6}, MergeSHA: "b"}}}
	if n := task.PROf("b").Number; n != 6 {
		t.Fatalf("PROf(b) = #%d", n)
	}
	if n := task.PROf("c").Number; n != 9 {
		t.Fatalf("PROf(c) = #%d", n)
	}
}
