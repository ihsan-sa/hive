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

// A withdrawal a lead, improve or a step-back made needs no lead's turn: it
// goes in the thread and the record only. One by another filer still wakes
// the lead.
func TestWithdrawnByALeadDoesNotWakeIt(t *testing.T) {
	m := queueRig(t)
	r, err := m.C.Runs.Start(core.RunSpec{Role: "lead", Brief: "lead", Persistent: true})
	if err != nil {
		t.Fatal(err)
	}
	main := r.ID
	m.C.J.MustAppend("chat.updated", chat.Chat{ID: "lead", Title: "lead", Kind: "lead", Lead: main})
	woke := func() (n int) {
		for _, e := range m.C.J.Since(0) {
			var q struct{ ID, Text string }
			if e.Kind == "run.queued" && e.Decode(&q) == nil && q.ID == main && strings.Contains(q.Text, "[notification") {
				n++
			}
		}
		return
	}
	lead := core.Run{RunSpec: core.RunSpec{ID: main, Role: "lead"}}
	filer := core.Run{RunSpec: core.RunSpec{ID: "w-one", Role: "worker", Project: "p"}}
	for _, r := range []core.Run{lead, {RunSpec: core.RunSpec{ID: "improve", Project: "p"}}, {RunSpec: core.RunSpec{ID: "stepback-p", Role: "stepback", Project: "p"}}, filer} {
		task, err := m.File(Brief{Project: "p", Title: "change by " + r.ID, Done: "done", From: r.ID})
		if err != nil {
			t.Fatal(err)
		}
		task.Held, task.HeldBy = true, main
		m.save(&task, "held")
		if _, err := m.Withdraw(r, task.ID, "", "not needed"); err != nil {
			t.Fatal(err)
		}
	}
	if n := woke(); n != 1 {
		t.Fatalf("withdrawals woke the lead %d times, want 1 (the worker's)", n)
	}
	if n := len(m.Chat.Messages("lead")); n != 4 {
		t.Fatalf("the chat has %d messages, want the 4 withdrawals in the feed", n)
	}
}

// A worker that finished blocked with no PR left a task no op could end
// (hive-eval 261028-132616-bf6f1f35c9): withdraw takes it, folded into
// another task or dropped, and its worker isn't woken again. One with a PR
// is still pr_close's.
func TestWithdrawABlockedTaskWithNoPR(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	into, err := m.File(Brief{Project: "p", Title: "The wider ask", Done: "done", From: "w-one"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.C.Runs.Start(core.RunSpec{Role: "worker", Project: "p", Brief: "x"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.File(Brief{Project: "p", Title: "The narrow ask", Done: "done", From: "w-one", Apart: true})
	if err != nil {
		t.Fatal(err)
	}
	tick(m)
	task, _ = m.Get(task.ID)
	task.Stage, task.Reason, task.Worker = StageBlocked, "the worker finished without any commits", w.ID
	m.save(&task, task.Reason)

	withPR := task
	withPR.PR.Number = 7
	if err := m.mayChange(lead, withPR, "withdrawn"); err == nil || !strings.Contains(err.Error(), "pr_close") {
		t.Fatalf("a blocked task with a PR was withdrawable: %v", err)
	}
	if _, err := m.Amend(lead, task.ID, "x", "", "", ""); err == nil {
		t.Fatal("a blocked task was amended")
	}
	if _, err := m.Withdraw(lead, task.ID, into.ID, "folded into the wider ask"); err != nil {
		t.Fatal(err)
	}
	tick(m)
	got, _ := m.Get(task.ID)
	if r, _ := m.C.Runs.Get(w.ID); !got.Withdrawn() || got.IssueOpen || !r.Terminal() {
		t.Fatalf("not withdrawn with its issue closed and worker stopped: %s %q open=%v run=%s", got.Stage, got.Reason, got.IssueOpen, r.Status)
	}
	if !strings.Contains(got.Reason, "covered by task "+into.ID) {
		t.Fatalf("the reason doesn't name the task that covers it: %q", got.Reason)
	}
	if got.Issue.Number != 0 {
		if cs := strings.Join(m.C.Effects.Forge.(*flakyForge).Comments("me/p", got.Issue.Number), "\n"); !strings.Contains(cs, "finished blocked with no PR") {
			t.Fatalf("the issue doesn't say it was withdrawn after its worker blocked: %q", cs)
		}
	}
	if err := m.Recheck(got.ID, "", "lead-1"); err == nil {
		t.Fatal("a withdrawn blocked task came back with recheck")
	}
}
