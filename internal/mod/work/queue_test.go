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
)

// queueRig is helperRig with a few source files on main, so briefs can name
// the same file or package, and at most two workers at once.
func queueRig(t *testing.T) *Module {
	m, forge, _ := helperRig(t)
	seed := filepath.Join(t.TempDir(), "seed")
	mustGit(t, "", "clone", "-q", forge.RepoPath("me/p"), seed)
	for _, f := range []string{"internal/core/effects.go", "internal/core/runs.go", "internal/tui/views.go", "docs/log.md", "docs/notes/plan.md", "modules/agents/worker.md", "modules/kb/filer.md"} {
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
// max_workers, two on the same file side by side (a conflict is settled at
// merge); the rest wait as queued, saying why. The lead's order goes first, a held
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
	want(c, StageQueued, "waiting for a worker (max_workers 2)")
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
	want(c, StageQueued, "waiting for a worker (max_workers 2)")

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

	// A higher cap: c starts beside b, though both touch effects.go.
	cfg.MaxWorkers = 4
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

// Moving a brief in line says the next tick weighs it, not the wait the old
// line gave it (issue #701).
func TestQueueOrderDropsStaleWait(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	file(t, m, "tidy the readme", "")
	tick(m)
	file(t, m, "rework effects", "internal/core/effects.go.")
	c := file(t, m, "cap the checks", "In internal/core/effects.go.")
	tick(m)
	if got := stageOf(t, m, c); !strings.Contains(got.Reason, "waiting for a worker") {
		t.Fatalf("before: %q", got.Reason)
	}
	out, err := m.opQueue(lead, json.RawMessage(`{"order":["`+c.ID+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if q := fmt.Sprint(out); !strings.Contains(q, "the next tick weighs it") {
		t.Fatalf("reply shows the old wait: %s", q)
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

// A working task whose worker has gone quiet holds no slot past its quiet
// alert until it resumes, when it counts toward max_workers again.
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
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	if start, wait := m.admit(m.List(nil)); start[fix.ID] {
		t.Fatalf("the fix took a working one's slot: %q", wait[fix.ID])
	}
	m.C.Runs.SetStatus("wq", core.StatusQuiet, "keeps ending turns without finishing")
	if err := m.checkWorker(&stuck, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(stuck.ID); !strings.Contains(got.Reason, "quiet") {
		t.Fatalf("no quiet note: %q", got.Reason)
	}
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

// A brief that has waited past overdue goes ahead of newer ones the lead
// ordered, oldest first, and the queue shows how long it has waited (task 065256 waited 10h20m,
// passed by 30 newer briefs "to go first").
func TestQueueOverdueGoesAheadOfTheOrder(t *testing.T) {
	m := queueRig(t) // max_workers 2
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	old := file(t, m, "the lead closes PRs", "In internal/core/effects.go.")
	older := file(t, m, "bound the check homes", "In internal/tui/views.go.")
	newer := file(t, m, "word the PR states", "Also internal/core/effects.go.")
	fresh := file(t, m, "a fresh idea", "")
	for task, ago := range map[*Task]time.Duration{&old: 10 * time.Hour, &older: 11 * time.Hour, &newer: time.Hour} {
		task.Created, task.Stage = time.Now().Add(-ago), StageQueued
		m.save(task, "")
	}
	fresh.Stage = StageQueued
	m.save(&fresh, "")
	if _, err := m.opQueue(lead, json.RawMessage(`{"order":["`+fresh.ID+`","`+newer.ID+`"]}`)); err != nil {
		t.Fatal(err)
	}
	q := m.Queue(lead)
	if len(q) != 4 || !strings.Contains(q[0], older.ID) || !strings.Contains(q[1], old.ID) || !strings.Contains(q[2], fresh.ID) || !strings.Contains(q[3], newer.ID) {
		t.Fatalf("queue: %q", q)
	}
	if !strings.Contains(q[1], "waited 10h0m0s, overdue: ahead of the lead's order") || strings.Contains(q[3], "overdue") || !strings.Contains(q[3], "waited 1h0m0s") || strings.Contains(q[2], "waited") {
		t.Fatalf("queue does not show the waits: %q", q)
	}
	start, wait := m.admit(m.List(nil))
	if !start[older.ID] || !start[old.ID] {
		t.Fatalf("the overdue briefs wait: %q, %q", wait[older.ID], wait[old.ID])
	}
	if !strings.Contains(wait[newer.ID], "waiting for a worker") {
		t.Fatalf("the newer brief: %q, want it to wait for a worker", wait[newer.ID])
	}
}
