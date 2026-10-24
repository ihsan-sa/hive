package work

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A task's checkouts go when it ends: its worker's once it is merged, its
// reviewer's once that run has ended and the task is over; a stopped task
// keeps its worker's (recheck brings the worker back in it) until it is
// stale; a run still going keeps its checkout; a folder no run names goes
// once it is old.
func TestCheckoutsGoWhenTheTaskEnds(t *testing.T) {
	m, _, task, p := noCommitRig(t, 0)
	root := m.C.WS.Checkouts(p.Name)
	mk := func(id string) string {
		d := filepath.Join(root, id)
		os.MkdirAll(filepath.Join(d, ".git"), 0o755)
		return d
	}
	run := func(id, role, task, status string) string {
		if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: role, Project: p.Name, Task: task, Checkout: mk(id), Harness: "fake"}); err != nil {
			t.Fatal(err)
		}
		if status != "" {
			m.C.Runs.SetStatus(id, status, "")
		}
		return filepath.Join(root, id)
	}
	worker := run("w-t1", "worker", "t1", core.StatusDone)
	reviewer := run("rv1", "reviewer", "t1", core.StatusDone)
	live := run("rv2", "reviewer", "t1", "")
	orphan := mk("gone-run")
	young := mk("young-run")
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(orphan, old, old)

	task.Worker, task.Stage = "w-t1", StageReviewing
	m.save(task, "reviewing")
	m.PruneCheckouts()
	for d, want := range map[string]bool{worker: true, reviewer: true, live: true, orphan: false, young: true} {
		if core.Exists(d) != want {
			t.Fatalf("while reviewing: %s exists %v, want %v", filepath.Base(d), !want, want)
		}
	}

	task.Stage = StageMerged
	m.save(task, "merged")
	m.Wait() // the save removes them off its own path
	for d, want := range map[string]bool{worker: false, reviewer: false, live: true} {
		if core.Exists(d) != want {
			t.Fatalf("merged: %s exists %v, want %v", filepath.Base(d), !want, want)
		}
	}

	// A stopped task keeps its worker's checkout until it is stale.
	m2, _, t2, p2 := noCommitRig(t, 0)
	d2 := filepath.Join(m2.C.WS.Checkouts(p2.Name), "w-t1")
	os.MkdirAll(d2, 0o755)
	m2.C.Runs.Start(core.RunSpec{ID: "w-t1", Role: "worker", Project: p2.Name, Task: "t1", Checkout: d2, Harness: "fake"})
	m2.C.Runs.SetStatus("w-t1", core.StatusDone, "")
	t2.Worker, t2.Stage = "w-t1", StageStopped
	m2.save(t2, "stopped")
	m2.PruneCheckouts()
	if !core.Exists(d2) {
		t.Fatal("a stopped task's worker checkout went before it was stale")
	}
	if !over(Task{Stage: StageStopped, Updated: time.Now().Add(-8 * 24 * time.Hour)}, time.Now()) {
		t.Fatal("a task stopped for a week is still waiting for its worker")
	}
}
