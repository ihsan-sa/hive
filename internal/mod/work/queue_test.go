package work

import (
	"encoding/json"
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
	for _, f := range []string{"internal/core/effects.go", "internal/core/runs.go", "internal/tui/views.go", "docs/log.md", "docs/notes/plan.md"} {
		os.MkdirAll(filepath.Join(seed, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(seed, f), []byte("package x\n"), 0o644)
	}
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
// max_workers; one naming the package another works in waits for it;
// the rest wait as queued, saying why. The lead's order goes first, a held
// brief waits until released, and a lower cap never stops a started worker.
func TestQueueCapsOrdersAndGroups(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	a := file(t, m, "tidy the readme", "")
	b := file(t, m, "cap the checks", "Checks run via internal/core/effects.go.")
	c := file(t, m, "hold strained turns", "Anywhere in internal/core, launch.")
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
	want(c, StageQueued, `waiting for "cap the checks" (task `+b.ID+") to land: both touch internal/core")
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
	want(c, StageQueued, "both touch internal/core")

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
	if _, err := m.C.Runs.Ops().Call(stranger, "queue", json.RawMessage(`{"hold":"`+c.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
	if got := stageOf(t, m, c); got.Held {
		t.Fatalf("a worker on another project held the brief: %+v", got)
	}
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

// A brief names its area only by paths that exist in the project, so a repo
// name or a URL doesn't tie unrelated briefs together.
func TestQueueAreas(t *testing.T) {
	m := queueRig(t)
	task := file(t, m, "fix it", "In ihsan-sa/hive, see https://github.com/x/y and internal/core/effects.go, internal/tui/, ./internal/core/runs.go.")
	got := m.areas(task)
	want := []string{"internal/core/effects.go", "internal/tui", "internal/core/runs.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("areas %q, want %q", got, want)
	}
}

// A symlink in the clone pointing outside it never names an area, so a brief
// can't test whether a path on the machine exists.
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
	task := file(t, m, "fix it", "See out/secret.txt, internal/leak.txt and internal/core/effects.go.")
	got := m.areas(task)
	want := []string{"internal/core/effects.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("areas %q, want %q", got, want)
	}
}

// A top-level directory ("in internal/.") and a doc that merges as union are
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
// top-level directory; one naming the package on its own waits for the
// worker on a file in it, but not once that worker has pushed.
func TestQueueHoldsOnlyRealConflicts(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 5
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "cap the checks", "In internal/core/effects.go; log it in docs/log.md. Nothing in internal/.")
	b := file(t, m, "hold strained turns", "In internal/core/runs.go; log it in docs/log.md. Nothing in internal/.")
	c := file(t, m, "rework the core", "All of internal/core.")
	tick(m)
	for _, task := range []Task{a, b} {
		if got := stageOf(t, m, task); got.Stage != StageWorking {
			t.Fatalf("%q: stage %q reason %q, want working", task.Title, got.Stage, got.Reason)
		}
	}
	if got := stageOf(t, m, c); got.Stage != StageQueued || !strings.Contains(got.Reason, "both touch internal/core") {
		t.Fatalf("package brief: stage %q reason %q", got.Stage, got.Reason)
	}

	// Both workers push: their tasks are testing and reviewing, and c starts.
	for id, stage := range map[string]string{a.ID: StageTesting, b.ID: StageReviewing} {
		got, _ := m.Get(id)
		got.Stage = stage
		m.save(&got, "pushed")
	}
	tick(m)
	if got := stageOf(t, m, c); got.Stage != StageWorking {
		t.Fatalf("package brief behind pushed tasks: stage %q reason %q", got.Stage, got.Reason)
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
