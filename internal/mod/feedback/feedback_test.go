package feedback

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func open(t *testing.T) (*core.Core, *Module) {
	t.Helper()
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, New(c)
}

func call(t *testing.T, c *core.Core, r core.Run, args map[string]string) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return c.Runs.Ops().Call(r, "feedback", raw)
}

// An agent's feedback is recorded as who sent it, from the run the core
// knows, whatever the arguments say; empty feedback is refused.
func TestAgentSendsFeedback(t *testing.T) {
	c, m := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Name: "writer", Project: "textkit"}}
	if _, err := call(t, c, w, map[string]string{"text": "  "}); err == nil {
		t.Fatal("empty feedback was accepted")
	}
	if _, err := call(t, c, w, map[string]string{"text": "the score needs a build first", "about": "agents/worker.md", "run": "someone-else", "project": "other"}); err != nil {
		t.Fatal(err)
	}
	got := m.List(nil)
	if len(got) != 1 {
		t.Fatalf("want one item, got %+v", got)
	}
	it := got[0]
	if it.Run != "w-1" || it.Role != "worker" || it.Name != "writer" || it.Project != "textkit" || it.About != "agents/worker.md" || it.Status != Open || it.Seen {
		t.Fatalf("recorded as %+v", it)
	}
	if m.Unseen() != 1 {
		t.Fatalf("unseen %d, want 1", m.Unseen())
	}
}

// An experiment may only report on its own copy, so it can't send feedback.
func TestExperimentCannotSendFeedback(t *testing.T) {
	c, _ := open(t)
	x := core.Run{RunSpec: core.RunSpec{ID: "x-1", Role: "worker", OneTurn: true}}
	if _, err := call(t, c, x, map[string]string{"text": "hi"}); err == nil {
		t.Fatal("an experiment sent feedback")
	}
}

// Seen, close, reopen and pass are journaled and survive a restart.
func TestFeedbackLifecycleSurvivesARestart(t *testing.T) {
	c, m := open(t)
	a, _ := m.Add(core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker"}}, "", "one")
	b, _ := m.Add(core.Run{RunSpec: core.RunSpec{ID: "w-2", Role: "reviewer"}}, "", "two")
	if err := m.Seen(a.ID); err != nil {
		t.Fatal(err)
	}
	seq := c.J.Seq()
	if err := m.Seen(a.ID); err != nil || c.J.Seq() != seq {
		t.Fatalf("seeing an item twice journaled again (%v)", err)
	}
	if _, err := m.Close(a.ID, "not now"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Passed(b.ID, "lead"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Passed(b.ID, "lead"); err == nil {
		t.Fatal("an item was passed twice")
	}
	if _, err := m.Close("nope", ""); err != core.ErrNotFound {
		t.Fatalf("closing an unknown item: %v", err)
	}
	again := New(c) // a fresh module replays the journal
	if it, _ := again.Get(a.ID); it.Status != Closed || it.Note != "not now" || !it.Seen {
		t.Fatalf("after replay a is %+v", it)
	}
	if it, _ := again.Get(b.ID); it.Status != Passed || it.Note != "passed to lead" {
		t.Fatalf("after replay b is %+v", it)
	}
	if _, err := again.Reopen(a.ID); err != nil {
		t.Fatal(err)
	}
	if it, _ := m.Get(a.ID); it.Status != Open || it.Note != "" {
		t.Fatalf("reopened a is %+v", it)
	}
}

// The sweep's digest carries open feedback only, and only for the projects
// it admits, so an isolated project's feedback stays in its own digest.
func TestDigestKeepsToItsProjects(t *testing.T) {
	_, m := open(t)
	m.Add(core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Project: "shared"}}, "tools", "shared feedback")
	m.Add(core.Run{RunSpec: core.RunSpec{ID: "w-2", Role: "worker", Project: "secret"}}, "", "isolated feedback")
	m.Add(core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}, "", "the hive's own")
	done, _ := m.Add(core.Run{RunSpec: core.RunSpec{ID: "w-3", Role: "worker", Project: "shared"}}, "", "already handled")
	m.Close(done.ID, "")
	isolated := map[string]bool{"secret": true}
	all := m.Digest(func(p string) bool { return !isolated[p] })
	if !strings.Contains(all, "shared feedback") || !strings.Contains(all, "about tools") || !strings.Contains(all, "the hive's own") {
		t.Fatalf("workspace digest is missing feedback:\n%s", all)
	}
	if strings.Contains(all, "isolated feedback") || strings.Contains(all, "already handled") {
		t.Fatalf("workspace digest carries isolated or closed feedback:\n%s", all)
	}
	own := m.Digest(func(p string) bool { return p == "secret" })
	if !strings.Contains(own, "isolated feedback") || strings.Contains(own, "shared feedback") || strings.Contains(own, "the hive's own") {
		t.Fatalf("the isolated project's digest:\n%s", own)
	}
}

func TestMessageQuotesTheFeedback(t *testing.T) {
	msg := Message(Item{Run: "w-1", Role: "worker", Name: "writer", About: "kb", Text: "line one\nline two"})
	for _, want := range []string{"writer (w-1)", "about kb", "> line one\n> line two"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
}

// Feedback linked to its fix closes when that task lands, at once when it
// already has, and leaves the sweep's digest; one still being fixed says so.
func TestFeedbackClosesWhenItsFixLands(t *testing.T) {
	c, m := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Project: "hive"}}
	a, _ := m.Add(w, "", "double turns")
	b, _ := m.Add(w, "", "main.bundle is stale")
	o, _ := m.Add(core.Run{RunSpec: core.RunSpec{ID: "w-9", Role: "worker", Project: "other"}}, "", "elsewhere")
	c.J.MustAppend("task.saved", map[string]string{"id": "t-done", "stage": "done"})
	sweep := core.Run{RunSpec: core.RunSpec{ID: "s-1", Role: "sweep"}}
	if _, err := m.Fix(sweep, a.ID, ""); err == nil {
		t.Fatal("a fix with no task was accepted")
	}
	if _, err := m.Fix(w, o.ID, "t-done"); err == nil {
		t.Fatal("a run linked another project's feedback")
	}
	raw, _ := json.Marshal(map[string]string{"fixed": a.ID, "task": "t-done"})
	if _, err := c.Runs.Ops().Call(sweep, "feedback", raw); err != nil {
		t.Fatal(err)
	}
	if it, _ := m.Get(a.ID); it.Status != Closed || it.Task != "t-done" || !strings.Contains(it.Note, "t-done") {
		t.Fatalf("an item whose fix had landed is %+v", it)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-open", "stage": "queued"})
	if _, err := m.Fix(sweep, b.ID, "t-open"); err != nil {
		t.Fatal(err)
	}
	if d := m.Digest(func(string) bool { return true }); strings.Contains(d, "double turns") || !strings.Contains(d, b.ID) || !strings.Contains(d, "fix filed: task t-open") {
		t.Fatalf("digest:\n%s", d)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-open", "stage": "working"})
	if it, _ := m.Get(b.ID); it.Status != Open {
		t.Fatalf("closed before its fix landed: %+v", it)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-open", "stage": "merged"})
	if it, _ := m.Get(b.ID); it.Status != Closed || it.Note != "fixed by task t-open" {
		t.Fatalf("after its fix merged: %+v", it)
	}
	if d := New(c).Digest(func(string) bool { return true }); strings.Contains(d, "stale") || !strings.Contains(d, "elsewhere") {
		t.Fatalf("digest after a restart:\n%s", d)
	}
}

// A task id the journal never saw is refused, so a typo leaves no mark that
// never resolves; a fix that is reverted reopens what it closed, and a
// reverted task can't be named as a fix.
func TestFixNeedsALiveTaskAndItsRevertReopens(t *testing.T) {
	c, m := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Project: "hive"}}
	a, _ := m.Add(w, "", "double turns")
	b, _ := m.Add(w, "", "stale bundle")
	if _, err := m.Fix(w, a.ID, "t-typo"); err == nil || !strings.Contains(err.Error(), "no task t-typo") {
		t.Fatalf("an unknown task was accepted: %v", err)
	}
	if it, _ := m.Get(a.ID); it.Task != "" {
		t.Fatalf("a refused fix left its mark: %+v", it)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-fix", "stage": "working"})
	if _, err := m.Fix(w, a.ID, "t-fix"); err != nil {
		t.Fatal(err)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-fix", "stage": "merged"})
	if it, _ := m.Get(a.ID); it.Status != Closed {
		t.Fatalf("not closed when its fix merged: %+v", it)
	}
	m.Close(b.ID, "not worth it")
	c.J.MustAppend("task.saved", map[string]string{"id": "t-rev", "kind": "revert", "revert_of": "t-fix", "stage": "reverting"})
	if it, _ := m.Get(a.ID); it.Status != Closed {
		t.Fatalf("reopened before the revert merged: %+v", it)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-rev", "kind": "revert", "revert_of": "t-fix", "stage": "merged"})
	it, _ := m.Get(a.ID)
	if it.Status != Open || it.Task != "" || !strings.Contains(it.Note, "reverted") {
		t.Fatalf("after its fix was reverted: %+v", it)
	}
	if it, _ := m.Get(b.ID); it.Status != Closed {
		t.Fatalf("the revert reopened an item the person closed: %+v", it)
	}
	if d := m.Digest(func(string) bool { return true }); !strings.Contains(d, "double turns") || !strings.Contains(d, "was reverted") {
		t.Fatalf("digest:\n%s", d)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-fix", "stage": "done"}) // a later save of the reverted task
	if _, err := m.Fix(w, a.ID, "t-fix"); err == nil {
		t.Fatal("a reverted task was accepted as a fix")
	}
	if d := New(c).Digest(func(string) bool { return true }); !strings.Contains(d, "double turns") {
		t.Fatalf("digest after a restart:\n%s", d)
	}
}

// A fix that is stopped before it lands (withdrawn, closed unmerged) drops
// its link, so the item reads as open again rather than "fix filed" in every
// digest; a stopped task can't be named as a fix, and an item linked before
// this rule is unlinked when the module loads.
func TestStoppedFixUnlinksItsFeedback(t *testing.T) {
	c, m := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Project: "hive"}}
	a, _ := m.Add(w, "", "tui restart test flakes")
	c.J.MustAppend("task.saved", map[string]string{"id": "t-w", "stage": "queued"})
	if _, err := m.Fix(w, a.ID, "t-w"); err != nil {
		t.Fatal(err)
	}
	c.J.MustAppend("task.saved", map[string]string{"id": "t-w", "stage": "stopped", "reason": "withdrawn by lead: overtaken"})
	it, _ := m.Get(a.ID)
	if it.Status != Open || it.Task != "" || !strings.Contains(it.Note, "t-w") || !strings.Contains(it.Note, "overtaken") {
		t.Fatalf("after its fix was withdrawn: %+v", it)
	}
	if d := m.Digest(func(string) bool { return true }); strings.Contains(d, "fix filed") || !strings.Contains(d, "was stopped") {
		t.Fatalf("digest:\n%s", d)
	}
	if _, err := m.Fix(w, a.ID, "t-w"); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("a stopped task was accepted as a fix: %v", err)
	}

	// An item linked to a task stopped before this rule existed.
	b, _ := m.Add(w, "", "old link")
	c.J.MustAppend("task.saved", map[string]string{"id": "t-old", "stage": "queued"})
	c.J.MustAppend("task.saved", map[string]string{"id": "t-old", "stage": "stopped"})
	b.Task = "t-old"
	c.J.MustAppend("feedback.saved", b)
	again := New(c)
	if it, _ := again.Get(b.ID); it.Status != Open || it.Task != "" || !strings.Contains(it.Note, "t-old") {
		t.Fatalf("an old link to a stopped task after a restart: %+v", it)
	}
	seq := c.J.Seq()
	New(c)
	if c.J.Seq() != seq {
		t.Fatal("a second load journaled the unlink again")
	}
}
