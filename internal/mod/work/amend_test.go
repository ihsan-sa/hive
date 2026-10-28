package work

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The lead re-filed a wider copy of the person's report and held the old one
// six times over (issue #405): a waiting brief is amended in place instead,
// and a superseded one withdrawn, its issue closed. Any agent on its project
// may do either (the person's own ask only they), and not once a worker has started; a withdrawn task
// doesn't come back with recheck.
func TestAmendAndWithdrawAWaitingBrief(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	other := core.Run{RunSpec: core.RunSpec{ID: "w-two", Role: "worker", Project: "q"}}
	op := func(fn func(core.Run, json.RawMessage) (any, error), r core.Run, a map[string]any) (map[string]string, error) {
		raw, _ := json.Marshal(a)
		out, err := fn(r, raw)
		if err != nil {
			return nil, err
		}
		return out.(map[string]string), nil
	}
	first := brief(t, m, "w-one", map[string]any{"title": "The chat box drops a pasted image", "done": "A pasted image shows in the thread"})
	second := brief(t, m, "w-one", map[string]any{"title": "The control center's footer hides the last line", "done": "The last line shows", "apart": true})
	for _, id := range []string{first["task"], second["task"]} { // held as they're filed, so both wait
		task, _ := m.Get(id)
		task.Held, task.HeldBy = true, "lead-1"
		m.save(&task, "held by lead-1")
	}
	tick(m)

	if _, err := op(m.opAmend, other, map[string]any{"task": first["task"], "title": "x"}); err == nil || !strings.Contains(err.Error(), "act only on your own") {
		t.Fatalf("a worker on another project amended a brief: %v", err)
	}
	if _, err := op(m.opAmend, lead, map[string]any{"task": first["task"]}); err == nil {
		t.Fatal("an amend that changes nothing was taken")
	}
	if _, err := op(m.opAmend, lead, map[string]any{"task": first["task"], "title": "The chat box drops a pasted image or a long paste",
		"known": "Also a paste over 10k characters is cut.", "why": "the person's second report"}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(first["task"])
	if got.Title != "The chat box drops a pasted image or a long paste" || got.Done != "A pasted image shows in the thread" || got.Known != "Also a paste over 10k characters is cut." || !got.Queued() || !got.Held {
		t.Fatalf("not amended in place: %+v", got)
	}
	forge := m.C.Effects.Forge.(*flakyForge)
	if title, body := forge.Issue("me/p", got.Issue.Number); title != got.Label() || !strings.Contains(body, "10k characters") || !strings.Contains(body, marker(got.ID)) {
		t.Fatalf("the issue doesn't show the amended brief: %q %q", title, body)
	}
	if cs := strings.Join(forge.Comments("me/p", got.Issue.Number), "\n"); !strings.Contains(cs, "amended by lead-1 (title, known): the person's second report") {
		t.Fatalf("the issue doesn't say who amended it and why: %q", cs)
	}

	if _, err := op(m.opWithdraw, lead, map[string]any{"task": second["task"]}); err == nil {
		t.Fatal("a withdraw with no why was taken")
	}
	if _, err := op(m.opWithdraw, other, map[string]any{"task": second["task"], "why": "x"}); err == nil {
		t.Fatal("another worker withdrew a brief it didn't file")
	}
	if _, err := op(m.opWithdraw, lead, map[string]any{"task": second["task"], "into": first["task"], "why": "folded into the chat task"}); err != nil {
		t.Fatal(err)
	}
	tick(m)
	w, _ := m.Get(second["task"])
	if !w.Withdrawn() || w.IssueOpen || w.Worker != "" {
		t.Fatalf("not withdrawn with its issue closed: %s %q open=%v worker=%q", w.Stage, w.Reason, w.IssueOpen, w.Worker)
	}
	if cs := strings.Join(forge.Comments("me/p", w.Issue.Number), "\n"); !strings.Contains(cs, "folded into the chat task") || !strings.Contains(cs, "Covered by #") {
		t.Fatalf("the issue doesn't say why it was withdrawn: %q", cs)
	}
	if err := m.Recheck(w.ID, "", "lead-1"); err == nil {
		t.Fatal("a withdrawn task came back with recheck")
	}

	// Once its worker starts, the brief is its worker's to cover: no amend.
	if _, err := m.opQueue(lead, json.RawMessage(`{"release":["`+first["task"]+`"]}`)); err != nil {
		t.Fatal(err)
	}
	tick(m)
	if s, _ := m.Get(first["task"]); s.Stage != StageWorking {
		t.Fatalf("the released brief didn't start: %s %s", s.Stage, s.Reason)
	}
	if _, err := op(m.opWithdraw, lead, map[string]any{"task": first["task"], "why": "x"}); err == nil || !strings.Contains(err.Error(), "left the queue") {
		t.Fatalf("a started task was withdrawn: %v", err)
	}

	// Its filer can still retract it once its ask turns out void: the
	// worker is stopped and the task doesn't come back.
	if _, err := m.Retract(other, first["task"], "void"); err == nil {
		t.Fatal("a run that didn't file it retracted a started task")
	}
	s, _ := m.Get(first["task"])
	if _, err := m.Retract(core.Run{RunSpec: core.RunSpec{ID: s.From}}, s.ID, "its test was never flaky"); err != nil {
		t.Fatal(err)
	}
	s, _ = m.Get(first["task"])
	if r, _ := m.C.Runs.Get(s.Worker); !s.Withdrawn() || !r.Terminal() {
		t.Fatalf("not retracted with its worker stopped: %s %q run=%s", s.Stage, s.Reason, r.Status)
	}
	if err := m.Recheck(s.ID, "", "lead-1"); err == nil {
		t.Fatal("a retracted task came back with recheck")
	}
}
