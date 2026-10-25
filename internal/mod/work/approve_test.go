package work

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The person's yes on a change to protected files holds while the change's
// own diff is the same: main merged into the branch after it (as on #864,
// asked three times) doesn't ask again.
func TestApprovalOutlivesCatchUp(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{".hive/goals.md": "# Goals\n\nA tight loop.\n"})
	if err := m.test(task, p); err != nil || task.Stage != StagePerson {
		t.Fatalf("stage %q, err %v, want it waiting on the person", task.Stage, err)
	}
	m.onPersonMerge(core.Ask{Kind: "merge", Ref: task.ID + "@" + task.SHA, Answer: "merge"})
	got, _ := m.Get(task.ID)
	*task = got
	old := task.SHA
	pushMain(t, f, map[string]string{"other.txt": "z"})
	main, _ := m.C.Effects.Sync(p)
	if ok, err := m.catchUp(task, p, main, "main moved"); !ok || err != nil || task.SHA == old {
		t.Fatalf("catch-up: %v %v, head %s", ok, err, core.Short(task.SHA))
	}
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage == StagePerson {
		t.Fatalf("asked again after main was merged in: %q", task.Reason)
	}
}
