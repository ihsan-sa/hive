package work

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Plain Claude Code did ten of the Hive's own issues in one session in an
// hour, while the Hive took 11-15 hours for three, on per-task overhead: the
// lead batches related fixes into one task, so one worker takes them all and
// lands them as one PR. The others are withdrawn into it, their issues closed
// as covered by the batch's; a gain isn't batched with fixes, and a worker
// can't batch briefs on another project.
func TestBatchFoldsRelatedFixesIntoOneTask(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	other := core.Run{RunSpec: core.RunSpec{ID: "w-two", Role: "worker", Project: "q"}}
	batch := func(r core.Run, a map[string]any) (map[string]string, error) {
		raw, _ := json.Marshal(a)
		out, err := m.opBatch(r, raw)
		if err != nil {
			return nil, err
		}
		return out.(map[string]string), nil
	}
	fix := func(title, why string) string {
		return brief(t, m, "w-one", map[string]any{"title": title, "done": title + " no more", "known": "seen in " + why, "aim": "fix", "why": why, "apart": true})["task"]
	}
	a := fix("The queue drops a held brief on restart", "held brief lost")
	b := fix("The footer hides the last line", "footer cut")
	c := fix("A stalled checkpoint never retries", "checkpoint stall")
	g := brief(t, m, "w-one", map[string]any{"title": "Make the dashboard faster", "done": "It renders in half the time", "aim": "gain", "apart": true})["task"]
	for _, id := range []string{a, b, c, g} {
		task, _ := m.Get(id)
		task.Held, task.HeldBy = true, "lead-1"
		m.save(&task, "held by lead-1")
	}
	tick(m)

	if _, err := batch(lead, map[string]any{"tasks": a + "," + b}); err == nil {
		t.Fatal("a batch with no why was taken")
	}
	if _, err := batch(other, map[string]any{"tasks": a + "," + b, "why": "x"}); err == nil {
		t.Fatal("a worker on another project batched its briefs")
	}
	if _, err := batch(lead, map[string]any{"tasks": []string{a, b, g}, "why": "x"}); err == nil || !strings.Contains(err.Error(), "fixes with fixes") {
		t.Fatalf("a gain was batched with fixes: %v", err)
	}
	if s, _ := m.Get(b); !s.Queued() || len(s.Joins) != 0 {
		t.Fatalf("a refused batch left %s changed: %s %q", b, s.Stage, s.Reason)
	}
	out, err := batch(lead, map[string]any{"tasks": a + "," + b + "," + c + "," + b, "why": "three small fixes in the work module"})
	if err != nil {
		t.Fatal(err)
	}
	if out["task"] != a || out["batched"] != b+","+c {
		t.Fatalf("batched wrong: %v", out)
	}
	tick(m)
	got, _ := m.Get(a)
	if !got.Queued() || len(got.Joins) != 2 || !strings.Contains(got.Known, "The footer hides the last line") || !strings.Contains(got.Known, "checkpoint stall") || len(got.Batched()) != 2 {
		t.Fatalf("the batch doesn't carry both asks: %s %+v\n%s", got.Stage, got.Joins, got.Known)
	}
	forge := m.C.Effects.Forge.(*flakyForge)
	for _, id := range []string{b, c} {
		w, _ := m.Get(id)
		if !w.Withdrawn() || w.IssueOpen || !strings.Contains(w.Reason, "batched into task "+a) {
			t.Fatalf("%s not withdrawn into the batch: %s %q open=%v", id, w.Stage, w.Reason, w.IssueOpen)
		}
		if cs := strings.Join(forge.Comments("me/p", w.Issue.Number), "\n"); !strings.Contains(cs, "Covered by #") {
			t.Fatalf("issue #%d doesn't say the batch covers it: %q", w.Issue.Number, cs)
		}
	}
	if !strings.Contains(batchNote(&got), "2 more tasks") {
		t.Fatalf("the brief doesn't say it is a batch: %q", batchNote(&got))
	}

	// A batch can take more once its worker has started; the worker is told.
	if _, err := m.opQueue(lead, json.RawMessage(`{"release":["`+a+`"]}`)); err != nil {
		t.Fatal(err)
	}
	tick(m)
	if s, _ := m.Get(a); s.Stage != StageWorking {
		t.Fatalf("the batch didn't start: %s %s", s.Stage, s.Reason)
	}
	d := fix("The stop op misses a parked worker", "parked worker")
	if _, err := batch(lead, map[string]any{"tasks": a + "," + d, "why": "same module"}); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Get(a); len(s.Joins) != 3 || !strings.Contains(batchNote(&s), "3 more tasks") {
		t.Fatalf("the working batch didn't take a fourth: %+v", s.Joins)
	}
}
