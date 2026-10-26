package work

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// queueRig is helperRig with a few source files on main, so briefs can name
// the same file or package, and at most two workers at once.
func queueRig(t *testing.T) *Module {
	m, forge, _ := helperRig(t)
	seed := filepath.Join(t.TempDir(), "seed")
	mustGit(t, "", "clone", "-q", forge.RepoPath("me/p"), seed)
	for _, f := range []string{"internal/core/effects.go", "internal/core/runs.go", "internal/tui/views.go", "docs/log.md", "docs/notes/plan.md", "modules/agents/worker.md", "modules/agents/module.json", "modules/kb/filer.md", "modules/kb/module.json", "modules/notes/README.md"} {
		os.MkdirAll(filepath.Join(seed, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(seed, f), []byte("package x\n"), 0o644)
	}
	os.MkdirAll(filepath.Join(seed, "textkit"), 0o755)
	os.WriteFile(filepath.Join(seed, "textkit", "__init__.py"), []byte("class Box:\n    def __init__(self):\n        pass\n\n\ndef truncate(text, width):\n    raise NotImplementedError\n\n\nasync def slugify(text):\n    raise NotImplementedError\n\n\ndef wrap(text, width):\n    raise NotImplementedError\n"), 0o644)
	os.WriteFile(filepath.Join(seed, "internal", "tui", "views.go"), []byte("package tui\n\nfunc (m *Model) render() string { return \"\" }\n\ntype Pane struct{}\n"), 0o644)
	os.WriteFile(filepath.Join(seed, ".gitattributes"), []byte("* text=auto\n# additive\ndocs/log.md merge=union\nplan.md merge=union\n"), 0o644)
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-q", "-m", "source")
	mustGit(t, seed, "push", "-q", "origin", "HEAD:main")
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 2
	m.C.WS.SaveConfig(cfg)
	return m
}

// tick runs the work module's tick until no step is still going.
func tick(m *Module) {
	for range 3 {
		m.Tick()
		for {
			m.mu.Lock()
			n := len(m.busy)
			m.mu.Unlock()
			if n == 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func file(t *testing.T, m *Module, title, known string) Task {
	t.Helper()
	task, err := m.File(Brief{Project: "p", Title: title, Known: known, From: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // filed in a known order
	return task
}

func stageOf(t *testing.T, m *Module, task Task) Task {
	t.Helper()
	got, _ := m.Get(task.ID)
	return got
}

// Briefs start a worker in the order they were filed, only as many as
// max_workers; one naming the file another works on waits for it;
// the rest wait as queued, saying why. The lead's order goes first, a held
// brief waits until released, and a lower cap never stops a started worker.
func TestQueueCapsOrdersAndGroups(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	a := file(t, m, "tidy the readme", "")
	b := file(t, m, "cap the checks", "Checks run via internal/core/effects.go.")
	c := file(t, m, "hold strained turns", "Also in internal/core/effects.go, launch.")
	d := file(t, m, "show the queue", "On the dashboard, internal/tui/views.go.")
	e := file(t, m, "a later idea", "")
	tick(m)

	want := func(task Task, stage, reason string) {
		t.Helper()
		got := stageOf(t, m, task)
		if got.Stage != stage || !strings.Contains(got.Reason, reason) {
			t.Fatalf("%q: stage %q reason %q, want %q with %q", task.Title, got.Stage, got.Reason, stage, reason)
		}
	}
	want(a, StageWorking, "")
	want(b, StageWorking, "")
	want(c, StageQueued, `waiting for "cap the checks" (task `+b.ID+") to land: both touch internal/core/effects.go")
	want(d, StageQueued, "waiting for a worker (max_workers 2)")
	want(e, StageQueued, "waiting for a worker")
	if r, ok := m.C.Runs.Get("w-" + d.ID); ok {
		t.Fatalf("a queued brief has a worker: %+v", r)
	}

	// The lead puts the last one first and holds the next.
	out, err := m.opQueue(lead, json.RawMessage(`{"order":["`+e.ID+`"],"hold":["`+d.ID+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if q := out.(map[string]any)["queue"].([]string); len(q) != 3 || !strings.Contains(q[0], e.ID) || !strings.Contains(q[2], d.ID) {
		t.Fatalf("queue after ordering: %q", q)
	}
	tick(m)
	want(d, StageQueued, "held by lead")

	// a lands: its slot goes to e, the lead's first, not to the held d.
	got := stageOf(t, m, a)
	got.Stage = StageDone
	m.save(&got, "landed")
	tick(m)
	want(e, StageWorking, "")
	want(d, StageQueued, "held by lead")
	want(c, StageQueued, "both touch internal/core/effects.go")

	// A lower cap holds new starts only: b and e work on.
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	if _, err := m.opQueue(lead, json.RawMessage(`{"release":"`+d.ID+`"}`)); err != nil { // as the CLI sends it
		t.Fatal(err)
	}
	tick(m)
	want(b, StageWorking, "")
	want(e, StageWorking, "")
	want(d, StageQueued, "waiting for a worker (max_workers 1)")
	for _, id := range []string{"w-" + b.ID, "w-" + e.ID} {
		if r, _ := m.C.Runs.Get(id); r.Terminal() {
			t.Fatalf("the queue stopped a started worker: %+v", r)
		}
	}

	// b lands: c, which waited on it, goes ahead of nothing it touches.
	got = stageOf(t, m, b)
	got.Stage = StageDone
	m.save(&got, "landed")
	cfg.MaxWorkers = 3
	m.C.WS.SaveConfig(cfg)
	tick(m)
	want(c, StageWorking, "")
	want(d, StageWorking, "")

	// The op refuses what isn't waiting, and its notes say so.
	out, _ = m.opQueue(lead, json.RawMessage(`{"hold":["`+c.ID+`"]}`))
	if notes := out.(map[string]any)["notes"].([]string); len(notes) != 1 || !strings.Contains(notes[0], "left the queue") {
		t.Fatalf("notes %q", notes)
	}
}

// A brief that only changes module prompts goes to the top of the line and
// may start past the worker limit, one at a time; one that names code too
// waits its turn.
func TestQueueQuickPromptBriefs(t *testing.T) {
	m := queueRig(t) // max_workers 2
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	a := file(t, m, "cap the checks", "In internal/core/effects.go.")
	b := file(t, m, "hold strained turns", "In internal/core/runs.go.")
	c := file(t, m, "show the queue", "In internal/tui/views.go.")
	mixed := file(t, m, "worker prompt and code", "In modules/agents/worker.md and internal/tui/views.go.")
	p1 := file(t, m, "word the worker prompt", "In modules/agents/worker.md.")
	p2 := file(t, m, "word the filer prompt", "In modules/kb/filer.md; see docs/log.md.")
	if m.quick(mixed) || !m.quick(p1) || !m.quick(p2) || m.quick(a) {
		t.Fatalf("quick: mixed %v, p1 %v, p2 %v, a %v", m.quick(mixed), m.quick(p1), m.quick(p2), m.quick(a))
	}
	// A package, a directory or a file still to be made, named next to a
	// prompt, is code work: not quick. A prompt still to be made is.
	for known, want := range map[string]bool{
		"modules/agents/lead.md and the queue in internal/mod/work.": false,
		"modules/kb/filer.md and the modules/kb/dream package.":      false,
		"modules/agents/lead.md, and a new internal/core/pace.go.":   false,
		"modules/agents/../../x/y.md":                                false,
		"A new modules/agents/scout.md, logged in docs/log.md.":      true,
		"modules/notes/README.md, a folder that holds no module.":    false,
		"A new modules/other/x.md, in no module's folder.":           false,
	} {
		if got := m.quick(Task{Project: "p", Known: known}); got != want {
			t.Errorf("quick(%q) = %v, want %v", known, got, want)
		}
	}
	for _, task := range []*Task{&a, &b, &c, &mixed, &p1, &p2} {
		task.Stage = StageQueued
		m.save(task, "")
	}
	q := m.Queue(lead)
	if len(q) != 6 || !strings.Contains(q[0], p1.ID) || !strings.Contains(q[1], p2.ID) || !strings.Contains(q[2], a.ID) {
		t.Fatalf("queue: %q", q)
	}
	for _, task := range []*Task{&a, &b} {
		task.Stage = StageWorking
		m.save(task, "")
	}
	// A max_workers the person set is their ceiling: a quick brief waits at it.
	start, wait := m.admit(m.List(nil))
	if start[p1.ID] || !strings.Contains(wait[p1.ID], "max_workers 2") {
		t.Fatalf("the prompt brief went past the person's max_workers: %q", wait[p1.ID])
	}
	// With none set, it goes past the pace.
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 0
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 1e6 // watched, never strained
	m.C.WS.SaveConfig(cfg)
	m.workers = pace{limit: 2, start: defaultWorkers, at: time.Now(), why: "the start"}
	start, wait = m.admit(m.List(nil))
	if !start[p1.ID] {
		t.Fatalf("the prompt brief waits past a full pace: %q", wait[p1.ID])
	}
	for _, task := range []Task{p2, c} {
		if !strings.Contains(wait[task.ID], "at the pace") {
			t.Fatalf("%q: %q, want waiting for a worker", task.Title, wait[task.ID])
		}
	}
	// It still waits on a strained machine and on a plan near its end.
	cfg.MemFloorMB = 1 << 40
	m.C.WS.SaveConfig(cfg)
	if start, wait = m.admit(m.List(nil)); start[p1.ID] || !strings.HasPrefix(wait[p1.ID], "waiting: ") {
		t.Fatalf("the prompt brief started on a strained machine: %q", wait[p1.ID])
	}
	cfg.MemFloorMB = 0
	m.C.WS.SaveConfig(cfg)
	pick := cfg.Route("worker", "")[0]
	plan := func(u float64) {
		reset := time.Now().Add(time.Hour)
		m.C.J.MustAppend("plan.usage", core.Plan{Harness: pick.Harness, Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour",
			Windows: map[string]harness.Window{"five_hour": {Utilization: u, ResetsAt: reset}}}})
	}
	plan(0.95)
	if start, wait = m.admit(m.List(nil)); start[p1.ID] || !strings.Contains(wait[p1.ID], "at the pace") {
		t.Fatalf("the prompt brief started on a spent plan: %q", wait[p1.ID])
	}
	plan(0.1)
	// A quick worker on: the next quick brief waits for a slot like any other.
	p1.Stage = StageWorking
	m.save(&p1, "")
	if start, wait = m.admit(m.List(nil)); start[p2.ID] || !strings.Contains(wait[p2.ID], "at the pace") {
		t.Fatalf("a second quick brief went past the pace: %q", wait[p2.ID])
	}
	// The mixed brief names p1's prompt: it waits on p1 even with a slot free.
	if !strings.Contains(wait[mixed.ID], p1.ID) {
		t.Fatalf("mixed: %q", wait[mixed.ID])
	}
	a.Stage = StageDone
	m.save(&a, "landed")
	if _, wait = m.admit(m.List(nil)); !strings.Contains(wait[mixed.ID], p1.ID) {
		t.Fatalf("mixed: %q", wait[mixed.ID])
	}
	// A quick worker gone quiet frees its slot, and the pace has room for
	// p2; at a full pace it still keeps p2 from going past.
	if _, err := m.C.Runs.Start(core.RunSpec{ID: "wp1", Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	p1.Worker = "wp1"
	m.save(&p1, "")
	m.C.Runs.SetStatus("wp1", core.StatusQuiet, "keeps ending turns without finishing")
	m.workers = pace{limit: 1, start: defaultWorkers, at: time.Now(), why: "the start"}
	if start, wait = m.admit(m.List(nil)); start[p2.ID] || !strings.Contains(wait[p2.ID], "at the pace") {
		t.Fatalf("a second quick brief went past the pace beside a quiet one: %q", wait[p2.ID])
	}
}

// Every agent owns the loop: a worker holds a queued brief on its own project
// as the lead would, on the record by its run id, and one on another project
// can't.
func TestWorkerHoldsQueuedBrief(t *testing.T) {
	m := queueRig(t)
	a := file(t, m, "tidy the readme", "")
	b := file(t, m, "cap the checks", "")
	c := file(t, m, "a later idea", "")
	tick(m)
	worker := core.Run{RunSpec: core.RunSpec{ID: "w-" + a.ID, Role: "worker", Project: "p"}}
	stranger := core.Run{RunSpec: core.RunSpec{ID: "w-other", Role: "worker", Project: "q"}}
	out, err := m.C.Runs.Ops().Call(stranger, "queue", json.RawMessage(`{"hold":"`+c.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if notes := fmt.Sprint(out.(map[string]any)["notes"]); !strings.Contains(notes, c.ID+" is on project p; you steer only your own") {
		t.Fatalf("the stranger wasn't refused by reach: %s", notes)
	}
	if got := stageOf(t, m, c); got.Held {
		t.Fatalf("a worker on another project held the brief: %+v", got)
	}
	// A new order from the stranger touches only its own project's line: p's
	// ordered brief keeps its place, with no note about a brief it never named.
	placed := stageOf(t, m, c)
	placed.Order = 1
	m.save(&placed, "")
	out, err = m.C.Runs.Ops().Call(stranger, "queue", json.RawMessage(`{"order":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	if notes := fmt.Sprint(out.(map[string]any)["notes"]); strings.Contains(notes, c.ID) || stageOf(t, m, c).Order != 1 {
		t.Fatalf("another project's order was reset or noted: %s, order %d", notes, stageOf(t, m, c).Order)
	}
	placed.Order = 0
	m.save(&placed, "")
	if _, err := m.C.Runs.Ops().Call(worker, "queue", json.RawMessage(`{"hold":"`+c.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	got := stageOf(t, m, c)
	if !got.Held || got.HeldBy != worker.ID || !strings.Contains(got.Log[len(got.Log)-1].Text, "held by "+worker.ID) {
		t.Fatalf("hold not recorded with the worker's id: %+v", got)
	}
	done := stageOf(t, m, b)
	done.Stage = StageDone
	m.save(&done, "landed")
	tick(m)
	if got := stageOf(t, m, c); got.Stage != StageQueued || got.Reason != "held by "+worker.ID {
		t.Fatalf("held brief: stage %q reason %q", got.Stage, got.Reason)
	}
}

// A brief names its area only by files that exist in the project, so a repo
// name, a URL or a package's directory doesn't tie unrelated briefs together.
func TestQueueAreas(t *testing.T) {
	m := queueRig(t)
	task := file(t, m, "fix it", "In ihsan-sa/hive, see https://github.com/x/y and internal/core/effects.go, internal/tui/, ./internal/core/runs.go.")
	got := m.areas(task)
	want := []string{"internal/core/effects.go", "internal/core/runs.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("areas %q, want %q", got, want)
	}
}

// A symlink in the clone pointing outside it never names an area, so a brief
// can't test whether a path on the machine exists; one to a directory in it is
// a directory, so no area either, and one to a file is that file.
func TestQueueAreasStayInsideTheClone(t *testing.T) {
	m := queueRig(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644)
	p, _ := m.C.Projects.Get("p")
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	clone := m.C.WS.Project("p")
	os.Symlink(outside, filepath.Join(clone, "out"))
	os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(clone, "internal", "leak.txt"))
	os.Symlink(filepath.Join(clone, "internal", "tui"), filepath.Join(clone, "internal", "ui")) // a directory, though Lstat says link
	os.Symlink(filepath.Join(clone, "internal", "core", "effects.go"), filepath.Join(clone, "internal", "fx.go"))
	task := file(t, m, "fix it", "See out/secret.txt, internal/leak.txt, internal/ui, internal/fx.go and internal/core/effects.go.")
	got := m.areas(task)
	want := []string{"internal/core/effects.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("areas %q, want %q", got, want)
	}
}

// A directory ("in internal/.", "docs/") and a doc that merges as union are
// never areas, so they hold no brief back.
func TestQueueAreasSkipTopLevelAndUnionDocs(t *testing.T) {
	m := queueRig(t)
	task := file(t, m, "fix it", "Nothing like it in internal/. or cmd/..., see docs/log.md, docs/notes/plan.md, docs/ and internal/tui/views.go.")
	got := m.areas(task)
	want := []string{"internal/tui/views.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("areas %q, want %q", got, want)
	}
}

// Only a real conflict holds a brief back: two briefs on different files of
// one package start together, as do two that share only a union doc or a
// directory, even one naming the package on its own (issue #399); one naming
// a file a worker is on waits for it, but not once that worker has pushed.
func TestQueueHoldsOnlyRealConflicts(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 5
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "cap the checks", "In internal/core/effects.go; log it in docs/log.md. Nothing in internal/.")
	b := file(t, m, "hold strained turns", "In internal/core/runs.go; log it in docs/log.md. Nothing in internal/.")
	c := file(t, m, "rework the core", "All of internal/core and internal/tui.")
	d := file(t, m, "split the effects", "Again internal/core/effects.go.")
	tick(m)
	for _, task := range []Task{a, b, c} {
		if got := stageOf(t, m, task); got.Stage != StageWorking {
			t.Fatalf("%q: stage %q reason %q, want working", task.Title, got.Stage, got.Reason)
		}
	}
	if got := stageOf(t, m, d); got.Stage != StageQueued || !strings.HasSuffix(got.Reason, "both touch internal/core/effects.go") {
		t.Fatalf("same-file brief: stage %q reason %q", got.Stage, got.Reason)
	}

	// Both workers push: their tasks are testing and reviewing, and d starts.
	for id, stage := range map[string]string{a.ID: StageTesting, b.ID: StageReviewing} {
		got, _ := m.Get(id)
		got.Stage = stage
		m.save(&got, "pushed")
	}
	tick(m)
	if got := stageOf(t, m, d); got.Stage != StageWorking {
		t.Fatalf("same-file brief behind pushed tasks: stage %q reason %q", got.Stage, got.Reason)
	}
}

// Briefs that name the same file start side by side when each names its own
// functions in it (issue #727: the four at-once benchmark asks ran one at a
// time); one naming no definition there, or one another names too, waits.
// The path itself names nothing (textkit/__init__.py is no __init__).
func TestQueueSameFileApartFunctions(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 8
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "In textkit, implement truncate in textkit/__init__.py.", "")
	b := file(t, m, "In textkit, implement slugify in textkit/__init__.py.", "")
	c := file(t, m, "In textkit, implement wrap in textkit/__init__.py.", "")
	d := file(t, m, "Tidy textkit/__init__.py.", "")
	e := file(t, m, "Make wrap and slugify share a helper", "In textkit/__init__.py.")
	f := file(t, m, "Draw the pane", "render() in internal/tui/views.go.")
	g := file(t, m, "Size the Pane", "In internal/tui/views.go.")
	tick(m)
	for _, task := range []Task{a, b, c, f, g} {
		if got := stageOf(t, m, task); got.Stage != StageWorking {
			t.Fatalf("%q: stage %q reason %q, want working", task.Title, got.Stage, got.Reason)
		}
	}
	for _, task := range []Task{d, e} {
		if got := stageOf(t, m, task); got.Stage != StageQueued || !strings.HasSuffix(got.Reason, "both touch textkit/__init__.py") {
			t.Fatalf("%q: stage %q reason %q, want waiting on the file", task.Title, got.Stage, got.Reason)
		}
	}
}

// A brief that waits on a working one's file holds no later brief back: one
// on its other file starts, rather than waiting for it to go first while it
// waits in turn (tasks 140258 and 163142, issue #493).
func TestQueueWaitsNeverChain(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 5
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "cap the checks", "In internal/core/effects.go.")
	tick(m)
	b := file(t, m, "rework effects and views", "internal/core/effects.go and internal/tui/views.go.")
	c := file(t, m, "show the queue", "On the dashboard, internal/tui/views.go.")
	tick(m)
	if got := stageOf(t, m, a); got.Stage != StageWorking {
		t.Fatalf("first brief: stage %q reason %q", got.Stage, got.Reason)
	}
	if got := stageOf(t, m, b); got.Stage != StageQueued || !strings.Contains(got.Reason, ") to land: both touch ") {
		t.Fatalf("overlapping brief: stage %q reason %q", got.Stage, got.Reason)
	}
	if got := stageOf(t, m, c); got.Stage != StageWorking {
		t.Fatalf("brief behind a waiting one: stage %q reason %q, want working", got.Stage, got.Reason)
	}
}

// A brief that waits on other briefs' files keeps the time it began, shown
// in the queue, and past filesTell the lead hears of it once; once it starts
// the time goes (issue #701: a broad brief passed by later ones mustn't
// starve unseen).
func TestQueueTellsLeadOfLongFileWait(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	a := file(t, m, "cap the checks", "In internal/core/effects.go.")
	tick(m)
	b := file(t, m, "rework effects", "internal/core/effects.go and internal/tui/views.go.")
	tick(m)
	got := stageOf(t, m, b)
	if got.FilesSince.IsZero() || got.FilesTold {
		t.Fatalf("waiting brief: since %v told %v reason %q", got.FilesSince, got.FilesTold, got.Reason)
	}
	since := got.FilesSince
	tick(m)
	if got = stageOf(t, m, b); !got.FilesSince.Equal(since) {
		t.Fatalf("wait start moved from %v to %v", since, got.FilesSince)
	}
	got.FilesSince = time.Now().Add(-2 * time.Hour)
	m.save(&got, "")
	if q := strings.Join(m.Queue(lead), "\n"); !strings.Contains(q, "on other briefs' files for 2h0m0s") {
		t.Fatalf("queue doesn't show the wait: %s", q)
	}
	old := filesTell
	filesTell = time.Hour
	t.Cleanup(func() { filesTell = old })
	tick(m)
	tick(m)
	if got = stageOf(t, m, b); !got.FilesTold || strings.Count(fmt.Sprint(got.Log), "told the lead") != 1 {
		t.Fatalf("told %v, log %v", got.FilesTold, got.Log)
	}
	if _, err := m.opQueue(lead, json.RawMessage(`{"now":["`+b.ID+`"],"why":"it has waited long"}`)); err != nil {
		t.Fatal(err)
	}
	tick(m)
	if got = stageOf(t, m, b); got.Stage != StageWorking || !got.FilesSince.IsZero() || got.FilesTold {
		t.Fatalf("started brief: stage %q since %v told %v", got.Stage, got.FilesSince, got.FilesTold)
	}
	_ = a
}

// Moving a brief in line says the next tick weighs it, not the wait the old
// line gave it, so the reply never shows two briefs waiting on each other
// (issue #701: 163142 still read "waiting for 140258 to go first" in the
// reply that put it first, while 140258 already waited for it to land).
func TestQueueOrderDropsStaleWait(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	file(t, m, "tidy the readme", "")
	tick(m)
	b := file(t, m, "rework effects", "internal/core/effects.go.")
	c := file(t, m, "cap the checks", "In internal/core/effects.go.")
	tick(m)
	if got := stageOf(t, m, c); !strings.Contains(got.Reason, b.ID+") to go first") {
		t.Fatalf("before: %q", got.Reason)
	}
	out, err := m.opQueue(lead, json.RawMessage(`{"order":["`+c.ID+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if q := fmt.Sprint(out); strings.Contains(q, b.ID+") to go first") || !strings.Contains(q, "the next tick weighs it") {
		t.Fatalf("reply shows the old wait: %s", q)
	}
}

// A file a worker has changed in its checkout holds a brief that names it,
// though the worker's own brief never did; a file it left as it was doesn't,
// and neither does one the brief names that the worker hasn't touched.
func TestQueueHoldsWhatAWorkerChanged(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 5
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "cap the checks", "Somewhere in internal/core.")
	tick(m)
	got := stageOf(t, m, a)
	r, ok := m.C.Runs.Get(got.Worker)
	if got.Stage != StageWorking || !ok || got.Base == "" {
		t.Fatalf("stage %q worker %q base %q", got.Stage, got.Worker, got.Base)
	}
	os.WriteFile(filepath.Join(r.Checkout, "internal/core/runs.go"), []byte("package x // changed\n"), 0o644)
	os.WriteFile(filepath.Join(r.Checkout, "internal/core/effects.go"), []byte("package x\r\n"), 0o644) // rewritten as it was, with CRLF as on Windows
	b := file(t, m, "hold strained turns", "In internal/core/runs.go.")
	c := file(t, m, "cap the effects", "In internal/core/effects.go.")
	d := file(t, m, "show the queue", "In internal/tui/views.go.")
	tick(m)
	if got := stageOf(t, m, b); got.Stage != StageQueued || !strings.Contains(got.Reason, "both touch internal/core/runs.go") {
		t.Fatalf("brief on a changed file: stage %q reason %q", got.Stage, got.Reason)
	}
	for _, task := range []Task{c, d} {
		if got := stageOf(t, m, task); got.Stage != StageWorking {
			t.Fatalf("%q: stage %q reason %q, want working", task.Title, got.Stage, got.Reason)
		}
	}

	// A removed file is a change too, seen once the checkout moves.
	os.Remove(filepath.Join(r.Checkout, "internal/tui/views.go"))
	if !m.edited(stageOf(t, m, a), "internal/tui/views.go") {
		t.Fatal("a removed file isn't seen as changed")
	}

	// A base the clone can't read holds nothing and keeps no answer, so
	// a passing git failure doesn't hold a brief until the file moves.
	odd := stageOf(t, m, a)
	odd.Base = strings.Repeat("e", 40)
	if m.edited(odd, "internal/core/runs.go") {
		t.Fatal("a file held against a base that can't be read")
	}
	if got := m.editCache[odd.Worker+"\x00internal/core/runs.go"]; strings.HasPrefix(got.key, odd.Base) {
		t.Fatal("an answer without a base tree was kept")
	}
}

// A worker that has merged main carries main's later changes; those aren't
// its edits and hold no brief, while a file it changed itself still does.
// Its answers go once its task leaves working.
func TestQueueMergedMainIsNotAnEdit(t *testing.T) {
	m := queueRig(t)
	a := file(t, m, "cap the checks", "Somewhere in internal/core.")
	tick(m)
	got := stageOf(t, m, a)
	r, _ := m.C.Runs.Get(got.Worker)
	if got.Stage != StageWorking {
		t.Fatalf("stage %q", got.Stage)
	}
	// main moves on runs.go; the worker merges it, and edits effects.go.
	moveMain(t, m, "internal/core/runs.go", "package x // main's\n")
	os.WriteFile(filepath.Join(r.Checkout, "internal/core/runs.go"), []byte("package x // main's\n"), 0o644)
	os.WriteFile(filepath.Join(r.Checkout, "internal/core/effects.go"), []byte("package x // mine\n"), 0o644)
	b := file(t, m, "hold strained turns", "In internal/core/runs.go.")
	c := file(t, m, "cap the effects", "In internal/core/effects.go.")
	tick(m)
	if got := stageOf(t, m, b); got.Stage != StageWorking {
		t.Fatalf("a brief on main's change waits: stage %q reason %q", got.Stage, got.Reason)
	}
	if got := stageOf(t, m, c); got.Stage != StageQueued || !strings.Contains(got.Reason, "both touch internal/core/effects.go") {
		t.Fatalf("a brief on the worker's own change: stage %q reason %q", got.Stage, got.Reason)
	}
	m.mu.Lock()
	n := len(m.editCache)
	m.mu.Unlock()
	if n == 0 {
		t.Fatal("no answers kept while working")
	}
	done := stageOf(t, m, a)
	done.Stage = StageReviewing
	m.save(&done, "")
	m.admit(m.List(nil))
	m.mu.Lock()
	for k := range m.editCache {
		if strings.HasPrefix(k, done.Worker+"\x00") {
			t.Errorf("an answer for a worker no longer working is kept: %q", k)
		}
	}
	m.mu.Unlock()
}

// A symlink is its link text, as git keeps it: a file made a link counts
// as changed, wherever the link points, and one the base already held as a
// link counts as unchanged until the link text moves.
func TestQueueEditSeesSymlinks(t *testing.T) {
	m := queueRig(t)
	p, _ := m.C.Projects.Get("p")
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	origin, _ := core.Git(m.C.WS.Project("p"), "remote", "get-url", "origin")
	seed := filepath.Join(t.TempDir(), "seed")
	mustGit(t, "", "clone", "-q", strings.TrimSpace(origin), seed)
	if err := os.Symlink("effects.go", filepath.Join(seed, "internal/core/link.go")); err != nil {
		t.Skip(err)
	}
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-q", "-m", "a link")
	mustGit(t, seed, "push", "-q", "origin", "HEAD:"+p.Branch)
	a := file(t, m, "cap the checks", "Somewhere in internal/core.")
	tick(m)
	got := stageOf(t, m, a)
	r, _ := m.C.Runs.Get(got.Worker)
	if got.Stage != StageWorking {
		t.Fatalf("stage %q", got.Stage)
	}
	link := filepath.Join(r.Checkout, "internal/core/link.go")
	if m.edited(got, "internal/core/link.go") {
		t.Fatal("a link as the base holds it is seen as changed")
	}
	os.WriteFile(filepath.Join(r.Checkout, "internal/core/effects.go"), []byte("package x // its target changed\n"), 0o644)
	if m.edited(got, "internal/core/link.go") {
		t.Fatal("a link whose text didn't move is seen as changed")
	}
	os.Remove(link)
	os.Symlink("runs.go", link)
	if !m.edited(got, "internal/core/link.go") {
		t.Fatal("a link pointed elsewhere isn't seen")
	}
	runs := filepath.Join(r.Checkout, "internal/core/runs.go")
	os.Remove(runs)
	os.Symlink("/etc/hostname", runs)
	if !m.edited(got, "internal/core/runs.go") {
		t.Fatal("a file made a link isn't seen")
	}
}

// moveMain commits f on the forge's main and syncs the host clone to it.
func moveMain(t *testing.T, m *Module, f, content string) {
	t.Helper()
	p, _ := m.C.Projects.Get("p")
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	origin, err := core.Git(m.C.WS.Project("p"), "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(t.TempDir(), "seed")
	mustGit(t, "", "clone", "-q", strings.TrimSpace(origin), seed)
	os.MkdirAll(filepath.Join(seed, filepath.Dir(f)), 0o755)
	os.WriteFile(filepath.Join(seed, f), []byte(content), 0o644)
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-q", "-m", "main moves")
	mustGit(t, seed, "push", "-q", "origin", "HEAD:"+p.Branch)
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
}

// Two briefs citing the goals start together; the goals hold a brief only
// once a worker has changed them.
func TestQueueGoalsHoldOnlyWhenChanged(t *testing.T) {
	m := queueRig(t)
	moveMain(t, m, ".hive/goals.md", "goals\n")
	a := file(t, m, "one", "As .hive/goals.md says.")
	b := file(t, m, "two", "As .hive/goals.md says too.")
	tick(m)
	for _, task := range []Task{a, b} {
		if got := stageOf(t, m, task); got.Stage != StageWorking {
			t.Fatalf("%q: stage %q reason %q", task.Title, got.Stage, got.Reason)
		}
	}
	got := stageOf(t, m, a)
	r, _ := m.C.Runs.Get(got.Worker)
	os.WriteFile(filepath.Join(r.Checkout, ".hive/goals.md"), []byte("changed\n"), 0o644)
	if !m.edited(got, ".hive/goals.md") {
		t.Fatal("goals a worker changed aren't seen as changed")
	}
}

// The .gitattributes matcher follows git: "**" spans directories, a name-only
// pattern matches at any depth, and the last line touching merge wins, so
// -merge, !merge or another driver undo an earlier merge=union.
func TestQueueUnionRules(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".gitattributes"), []byte(`* text=auto
docs/**/*.md merge=union
docs/keep/*.md -merge
CHANGES merge=union
docs/odd.md merge=binary
"quoted name" merge=union
!docs/neg.md merge=union
notes/ merge=union
`), 0o644)
	rules := unionRules(root)
	for p, want := range map[string]bool{
		"docs/log.md":       true,
		"docs/a/b/plan.md":  true,
		"docs/keep/plan.md": false,
		"docs/odd.md":       false,
		"pkg/CHANGES":       true,
		"CHANGES":           true,
		"internal/x.go":     false,
		"notes/a.md":        false,
		"docs/log.txt":      false,
	} {
		if got := isUnion(rules, p); got != want {
			t.Errorf("isUnion(%q) = %v, want %v", p, got, want)
		}
	}
}

// A doc main newly marks union stops being an area without the brief changing.
func TestQueueAreasFollowGitattributes(t *testing.T) {
	m := queueRig(t)
	task := file(t, m, "fix it", "See internal/core/runs.go and internal/tui/views.go.")
	if got := strings.Join(m.areas(task), " "); got != "internal/core/runs.go internal/tui/views.go" {
		t.Fatalf("areas %q", got)
	}
	ga := filepath.Join(m.C.WS.Project("p"), ".gitattributes")
	b, _ := os.ReadFile(ga)
	os.WriteFile(ga, append(b, "internal/core/runs.go merge=union\n"...), 0o644)
	if got := strings.Join(m.areas(task), " "); got != "internal/tui/views.go" {
		t.Fatalf("areas after .gitattributes changed %q", got)
	}
}

// The hive project's queue at 14:45Z on 2026-10-24, replayed: 68 queued, 5
// working, and 51 of the queued waiting on "both touch <area>", mostly a
// package or directory a brief cited as evidence. With only files holding,
// half of them are free to start, and every one still waiting waits on a
// file.
func TestQueueReplay1445(t *testing.T) {
	var fix struct {
		Files []string
		Tasks []struct {
			ID, Title, Done, Known, Stage string
			Held, Now                     bool
			Order                         int
			Created                       time.Time
		}
	}
	b, err := os.ReadFile("testdata/queue-1445.json")
	if err != nil || json.Unmarshal(b, &fix) != nil {
		t.Fatalf("fixture: %v", err)
	}
	m, forge, p := helperRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 0 // as the lead set it at 14:22Z
	m.C.WS.SaveConfig(cfg)
	seed := filepath.Join(t.TempDir(), "seed")
	mustGit(t, "", "clone", "-q", forge.RepoPath("me/p"), seed)
	for _, f := range fix.Files {
		os.MkdirAll(filepath.Join(seed, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(seed, f), []byte("x\n"), 0o644)
	}
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-q", "-m", "main at 14:45Z")
	mustGit(t, seed, "push", "-q", "origin", "HEAD:main")
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, f := range fix.Files {
		files[f] = true
	}
	var tasks []Task
	queued := 0
	for _, f := range fix.Tasks {
		tasks = append(tasks, Task{ID: f.ID, Project: "p", Title: f.Title, Done: f.Done, Known: f.Known, Stage: f.Stage, Held: f.Held, Now: f.Now, Order: f.Order, Created: f.Created})
		if f.Stage == StageQueued {
			queued++
		}
	}
	if queued != 68 || len(tasks) != 73 {
		t.Fatalf("fixture has %d queued of %d", queued, len(tasks))
	}
	// The pace (pace.go) starts at 4 workers and 5 already work, so a brief
	// free of every overlap waits for a worker: it counts as free here.
	start, wait := m.admit(tasks)
	free, overlaps := len(start), 0
	for id, why := range wait {
		if strings.HasPrefix(why, "waiting for a worker") {
			free++
			continue
		}
		i := strings.LastIndex(why, "both touch ")
		if i < 0 {
			continue
		}
		overlaps++
		if area := why[i+len("both touch "):]; !files[area] {
			t.Errorf("%s waits on %q, not a file: %s", id, area, why)
		}
	}
	t.Logf("%d free of any overlap, %d wait on a shared file, %d held", free, overlaps, len(wait)+len(start)-free-overlaps)
	// Pinned both ways: fewer free is a hold come back, more is a same-file
	// hold that quietly stopped working. The 18 left wait on files more than
	// one brief cites (7 on tests/LANDING-security.toml); none on the goals.
	if overlaps != 18 || free != 33 {
		t.Fatalf("%d free and %d wait on a shared file, want 33 and 18 (51 waited at 14:45Z)", free, overlaps)
	}
}

// A working task whose worker has gone quiet holds no brief back past its
// quiet alert: the brief on its file starts, and the quiet worker's slot is
// free until it resumes, when it counts toward max_workers again.
func TestQuietWorkerHoldsNoOne(t *testing.T) {
	m := queueRig(t)
	p, _ := m.C.Projects.Get("p")
	stuck := file(t, m, "the stuck one", "In internal/core/effects.go.")
	fix := file(t, m, "the queue fix", "In internal/core/effects.go too.")
	if _, err := m.C.Runs.Start(core.RunSpec{ID: "wq", Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	stuck.Stage, stuck.Worker, fix.Stage = StageWorking, "wq", StageQueued
	m.save(&stuck, "")
	m.save(&fix, "")
	if _, wait := m.admit(m.List(nil)); !strings.Contains(wait[fix.ID], stuck.ID) {
		t.Fatalf("the fix doesn't wait on a working one: %q", wait[fix.ID])
	}
	m.C.Runs.SetStatus("wq", core.StatusQuiet, "keeps ending turns without finishing")
	if err := m.checkWorker(&stuck, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(stuck.ID); !strings.Contains(got.Reason, "quiet") {
		t.Fatalf("no quiet note: %q", got.Reason)
	}
	if start, wait := m.admit(m.List(nil)); !start[fix.ID] {
		t.Fatalf("the fix still waits on a quiet worker: %q", wait[fix.ID])
	}
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	if start, wait := m.admit(m.List(nil)); !start[fix.ID] {
		t.Fatalf("a quiet worker still holds its slot: %q", wait[fix.ID])
	}
	m.C.Runs.SetStatus("wq", core.StatusRunning, "a message arrived")
	other := file(t, m, "another one", "In internal/core/runs.go.")
	other.Stage = StageQueued
	m.save(&other, "")
	if start, wait := m.admit(m.List(nil)); start[other.ID] || !strings.Contains(wait[other.ID], "max_workers") {
		t.Fatalf("a resumed worker's slot was given away: %q", wait[other.ID])
	}
}

// A brief that waits on one worker keeps saying so when another that also
// touches its files starts ahead of it: the reason, and the saved task, stay
// as they were until the first lets go (task 232db4fc42 swapped between
// holders on runs.go and work.go every few minutes for an hour, issue #676).
func TestQueueKeepsItsHolder(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 5
	m.C.WS.SaveConfig(cfg)
	x := file(t, m, "cap the checks", "In internal/core/effects.go.")
	tick(m)
	a := file(t, m, "rework effects and runs", "internal/core/effects.go and internal/core/runs.go.")
	tick(m)
	first := stageOf(t, m, a)
	if first.Stage != StageQueued || !strings.Contains(first.Reason, "(task "+x.ID+") to land") {
		t.Fatalf("overlapping brief: stage %q reason %q", first.Stage, first.Reason)
	}
	y := file(t, m, "hold strained turns", "In internal/core/runs.go.")
	tick(m)
	if got := stageOf(t, m, y); got.Stage != StageWorking {
		t.Fatalf("brief behind a waiting one: stage %q reason %q, want working", got.Stage, got.Reason)
	}
	if got := stageOf(t, m, a); got.Reason != first.Reason || !got.Updated.Equal(first.Updated) {
		t.Fatalf("reason moved from %q (%s) to %q (%s)", first.Reason, first.Updated, got.Reason, got.Updated)
	}
	// Once x lets go, a waits on y.
	got, _ := m.Get(x.ID)
	got.Stage = StageTesting
	m.save(&got, "pushed")
	tick(m)
	if got := stageOf(t, m, a); !strings.Contains(got.Reason, "(task "+y.ID+") to land") {
		t.Fatalf("after the first holder pushed: reason %q", got.Reason)
	}
}

// A holder's label that itself reads like a wait reason doesn't name a task:
// the holder is the one after the quoted label.
func TestHolderPastLabel(t *testing.T) {
	for _, c := range []struct{ label, verb string }{
		{`plain`, "to land"},
		{`waits on (task 111) to land: both touch x.go`, "to go first"},
		{`a "quoted" (task 222) to go first: both touch y.go`, "to land"},
	} {
		why := fmt.Sprintf("waiting for %q (task %s) %s%s%s", c.label, "333", c.verb, bothTouch, "z.go")
		if got := holder(why); got != "333" {
			t.Errorf("holder(%q) = %q, want 333", why, got)
		}
	}
	for _, why := range []string{"", "waiting for a worker (max_workers 4)", "held by the lead"} {
		if got := holder(why); got != "" {
			t.Errorf("holder(%q) = %q, want none", why, got)
		}
	}
}
