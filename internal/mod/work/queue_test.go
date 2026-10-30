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
// only saying why.
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
	if notes := fmt.Sprint(out.(map[string]any)["notes"]); !strings.Contains(notes, c.ID+" is on project p; say why with why") {
		t.Fatalf("the stranger wasn't refused by reach: %s", notes)
	}
	if got := stageOf(t, m, c); got.Held {
		t.Fatalf("a worker on another project held the brief without why: %+v", got)
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
	// Saying why, the stranger moves p's brief first, on its record (10-29:
	// a lead twice asked the lead to move another project's briefs).
	if _, err := m.C.Runs.Ops().Call(stranger, "queue", json.RawMessage(`{"order":"`+c.ID+`,`+a.ID+`","hold":"`+c.ID+`","why":"it unblocks q"}`)); err != nil {
		t.Fatal(err)
	}
	if got := stageOf(t, m, c); !got.Held || got.HeldBy != stranger.ID || !strings.Contains(got.Log[len(got.Log)-1].Text, "held by w-other: it unblocks q") {
		t.Fatalf("the stranger's hold with why wasn't set on the record: %+v", got)
	}
	if _, err := m.C.Runs.Ops().Call(stranger, "queue", json.RawMessage(`{"release":"`+c.ID+`","why":"done with it"}`)); err != nil {
		t.Fatal(err)
	}
	if stageOf(t, m, c).Held {
		t.Fatal("the stranger's release with why didn't take")
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

// A worker that can't take a turn now holds no slot: one parked until a
// usage reset hours off, or finished with its task awaiting take-out. On
// 10-28 such ones held the pace of 4-6 with one turn running while 14 briefs
// waited 2.5-4h. One parked for a short retry keeps its slot, and a worker
// sent back is let through past them and then counts.
func TestWorkersThatCantTakeATurnHoldNoSlot(t *testing.T) {
	m := queueRig(t) // max_workers 2
	for _, id := range []string{"wreset", "wdone", "wretry", "wback"} {
		if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		task := file(t, m, "worked on by "+id, "")
		task.Stage, task.Worker = StageWorking, id
		m.save(&task, "")
	}
	park := func(id string, d time.Duration) {
		m.C.J.MustAppend("run.status", map[string]any{"id": id, "status": core.StatusParked, "reason": "usage limit reached", "until": time.Now().Add(d)})
	}
	park("wreset", 5*time.Hour)
	m.C.Runs.SetStatus("wdone", core.StatusDone, "finished")
	m.C.Runs.SetStatus("wretry", core.StatusDone, "finished")
	m.C.Runs.SetStatus("wback", core.StatusDone, "finished")
	if err := m.C.Runs.Reopen("wback", "A reviewer's findings."); err != nil {
		t.Fatal(err)
	}
	back, _ := m.C.Runs.Get("wback")
	if why := m.C.Runs.Admit(back); why != "" {
		t.Fatalf("the worker sent back, past a parked and two finished ones: %q, want it let through", why)
	}
	fix := file(t, m, "the brief waiting", "")
	fix.Stage = StageQueued
	m.save(&fix, "")
	if start, wait := m.admit(m.List(nil)); !start[fix.ID] {
		t.Fatalf("a parked and two finished workers hold the slot: %q", wait[fix.ID])
	}
	park("wretry", time.Minute)
	if start, wait := m.admit(m.List(nil)); start[fix.ID] || !strings.Contains(wait[fix.ID], "waiting for a worker") {
		t.Fatalf("a worker parked for a minute's retry gave its slot away: %q", wait[fix.ID])
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

// A brief the pace holds back starts anyway while an executor module has
// room, one a pass and as many as max_workers, and its worker's turns go to
// the runner first; it holds no slot here while they do, and none starts so
// with no room.
func TestPaceSendsAWorkerToARunnerWithRoom(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	if _, err := m.C.Runs.Start(core.RunSpec{ID: "wq", Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	busy := file(t, m, "the working one", "")
	busy.Stage, busy.Worker = StageWorking, "wq"
	m.save(&busy, "")
	a, b := file(t, m, "first held", ""), file(t, m, "second held", "")
	for _, x := range []*Task{&a, &b} {
		x.Stage = StageQueued
		m.save(x, "")
	}
	room := false
	m.elsewhere = func() bool { return room }
	if start, wait := m.admit(m.List(nil)); start[a.ID] || start[b.ID] || !strings.Contains(wait[a.ID], "waiting for a worker") {
		t.Fatalf("started past the pace with no room elsewhere: %v %q", start, wait[a.ID])
	}
	room = true
	start, wait := m.admit(m.List(nil))
	if !start[a.ID] || start[b.ID] || !strings.Contains(wait[b.ID], "waiting for a worker") {
		t.Fatalf("want one past the pace while a runner has room: %v %q", start, wait[b.ID])
	}
	p, _ := m.C.Projects.Get("p")
	if err := m.startWorker(&a, p); err != nil {
		t.Fatal(err)
	}
	if r, _ := m.C.Runs.Get(a.Worker); !r.Away {
		t.Fatal("the worker started past the pace isn't sent away")
	}
	if m.away[a.ID] {
		t.Error("the away mark outlived its worker's start")
	}
	// As many go away as max_workers; then the rest wait for a worker.
	if start, wait := m.admit(m.List(nil)); start[b.ID] || !strings.Contains(wait[b.ID], "waiting for a worker") {
		t.Fatalf("more away than max_workers: %v %q", start, wait[b.ID])
	}
	// Its turns haven't run here, so it holds no slot here: the working one
	// done, the next starts here.
	busy.Stage = StageDone
	m.save(&busy, "")
	room = false
	if start, wait := m.admit(m.List(nil)); !start[b.ID] {
		t.Fatalf("an away worker holds a slot here: %q", wait[b.ID])
	}
}

// A main merge that conflicts with many PRs sends their workers back at the
// pace, not all at once (one did 19 at a pace of 1): the rest stay with their
// own workers, waiting, ahead of new briefs.
func TestConflictsGoBackAtThePace(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	var sent []Task
	for i, title := range []string{"one", "two", "three"} {
		task := file(t, m, title, "")
		w := fmt.Sprintf("wc%d", i)
		if _, err := m.C.Runs.Start(core.RunSpec{ID: w, Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		m.C.Runs.SetStatus(w, core.StatusDone, "finished")
		task.Stage, task.Worker, task.Resend = StageWorking, w, "Main moved and your branch conflicts with it."
		m.save(&task, "")
		sent = append(sent, task)
	}
	fresh := file(t, m, "a new brief", "")
	fresh.Stage = StageQueued
	m.save(&fresh, "")
	tick(m)
	reopened := 0
	for _, task := range sent {
		got := stageOf(t, m, task)
		if got.Stage != StageWorking {
			t.Fatalf("%s: stage %q, want it kept with its worker", got.Title, got.Stage)
		}
		if got.Resend == "" {
			reopened++
			if queued(m, got.Worker) == "" {
				t.Fatalf("%s was let go but its worker wasn't told", got.Title)
			}
		} else if queued(m, got.Worker) != "" || !strings.Contains(got.Reason, "waiting for a worker") {
			t.Fatalf("%s: queued %q, reason %q: want it waiting at the pace", got.Title, queued(m, got.Worker), got.Reason)
		}
	}
	if reopened != 1 {
		t.Fatalf("%d workers sent back at a pace of 1", reopened)
	}
	if got := stageOf(t, m, fresh); got.Stage != StageQueued || got.Worker != "" {
		t.Fatalf("the new brief went ahead of the workers waiting: stage %q", got.Stage)
	}
	if start, _ := m.admit(m.List(nil)); len(start) != 0 {
		t.Fatalf("started %v past the pace", start)
	}
	q := m.Queue(core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}})
	if len(q) != 3 || !strings.Contains(q[0], "goes back to merge main") || !strings.Contains(q[2], fresh.ID) {
		t.Fatalf("queue: %q, want the two waiting to go back ahead of the new brief", q)
	}
	// One stopped while it waits never takes the stale conflict to a later worker.
	var waiting Task
	for _, task := range sent {
		if got := stageOf(t, m, task); got.Resend != "" {
			waiting = got
		}
	}
	if err := m.Stop(waiting.ID, "not wanted"); err != nil {
		t.Fatal(err)
	}
	if got := stageOf(t, m, waiting); got.Resend != "" || got.Resending() {
		t.Fatalf("a stopped task keeps its conflict to send: %q", got.Resend)
	}
}

// A worker a conflict sends back is reopened, not launched, so it never goes
// past the pace to a runner: it waits for a slot here, and leaves no away
// mark behind to count against the runners.
func TestConflictWaitsHereNotOnARunner(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	m.elsewhere = func() bool { return true }
	for _, id := range []string{"wb", "wr"} {
		if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
	}
	busy := file(t, m, "the working one", "")
	busy.Stage, busy.Worker = StageWorking, "wb"
	m.save(&busy, "")
	back := file(t, m, "sent back", "")
	back.Stage, back.Worker, back.Resend = StageWorking, "wr", "Main moved and your branch conflicts with it."
	m.save(&back, "")
	if start, wait := m.admit(m.List(nil)); start[back.ID] || !strings.Contains(wait[back.ID], "waiting for a worker") || m.away[back.ID] {
		t.Fatalf("a conflict went past the pace: start %v, wait %q", start, wait[back.ID])
	}
}

// A worker a conflict sent back waits in the line as the queue lists it, so
// queue moves, holds and starts it like a brief: on 10-29 the pivot #2046 sat
// 23rd behind 22 sendbacks, and order said it had left the queue (#2085).
func TestQueueOrdersASendback(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	var back []Task
	for i, w := range []string{"wo", "wn", "wh"} {
		if _, err := m.C.Runs.Start(core.RunSpec{ID: w, Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		task := file(t, m, "sent back "+w, "")
		task.Stage, task.Worker, task.Resend = StageWorking, w, "Main moved and your branch conflicts with it."
		task.Created = time.Now().Add(time.Duration(i-10) * time.Minute)
		m.save(&task, "")
		back = append(back, task)
	}
	older, newer, held := back[0], back[1], back[2]
	out, err := m.opQueue(lead, json.RawMessage(`{"order":"`+newer.ID+`","hold":"`+held.ID+`","why":"the pivot first"}`))
	if err != nil {
		t.Fatal(err)
	}
	if notes := out.(map[string]any)["notes"].([]string); len(notes) != 0 {
		t.Fatalf("notes %q, want none", notes)
	}
	if q := out.(map[string]any)["queue"].([]string); len(q) != 3 || !strings.Contains(q[0], newer.ID) || !strings.Contains(q[1], older.ID) {
		t.Fatalf("queue after ordering: %q", q)
	}
	start, wait := m.admit(m.List(nil))
	if !start[newer.ID] || start[older.ID] || start[held.ID] {
		t.Fatalf("started %v, want only the ordered sendback", start)
	}
	if !strings.Contains(wait[held.ID], "held by lead") {
		t.Fatalf("held sendback waits %q", wait[held.ID])
	}
	// A worker under way is still not the queue's.
	busy := file(t, m, "working", "")
	busy.Stage, busy.Worker = StageWorking, "wo"
	m.save(&busy, "")
	out, _ = m.opQueue(lead, json.RawMessage(`{"order":"`+busy.ID+`"}`))
	if notes := out.(map[string]any)["notes"].([]string); len(notes) != 1 || !strings.Contains(notes[0], "has left the queue") {
		t.Fatalf("notes %q", notes)
	}
}

// A worker held back from a conflict that takes a turn anyway (a message
// reopened it) and finishes has its commits taken out at once: it never waits
// at the pace to be sent the same conflict again. One that hasn't turned since
// waits, and says its PR was taken out and what waits is the merge with main
// (on 10-29 #41's "waiting for a worker (at the pace)" read as nothing pushed).
func TestFinishedAfterConflictIsTakenOutAtOnce(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	for _, id := range []string{"wb", "wf", "ww"} {
		if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
	}
	busy := file(t, m, "the working one", "")
	busy.Stage, busy.Worker = StageWorking, "wb"
	m.save(&busy, "")
	conflict := time.Now().Add(-time.Hour)
	var finished, waiting Task
	for _, w := range []string{"wf", "ww"} {
		task := file(t, m, "sent back "+w, "")
		task.Stage, task.Worker, task.Resend, task.ResendAt = StageWorking, w, "Main moved and your branch conflicts with it.", conflict
		task.PR, task.SHA = core.PR{Number: 41, URL: "https://example/pull/41"}, "2fd3f2988b7f4862a9b6fe5234d009476f2651a5"
		m.save(&task, "")
		m.C.J.MustAppend("run.turn_started", map[string]any{"id": w, "turn": 2}) // reopened by a message
		m.C.Runs.SetStatus(w, core.StatusDone, "finished")
		if w == "wf" {
			finished = task
		} else {
			waiting = task
		}
	}
	// ww's conflict came after its turn; wf's turn came after its conflict.
	waiting.ResendAt = time.Now().Add(time.Minute)
	m.save(&waiting, "")
	tick(m)
	got := stageOf(t, m, finished)
	if got.Resending() || got.Stage == StageWorking || queued(m, "wf") != "" {
		t.Fatalf("a worker that finished after its conflict waited or was sent it again: stage %q, reason %q, queued %q", got.Stage, got.Reason, queued(m, "wf"))
	}
	got = stageOf(t, m, waiting)
	if !got.Resending() || queued(m, "ww") != "" {
		t.Fatalf("a worker yet to merge main went past the pace: stage %q, resend %q", got.Stage, got.Resend)
	}
	if !strings.Contains(got.Reason, "PR #41 (2fd3f29") || !strings.Contains(got.Reason, "waiting for a worker") {
		t.Fatalf("reason %q, want it to say the PR was taken out and waits at the pace to merge main", got.Reason)
	}
	q := strings.Join(m.Queue(core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}), "\n")
	if strings.Count(q, "goes back to merge main") != 1 {
		t.Fatalf("queue: %q, want it said once", q)
	}
}

// A run's main.bundle is main's tip when the run starts, and again when a
// conflict held for the pace finally sends its worker back: main may have
// moved while it waited, and merging an older main conflicts again (issue
// #1945).
func TestBundleIsMainWhenTheWorkerStartsOrResumes(t *testing.T) {
	m := queueRig(t)
	repo := m.C.Effects.Forge.(*flakyForge).RepoPath("me/p")
	push := func(name string) string {
		seed := filepath.Join(t.TempDir(), "seed")
		mustGit(t, "", "clone", "-q", repo, seed)
		os.WriteFile(filepath.Join(seed, name), []byte(name+"\n"), 0o644)
		mustGit(t, seed, "add", "-A")
		mustGit(t, seed, "commit", "-q", "-m", name)
		mustGit(t, seed, "push", "-q", "origin", "HEAD:main")
		return strings.TrimSpace(must(t, seed, "rev-parse", "HEAD"))
	}
	heads := func(worker string) string {
		return must(t, "", "bundle", "list-heads", filepath.Join(m.C.WS.RunRO(worker), "main.bundle"))
	}
	task := file(t, m, "starts", "")
	task.Stage = StageQueued
	m.save(&task, "")
	first := push("first.txt")
	tick(m)
	got := stageOf(t, m, task)
	if got.Worker == "" {
		t.Fatalf("no worker started: stage %q, reason %q", got.Stage, got.Reason)
	}
	if h := heads(got.Worker); !strings.Contains(h, first) {
		t.Fatalf("main.bundle as the worker starts holds %q, want main at %s", h, first)
	}
	// A conflict found at the first main waits; main moves on before it goes back.
	m.C.Runs.SetStatus(got.Worker, core.StatusDone, "finished")
	got.Resend = "Main moved and your branch conflicts with it."
	m.save(&got, "")
	later := push("later.txt")
	tick(m)
	got = stageOf(t, m, task)
	if got.Resend != "" {
		t.Fatalf("the conflict wasn't sent back: reason %q", got.Reason)
	}
	if h := heads(got.Worker); !strings.Contains(h, later) {
		t.Fatalf("main.bundle as the worker resumes holds %q, want main at %s", h, later)
	}
	if msg := queued(m, got.Worker); !strings.Contains(msg, "main.bundle holds main at "+core.Short(later)) {
		t.Fatalf("the resend doesn't name the main it holds: %q", msg)
	}
}
