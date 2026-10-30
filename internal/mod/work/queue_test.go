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
	cfg.TurnMB = 0
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

// Every agent owns the loop: a worker holds a queued brief on its own project
// as the lead would, on the record by its run id, and one on another project
// only saying why.
func TestWorkerHoldsQueuedBrief(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.TurnMB = 1 << 30 // no turn fits: every brief stays in line
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "tidy the readme", "")
	b := file(t, m, "cap the checks", "")
	c := file(t, m, "a later idea", "")
	tick(m)
	worker := core.Run{RunSpec: core.RunSpec{ID: "w-" + a.ID, Role: "worker", Project: "p"}}
	stranger := core.Run{RunSpec: core.RunSpec{ID: "w-other", Role: "worker", Project: "q"}}
	// A new order from the stranger touches only its own project's line: p's
	// ordered brief keeps its place, with no note about a brief it never named.
	placed := stageOf(t, m, c)
	placed.Order = 1
	m.save(&placed, "")
	out, err := m.C.Runs.Ops().Call(stranger, "queue", json.RawMessage(`{"order":"nope"}`))
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
