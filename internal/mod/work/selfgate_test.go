package work

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	return selfRigSeeded(t, nil, files, outcome)
}

// selfRigSeeded is selfRig with more files on main to start with.
func selfRigSeeded(t *testing.T, onMain, files map[string]string, outcome string) (*Module, *Task, core.Project, *core.Local, *[]GateJob) {
	t.Helper()
	m, task, p, f := gateRigSeeded(t, false, onMain, files)
	p.Self = true
	task.Aim = AimGain
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
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main, Subject: "x", Gain: true}); err == nil || !strings.Contains(err.Error(), "recursive gate") {
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

// A head that lacks main goes to the gate as it is, against main: the gate
// merges main in its own clone, so the branch isn't pushed and tested again
// on every move of main while it waits for a gate.
func TestSelfGateJudgesTheHeadWithMainIn(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	pushMain(t, f, map[string]string{"other.go": "package p\n"})
	main := strings.TrimSpace(must(t, f.RepoPath("me/p"), "rev-parse", "main"))
	old := task.SHA
	mergeOnce(t, m, task, p)
	m.Wait()
	if task.SHA != old {
		t.Fatalf("the branch moved to %s before the gate: want its head judged with main merged in by the gate", core.Short(task.SHA))
	}
	if len(*jobs) != 1 || (*jobs)[0].Head != old || (*jobs)[0].Main != main {
		t.Fatalf("gate jobs %+v: want one on %s against main at %s", *jobs, core.Short(old), core.Short(main))
	}
}

// A rollback goes back to the worker with the report and a fresh
// main.bundle, and the PR stays open; it asks for no line in lessons.md.
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
	if msg := queued(m, task.Worker); !strings.Contains(msg, "main.bundle holds main at") {
		t.Fatalf("the rollback went back without main.bundle:\n%s", msg)
	}
	if strings.Contains(queued(m, task.Worker)+task.Findings, "lessons.md") {
		t.Fatal("a rollback asks its worker for a line in lessons.md")
	}
	if c := strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"); !strings.Contains(c, "Recursive gate: ROLLED BACK") {
		t.Fatalf("the PR doesn't say it was rolled back:\n%s", c)
	}
}

// A head the gate can't merge with main goes back to its worker to merge
// main: not a rollback, no alert, and it holds no other change.
func TestSelfGateConflictSendsBackToMerge(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.Reason, g.Conflicts = "the candidate conflicts with the base in NOTES.md", []string{"NOTES.md"}
		return g
	}
	gateOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Conflicts != 1 || strings.Contains(task.Findings, gateRolledBack) {
		t.Fatalf("stage %q, conflicts %d, findings %q: want sent back to merge main", task.Stage, task.Conflicts, task.Findings)
	}
	if _, err := os.Stat(filepath.Join(m.C.WS.RunRO(task.Worker), "main.bundle")); err != nil {
		t.Fatalf("the worker has no main to merge: %v", err)
	}
	if msg := queued(m, task.Worker); !strings.Contains(msg, "recursive gate") || !strings.Contains(msg, "- NOTES.md") {
		t.Fatalf("the worker isn't told where the gate's merge conflicts: %q", msg)
	}
	if prState(t, f, task.PR.Number) != "OPEN" || alerts(m, "gate") != 0 {
		t.Fatalf("PR %s, %d alerts: want open and no can't-run alert", prState(t, f, task.PR.Number), alerts(m, "gate"))
	}
	main, _ := m.C.Effects.Sync(p)
	if g, ok := m.C.Effects.GateAtMain(p.Name, main); ok {
		t.Fatalf("a conflict holds other changes: %+v", g)
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
	m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Outcome: core.GateCantRun, Head: "0ther", Main: main, Reason: "no bubblewrap", At: time.Now()})
	mergeOnce(t, m, task, p)
	m.Wait()
	if len(*jobs) != 0 || !strings.Contains(task.Reason, "couldn't run against main") {
		t.Fatalf("%d runs, reason %q: want held behind the other change's can't-run", len(*jobs), task.Reason)
	}
	m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Outcome: core.GateCantRun, Head: "0ther", Main: main, At: time.Now().Add(-2 * gateRetry)})
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	gateOnce(t, m, task, p)
	if len(*jobs) != 1 || task.Stage != StageMerged {
		t.Fatalf("%d runs, stage %q: want a run once the retry passed", len(*jobs), task.Stage)
	}
}

// A can't-run whose cause is that change alone (its own build, its clock)
// holds no other change at the same main.
func TestSelfGateCantRunAloneHoldsNobody(t *testing.T) {
	m, task, p, _, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.Alone = true
		return g
	}
	gateOnce(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	_, held := m.C.Effects.GateAtMain(p.Name, main)
	if len(*jobs) != 1 || held {
		t.Fatalf("%d runs, held %v: want the run recorded without holding others", len(*jobs), held)
	}
}

// A gate that can't judge because main itself fails in it is remembered: no
// other change at that main runs it, and a change to the gate's own code
// gets the person one Waiting item to land it. "merge" lands that exact
// head; a gate that judges again closes the item.
func TestSelfGateMainFailsAsksToLandTheGateFix(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"internal/gate/unit.go": "package gate\n"}, core.GateCantRun)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.MainFails = true
		return g
	}
	gateOnce(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	if g, ok := m.C.Effects.GateAtMain(p.Name, main); !ok || !g.MainFails || retryAfter(g).Sub(g.At) != gateRetry {
		t.Fatalf("remembered %+v %v: want main's failure kept for gateRetry the first time", g, ok)
	}
	var asks []core.Ask
	for i := 0; i < 3; i++ {
		m.mu.Lock()
		delete(m.later, task.ID)
		m.mu.Unlock()
		mergeOnce(t, m, task, p)
		asks = nil
		for _, a := range m.C.Person.Open() {
			if a.Kind == "gate" {
				asks = append(asks, a)
			}
		}
	}
	m.Wait()
	if len(*jobs) != 1 || len(asks) != 1 || !strings.Contains(asks[0].Ref, task.SHA) || task.Stage != StageMerging {
		t.Fatalf("%d runs, asks %+v, stage %q: want one run and one Waiting item naming the head", len(*jobs), asks, task.Stage)
	}
	if _, err := m.C.Person.Answer(asks[0].ID, "merge", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000 && !m.C.Effects.Approved("gate", p.Name, task.SHA+"@"+main); i++ {
		time.Sleep(10 * time.Millisecond) // the handler runs on its own; a loaded machine can take seconds
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" || len(*jobs) != 1 {
		t.Fatalf("stage %q, %d runs: want merged at the head the person landed, with no other run", task.Stage, len(*jobs))
	}
}

// Main failing in the gate again at the same main holds longer each time,
// up to gateRetryMain.
func TestSelfGateMainFailsHoldGrows(t *testing.T) {
	m, _, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	main, _ := m.C.Effects.Sync(p)
	var holds []time.Duration
	for i := 0; i < 5; i++ {
		m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Head: "h", Main: main, Outcome: core.GateCantRun, MainFails: true, At: time.Now()})
		g, _ := m.C.Effects.GateAtMain(p.Name, main)
		holds = append(holds, retryAfter(g).Sub(g.At))
	}
	want := []time.Duration{gateRetry, 2 * gateRetry, 4 * gateRetry, gateRetryMain, gateRetryMain}
	if !slices.Equal(holds, want) {
		t.Fatalf("holds %v, want %v", holds, want)
	}
	// A run that started before the last failure was recorded ran beside
	// it, at the same main: it adds no repeat.
	g, _ := m.C.Effects.GateAtMain(p.Name, main)
	m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Head: "h2", Main: main, Outcome: core.GateCantRun, MainFails: true, Started: g.At.Add(-time.Minute), At: g.At.Add(time.Minute)})
	if g2, _ := m.C.Effects.GateAtMain(p.Name, main); g2.Repeats != g.Repeats {
		t.Fatalf("repeats %d after a run beside the last, want %d", g2.Repeats, g.Repeats)
	}
}

// "keep waiting" stands: the person isn't asked again about that head at
// that main. An item whose PR the lead closed leaves Waiting.
func TestSelfGateFixAskOnceAndClosedWithItsPR(t *testing.T) {
	m, task, p, _, _ := selfRig(t, map[string]string{"internal/gate/unit.go": "package gate\n"}, core.GateCantRun)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.MainFails = true
		return g
	}
	gateOnce(t, m, task, p)
	gates := func() (open int) {
		for _, a := range m.C.Person.Open() {
			if a.Kind == "gate" {
				open++
			}
		}
		return open
	}
	var id string
	for _, a := range m.C.Person.Open() {
		if a.Kind == "gate" {
			id = a.ID
		}
	}
	if id == "" {
		t.Fatal("no gate item")
	}
	if _, err := m.C.Person.Answer(id, "keep waiting", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		m.mu.Lock()
		delete(m.later, task.ID)
		m.mu.Unlock()
		*task, _ = m.Get(task.ID)
		mergeOnce(t, m, task, p)
	}
	if n := gates(); n != 0 {
		t.Fatalf("%d gate items open after the person said keep waiting: want none", n)
	}
	// A new head or main is a new question (its ref differs; cleared here).
	*task, _ = m.Get(task.ID)
	task.GateAsked = ""
	m.save(task, "")
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	if n := gates(); n != 1 {
		t.Fatalf("%d gate items: want one again", n)
	}
	if _, err := m.ClosePR(task.ID, "lead", "superseded", false); err != nil {
		t.Fatal(err)
	}
	if n := gates(); n != 0 {
		t.Fatalf("%d gate items open after the lead closed the PR: want none", n)
	}
}

// A change that isn't to the gate gets no Waiting item when main fails in
// the gate; it waits, and the item an earlier one raised closes once the
// gate judges again.
func TestSelfGateMainFailsAsksNothingOfOtherChanges(t *testing.T) {
	m, task, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.MainFails = true
		return g
	}
	gateOnce(t, m, task, p)
	for _, a := range m.C.Person.Open() {
		if a.Kind == "gate" {
			t.Fatalf("asked %+v for a change not to the gate", a)
		}
	}
	main, _ := m.C.Effects.Sync(p)
	if task.GateAsked != "gate-fix:"+task.ID+"@"+task.SHA+"@"+main {
		t.Fatalf("looked at %q: want the head and main remembered, so it isn't diffed on every look", task.GateAsked)
	}
	// A late verdict on another main leaves an item at this main open.
	at := m.C.Person.Ask(core.Ask{Kind: "gate", Ref: "gate-fix:t1@abc@" + main, Project: p.Name, Options: []string{"merge", "keep waiting"}})
	m.tellGate(task, p, core.GateResult{Project: p.Name, Head: task.SHA, Main: "0123456789abcdef0123456789abcdef01234567", Outcome: core.GateRollback, Reason: "worse", At: time.Now()})
	if a, _ := m.C.Person.Get(at.ID); a.Answer != "" {
		t.Fatalf("item %+v: want open, the verdict was at another main", a)
	}
	old := m.C.Person.Ask(core.Ask{Kind: "gate", Ref: "gate-fix:t0@abc", Project: p.Name, Options: []string{"merge", "keep waiting"}})
	m.tellGate(task, p, core.GateResult{Project: p.Name, Head: task.SHA, Main: main, Outcome: core.GateRollback, Reason: "worse", At: time.Now()})
	for _, id := range []string{old.ID, at.ID} {
		if a, _ := m.C.Person.Get(id); a.Answer != "keep waiting" {
			t.Fatalf("item %+v: want closed once the gate judges again", a)
		}
	}
}

// mainFailsGateFix is a change to the gate's code waiting with its Waiting
// item, main failing in the gate.
func mainFailsGateFix(t *testing.T) (*Module, *Task, core.Project, *core.Local, core.Ask) {
	t.Helper()
	m, task, p, f, _ := selfRig(t, map[string]string{"internal/gate/unit.go": "package gate\n"}, core.GateCantRun)
	inner := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		g := inner(ctx, j)
		g.MainFails = true
		return g
	}
	gateOnce(t, m, task, p)
	for _, a := range m.C.Person.Open() {
		if a.Kind == "gate" {
			return m, task, p, f, a
		}
	}
	t.Fatal("no gate item")
	return nil, nil, p, nil, core.Ask{}
}

// A PR merged on the forge by hand closes its gate item.
func TestSelfGateFixItemClosesWhenMergedOnForge(t *testing.T) {
	m, task, p, f, item := mainFailsGateFix(t)
	if _, err := f.MergePR(p.Repo, task.PR.Number, task.SHA, "work"); err != nil {
		t.Fatal(err)
	}
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if a, _ := m.C.Person.Get(item.ID); task.Stage != StageMerged || a.Answer != "keep waiting" {
		t.Fatalf("stage %q, item %+v: want merged and the item closed", task.Stage, a)
	}
}

// Main moving is a new question: the item asked at the old main closes,
// one at the new main opens, and a "merge" on the old one lands nothing
// and says so.
func TestSelfGateFixItemFollowsMain(t *testing.T) {
	m, task, p, _, item := mainFailsGateFix(t)
	old, _ := m.C.Effects.Sync(p)
	moveMain(t, m, "other.txt", "x")
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	gateOnce(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	if a, _ := m.C.Person.Get(item.ID); main == old || a.Answer != "keep waiting" || !strings.Contains(a.Note, "main moved on") {
		t.Fatalf("old item %+v: want closed once main moved", a)
	}
	var now []core.Ask
	for _, a := range m.C.Person.Open() {
		if a.Kind == "gate" {
			now = append(now, a)
		}
	}
	if len(now) != 1 || !strings.HasSuffix(now[0].Ref, "@"+main) || !strings.Contains(now[0].Detail, "internal/gate/unit.go") {
		t.Fatalf("items %+v: want one at the new main naming the gate's files", now)
	}
	// The old item answered "merge" all the same (its handler, run as
	// late as it can be): no approval, and the person hears it.
	late := item
	late.Answer = "merge"
	m.onPersonGate(late)
	if m.C.Effects.Approved("gate", p.Name, task.SHA+"@"+old) || m.C.Effects.Approved("gate", p.Name, task.SHA+"@"+main) {
		t.Fatal("a late answer approved the head")
	}
	if told := m.C.Person.Told(5); len(told) == 0 || !strings.Contains(told[0].Title, "landed nothing") || !strings.Contains(told[0].Detail, "main moved on") {
		t.Fatalf("told %+v: want the person told their answer did nothing", told)
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

// maxGates sets the person's ceiling on gates at once.
func maxGates(m *Module, n int) {
	cfg := m.C.WS.LoadConfig()
	cfg.MaxGates = n
	m.C.WS.SaveConfig(cfg)
}

// An improvement must show a gain: even docs and prompts run with no ties
// allowed; past the gates' limit a change waits its turn; a project that
// isn't the Hive's own never waits for one.
func TestSelfGateNoTiesAndTurns(t *testing.T) {
	m, task, p, _, jobs := selfRig(t, map[string]string{"docs/note.md": "x\n", "modules/agents/worker.md": "y\n"}, core.GateKeep)
	maxGates(m, 1)
	m.mu.Lock()
	m.gating = map[string]*gateRun{"other": {key: "other", task: "t9"}}
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	if !strings.Contains(task.Reason, "every one is in use") || len(*jobs) != 0 {
		t.Fatalf("reason %q, %d runs: want waiting its turn", task.Reason, len(*jobs))
	}
	m.mu.Lock()
	m.gating = nil
	delete(m.later, task.ID)
	m.mu.Unlock()
	gateOnce(t, m, task, p)
	if len(*jobs) != 1 || (*jobs)[0].AllowTie || task.Stage != StageMerged {
		t.Fatalf("%d runs (%+v), stage %q: want one run with no ties and a merge", len(*jobs), *jobs, task.Stage)
	}

	m2, task2, p2, f2 := gateRig(t, false, map[string]string{"feature.txt": "y"})
	task2.Aim = AimGain
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

// A clear fix to the Hive itself merges on its tests, reads and checks,
// with no gate run.
func TestSelfFixMergesWithoutTheGate(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateRollback)
	task.Aim, task.AimWhy = AimFix, "TestMerge fails: the merge never lands"
	mergeOnce(t, m, task, p)
	m.Wait()
	if len(*jobs) != 0 {
		t.Fatalf("%d gate runs on a fix, want none", len(*jobs))
	}
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q: want a fix merged", task.Stage, task.Reason)
	}
}

// A change that says neither fix nor gain is unclear, and is taken as a gain
// (issue #568): it waits for the gate, and the gate's rollback holds it.
func TestUnclearIsTakenAsAGain(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateRollback)
	task.Aim, task.AimWhy = "", ""
	if !task.Gain() || !strings.Contains(task.AimWord(), "nothing says it is a fix") {
		t.Fatalf("an unclear task is %q, want a gain", task.AimWord())
	}
	gateOnce(t, m, task, p)
	if len(*jobs) != 1 || task.Stage == StageMerged || prState(t, f, task.PR.Number) == "MERGED" {
		t.Fatalf("%d gate runs, stage %q: want an unclear change gated and not merged", len(*jobs), task.Stage)
	}
	rv := Task{Kind: "revert"}
	if rv.Gain() {
		t.Fatal("a revert waits for the gate")
	}
}

// The lead turns an improvement waiting for the gate into a fix, and it
// merges; a worker sets only its own task's aim; an aim is fix or gain.
func TestAimOp(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	maxGates(m, 1)
	m.mu.Lock()
	m.gating = map[string]*gateRun{"other": {key: "other", task: "t9"}}
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	if !strings.HasPrefix(task.Reason, GateWait) {
		t.Fatalf("reason %q: want waiting for the gate", task.Reason)
	}
	lead := core.Run{ID: "lead-1", Role: "lead"}
	other := core.Run{ID: "w-other", Role: "worker"}
	if _, err := m.opAim(other, []byte(`{"task":"`+task.ID+`","aim":"fix","why":"x"}`)); err == nil {
		t.Fatal("a worker set another task's aim")
	}
	if _, err := m.opAim(lead, []byte(`{"task":"`+task.ID+`","aim":"huge","why":"x"}`)); err == nil {
		t.Fatal("took an aim that is neither fix nor gain")
	}
	if _, err := m.opAim(lead, []byte(`{"task":"`+task.ID+`","aim":"fix"}`)); err == nil {
		t.Fatal("set an aim with no why")
	}
	if _, err := m.opAim(lead, []byte(`{"task":"`+task.ID+`","aim":"fix","why":"a plain fix"}`)); err != nil {
		t.Fatal(err)
	}
	*task, _ = m.Get(task.ID)
	if task.Gain() || !strings.Contains(task.Log[len(task.Log)-1].Text, "a plain fix") || task.AimWord() != "a fix: a plain fix" {
		t.Fatalf("aim %q, log %+v", task.Aim, task.Log[len(task.Log)-1])
	}
	mergeOnce(t, m, task, p)
	if len(*jobs) != 0 || task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("%d runs, stage %q, reason %q: want the fix merged without the gate", len(*jobs), task.Stage, task.Reason)
	}
	own := core.Run{ID: task.Worker, Role: "worker"}
	if _, err := m.opAim(own, []byte(`{"aim":"gain","why":"x"}`)); err == nil {
		t.Fatal("set the aim of a merged task")
	}
}

// More than one gate runs at once while the machine has room for another:
// with one in flight, a second change starts its own run, and it waits while
// the machine is strained.
func TestSelfGatesRunSideBySide(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	maxGates(m, 2)
	strain := "machine low on memory (900 MB free, floor 4096 MB)"
	defer func(g func(core.Config) (string, bool)) { gateMachine = g }(gateMachine)
	gateMachine = func(core.Config) (string, bool) { return strain, true }
	release := make(chan struct{})
	gate := m.Gate
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult { <-release; return gate(ctx, j) }
	m.mu.Lock()
	m.gating = map[string]*gateRun{"other": {key: "other", task: "t9"}}
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	if !strings.Contains(task.Reason, "no room for another") || len(*jobs) != 0 {
		t.Fatalf("reason %q, %d runs: want waiting while the machine is strained", task.Reason, len(*jobs))
	}
	strain = ""
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	m.mu.Lock()
	n := len(m.gating)
	m.mu.Unlock()
	if n != 2 || !strings.Contains(task.Reason, "started") {
		t.Fatalf("%d runs in flight, reason %q: want a second run beside the first", n, task.Reason)
	}
	close(release)
	m.Wait()
	m.mu.Lock()
	delete(m.later, task.ID)
	_, other := m.gating["other"]
	m.mu.Unlock()
	if !other || len(*jobs) != 1 {
		t.Fatalf("other in flight %v, %d runs: want this one done and the other still running", other, len(*jobs))
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q: want merged on its keep", task.Stage, task.Reason)
	}
}

// Turning an improvement into a fix while its gate runs cancels that run, so
// its gate slot frees for the gains behind it; the cancelled run records
// no verdict and holds nobody.
func TestAimFixCancelsItsGate(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	started := make(chan struct{})
	m.Gate = func(ctx context.Context, j GateJob) core.GateResult {
		close(started)
		<-ctx.Done()
		return core.GateResult{Outcome: core.GateCantRun, Reason: "the gate ran out of time (" + ctx.Err().Error() + ")"}
	}
	main, _ := m.C.Effects.Sync(p)
	mergeOnce(t, m, task, p)
	<-started
	lead := core.Run{ID: "lead-1", Role: "lead"}
	if _, err := m.opAim(lead, []byte(`{"task":"`+task.ID+`","aim":"fix","why":"a plain fix"}`)); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the gate run went on after its task became a fix")
	}
	m.mu.Lock()
	gating := len(m.gating)
	m.mu.Unlock()
	_, held := m.C.Effects.GateAtMain(p.Name, main)
	if gating != 0 || held {
		t.Fatalf("%d gating, held %v: want the slot free and nobody held", gating, held)
	}
	if g, ok := m.C.Effects.GateResult(p.Name, task.SHA, main); ok {
		t.Fatalf("a cancelled run recorded a verdict: %+v", g)
	}
	*task, _ = m.Get(task.ID)
	if !strings.Contains(task.Log[len(task.Log)-1].Text, "cancelled") {
		t.Fatalf("the task doesn't say its gate run was cancelled: %+v", task.Log[len(task.Log)-1])
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q: want the fix merged", task.Stage, task.Reason)
	}
}

// An aim asked while a step holds the task isn't refused: it is set as the
// step ends, and a fix cancels the gate at once.
func TestAimWhileAStepRuns(t *testing.T) {
	m, task, _, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	m.mu.Lock()
	m.busy[task.ID] = true
	m.gating = map[string]*gateRun{"g": {key: "g", task: task.ID}}
	m.mu.Unlock()
	lead := core.Run{ID: "lead-1", Role: "lead"}
	done := make(chan error, 1)
	go func() {
		out, err := m.opAim(lead, []byte(`{"task":"`+task.ID+`","aim":"fix","why":"a plain fix"}`))
		if err == nil && !strings.Contains(out.(map[string]string)["note"], "as that step ends") {
			err = fmt.Errorf("no note that it waits: %+v", out)
		}
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	dropped := m.gating["g"].dropped
	m.mu.Unlock()
	if got, _ := m.Get(task.ID); !got.Gain() || !dropped {
		t.Fatalf("aim %q before the step ended, gate dropped %v: want unchanged, and the gate cancelled", got.Aim, dropped)
	}
	m.release(task.ID)
	got, _ := m.Get(task.ID)
	if n := len(got.Log); got.Gain() || got.AimWhy != "a plain fix" || n < 2 || !strings.Contains(got.Log[n-2].Text, "aim set by lead-1") || !strings.Contains(got.Log[n-1].Text, "cancelled") {
		t.Fatalf("aim %q (%q), log %+v after the step: want the fix set and its gate's cancel said", got.Aim, got.AimWhy, got.Log)
	}
}

// A worker whose gain the gate rolled back can't make it a fix on its own
// word, which would let a change move its own bar; the lead can.
func TestWorkerCantUnaimARolledBackGain(t *testing.T) {
	m, task, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateRollback)
	gateOnce(t, m, task, p)
	if task.Stage != StageWorking {
		t.Fatalf("stage %q: want sent back", task.Stage)
	}
	own := core.Run{ID: task.Worker, Role: "worker"}
	if _, err := m.opAim(own, []byte(`{"aim":"fix","why":"it is a fix really"}`)); err == nil || !strings.Contains(err.Error(), "lead") {
		t.Fatalf("a worker turned its rolled-back gain into a fix: %v", err)
	}
	if _, err := m.opAim(core.Run{ID: "lead-1", Role: "lead"}, []byte(`{"task":"`+task.ID+`","aim":"fix","why":"it is a fix"}`)); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(task.ID); got.Gain() {
		t.Fatal("the lead's fix didn't take")
	}
}

// A brief may say it aims at a gain or is a fix naming its failure; one that
// says neither is taken as a gain; its worker's brief says which, and why.
func TestBriefAim(t *testing.T) {
	m, _, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	if _, err := m.File(Brief{Project: p.Name, Title: "x", Aim: "bigger"}); err == nil {
		t.Fatal("filed a brief with an aim that is neither fix nor gain")
	}
	if _, err := m.File(Brief{Project: p.Name, Title: "x", Aim: "fix"}); err == nil {
		t.Fatal("filed a fix that names no failure")
	}
	g, err := m.File(Brief{Project: p.Name, Title: "faster merges", Aim: "gain"})
	if err != nil || !g.Gain() {
		t.Fatalf("aim %q, %v", g.Aim, err)
	}
	fx, err := m.File(Brief{Project: p.Name, Title: "a fix", Aim: "fix", Why: "TestMerge fails"})
	if err != nil || !fx.Fix() || fx.AimWhy != "TestMerge fails" {
		t.Fatalf("aim %q (%q), %v", fx.Aim, fx.AimWhy, err)
	}
	u, _ := m.File(Brief{Project: p.Name, Title: "tidy the merge loop"})
	if !u.Gain() || !strings.Contains(u.Log[len(u.Log)-1].Text, "taken as a gain") {
		t.Fatalf("an unclear brief: aim %q, log %+v", u.Aim, u.Log)
	}
	// A brief that says no aim but names something broken is a fix.
	for _, b := range []Brief{
		{Title: "The pace pulls back on check timeouts, flakes and slow turns"},
		{Title: "A review follow-up whose worker makes no change ends done, not blocked"},
		{Title: "forge-failures.md names main as the base", Known: "It names today's main. The base it names is wrong when CI tested an older one."},
		{Title: "aim can be set while a step runs", Known: "hive agent aim was refused three times."},
	} {
		b.Project = p.Name
		fb, err := m.File(b)
		if err != nil || !fb.Fix() || !strings.HasPrefix(fb.AimWhy, "the brief names something broken: ") {
			t.Fatalf("%q: aim %q (%q), %v: want a fix", b.Title, fb.Aim, fb.AimWhy, err)
		}
	}
	if fb, _ := m.File(Brief{Project: p.Name, Title: "speed up the fix path", Known: "The gate fails often.", Aim: "gain"}); !fb.Gain() {
		t.Fatalf("a brief that says it aims at a gain was taken as %s", fb.AimWord())
	}
	main, _ := m.C.Effects.Sync(p)
	if b := m.renderBrief(&g, p, main); !strings.Contains(b, "Aim: a gain (aims at a measurable gain)") {
		t.Fatalf("the brief doesn't say it aims at a gain:\n%s", b)
	}
	if b := m.renderBrief(&fx, p, main); !strings.Contains(b, "Aim: a fix (TestMerge fails)") {
		t.Fatalf("the brief doesn't say it is a fix:\n%s", b)
	}
	if b := m.renderBrief(&u, p, main); !strings.Contains(b, "Aim: a gain, as nothing says it is a fix") {
		t.Fatalf("the brief doesn't say it is taken as a gain:\n%s", b)
	}
}

// The reader of a change to the Hive declared a fix is asked to flag one
// aimed at a gain; a gain, an unclear change (taken as a gain), a revert or
// another project's change isn't.
func TestAimCheckOnSelfFixes(t *testing.T) {
	self, other := core.Project{Name: "hive", Self: true}, core.Project{Name: "p"}
	if s := aimCheck(&Task{Aim: AimFix, AimWhy: "TestMerge fails"}, self); !strings.Contains(s, "declared a fix (TestMerge fails)") || !strings.Contains(s, "--aim gain") {
		t.Fatalf("a self fix's reader isn't asked about its aim: %q", s)
	}
	for _, c := range []struct {
		t *Task
		p core.Project
	}{{&Task{Aim: AimGain}, self}, {&Task{}, self}, {&Task{Kind: "revert"}, self}, {&Task{Aim: AimFix, AimWhy: "x"}, other}} {
		if s := aimCheck(c.t, c.p); s != "" {
			t.Fatalf("aim %q kind %q self %v: got %q", c.t.Aim, c.t.Kind, c.p.Self, s)
		}
	}
}

// A worker that merged the main its last conflict sent starts the count over
// when main moves on and conflicts again; one that didn't keeps adding up.
func TestOnlyUnresolvedConflictsCount(t *testing.T) {
	m, task, p, f, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateCantRun)
	dir := m.C.WS.Project(p.Name)
	pushMain(t, f, map[string]string{"other.txt": "a\n"})
	sent, _ := m.C.Effects.Sync(p) // the main the third conflict sent
	tree, err := core.Git(dir, "merge-tree", "--write-tree", task.SHA, sent)
	if err != nil {
		t.Fatal(err)
	}
	head, err := core.Git(dir, "commit-tree", "-p", task.SHA, "-p", sent, "-m", "merge main", strings.TrimSpace(tree))
	if err != nil {
		t.Fatal(err)
	}
	head = strings.TrimSpace(head) // the worker merged it
	pushMain(t, f, map[string]string{"feature.go": "package q\n"})
	moved, _ := m.C.Effects.Sync(p) // and main moved on, conflicting again
	task.Conflicts, task.ConflictMain = 3, sent
	if err := m.conflict(task, p, head, errors.New("main moved")); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageWorking || task.Conflicts != 1 || task.ConflictMain != moved {
		t.Fatalf("stage %q, conflicts %d, main %q: want a fresh count, sent back to merge %s", task.Stage, task.Conflicts, task.ConflictMain, moved)
	}
	for range 3 { // sent back again and again without merging what it was sent
		if err := m.conflict(task, p, head, errors.New("still")); err != nil {
			t.Fatal(err)
		}
	}
	if task.Stage != StageStopped || !strings.Contains(task.Reason, "keeps conflicting") {
		t.Fatalf("stage %q, reason %q: want given up after four unresolved", task.Stage, task.Reason)
	}
}

// The gates' limit starts at two, grows by one at most every gateStep while
// the machine has room, never past one gate per gateCores cores or the
// person's max_gates, pulls back under strain, and stays at one where the
// machine can't be read.
func TestGatePace(t *testing.T) {
	defer func(g func(core.Config) (string, bool)) { gateMachine = g }(gateMachine)
	strain, watching := "", true
	gateMachine = func(core.Config) (string, bool) { return strain, watching }
	j, err := core.OpenJournal(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	fresh := func() *Module { return &Module{C: &core.Core{J: j}} }
	now := time.Now()
	m := fresh()
	cfg := core.Config{MaxGates: 0}
	top := gateCeiling(cfg)
	if l, _ := m.gateLimit(now, 0, cfg); l != min(2, top) {
		t.Fatalf("start %d, want %d", l, min(2, top))
	}
	cfg.MaxGates = 4
	m = fresh()
	if l, _ := m.gateLimit(now, 4, cfg); l != 4 {
		t.Fatalf("start under max_gates 4: %d", l)
	}
	cfg.MaxGates = 5
	m = fresh()
	m.gateLimit(now, 3, cfg) // starts at 5
	m.gates.limit = 3        // as if pulled back
	if l, _ := m.gateLimit(now.Add(gateStep-time.Second), 3, cfg); l != 3 {
		t.Fatalf("grew to %d within %s", l, gateStep)
	}
	if l, _ := m.gateLimit(now.Add(gateStep), 3, cfg); l != 4 {
		t.Fatalf("limit %d after %s with room, want 4", l, gateStep)
	}
	if l, _ := m.gateLimit(now.Add(2*gateStep), 4, cfg); l != 5 {
		t.Fatalf("limit %d, want 5", l)
	}
	if l, _ := m.gateLimit(now.Add(3*gateStep), 5, cfg); l != 5 {
		t.Fatalf("grew past max_gates to %d", l)
	}
	strain = "machine low on memory"
	if l, why := m.gateLimit(now.Add(4*gateStep), 5, cfg); l != 3 || why != strain {
		t.Fatalf("limit %d (%q) under strain, want 3", l, why)
	}
	var changes []string // a change past the first read is journaled
	j.Replay(func(e core.Event) {
		var c struct {
			Limit    string
			From, To int
		}
		if e.Kind == "pace.changed" && e.Decode(&c) == nil {
			changes = append(changes, fmt.Sprintf("%s %d>%d", c.Limit, c.From, c.To))
		}
	})
	if got := strings.Join(changes, ", "); got != "gates 3>4, gates 4>5, gates 5>3" {
		t.Fatalf("journaled %q", got)
	}
	strain, watching, cfg.MaxGates = "", false, 0
	m = fresh()
	if l, _ := m.gateLimit(now, 0, cfg); l != 1 {
		t.Fatalf("limit %d where the machine can't be read, want 1", l)
	}
	if l, _ := m.gateLimit(now.Add(gateStep), 1, cfg); l != 1 {
		t.Fatalf("grew to %d unseen", l)
	}
}

// A task whose head moved while its gate ran starts a run on the new head
// and ends the one on the old: its verdict would judge what no longer lands.
func TestNewHeadCancelsItsOldGate(t *testing.T) {
	m, task, p, _, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	maxGates(m, 3)
	defer func(g func(core.Config) (string, bool)) { gateMachine = g }(gateMachine)
	gateMachine = func(core.Config) (string, bool) { return "", true }
	cancelled := false
	old := &gateRun{key: p.Name + "@oldhead@main", task: task.ID, cancel: func() { cancelled = true }}
	other := &gateRun{key: "other", task: "t9", cancel: func() { t.Error("another task's run was cancelled") }}
	m.mu.Lock()
	m.gating = map[string]*gateRun{old.key: old, other.key: other}
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	m.Wait()
	if !old.dropped || !cancelled || other.dropped || len(*jobs) != 1 || (*jobs)[0].Head != task.SHA {
		t.Fatalf("old dropped %v cancelled %v, other dropped %v, %d runs: want the old head's run ended and the new one started", old.dropped, cancelled, other.dropped, len(*jobs))
	}
}

// A run on the task's same head goes on when main moves under it: main
// moves more often than a gate takes, and restarting it there left no gate
// ever finishing. Its verdict is judged against the main it ran on.
func TestMainMovingKeepsTheGateRunning(t *testing.T) {
	m, task, p, _, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	maxGates(m, 3)
	defer func(g func(core.Config) (string, bool)) { gateMachine = g }(gateMachine)
	gateMachine = func(core.Config) (string, bool) { return "", true }
	cancelled := false
	run := &gateRun{key: p.Name + "@" + task.SHA + "@oldmain", task: task.ID, head: task.SHA, main: "oldmain", since: time.Now(), cancel: func() { cancelled = true }}
	m.mu.Lock()
	m.gating = map[string]*gateRun{run.key: run}
	m.mu.Unlock()
	mergeOnce(t, m, task, p)
	m.Wait()
	if run.dropped || cancelled || len(*jobs) != 0 || task.Stage != StageMerging || !strings.Contains(task.Reason, "main has moved") {
		t.Fatalf("dropped %v cancelled %v, %d runs, stage %q, reason %q: want the run going on and the task waiting on it", run.dropped, cancelled, len(*jobs), task.Stage, task.Reason)
	}
}

// A task whose head moves (a merge of main, a fix sent back) ends its
// gate's run on the old head as it is saved, freeing the slot for the gains
// queued behind it instead of holding it until the new head reaches the
// gate. A run on the head it keeps goes on.
func TestNewHeadFreesItsOldGateAtOnce(t *testing.T) {
	m, task, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	cancelled := 0
	same := &gateRun{key: p.Name + "@" + task.SHA + "@main", task: task.ID, head: task.SHA, main: "main", cancel: func() { cancelled++ }}
	m.mu.Lock()
	m.gating = map[string]*gateRun{same.key: same}
	m.mu.Unlock()
	m.save(task, "looked again")
	if same.dropped || cancelled != 0 {
		t.Fatalf("a run on the task's own head was ended")
	}
	task.SHA, task.Stage = "newhead", StageTesting
	m.save(task, "pushed newhead")
	if !same.dropped || cancelled != 1 || !strings.Contains(task.Log[len(task.Log)-1].Text, "earlier head ends") {
		t.Fatalf("dropped %v, cancelled %d, log %q: want the old head's run ended and said", same.dropped, cancelled, task.Log[len(task.Log)-1].Text)
	}
}

// A task that has merged, finished or been stopped ends its gate's run even
// on its own head: no verdict on it lands any more.
func TestEndedTaskFreesItsGateAtOnce(t *testing.T) {
	for _, stage := range []string{StageMerged, StageDone, StageStopped} {
		m, task, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
		cancelled := 0
		run := &gateRun{key: p.Name + "@" + task.SHA + "@main", task: task.ID, head: task.SHA, main: "main", cancel: func() { cancelled++ }}
		m.mu.Lock()
		m.gating = map[string]*gateRun{run.key: run}
		m.mu.Unlock()
		task.Stage = stage
		m.save(task, "ended")
		if !run.dropped || cancelled != 1 {
			t.Fatalf("%s: dropped %v, cancelled %d: want the run on its own head ended", stage, run.dropped, cancelled)
		}
	}
}

// save must be handed the live task: a step saving a copy taken before a
// later save (which may carry a head the task has since left) doesn't end
// the run on the head the task is on now.
func TestStaleCopyKeepsTheLiveHeadsGate(t *testing.T) {
	m, task, p, _, _ := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	old := *task // a step's copy, taken on the old head
	task.SHA = "newhead"
	m.save(task, "pushed newhead")
	cancelled := 0
	run := &gateRun{key: p.Name + "@newhead@main", task: task.ID, head: "newhead", main: "main", cancel: func() { cancelled++ }}
	m.mu.Lock()
	m.gating = map[string]*gateRun{run.key: run}
	m.mu.Unlock()
	m.save(&old, "a slow step's note")
	if run.dropped || cancelled != 0 {
		t.Fatalf("a stale copy on the old head ended the gate's run on the live head")
	}
}

// A gain the gate rolled back against a main that has since moved goes back
// to its worker from wherever it stands in the merge line, with the verdict
// in its log, and isn't tested or gated again at the new main (issue #1799:
// #836 held the line's front for hours on a rollback that stood at no later
// main).
func TestSelfGateRollbackAtAnOlderMainLeavesTheLine(t *testing.T) {
	m, task, p, f, jobs := selfRig(t, map[string]string{"feature.go": "package p\n"}, core.GateKeep)
	old, _ := m.C.Effects.Sync(p)
	if err := m.C.Effects.RecordGate(core.GateResult{Project: p.Name, Head: task.SHA, Main: old, Outcome: core.GateRollback, Reason: "nothing moved beyond the noise"}); err != nil {
		t.Fatal(err)
	}
	pushMain(t, f, map[string]string{"tests/x.sh": "x"}) // what the gate measures with: the verdict no longer stands
	main, _ := m.C.Effects.Sync(p)
	if _, ok := m.C.Effects.StandingGate(p, task.SHA, main); ok {
		t.Fatal("the rollback stands at the new main: the test proves nothing")
	}
	front := &Task{ID: "t-front", Project: p.Name, Kind: "work", Stage: StageMerging, SHA: "feedface", PR: core.PR{Number: task.PR.Number - 1}, Reason: MergeLineWait + ": front"}
	m.mu.Lock()
	m.tasks[front.ID] = front
	m.mu.Unlock()
	task.Reason = MergeLineWait + ": behind #" + fmt.Sprint(front.PR.Number)
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || !strings.Contains(task.Findings, gateRolledBack) || !strings.Contains(task.Findings, "nothing moved beyond the noise") {
		t.Fatalf("stage %q, reason %q, findings %q: want sent back on its rollback", task.Stage, task.Reason, task.Findings)
	}
	if len(*jobs) != 0 || task.MergedTest != "" || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("%d gate runs, merged test %q, PR %s: want neither gated nor tested again, and open", len(*jobs), task.MergedTest, prState(t, f, task.PR.Number))
	}
	logged := slices.ContainsFunc(task.Log, func(e Entry) bool { return strings.Contains(e.Text, "the recursive gate: ROLLED BACK") })
	if !logged || task.Gate == nil || task.Gate.Main != old {
		t.Fatalf("the task's log doesn't carry the verdict: gate %+v, log %+v", task.Gate, task.Log)
	}
}
