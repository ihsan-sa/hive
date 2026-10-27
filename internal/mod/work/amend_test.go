package work

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// The lead re-filed a wider copy of the person's report and held the old one
// six times over (issue #405): a waiting brief is amended in place instead,
// and a superseded one withdrawn, its issue closed. Only its filer or the
// lead may do either, and not once a worker has started; a withdrawn task
// doesn't come back with recheck.
func TestAmendAndWithdrawAWaitingBrief(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	other := core.Run{RunSpec: core.RunSpec{ID: "w-two", Role: "worker", Project: "p"}}
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

	if _, err := op(m.opAmend, other, map[string]any{"task": first["task"], "title": "x"}); err == nil || !strings.Contains(err.Error(), "only its filer or the lead") {
		t.Fatalf("another worker amended a brief it didn't file: %v", err)
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
	if cs := strings.Join(forge.Comments("me/p", got.Issue.Number), "\n"); !strings.Contains(cs, "amended by lead-1") || !strings.Contains(cs, "long paste") {
		t.Fatalf("the issue doesn't show the amended brief: %q", cs)
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
}

// A withdrawal a lead, improve or a step-back made needs no lead's turn: it
// goes in the thread and the record only. One by another filer still wakes
// a lead, and the project's own lead rather than the main one when the
// project has one; a change only the person approves still reaches the main
// lead's chat, the one the person reads.
func TestWithdrawnByALeadDoesNotWakeIt(t *testing.T) {
	m := queueRig(t)
	start := func(c chat.Chat) string {
		r, err := m.C.Runs.Start(core.RunSpec{Role: "lead", Brief: "lead", Persistent: true})
		if err != nil {
			t.Fatal(err)
		}
		c.Lead = r.ID
		m.C.J.MustAppend("chat.updated", c)
		return r.ID
	}
	main := start(chat.Chat{ID: "lead", Title: "lead", Kind: "lead"})
	own := start(chat.Chat{ID: "project:p", Title: "p", Kind: "project", Project: "p"})
	woke := func(run string) (n int) {
		for _, e := range m.C.J.Since(0) {
			var q struct{ ID, Text string }
			if e.Kind == "run.queued" && e.Decode(&q) == nil && q.ID == run && strings.Contains(q.Text, "[notification") {
				n++
			}
		}
		return
	}
	lead := core.Run{RunSpec: core.RunSpec{ID: main, Role: "lead"}}
	filer := core.Run{RunSpec: core.RunSpec{ID: "w-one", Role: "worker", Project: "p"}}
	var ids []string
	for _, r := range []core.Run{lead, {RunSpec: core.RunSpec{ID: "improve", Project: "p"}}, filer} {
		task, err := m.File(Brief{Project: "p", Title: "change by " + r.ID, Done: "done", From: r.ID})
		if err != nil {
			t.Fatal(err)
		}
		task.Held, task.HeldBy = true, main
		m.save(&task, "held")
		ids = append(ids, task.ID)
		if _, err := m.Withdraw(r, task.ID, "", "not needed"); err != nil {
			t.Fatal(err)
		}
	}
	if n, p := woke(main), woke(own); n != 0 || p != 1 {
		t.Fatalf("withdrawals woke the main lead %d times and p's lead %d, want 0 and 1 (the worker's)", n, p)
	}
	if n := len(m.Chat.Messages("lead")); n != 3 {
		t.Fatalf("the main chat has %d messages, want the 3 withdrawals in the feed", n)
	}
	task, _ := m.Get(ids[0])
	m.askPerson(&task, approveNotice(task.Label(), []string{".hive/goals.md"}, ""))
	if n := woke(main); n != 1 {
		t.Fatalf("an approval woke the main lead %d times, want 1", n)
	}
}
