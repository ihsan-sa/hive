package work

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// baseMovedForge is GitHub with another merge of the batch moving main as
// this one goes in.
type baseMovedForge struct{ *core.Local }

func (baseMovedForge) MergePR(repo string, number int, sha, subject string) (string, error) {
	return "", fmt.Errorf("PR #%d: %w (%v)", number, core.ErrBaseMoved, errors.New("gh pr merge: GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)"))
}

// Main moving mid-merge, as it does in a batch, counts toward no stop: the
// task stays in the merge however often it happens, and merges once main is
// still.
func TestBaseMovedRetriesTheMerge(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.C.Effects.Forge = baseMovedForge{f}
	for range 12 {
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || task.Failures != 0 || !logged(task, "main moved as it merged") {
		t.Fatalf("stage %q, %d failures, log %v", task.Stage, task.Failures, task.Log)
	}
	m.C.Effects.Forge = f
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q after main was still, log %v", task.Stage, task.Log)
	}
}

// A task stopped on it before this fix comes back on its own.
func TestBaseMovedStopsComeBack(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Failures = 8
	m.giveUp(task, "gave up after repeated errors: gh pr merge 904: GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)")
	m.revive()
	got, _ := m.Get(task.ID)
	if got.Stage != StageLanding || got.Failures != 0 || got.Reason != "" || !logged(&got, "main moved while it merged") {
		t.Fatalf("not brought back: %+v", got)
	}
}
