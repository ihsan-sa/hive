package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// selfRig is a self project's task at the merge, its tests, read and forge
// checks passed, with a gate that answers outcome and records its jobs.
func selfRig(t *testing.T, files map[string]string, outcome string) (*Module, *Task, core.Project, *core.Local, *[]GateJob) {
	t.Helper()
	m, task, p, f := gateRig(t, false, files)
	p.Self = true
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass})
	var mu sync.Mutex
	jobs := &[]GateJob{}
	m.Gate = func(_ context.Context, j GateJob) core.GateResult {
		mu.Lock()
		*jobs = append(*jobs, j)
		mu.Unlock()
		os.MkdirAll(j.Out, 0o755)
		report := filepath.Join(j.Out, "report.md")
		os.WriteFile(report, []byte("# Gate gate-1: "+outcome+"\n\nprobe table\n"), 0o644)
		return core.GateResult{Outcome: outcome, Reason: "said " + outcome, Report: report, Sandbox: "bwrap"}
	}
	return m, task, p, f, jobs
}

// gateOnce looks at the merge, lets a gate it started finish, and looks again.
func gateOnce(t *testing.T, m *Module, task *Task, p core.Project) {
	t.Helper()
	mergeOnce(t, m, task, p)
	m.Wait()
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
}

// A self project's change that passed everything waits for the recursive
// gate on its exact head against main, and merges once the gate keeps it,
// with the report on the PR.
func TestSelfGateKeepMerges(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	main, _ := m.C.Effects.Sync(p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main, Subject: "x"}); err == nil || !strings.Contains(err.Error(), "recursive gate") {
		t.Fatalf("a self merge with no gate verdict went through: %v", err)
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.HasPrefix(task.Reason, GateWait) {
		t.Fatalf("stage %q, reason %q: want waiting for the gate", task.Stage, task.Reason)
	}
	m.Wait()
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q: want merged after a keep", task.Stage, task.Reason)
	}
	if len(*jobs) != 1 {
		t.Fatalf("%d gate runs, want 1", len(*jobs))
	}
	j := (*jobs)[0]
	if j.Head != task.SHA || j.Main != main || j.AllowTie || j.Repo != m.C.WS.Project(p.Name) {
		t.Fatalf("job %+v: want head %s against main %s, no tie", j, task.SHA, main)
	}
	if task.Gate == nil || task.Gate.Outcome != core.GateKeep {
		t.Fatalf("the task doesn't show the verdict: %+v", task.Gate)
	}
	if c := strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"); !strings.Contains(c, "Recursive gate: KEPT") || !strings.Contains(c, "probe table") {
		t.Fatalf("the PR doesn't carry the gate's report:\n%s", c)
	}
}

// A rollback goes back to the worker with the report and the lesson to
// record, and the PR stays open.
func TestSelfGateRollbackSendsBack(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateRollback)
	gateOnce(t, m, task, p)
	if task.Stage != StageWorking || !strings.Contains(task.Findings, gateRolledBack) || !strings.Contains(task.Findings, "said rollback") {
		t.Fatalf("stage %q, findings %q: want sent back", task.Stage, task.Findings)
	}
	if prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatal("a rolled-back change merged")
	}
	if raw, err := os.ReadFile(filepath.Join(m.C.WS.RunRO(task.Worker), gateReportFile)); err != nil || !strings.Contains(string(raw), "probe table") {
		t.Fatalf("the worker has no report: %v", err)
	}
	if c := strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"); !strings.Contains(c, "Recursive gate: ROLLED BACK") {
		t.Fatalf("the PR doesn't say it was rolled back:\n%s", c)
	}
}

// A gate that can't run leaves the change waiting, never merged, raises it
// once, and tries again only after gateRetry.
func TestSelfGateCantRunWaits(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	gateOnce(t, m, task, p)
	for i := 0; i < 3; i++ {
		m.mu.Lock()
		delete(m.later, task.ID)
		m.mu.Unlock()
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "can't run") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q, PR %s: want waiting unmerged", task.Stage, task.Reason, prState(t, f, task.PR.Number))
	}
	if len(*jobs) != 1 || alerts(m, "gate") != 1 {
		t.Fatalf("%d runs and %d alerts, want 1 and 1", len(*jobs), alerts(m, "gate"))
	}
	old := gateRetry
	gateRetry = 0
	t.Cleanup(func() { gateRetry = old })
	mergeOnce(t, m, task, p)
	m.Wait()
	if len(*jobs) != 2 {
		t.Fatalf("%d runs, want a second try after the retry gap", len(*jobs))
	}
}

// A gate that couldn't run for a cause that may pass (load, a test failing
// on both sides) tries again after gateRetrySoon, not the hour.
func TestSelfGateTransientRetriesSooner(t *testing.T) {
	m, task, p, _, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.Transient = true
		return g
	}
	old := gateRetrySoon
	gateRetrySoon = time.Hour
	t.Cleanup(func() { gateRetrySoon = old })
	gateOnce(t, m, task, p)
	if !strings.Contains(task.Reason, "tries again after") {
		t.Fatalf("reason %q", task.Reason)
	}
	gateRetrySoon = 0 // gateRetry stays an hour
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	m.Wait()
	if len(*jobs) != 2 {
		t.Fatalf("%d runs, want a second try once the short gap passed", len(*jobs))
	}
}

// While a gate couldn't run against a main, another change against the same
// main waits for its retry instead of starting a run that would fail alike.
func TestSelfGateCantRunHoldsOthersAtTheSameMain(t *testing.T) {
	m, task, p, _, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	main, _ := m.C.Effects.Sync(p)
	m.mu.Lock()
	m.gateCantRun = map[string]core.GateResult{p.Name + "@" + main: {Outcome: core.GateCantRun, Head: "0ther", Main: main, Reason: "no bubblewrap", At: time.Now()}}
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	m.Wait()
	if len(*jobs) != 0 || !strings.Contains(task.Reason, "couldn't run against main") {
		t.Fatalf("%d runs, reason %q: want held behind the other change's can't-run", len(*jobs), task.Reason)
	}
	m.mu.Lock()
	m.gateCantRun[p.Name+"@"+main] = core.GateResult{Outcome: core.GateCantRun, Head: "0ther", Main: main, At: time.Now().Add(-2 * gateRetry)}
	delete(m.later, task.ID)
	m.mu.Unlock()
	gateOnce(t, m, task, p)
	if len(*jobs) != 1 || task.Stage != StageMerged {
		t.Fatalf("%d runs, stage %q: want a run once the retry passed", len(*jobs), task.Stage)
	}
}

// With no gate runner a self project's change can't be judged and waits.
func TestSelfGateWithoutRunnerWaits(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	m.Gate = nil
	gateOnce(t, m, task, p)
	if task.Stage != StageMerging || prState(t, f, task.PR.Number) != "OPEN" || task.Gate == nil || task.Gate.Outcome != core.GateCantRun {
		t.Fatalf("stage %q, gate %+v: want waiting as can't run", task.Stage, task.Gate)
	}
}

// Docs and prompts run with ties allowed; one gate runs at a time; a
// project that isn't the Hive's own never waits for one.
func TestSelfGateTiesAndTurns(t *testing.T) {
	m, task, p, _, jobs := selfRig(t, map[string]string{"docs/note.md": "x\n", "modules/agents/worker.md": "y\n"}, core.GateKeep)
	m.mu.Lock()
	m.gating = &gateRun{key: "other", task: "t9"}
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	if !strings.Contains(task.Reason, "one change at a time") || len(*jobs) != 0 {
		t.Fatalf("reason %q, %d runs: want waiting its turn", task.Reason, len(*jobs))
	}
	m.mu.Lock()
	m.gating = nil
	delete(m.later, task.ID)
	m.mu.Unlock()
	gateOnce(t, m, task, p)
	if len(*jobs) != 1 || !(*jobs)[0].AllowTie || task.Stage != StageMerged {
		t.Fatalf("%d runs (%+v), stage %q: want one tie-allowed run and a merge", len(*jobs), *jobs, task.Stage)
	}

	m2, task2, p2, f2 := gateRig(t, false, map[string]string{"feature.txt": "y"})
	m2.Gate = func(context.Context, GateJob) core.GateResult {
		t.Fatal("gated a project that isn't the Hive's")
		return core.GateResult{}
	}
	toMerging(t, m2, task2, p2)
	f2.SetChecks(p2.Repo, task2.SHA, core.CheckRun{Name: "test", State: core.CheckPass})
	mergeOnce(t, m2, task2, p2)
	if task2.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q", task2.Stage, task2.Reason)
	}
}

// A rollback's lesson the gate wrote on this machine reaches the next brief
// until main's lessons name the same commit.
func TestGateLessonsReachTheBrief(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	dir := filepath.Join(m.C.WS.Gates(), p.Name, "recursive")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "lessons.md"), []byte("# Lessons\n\n- The gate rolled back abc1234 (run gate-1): not better\n"), 0o644)
	main, _ := m.C.Effects.Sync(p)
	if b := m.renderBrief(task, p, main); !strings.Contains(b, "> - The gate rolled back abc1234") {
		t.Fatalf("the brief misses the gate's lesson:\n%s", b)
	}
	pushMain(t, f, map[string]string{".hive/lessons.md": "# Lessons\n\n- 2026-10-24: the recursive gate rolled back abc1234 (work): not better\n"})
	main, _ = m.C.Effects.Sync(p)
	if b := m.renderBrief(task, p, main); strings.Count(b, "rolled back abc1234") != 1 {
		t.Fatalf("want the lesson once, from main:\n%s", b)
	}
}
