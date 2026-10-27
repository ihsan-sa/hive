package work

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// passMerged runs t's merged test at main to its end and fails unless it passed.
func passMerged(t *testing.T, m *Module, task *Task, p core.Project, main string) {
	t.Helper()
	for i := 0; i < 4; i++ {
		wait, err := m.mergedTests(task, p, main)
		if err != nil {
			t.Fatal(err)
		}
		if !wait {
			return
		}
		m.Wait()
	}
	t.Fatalf("the merged test at %s never passed: reason %q", core.Short(main), task.Reason)
}

// A merged test that passed stands as main moves, while main's commits
// touch none of the head's files nor what the gate measures with (issue
// #1794); a commit that touches either sends it to test again.
func TestMergedPassStandsWhileMainMissesItsFiles(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	pushMain(t, f, map[string]string{"other.txt": "z"})
	main, _ := m.C.Effects.Sync(p)
	passMerged(t, m, task, p, main)
	tested := task.MergedAt
	if tested.IsZero() {
		t.Fatal("the pass records no time")
	}
	pushMain(t, f, map[string]string{"another.txt": "w"})
	moved, _ := m.C.Effects.Sync(p)
	if wait, err := m.mergedTests(task, p, moved); wait || err != nil || task.MergedOK != task.SHA+"+"+moved || task.MergedTest != "" {
		t.Fatalf("an unrelated main move: wait %v, err %v, merged ok %q, merged test %q", wait, err, task.MergedOK, task.MergedTest)
	}
	if !task.MergedAt.Equal(tested) {
		t.Fatal("a carried pass moved its test time")
	}
	for _, touch := range []map[string]string{{"feature.txt": "clash"}, {"tests/x.sh": "x"}} {
		pushMain(t, f, touch)
		at, _ := m.C.Effects.Sync(p)
		if wait, err := m.mergedTests(task, p, at); !wait || err != nil {
			t.Fatalf("main touching %v kept the pass: wait %v, err %v, reason %q", touch, wait, err, task.Reason)
		}
		m.Wait()
		m.later = map[string]time.Time{}
		m.mergedTests(task, p, at) // takes the result, red or green
	}
}

// The starvation of issue #1794: the line's front passes its merged test,
// waits for a gate slot, and main moves past it again and again by merges
// that miss its files. It isn't sent back to test, and when a slot frees it
// takes it ahead of a newer PR that also waits for one, and merges.
func TestMergeLineFrontNotStarved(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	maxGates(m, 1)
	m.mu.Lock()
	m.gating = map[string]*gateRun{"other": {key: "other", task: "t9"}}
	m.mu.Unlock()
	pushMain(t, f, map[string]string{"other.txt": "z"})
	mergeOnce(t, m, task, p)
	if !strings.HasPrefix(task.Reason, gateSlotWait) {
		t.Fatalf("reason %q: want waiting for a gate slot", task.Reason)
	}
	newer := &Task{ID: "t-newer", Project: p.Name, Kind: "work", Stage: StageMerging, SHA: "feedface", PR: core.PR{Number: task.PR.Number + 1}, Reason: gateSlotWait + ": behind"}
	m.mu.Lock()
	m.tasks[newer.ID] = newer
	m.mu.Unlock()
	for i := range 5 { // main moves every round, never touching its files
		pushMain(t, f, map[string]string{fmt.Sprintf("moved%d.txt", i): "x"})
		m.later = map[string]time.Time{}
		if err := m.merge(task, p); err != nil {
			t.Fatal(err)
		}
		if task.MergedTest != "" || !strings.HasPrefix(task.Reason, gateSlotWait) {
			t.Fatalf("round %d: merged test %q, reason %q: want the pass standing and the slot awaited", i, task.MergedTest, task.Reason)
		}
	}
	// A slot frees: the newer PR, looking first, leaves it to the front.
	m.mu.Lock()
	delete(m.gating, "other")
	m.mu.Unlock()
	main, _ := m.C.Effects.Sync(p)
	if wait, err := m.recursiveGate(newer, p, main); !wait || err != nil || !strings.Contains(newer.Reason, fmt.Sprintf("#%d", task.PR.Number)) {
		t.Fatalf("the newer PR: wait %v, err %v, reason %q: want it behind the front", wait, err, newer.Reason)
	}
	m.later = map[string]time.Time{}
	gateOnce(t, m, task, p)
	if task.Stage != StageMerged || len(*jobs) != 1 || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, %d gate runs, reason %q: want the front merged on its keep", task.Stage, len(*jobs), task.Reason)
	}
}

// A merge the Hive makes that would end an older PR's standing merged test
// waits behind it; one that misses its files, a revert, one past
// lineFirstFor, and one the front's own batch tested go on.
func TestLineFirst(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	front := lineMember(t, m, f, p, "t-front", task.PR.Number-1, map[string]string{"feature.txt": "front"})
	apart := lineMember(t, m, f, p, "t-apart", task.PR.Number-2, map[string]string{"apart.txt": "a"})
	for _, u := range []*Task{front, apart} {
		u.MergedOK, u.MergedAt, u.Reason = u.SHA+"+"+main, time.Now(), GateWait+" on it"
	}
	if why := m.lineFirst(task, p, main); !strings.HasPrefix(why, LineFirstWait) || !strings.Contains(why, fmt.Sprintf("#%d", front.PR.Number)) {
		t.Fatalf("a merge touching the front's file: %q", why)
	}
	task.Kind = "revert"
	if why := m.lineFirst(task, p, main); why != "" {
		t.Fatalf("a revert waited: %q", why)
	}
	task.Kind = "work"
	m.batches = map[string][]*mergeBatch{p.Name: {{front: front.ID, head: front.SHA, tip: main, ok: true, heads: map[string]string{task.ID: task.SHA}}}}
	if why := m.lineFirst(task, p, main); why != "" {
		t.Fatalf("a member of the front's own batch waited: %q", why)
	}
	m.batches = nil
	front.MergedAt = time.Now().Add(-lineFirstFor - time.Minute)
	if why := m.lineFirst(task, p, main); why != "" {
		t.Fatalf("a front past lineFirstFor held it: %q", why)
	}
	front.MergedAt = time.Now()
	front.PR.Number = task.PR.Number + 1 // behind it in the line
	if why := m.lineFirst(task, p, main); why != "" {
		t.Fatalf("a newer PR held it: %q", why)
	}
}

// The front of a batch keeps its pass as the PRs its batch tested merge
// first.
func TestBatchFrontFollowsItsTip(t *testing.T) {
	front := &Task{ID: "f", Project: "p", SHA: "head"}
	m := &Module{}
	m.addBatch("p", "m1", &mergeBatch{front: "f", head: "head", label: "#1", tip: "m1", ok: true, heads: map[string]string{"a": "ah"}})
	m.batchMerged(&Task{ID: "a", Project: "p", SHA: "ah"}, "m1", "m2")
	if m.batchPassed(front, "m2") == "" {
		t.Fatal("the front lost its batch's pass to its own member's merge")
	}
	front.SHA = "moved"
	if m.batchPassed(front, "m2") != "" {
		t.Fatal("a new head kept the batch's pass")
	}
}

// A front holds the line only if it can merge on what it has now (issue
// #1825): a gain still in its recursive gate holds back nothing, and a fix
// whose tests passed that touches its files merges without waiting for the
// gate; once the gate kept the gain, it goes first again.
func TestGainInItsGateHoldsNothingBack(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	task.Aim, task.AimWhy = AimFix, "TestX fails"
	main, _ := m.C.Effects.Sync(p)
	gain := lineMember(t, m, f, p, "t-gain", task.PR.Number-1, map[string]string{"feature.go": "package q\n"})
	gain.Aim, gain.MergedOK, gain.MergedAt = AimGain, gain.SHA+"+"+main, time.Now()
	gain.Reason = fmt.Sprintf("%s on %s against main at %s (started %s, at most %s)", GateWait, core.Short(gain.SHA), core.Short(main), time.Now().UTC().Format("15:04Z"), GateTimeout)
	if why := m.lineFirst(task, p, main); why != "" {
		t.Fatalf("a fix waited behind a gain in its gate: %q", why)
	}
	m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Head: gain.SHA, Main: main, Outcome: core.GateKeep, Reason: "better", At: time.Now()})
	if why := m.lineFirst(task, p, main); !strings.HasPrefix(why, LineFirstWait) {
		t.Fatalf("a fix behind a gain the gate kept: %q", why)
	}
	gain.SHA = "0ther" // a new head, not gated yet
	gain.MergedOK = gain.SHA + "+" + main
	mergeOnce(t, m, task, p)
	m.Wait()
	if len(*jobs) != 0 || task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("%d gate runs, stage %q, reason %q: want the fix merged ahead of the gain", len(*jobs), task.Stage, task.Reason)
	}
}
