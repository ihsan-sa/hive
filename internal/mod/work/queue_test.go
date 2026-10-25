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
	want(d, StageQueued, "held by the lead")

	// a lands: its slot goes to e, the lead's first, not to the held d.
	got := stageOf(t, m, a)
	got.Stage = StageDone
	m.save(&got, "landed")
	tick(m)
	want(e, StageWorking, "")
	want(d, StageQueued, "held by the lead")
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
	if overlaps > 25 || free < 26 { // 26 free, and 25 wait on files cited by more than one brief (7 on tests/LANDING-security.toml)
		t.Fatalf("%d free and %d still wait on a shared file (51 did at 14:45Z)", free, overlaps)
	}
}

// A working task whose worker has gone quiet holds no brief back past its
// quiet alert: the brief on its file starts, and the quiet worker still
// counts toward max_workers.
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
	if _, wait := m.admit(m.List(nil)); !strings.Contains(wait[fix.ID], "max_workers") {
		t.Fatalf("a quiet worker's slot was given away: %q", wait[fix.ID])
	}
}
