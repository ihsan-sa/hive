package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// measuredRig is selfRig with a gate whose verdict measured both sides, so
// the judge weighs it.
func measuredRig(t *testing.T, outcome string) (*Module, *Task, core.Project, *core.Local) {
	t.Helper()
	return measuredRigSeeded(t, nil, map[string]string{"feature.go": "package p\n"}, outcome)
}

// measuredRigSeeded is measuredRig with files on main and in the change.
func measuredRigSeeded(t *testing.T, onMain, files map[string]string, outcome string) (*Module, *Task, core.Project, *core.Local) {
	t.Helper()
	m, task, p, f, _ := selfRigSeeded(t, onMain, files, outcome)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.Measured = true
		return g
	}
	return m, task, p, f
}

// judgeSays ends task's judge run on its head with verdict.
func judgeSays(t *testing.T, m *Module, task *Task, main, verdict string) core.Run {
	t.Helper()
	id := "j-" + task.ID + "-" + core.Short(task.SHA) + "-" + core.Short(main) + "-0"
	r, ok := m.C.Runs.Get(id)
	if !ok || !r.ReadOnly || r.Role != core.JudgeRole || r.Inputs["sha"] != task.SHA || r.Inputs["main"] != main {
		t.Fatalf("no read-only judge run %s: %+v", id, r)
	}
	if !strings.Contains(r.Inputs["numbers.md"], "probe table") || !strings.Contains(r.Inputs["diff.patch"], "feature.go") || !strings.Contains(r.Inputs["brief.md"], "The numbers alone say: ") {
		t.Fatalf("the judge doesn't have the case: %+v", r.Inputs)
	}
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone,
		"result": harness.Result{Structured: []byte(`{"verdict":"` + verdict + `","summary":"judge says ` + verdict + `","token":"` + core.JudgeToken(id) + `"}`)}})
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	return r
}

// The gate's numbers are one thing the judge weighs: a measured rollback the
// judge keeps merges, and the merge step accepts the judge's keep.
func TestJudgeKeepsOverTheNumbers(t *testing.T) {
	m, task, p, f := measuredRig(t, core.GateRollback)
	main, _ := m.C.Effects.Sync(p)
	gateOnce(t, m, task, p)
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "judge") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q: want waiting on the judge", task.Stage, task.Reason)
	}
	judgeSays(t, m, task, main, core.GateKeep)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q: want merged on the judge's keep", task.Stage, task.Reason)
	}
	if c := strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"); !strings.Contains(c, "Judge (") || !strings.Contains(c, "judge says keep") {
		t.Fatalf("the PR doesn't carry the judgement:\n%s", c)
	}
	if raw, _ := os.ReadFile(filepath.Join(m.C.WS.Gates(), p.Name, "recursive", "lessons.md")); strings.Contains(string(raw), core.Short(task.SHA)) {
		t.Fatalf("a kept change has a rollback lesson: %s", raw)
	}
}

// A measured keep the judge rolls back goes back to the worker with the
// judge's reasons, its lesson written, and the merge step refuses it.
func TestJudgeRollsBackOverTheNumbers(t *testing.T) {
	m, task, p, f := measuredRig(t, core.GateKeep)
	main, _ := m.C.Effects.Sync(p)
	head := task.SHA
	gateOnce(t, m, task, p)
	judgeSays(t, m, task, main, core.GateRollback)
	mergeOnce(t, m, task, p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: head, Main: main, Subject: "x", Gain: true}); err == nil {
		t.Fatal("the merge step took a change its judge rolled back")
	}
	if task.Stage != StageWorking || !strings.Contains(task.Findings, "judge says rollback") || !strings.Contains(task.Findings, "kept by the numbers, rolled back by its judge") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, findings %q: want sent back with the judge's reasons", task.Stage, task.Findings)
	}
	if raw, _ := os.ReadFile(filepath.Join(m.C.WS.Gates(), p.Name, "recursive", "lessons.md")); !strings.Contains(string(raw), core.Short(head)+" was kept by the gate's numbers and rolled back by its judge") || strings.Contains(string(raw), "judge says") {
		t.Fatalf("lesson: %q", raw)
	}
}

// A candidate that failed before it was measured is rolled back as the gate
// said, with no judge; and a judge with no usable answer leaves the numbers
// to decide.
func TestJudgeWeighsOnlyMeasuredVerdicts(t *testing.T) {
	m, task, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateRollback)
	gateOnce(t, m, task, p)
	if task.Stage != StageWorking {
		t.Fatalf("stage %q: an unmeasured rollback goes straight back", task.Stage)
	}
	if n := len(m.C.Runs.List(func(r *core.Run) bool { return r.Role == core.JudgeRole })); n != 0 {
		t.Fatalf("%d judge runs for an unmeasured verdict", n)
	}

	m, task, p, f := measuredRig(t, core.GateKeep)
	main, _ := m.C.Effects.Sync(p)
	gateOnce(t, m, task, p)
	for i := 0; i < JudgeTries; i++ {
		id := "j-" + task.ID + "-" + core.Short(task.SHA) + "-" + core.Short(main) + "-" + string(rune('0'+i))
		if _, ok := m.C.Runs.Get(id); !ok {
			t.Fatalf("try %d: no judge run %s", i, id)
		}
		m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone, "result": harness.Result{Structured: []byte(`{"verdict":"keep","summary":"no token"}`)}})
		m.mu.Lock()
		delete(m.later, task.ID)
		m.mu.Unlock()
		mergeOnce(t, m, task, p)
		if task.Stage == StageMerging {
			mergeOnce(t, m, task, p) // the next try starts
		}
	}
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q: want the numbers' keep once the judge gave nothing usable", task.Stage, task.Reason)
	}
}

// A measured rollback whose judge hangs, then gives nothing usable, stands
// as the numbers said once JudgeTries runs are spent: the hung run is
// stopped and counts as a try, and the gate's lesson is written, since the
// gate left it to the judge.
func TestJudgeWithNoAnswerLeavesTheLesson(t *testing.T) {
	m, task, p, _ := measuredRig(t, core.GateRollback)
	main, _ := m.C.Effects.Sync(p)
	head := task.SHA
	gateOnce(t, m, task, p)
	lessons := filepath.Join(m.C.WS.Gates(), p.Name, "recursive", "lessons.md")
	for i := 0; i < JudgeTries; i++ {
		id := "j-" + task.ID + "-" + core.Short(task.SHA) + "-" + core.Short(main) + "-" + string(rune('0'+i))
		if _, ok := m.C.Runs.Get(id); !ok {
			t.Fatalf("try %d: no judge run %s", i, id)
		}
		if raw, _ := os.ReadFile(lessons); strings.Contains(string(raw), core.Short(head)) {
			t.Fatalf("try %d: a lesson before the numbers decided: %s", i, raw)
		}
		if i == 0 {
			old := JudgeWait
			JudgeWait = 0 // the first run hangs past its time
			mergeOnce(t, m, task, p)
			JudgeWait = old
			if r, _ := m.C.Runs.Get(id); !r.Terminal() {
				t.Fatalf("a judge run past JudgeWait wasn't stopped: %s", r.Status)
			}
		} else {
			m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone, "result": harness.Result{Structured: []byte(`{"verdict":"keep","summary":"no token"}`)}})
			m.mu.Lock()
			delete(m.later, task.ID)
			m.mu.Unlock()
			mergeOnce(t, m, task, p)
		}
		m.mu.Lock()
		delete(m.later, task.ID)
		m.mu.Unlock()
		if task.Stage == StageMerging {
			mergeOnce(t, m, task, p) // the next try starts
		}
	}
	if task.Stage != StageWorking {
		t.Fatalf("stage %q, reason %q: want the numbers' rollback once the judge gave nothing usable", task.Stage, task.Reason)
	}
	if raw, _ := os.ReadFile(lessons); !strings.Contains(string(raw), "The gate rolled back "+core.Short(head)) || strings.Count(string(raw), core.Short(head)) != 1 {
		t.Fatalf("lesson: %q", raw)
	}
}

// A measured rollback of a change to security-listed files stands as the
// numbers said: no judge, whose read of the diff an injection could reach,
// overrides it.
func TestJudgeDoesNotOverrideASecurityRollback(t *testing.T) {
	m, task, p, _ := measuredRigSeeded(t, securityList, map[string]string{"secure/gate.go": "package secure\n"}, core.GateRollback)
	mergeOnce(t, m, task, p)
	seatSays(t, m, task, core.SeatSecurity, "land")
	mergeOnce(t, m, task, p) // records the seat's land
	gateOnce(t, m, task, p)
	if n := len(m.C.Runs.List(func(r *core.Run) bool { return r.Role == core.JudgeRole })); n != 0 || task.Stage != StageWorking {
		t.Fatalf("%d judge runs, stage %q (%s): a security-listed rollback goes straight back", n, task.Stage, task.Reason)
	}
	if raw, _ := os.ReadFile(filepath.Join(m.C.WS.Gates(), p.Name, "recursive", "lessons.md")); !strings.Contains(string(raw), "The gate rolled back "+core.Short(task.SHA)) {
		t.Fatalf("lesson: %q", raw)
	}
}

// The judge sits on a model other than the change's author's where the
// config has one: its route, then the reviewer's, then a tribunal seat's.
func TestJudgePickAvoidsTheAuthor(t *testing.T) {
	cfg := core.Config{Harness: "claude", Model: "opus"}
	if p, off := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); off || p.Model != "opus" {
		t.Fatalf("only the author's model: %v %v", p, off)
	}
	cfg.TribunalModels = []core.TribunalSeat{{Model: "opus"}, {Harness: "codex", Model: "gpt"}}
	if p, off := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); !off || p.Model != "gpt" || p.Harness != "codex" {
		t.Fatalf("a tribunal seat off the author: %v %v", p, off)
	}
	cfg.ReviewerModel = "sonnet"
	if p, _ := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); p.Model != "sonnet" {
		t.Fatalf("the reviewer's model first: %v", p)
	}
	if p, _ := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "sonnet"}); p.Model != "opus" {
		t.Fatalf("a reviewer on the author's model is passed over: %v", p)
	}
	cfg.Routes = map[string][]string{core.JudgeRole: {"claude:haiku"}}
	if p, _ := cfg.JudgePick("x", core.Pick{Harness: "claude", Model: "opus"}); p.Model != "haiku" {
		t.Fatalf("the judge route first: %v", p)
	}
}

// A measured verdict judged at an older main carries to a main that moved
// elsewhere: no new gate runs, and the judge's call there decides, a keep
// merging at the new main and a rollback sending the task back.
func TestACarriedVerdictIsJudgedAtItsOwnMain(t *testing.T) {
	for _, verdict := range []string{core.GateKeep, core.GateRollback} {
		m, task, p, f := measuredRig(t, core.GateKeep)
		gates := 0
		inner := m.Gate
		m.Gate = func(ctx context.Context, j GateJob) core.GateResult { gates++; return inner(ctx, j) }
		old, _ := m.C.Effects.Sync(p)
		gateOnce(t, m, task, p)
		pushMain(t, f, map[string]string{"elsewhere.md": "main moved\n"})
		judgeSays(t, m, task, old, verdict)
		mergeOnce(t, m, task, p)
		m.Wait()
		if gates != 1 {
			t.Fatalf("%s: %d gate runs, want the one at %s carried", verdict, gates, core.Short(old))
		}
		main, _ := m.C.Effects.Sync(p)
		if main == old {
			t.Fatal("main didn't move")
		}
		if verdict == core.GateKeep && (task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED") {
			t.Fatalf("stage %q, reason %q: want the carried keep merged at %s", task.Stage, task.Reason, core.Short(main))
		}
		if verdict == core.GateRollback && (task.Stage != StageWorking || !strings.Contains(task.Findings, "judge says rollback") || prState(t, f, task.PR.Number) != "OPEN") {
			t.Fatalf("stage %q, findings %q: want the carried rollback sent back", task.Stage, task.Findings)
		}
	}
}
