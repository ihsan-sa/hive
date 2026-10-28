package work

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
	if err := testStep(m, task, p); err != nil || task.Stage != StageReviewing {
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

// A measurement of the last checkpoint that lands while a commit is being
// weighed counts: here main is measured at 7 as the commit (also 7) is taken
// out, so the commit is no gain and is no checkpoint. Read before, the kept
// best was 5 and the commit merged as one more (Windows ci, banked 3).
func TestCheckpointCountsAMeasurementInFlight(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "7")
	task.Banks, task.Banked = []Bank{{PR: core.PR{Number: 2}, MergeSHA: "m1"}}, 1
	measured := false
	oldB, oldM := BestScore, Measured
	BestScore = func(string) (float64, bool) {
		if measured {
			return 7, true
		}
		return 5, true
	}
	Measured = func(_, sha string) bool {
		measured = true // the measurement of m1 lands now
		return sha == "m1"
	}
	t.Cleanup(func() { BestScore, Measured = oldB, oldM })
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Checkpoint != "" || task.PR.Number != 0 || !logged(task, "doesn't beat the best kept score 7") {
		t.Fatalf("checkpoint %q, PR %d, log %v", task.Checkpoint, task.PR.Number, task.Log)
	}
}

// A checkpoint merged on the forge by hand tells its worker how it merged,
// not that it beat the best kept score.
func TestCheckpointMergedOnForgeTellsWorker(t *testing.T) {
	m, task, _ := checkpointRig(t, 5, "7")
	task.Checkpoint = "abc1234567"
	m.merged(task, "def7654321", "merged on the forge outside the Hive (PR #3) as def7654")
	r, _ := m.C.Runs.Get("w1")
	if task.Stage != StageWorking || len(r.Pending) != 1 {
		t.Fatalf("stage %q, worker told %v", task.Stage, r.Pending)
	}
	if got := fmt.Sprint(r.Pending[0]); !strings.Contains(got, "merged on the forge outside the Hive") || strings.Contains(got, "beat the best kept score") {
		t.Fatalf("worker told %q", got)
	}
}

// A change to the Hive aimed at a gain is never merged as checkpoints, however
// it scores: the task says why, and a fix's checkpoints come back once the
// lead makes it one (issue #759).
func TestCheckpointRefusedForAGain(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "7")
	p.Self = true
	task.Aim = AimGain
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageWorking || task.Checkpoint != "" || task.PR.Number != 0 || task.NoBank != noBankGain || !logged(task, "no checkpoints: it aims at a gain") {
		t.Fatalf("stage %q, checkpoint %q, PR %d, no bank %q, log %v", task.Stage, task.Checkpoint, task.PR.Number, task.NoBank, task.Log)
	}
	n := len(task.Log)
	task.Seen = "" // a new commit
	m.checkWorker(task, p)
	if len(task.Log) != n || task.PR.Number != 0 {
		t.Fatalf("said it again or opened a checkpoint: %v", task.Log[n:])
	}
	task.Aim, task.AimWhy = AimFix, "a named failure"
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Checkpoint == "" || task.NoBank != "" {
		t.Fatalf("a fix got no checkpoint: no bank %q, log %v", task.NoBank, task.Log)
	}
}

// A checkpoint opened while its task was a fix, and aimed at a gain since,
// is dropped at the gate unjudged; the worker's finished head then goes
// through the recursive gate whole, and merges on its keep.
func TestGainCheckpointDroppedFinalHeadGated(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	task.Checkpoint = task.SHA
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Checkpoint != "" || len(*jobs) != 0 || prState(t, f, task.PR.Number) == "MERGED" || !logged(task, "checkpoint not merged: it aims at a gain") {
		t.Fatalf("stage %q, checkpoint %q, %d gate runs, log %v", task.Stage, task.Checkpoint, len(*jobs), task.Log)
	}
	task.Stage = StageMerging // the worker finished on the same head
	gateOnce(t, m, task, p)
	if len(*jobs) != 1 || task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("%d gate runs, stage %q, reason %q: want the whole head merged on the gate's keep", len(*jobs), task.Stage, task.Reason)
	}
}

// While checks wait for a slot, a commit isn't weighed: the step would hold
// the task behind the backlog, so a worker that finished meanwhile would sit
// in working. The commit is weighed once the backlog clears.
func TestCheckpointWaitsOutACheckBacklog(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "5")
	slots := core.NewSlots(func() int { return 1 })
	m.C.Effects.Slots = slots
	release := slots.Acquire("other", "abc", "test")
	go func() { slots.Acquire("other", "def", "test")() }()
	for _, waiting := slots.State(); len(waiting) == 0; _, waiting = slots.State() {
		time.Sleep(time.Millisecond)
	}
	done := make(chan error, 1)
	go func() { done <- m.checkWorker(task, p) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the checkpoint waited for a check slot")
	}
	if task.Seen != "" || logged(task, "doesn't beat") {
		t.Fatalf("weighed behind the backlog: seen %q, log %v", task.Seen, task.Log)
	}
	release()
	for running, waiting := slots.State(); len(running)+len(waiting) > 0; running, waiting = slots.State() {
		time.Sleep(time.Millisecond)
	}
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if !logged(task, "doesn't beat the best kept score 5") {
		t.Fatalf("not weighed once the backlog cleared: %v", task.Log)
	}
}

// Every slot taken with nothing yet waiting holds the step just the same: the
// commit waits for a free slot too.
func TestCheckpointWaitsOutFullSlots(t *testing.T) {
	m, task, p := checkpointRig(t, 5, "5")
	slots := core.NewSlots(func() int { return 1 })
	m.C.Effects.Slots = slots
	release := slots.Acquire("other", "abc", "test")
	done := make(chan error, 1)
	go func() { done <- m.checkWorker(task, p) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the checkpoint waited for a check slot")
	}
	if task.Seen != "" {
		t.Fatalf("weighed with every slot taken: seen %q, log %v", task.Seen, task.Log)
	}
	release()
	if err := m.checkWorker(task, p); err != nil {
		t.Fatal(err)
	}
	if !logged(task, "doesn't beat the best kept score 5") {
		t.Fatalf("not weighed once a slot was free: %v", task.Log)
	}
}
