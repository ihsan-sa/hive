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

// A run's private folder goes by its checkout's rule, and the caches in an
// ended run's home go once it has sat idle an hour; a run still going keeps
// its home whole.
func TestRunHomesGo(t *testing.T) {
	m, _, task, p := noCommitRig(t, 0)
	ws := m.C.WS
	put := func(id, rel string) string {
		f := filepath.Join(ws.RunHome(id), rel)
		os.MkdirAll(filepath.Dir(f), 0o755)
		os.WriteFile(f, []byte("data"), 0o444)
		return f
	}
	start := func(id, role, status string) {
		if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: role, Project: p.Name, Task: "t1", Harness: "fake"}); err != nil {
			t.Fatal(err)
		}
		if status != "" {
			m.C.Runs.SetStatus(id, status, "")
		}
	}
	start("w-t1", "worker", core.StatusDone)
	start("rv1", "reviewer", "")
	task.Worker, task.Stage = "w-t1", StageReviewing
	m.save(task, "reviewing")
	wCache, wSession := put("w-t1", ".cache/go-build/x"), put("w-t1", ".claude/projects/s.jsonl")
	wMods := put("w-t1", "go/pkg/mod/m/f.go")
	os.Chmod(filepath.Dir(wMods), 0o555)
	rCache := put("rv1", ".cache/go-build/x")
	// An ended run whose ~/go links out of its home: the folder it points at stays.
	start("w-link", "worker", core.StatusDone)
	outside := filepath.Join(t.TempDir(), "project")
	outPkg := filepath.Join(outside, "pkg", "keep.go")
	os.MkdirAll(filepath.Dir(outPkg), 0o755)
	os.WriteFile(outPkg, []byte("package keep"), 0o644)
	os.MkdirAll(ws.RunHome("w-link"), 0o755)
	if err := os.Symlink(outside, filepath.Join(ws.RunHome("w-link"), "go")); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(ws.Private(), "runs", "gone-run")
	os.MkdirAll(orphan, 0o755)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(orphan, old, old)

	m.pruneRunHomes(time.Now())
	for f, want := range map[string]bool{wCache: true, wSession: true, rCache: true, orphan: false} {
		if core.Exists(f) != want {
			t.Fatalf("just ended: %s exists %v, want %v", f, !want, want)
		}
	}
	m.pruneRunHomes(time.Now().Add(2 * time.Hour))
	for f, want := range map[string]bool{wCache: false, wMods: false, wSession: true, rCache: true, outPkg: true} {
		if core.Exists(f) != want {
			t.Fatalf("an hour idle: %s exists %v, want %v", f, !want, want)
		}
	}
	task.Stage = StageMerged
	m.save(task, "merged")
	m.Wait()
	m.pruneRunHomes(time.Now())
	if core.Exists(ws.RunPrivate("w-t1")) || !core.Exists(rCache) {
		t.Fatal("a merged task's worker home stayed, or a running reviewer's went")
	}
}
