package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// treeRig is selfRig with a gate that rolls back for reason.
func treeRig(t *testing.T, reason string) (*Module, *Task, core.Project) {
	t.Helper()
	m, task, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateRollback)
	m.Gate = func(_ context.Context, j GateJob) core.GateResult {
		os.MkdirAll(j.Out, 0o755)
		report := filepath.Join(j.Out, "report.md")
		os.WriteFile(report, []byte("# Gate: rollback\n"), 0o644)
		return core.GateResult{Outcome: core.GateRollback, Reason: reason, Report: report, Sandbox: "bwrap"}
	}
	return m, task, p
}

func branchesOf(m *Module, id string) []Task {
	return m.List(func(o Task) bool { return o.Parent == id })
}

// A rollback for the change's own fallout goes back to its worker and also
// branches a sibling from the rolled-back head, once per head; the branch's
// worker starts from that head, and the two don't wait on each other's files.
func TestTreeBranchesOnFallout(t *testing.T) {
	m, task, p := treeRig(t, "worse than the current Hive: e2e passes 0/3, the control 3/3")
	head := task.SHA
	gateOnce(t, m, task, p)
	if task.Stage != StageWorking || !strings.Contains(task.Findings, gateRolledBack) {
		t.Fatalf("stage %q: want the worker sent back as before", task.Stage)
	}
	bs := branchesOf(m, task.ID)
	if len(bs) != treeWidth-1 {
		t.Fatalf("%d branches, want %d", len(bs), treeWidth-1)
	}
	b := bs[0]
	if b.Start != head || b.Tree != task.ID || b.Depth != 1 || !b.Gain() || !strings.Contains(b.Known, "worse than the current Hive") {
		t.Fatalf("branch %+v: want it from %s in tree %s at depth 1, a gain, told the fallout", b, core.Short(head), task.ID)
	}
	// The same head never branches twice.
	m.branchOut(task, p, core.GateResult{Head: head, Reason: "worse than the current Hive: x"}, "")
	if n := len(branchesOf(m, task.ID)); n != 1 {
		t.Fatalf("%d branches after the same head again, want 1", n)
	}
	if o, _ := m.overlap(b, []Task{*task}, ""); o != nil {
		t.Fatalf("the branch waits on its parent's files: %s", o.ID)
	}
	for b.Stage == StageNew || b.Queued() {
		if err := m.step(&b); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(m.C.WS.Checkouts(p.Name), b.Worker)
	if got := strings.TrimSpace(must(t, dir, "rev-parse", "HEAD")); got != head {
		t.Fatalf("the branch's worker starts at %s, want the rolled-back head %s", core.Short(got), core.Short(head))
	}
}

// A rollback for nothing moving, which no fix of fallout helps, doesn't
// branch; nor does a node at the tree's depth.
func TestTreeNoBranchWithoutFallout(t *testing.T) {
	m, task, p := treeRig(t, "not better than the current Hive: nothing moved beyond the noise")
	gateOnce(t, m, task, p)
	if n := len(branchesOf(m, task.ID)); n != 0 {
		t.Fatalf("%d branches on no gain, want none", n)
	}
	task.Depth = treeDepth
	m.branchOut(task, p, core.GateResult{Head: "abc", Reason: "fails its own vet or short tests: x"}, "")
	if n := len(branchesOf(m, task.ID)); n != 0 {
		t.Fatalf("%d branches at the tree's depth, want none", n)
	}
	// Nor does a tree that has filed its total of branches.
	task.Depth = 0
	defer func(n int) { treeTotal = n }(treeTotal)
	treeTotal = 0
	m.branchOut(task, p, core.GateResult{Head: "abd", Reason: "fails its own vet or short tests: x"}, "")
	if n := len(branchesOf(m, task.ID)); n != 0 {
		t.Fatalf("%d branches past the tree's total, want none", n)
	}
}

// Once a node of the tree merges, the other open nodes are archived at
// their next step: stopped, their workers stopped, the merged one kept.
func TestTreeArchivesTheRest(t *testing.T) {
	m, task, p := treeRig(t, "fails its own vet or short tests: internal/x TestY")
	gateOnce(t, m, task, p)
	b := branchesOf(m, task.ID)[0]
	b.Stage, b.MergeSHA = StageMerged, "deadbeef"
	m.save(&b, "merged (test)")
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageStopped || !strings.HasPrefix(task.Reason, treeArchived) || !strings.Contains(task.Reason, b.ID) {
		t.Fatalf("stage %q, reason %q: want the root archived for its merged branch", task.Stage, task.Reason)
	}
	if r, ok := m.C.Runs.Get(task.Worker); ok && (!r.Terminal() || r.Status == core.StatusStopped || r.StopAsked) {
		t.Fatalf("the archived node's worker %s is %s (stop asked %v): want it ended blocked, not stopped as the person", r.ID, r.Status, r.StopAsked)
	}
	// Recheck brings an archived node back, out of its tree, so it isn't
	// archived again at its next step.
	if err := m.Recheck(task.ID, "", "lead"); err != nil {
		t.Fatalf("recheck refused an archived node: %v", err)
	}
	*task, _ = m.Get(task.ID)
	if m.archived(task) || task.Stage == StageStopped {
		t.Fatalf("stage %q, reason %q: the rechecked node was archived again", task.Stage, task.Reason)
	}
	// A task in no tree is never archived.
	lone := Task{ID: "lone", Kind: "work", Stage: StageWorking}
	if m.archived(&lone) {
		t.Fatal("a task in no tree was archived")
	}
}
