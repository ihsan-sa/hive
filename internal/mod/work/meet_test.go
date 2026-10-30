package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// toldRun are the texts sent to run id, in order.
func toldRun(m *Module, id string) []string {
	var out []string
	m.C.J.Replay(func(e core.Event) {
		var s struct{ ID, Text string }
		if e.Kind == "run.queued" && e.Decode(&s) == nil && s.ID == id {
			out = append(out, s.Text)
		}
	})
	return out
}

// Two briefs naming the same file start side by side and meet: the later
// one's brief names the earlier run, the earlier run is told of the later,
// the meeting is journaled, and each one's finish reply names the other. A
// union doc both name, and a file only one names, make no meeting.
func TestOverlappingBriefsMeet(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 5
	m.C.WS.SaveConfig(cfg)
	a := file(t, m, "cap the checks", "In internal/core/effects.go; log it in docs/log.md.")
	c := file(t, m, "draw the pane", "In internal/tui/views.go; log it in docs/log.md.")
	tick(m)
	b := file(t, m, "split the effects", "Again internal/core/effects.go.")
	got, _ := m.Get(b.ID)
	got.Now = true // started past the file hold, as with no hold at all
	m.save(&got, "now")
	tick(m)
	a, b, c = stageOf(t, m, a), stageOf(t, m, b), stageOf(t, m, c)
	for _, x := range []Task{a, b, c} {
		if x.Stage != StageWorking {
			t.Fatalf("%q: stage %q reason %q", x.Title, x.Stage, x.Reason)
		}
	}
	rb, _ := m.C.Runs.Get(b.Worker)
	if !strings.Contains(rb.Brief, "## Working beside you") || !strings.Contains(rb.Brief, a.Worker+" is on \"cap the checks\"") || !strings.Contains(rb.Brief, "`internal/core/effects.go`") || strings.Contains(rb.Brief, c.Worker) {
		t.Fatalf("b's brief doesn't name a alone:\n%s", rb.Brief)
	}
	ra, _ := m.C.Runs.Get(a.Worker)
	if strings.Contains(ra.Brief, "Working beside you") {
		t.Fatalf("a started alone, yet its brief names neighbours:\n%s", ra.Brief)
	}
	if msgs := toldRun(m, a.Worker); len(msgs) != 1 || !strings.Contains(msgs[0], b.Worker+" started on \"split the effects\"") {
		t.Fatalf("a's worker was told %q", msgs)
	}
	if msgs := toldRun(m, c.Worker); len(msgs) != 0 {
		t.Fatalf("c shares only a union doc, yet was told %q", msgs)
	}
	if msgs := toldRun(m, b.Worker); len(msgs) != 0 {
		t.Fatalf("b's brief named a, yet it was told again: %q", msgs)
	}
	met := 0
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "work.met" {
			met++
			if !strings.Contains(string(e.Data), a.Worker) || !strings.Contains(string(e.Data), b.Worker) {
				t.Errorf("met: %s", e.Data)
			}
		}
	})
	if met != 1 {
		t.Fatalf("%d work.met events, want 1", met)
	}
	out, err := m.C.Runs.Ops().Call(ra, "finish", json.RawMessage(`{"status":"done","account":"capped"}`))
	if s, _ := out.(string); err != nil || !strings.HasPrefix(s, "recorded") || !strings.Contains(s, b.Worker+" on \"split the effects\"") {
		t.Fatalf("a's finish reply %q, %v", out, err)
	}
	rc, _ := m.C.Runs.Get(c.Worker)
	if out, _ := m.C.Runs.Ops().Call(rc, "finish", json.RawMessage(`{"status":"done","account":"drawn"}`)); strings.Contains(out.(string), "same files") {
		t.Fatalf("c's finish reply names neighbours: %q", out)
	}
}

// Two starting in the same moment still meet: the one whose brief came
// first, naming no one, is told of the other.
func TestSimultaneousStartsMeet(t *testing.T) {
	m := queueRig(t)
	a := file(t, m, "cap the checks", "In internal/core/effects.go.")
	tick(m)
	a = stageOf(t, m, a)
	b := file(t, m, "split the effects", "Again internal/core/effects.go.")
	got, _ := m.Get(b.ID)
	got.Now = true
	m.save(&got, "now")
	tick(m)
	b = stageOf(t, m, b)
	m.meet(&b, "a brief rendered before a was working")
	if msgs := toldRun(m, b.Worker); len(msgs) != 1 || !strings.Contains(msgs[0], a.Worker+" is on \"cap the checks\"") {
		t.Fatalf("b was told %q", msgs)
	}
}

// A conflict names the merged task that changed the branch's files on main
// since the branch's base, and how to reach it.
func TestConflictNamesWhatMovedMain(t *testing.T) {
	m := queueRig(t)
	p, _ := m.C.Projects.Get("p")
	dir := m.C.WS.Project("p")
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	rev := func(r string) string {
		out, err := core.Git(dir, "rev-parse", r)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	base := rev("main")
	commit := func(f, content, msg string) string {
		os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644)
		mustGit(t, dir, "add", f)
		mustGit(t, dir, "commit", "-q", "-m", msg)
		return rev("HEAD")
	}
	mustGit(t, dir, "checkout", "-q", "-b", "side", base)
	head := commit("internal/core/effects.go", "package core // mine\n", "mine")
	mustGit(t, dir, "checkout", "-q", "main")
	other := commit("internal/tui/views.go", "package tui // elsewhere\n", "elsewhere")
	moved := commit("internal/core/effects.go", "package core // theirs\n", "theirs")
	main := rev("main")
	o := Task{ID: "to", Project: "p", Title: "rework effects", Stage: StageMerged, Worker: "w-to", MergeSHA: moved}
	m.save(&o, "")
	e := Task{ID: "te", Project: "p", Title: "draw elsewhere", Stage: StageMerged, Worker: "w-te", MergeSHA: other}
	m.save(&e, "")
	got := m.movedBy(p, head, main)
	if !strings.Contains(got, "\"rework effects\" (task to, run w-to) merged as "+core.Short(moved)+" and changed `internal/core/effects.go`") || strings.Contains(got, "draw elsewhere") {
		t.Fatalf("movedBy:\n%s", got)
	}
	if m.movedBy(p, head, base) != "" || m.movedBy(p, "", main) != "" {
		t.Fatal("named a mover where none moved main")
	}
}
