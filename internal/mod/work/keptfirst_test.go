package work

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// keptBeside puts a second task at the merge in task's project, a gain whose
// head changes files, kept by a measured gate against main at measured.
func keptBeside(t *testing.T, m *Module, p core.Project, f *core.Local, main string, files map[string]string, measured time.Time) *Task {
	t.Helper()
	sha := commitTo(t, f.RepoPath(p.Repo), "kept", "main", "kept", files)
	must(t, m.C.WS.Project(p.Name), "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*")
	u := &Task{ID: "t-kept", Project: p.Name, Stage: StageMerging, Aim: AimGain, SHA: sha, PR: core.PR{Number: 99}}
	m.mu.Lock()
	m.tasks[u.ID] = u
	m.mu.Unlock()
	m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Head: u.SHA, Main: main, Outcome: core.GateKeep, Reason: "better", Measured: true, At: measured})
	return u
}

// While a gain's measured keep stands at main, a merge that would change
// what it measured waits behind it (issue #1021); once the keep is too old to
// hold others, it merges.
func TestAKeptGainMergesFirst(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	task.Aim, task.AimWhy = AimFix, "TestX fails"
	main, _ := m.C.Effects.Sync(p)
	u := keptBeside(t, m, p, f, main, map[string]string{"feature.go": "package p // faster\n"}, time.Now())
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.HasPrefix(task.Reason, KeptFirstWait) || !strings.Contains(task.Reason, "#99") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q: want waiting behind the kept gain", task.Stage, task.Reason)
	}
	m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Head: u.SHA, Main: main, Outcome: core.GateKeep, Reason: "better", Measured: true, At: time.Now().Add(-keptFirstFor - time.Minute)})
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q: want merged once the keep is stale", task.Stage, task.Reason)
	}
}

// A merge that leaves a kept gain's files and the gate's measures alone
// doesn't wait for it: the verdict still stands after it.
func TestAMergeThatLeavesAKeepStandingGoesOn(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	task.Aim, task.AimWhy = AimFix, "TestX fails"
	main, _ := m.C.Effects.Sync(p)
	keptBeside(t, m, p, f, main, map[string]string{"other.go": "package p // faster\n"}, time.Now())
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q: want merged beside a keep it leaves standing", task.Stage, task.Reason)
	}
}

// Two keeps that would unseat each other: the one measured first merges, the
// other waits, so neither waits forever.
func TestTheFirstOfTwoKeepsGoesFirst(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	main, _ := m.C.Effects.Sync(p)
	keptBeside(t, m, p, f, main, map[string]string{"feature.go": "package p // faster\n"}, time.Now().Add(-time.Hour))
	m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Head: task.SHA, Main: main, Outcome: core.GateKeep, Reason: "better", Measured: true, At: time.Now()})
	if why := m.keptFirst(task, p, main); !strings.HasPrefix(why, KeptFirstWait) {
		t.Fatalf("%q: want the later keep waiting behind the earlier", why)
	}
	m.mu.Lock()
	u := m.tasks["t-kept"]
	m.mu.Unlock()
	if why := m.keptFirst(u, p, main); why != "" {
		t.Fatalf("the earlier keep waits too: %q", why)
	}
}

// A keep its judge has since rolled back holds nothing: the other merge
// goes on (liveKeep's judgement branch).
func TestAKeepItsJudgeRolledBackHoldsNothing(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	task.Aim, task.AimWhy = AimFix, "TestX fails"
	main, _ := m.C.Effects.Sync(p)
	measured := time.Now().Add(-time.Minute)
	u := keptBeside(t, m, p, f, main, map[string]string{"feature.go": "package p // faster\n"}, measured)
	if why := m.keptFirst(task, p, main); !strings.HasPrefix(why, KeptFirstWait) {
		t.Fatalf("%q: want waiting behind the keep before its judge calls it", why)
	}
	m.C.J.MustAppend("judge.decided", core.Judgement{Project: p.Name, SHA: u.SHA, Main: main, Run: "j1", Numbers: core.GateKeep, Verdict: core.GateRollback, At: time.Now()})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q: want merged once the keep's judge rolled it back", task.Stage, task.Reason)
	}
}

// A kept task's label with its own parentheses doesn't make the hold's key
// vary, so the PR hears the hold once.
func TestAKeptFirstHoldKeyIgnoresALabelsParentheses(t *testing.T) {
	why := func(until string) string {
		return KeptFirstWait + ": #99 (Fix (the flaky) TestX (internal/core)) was kept on abc against main at def, and this merge would change what that verdict measured; it merges first (or until " + until + ")"
	}
	if a, b := HoldKey(why("10:00Z")), HoldKey(why("11:00Z")); a != b {
		t.Fatalf("hold key varies with the bound: %q vs %q", a, b)
	}
}
