package work

import "testing"

// Main moving onto a line the branch changed too sends the branch back to its
// worker from testing, before its test runs, not at the merge.
func TestMainMovedConflictGoesBackFromTesting(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y\n"})
	pushMain(t, f, map[string]string{"feature.txt": "z\n"})
	head := task.SHA
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	if !task.Resending() || task.SHA != head || task.Conflicts != 1 {
		t.Fatalf("stage %q, resend %q, conflicts %d, log %v", task.Stage, task.Resend, task.Conflicts, task.Log)
	}
	if c, ok := m.C.Effects.GetCheck(p.Name, head, "test"); ok {
		t.Fatalf("its test ran anyway: %+v", c)
	}
}

// Main moving elsewhere leaves the head as it is: tested and on to the merge,
// with no catch-up push to restart its checks.
func TestMainMovedCleanlyLeavesTheHead(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y\n"})
	pushMain(t, f, map[string]string{"other.txt": "z\n"})
	head := task.SHA
	toMerging(t, m, task, p)
	if task.SHA != head || task.Conflicts != 0 {
		t.Fatalf("head %s (was %s), conflicts %d, log %v", task.SHA, head, task.Conflicts, task.Log)
	}
}

// A head already merging is sent back when main moves onto its lines, from
// the Hive's own look and not only the forge's.
func TestMainMovedConflictGoesBackFromMerging(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y\n"})
	toMerging(t, m, task, p)
	pushMain(t, f, map[string]string{"feature.txt": "z\n"})
	mergeOnce(t, m, task, p)
	if !task.Resending() || task.Conflicts != 1 {
		t.Fatalf("stage %q, resend %q, log %v", task.Stage, task.Resend, task.Log)
	}
}
