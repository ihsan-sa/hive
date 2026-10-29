package work

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// heldRig is a gate rig whose task is ready to merge.
func heldRig(t *testing.T) (*Module, *Task, core.Project, *core.Local) {
	t.Helper()
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	return m, task, p, f
}

// pollAgain lets the next merge step ask the forge for releases at once.
func pollAgain(m *Module) {
	m.mu.Lock()
	clear(m.holdPolled)
	m.mu.Unlock()
}

func expectHeld(t *testing.T, m *Module, task *Task, p core.Project, f *core.Local) {
	t.Helper()
	for i := 0; i < 2; i++ {
		pollAgain(m)
		mergeOnce(t, m, task, p)
	}
	if task.Stage != StageMerging || !strings.Contains(task.Reason, "merges held") || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q, PR %s; want it held", task.Stage, task.Reason, prState(t, f, task.PR.Number))
	}
	if n := strings.Count(strings.Join(f.Comments(p.Repo, task.PR.Number), "\n"), "merges held"); n != 1 {
		t.Fatalf("%d hold comments, want 1", n)
	}
	// The point of effect refuses it too, whoever asks.
	main, _ := m.C.Effects.Sync(p)
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: task.SHA, Main: main}); err == nil || !strings.Contains(err.Error(), "merges held") {
		t.Fatalf("Effects.Merge under a hold: %v", err)
	}
}

func expectMerged(t *testing.T, m *Module, task *Task, p core.Project, f *core.Local) {
	t.Helper()
	pollAgain(m)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q; want merged", task.Stage, task.Reason)
	}
}

// A person's hold stops the Hive's own work at the last step, a ready PR
// that waits, and it merges once the hold is lifted.
func TestHoldStopsAMergeUntilLifted(t *testing.T) {
	m, task, p, f := heldRig(t)
	if _, err := m.SetHold(MergeHold{Project: p.Name, Why: "nothing else until H0 runs the next release", By: "person"}); err != nil {
		t.Fatal(err)
	}
	expectHeld(t, m, task, p, f)
	if err := m.LiftHold(p.Name, "person", "go ahead"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Hold(p.Name); ok {
		t.Fatal("the hold is still on")
	}
	expectMerged(t, m, task, p, f)
}

// Only the PRs a hold names may merge under it.
func TestHoldLetsItsOnlyPRMerge(t *testing.T) {
	m, task, p, f := heldRig(t)
	m.SetHold(MergeHold{Project: p.Name, Why: "only the reds fix", Only: []int{task.PR.Number + 1}, By: "person"})
	expectHeld(t, m, task, p, f)
	m.SetHold(MergeHold{Project: p.Name, Why: "only this one", Only: []int{task.PR.Number}, By: "person"})
	expectMerged(t, m, task, p, f)
	if _, ok := m.Hold(p.Name); !ok {
		t.Fatal("merging the PR it names lifted the hold")
	}
}

// A hold until the next release ends on its own once a newer release is out,
// and is kept across a restart (it lives in the journal).
func TestHoldUntilTheNextRelease(t *testing.T) {
	m, task, p, f := heldRig(t)
	f.CreateRelease(p.Repo, "v0.1.7", "", "")
	h, err := m.SetHold(MergeHold{Project: p.Name, Why: "until the next release is out", Until: UntilRelease, By: "lead-1"})
	if err != nil || len(h.Since) != 1 || h.Since[0] != "me/p@v0.1.7" {
		t.Fatalf("hold %+v, %v", h, err)
	}
	expectHeld(t, m, task, p, f)
	again := New(m.C, m.Agents, m.Chat)
	if got, ok := again.Hold(p.Name); !ok || got.Until != UntilRelease || got.By != "lead-1" {
		t.Fatalf("after a restart: %+v %v", got, ok)
	}
	f.CreateRelease(p.Repo, "v0.1.8", "", "")
	expectMerged(t, m, task, p, f)
	if _, ok := m.Hold(p.Name); ok {
		t.Fatal("the hold outlived its release")
	}
	if _, err := m.SetHold(MergeHold{Project: p.Name, Until: "friday", By: "person"}); err == nil {
		t.Fatal("an unknown condition was taken")
	}
}

// Any agent sets a hold only with the person's words, and only on its own
// project; a hold the person set themselves only they lift or change.
func TestHoldNeedsThePersonsWords(t *testing.T) {
	m, _, p, _ := heldRig(t)
	lead := core.Run{ID: "lead-1", Role: "lead"}
	if _, err := m.opHold(lead, []byte(`{"project":"p"}`)); err == nil {
		t.Fatal("a hold without the person's words was set")
	}
	if _, err := m.opHold(lead, []byte(`{"project":"p","why":"person: hold merges","only":"#3"}`)); err != nil {
		t.Fatal(err)
	}
	if h, ok := m.Hold(p.Name); !ok || h.By != "lead-1" || len(h.Only) != 1 {
		t.Fatalf("hold %+v %v", h, ok)
	}
	if _, err := m.opHold(lead, []byte(`{"project":"p","why":"person: lift it","lift":"true"}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Hold(p.Name); ok {
		t.Fatal("not lifted")
	}
	for _, elsewhere := range []core.Run{{ID: "w-q", Role: "worker", Project: "q"}, {ID: "lead-q", Role: "lead", Project: "q"}} {
		if _, err := m.opHold(elsewhere, []byte(`{"project":"p","why":"person: hold merges"}`)); err == nil {
			t.Fatalf("a %s on another project held p's merges", elsewhere.Role)
		}
	}
	worker := core.Run{ID: "w-p", Role: "worker", Project: "p"}
	if _, err := m.opHold(worker, []byte(`{"project":"p","why":"person: hold merges"}`)); err != nil {
		t.Fatalf("a worker on p couldn't hold p on the person's word: %v", err)
	}
	if _, err := m.opHold(worker, []byte(`{"project":"p","why":"person: lift it","lift":"true"}`)); err != nil {
		t.Fatalf("an agent's own hold isn't liftable by an agent: %v", err)
	}
	if _, err := m.SetHold(MergeHold{Project: p.Name, Why: "nothing until the next release", By: "person"}); err != nil {
		t.Fatal(err)
	}
	for _, ask := range []string{`{"project":"p","why":"person: lift it","lift":"true"}`, `{"project":"p","why":"person: only #3","only":"#3"}`} {
		if _, err := m.opHold(worker, []byte(ask)); err == nil || !strings.Contains(err.Error(), "set by the person") {
			t.Fatalf("a worker changed the person's own hold with %s: %v", ask, err)
		}
	}
	if h, ok := m.Hold(p.Name); !ok || h.By != "person" || len(h.Only) != 0 {
		t.Fatalf("the person's hold moved: %+v %v", h, ok)
	}
}

// A release read for one hold doesn't lift a hold set since.
func TestReleaseLiftsOnlyTheHoldItRead(t *testing.T) {
	m, _, p, f := heldRig(t)
	f.CreateRelease(p.Repo, "v0.1.7", "", "")
	old, err := m.SetHold(MergeHold{Project: p.Name, Why: "until the next release", Until: UntilRelease, By: "person"})
	if err != nil {
		t.Fatal(err)
	}
	f.CreateRelease(p.Repo, "v0.1.8", "", "")
	time.Sleep(time.Millisecond) // a later At on a coarse clock
	if _, err := m.SetHold(MergeHold{Project: p.Name, Why: "hold everything", By: "person"}); err != nil {
		t.Fatal(err)
	}
	if m.releasedSince(old) {
		t.Fatal("a release out for the old hold let the newer one's merges through")
	}
	if h, ok := m.Hold(p.Name); !ok || h.Until != "" {
		t.Fatalf("the newer hold was lifted: %+v %v", h, ok)
	}
}
