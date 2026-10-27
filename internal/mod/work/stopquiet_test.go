package work

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The lead ends a working task's quiet worker (issue #846: a trial worker sat
// quiet for 25 hours, and only the person could stop it): refused while the
// worker takes turns, with no why, or on another stage; once quiet, the run
// ends blocked, the task goes blocked with the lead's why and recheck takes
// it back; with fresh it goes straight to a new worker with the old one's notes.
func TestLeadStopsAQuietWorker(t *testing.T) {
	m := queueRig(t)
	p, _ := m.C.Projects.Get("p")
	start := func(id string) Task {
		task := file(t, m, "the stuck one "+id, "In internal/core/effects.go.")
		if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "worker", Project: "p", Harness: "fake", Checkout: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		task.Stage, task.Worker = StageWorking, id
		m.save(&task, "")
		return task
	}
	stuck := start("wq")
	if _, err := callOp(m, leadRun, "stop", `{"task":"`+stuck.ID+`","why":"quiet a day"}`); err == nil || !strings.Contains(err.Error(), "not quiet") {
		t.Fatalf("stopped a worker that isn't quiet: %v", err)
	}
	m.C.Runs.SetStatus("wq", core.StatusQuiet, "keeps ending turns without finishing")
	if _, err := callOp(m, leadRun, "stop", `{"task":"`+stuck.ID+`"}`); err == nil || !strings.Contains(err.Error(), "why") {
		t.Fatalf("stopped with no why: %v", err)
	}
	other := core.Run{RunSpec: core.RunSpec{ID: "w2", Role: "worker", Project: "elsewhere"}}
	if _, err := callOp(m, other, "stop", `{"task":"`+stuck.ID+`","why":"x"}`); err == nil {
		t.Fatal("a worker on another project stopped this one's worker")
	}
	if _, err := callOp(m, leadRun, "stop", `{"task":"`+stuck.ID+`","why":"quiet a day"}`); err != nil {
		t.Fatal(err)
	}
	if r, _ := m.C.Runs.Get("wq"); r.Status != core.StatusBlocked || !strings.Contains(r.Reason, "quiet a day") {
		t.Fatalf("run %s: %q", r.Status, r.Reason)
	}
	m.C.Runs.Send("wq", "are you there?") // refused, as the run has ended
	if r, _ := m.C.Runs.Get("wq"); r.Status != core.StatusBlocked {
		t.Fatalf("a message woke the stopped run: %s %q", r.Status, r.Reason)
	}
	cur, _ := m.Get(stuck.ID)
	if err := m.checkWorker(&cur, p); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(stuck.ID); got.Stage != StageBlocked || got.ByPerson || !strings.Contains(got.Reason, "lead: quiet a day") {
		t.Fatalf("task %s (by person %v): %q", got.Stage, got.ByPerson, got.Reason)
	}
	if _, err := callOp(m, leadRun, "stop", `{"task":"`+stuck.ID+`","why":"again"}`); err == nil {
		t.Fatal("stopped a blocked task")
	}
	if err := m.Recheck(stuck.ID, "", "lead"); err != nil {
		t.Fatalf("a task whose quiet worker the lead stopped doesn't come back: %v", err)
	}

	fresh := start("wf")
	m.C.Runs.SetStatus("wf", core.StatusQuiet, "keeps ending turns without finishing")
	if _, err := callOp(m, leadRun, "stop", `{"task":"`+fresh.ID+`","why":"try again","fresh":true}`); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(fresh.ID); got.Stage != StageQueued || got.Worker != "" || !strings.Contains(got.Known, "A previous worker (wf) stopped") {
		t.Fatalf("not handed on: %s worker %q known %q", got.Stage, got.Worker, got.Known)
	}
}

// The point of #846 through a tick: a quiet worker's alert names the stop op,
// and once the lead stops it with fresh the task gets a new worker on the
// next tick, under max_workers 1, instead of waiting a day on the quiet one.
func TestAStoppedQuietWorkerIsReplacedOnTheNextTick(t *testing.T) {
	m := queueRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1
	m.C.WS.SaveConfig(cfg)
	p, _ := m.C.Projects.Get("p")
	task := file(t, m, "the stuck one", "In internal/core/effects.go.")
	tick(m)
	got, _ := m.Get(task.ID)
	if got.Stage != StageWorking || got.Worker == "" {
		t.Fatalf("not started: %s %q", got.Stage, got.Reason)
	}
	first := got.Worker
	m.C.Runs.SetStatus(first, core.StatusQuiet, "keeps ending turns without finishing")
	if err := m.checkWorker(&got, p); err != nil {
		t.Fatal(err)
	}
	var alert string
	for _, e := range m.C.J.Since(0) {
		if e.Kind == "alert.raised" && strings.Contains(string(e.Data), `"quiet"`) {
			alert = string(e.Data)
		}
	}
	if !strings.Contains(alert, "hive agent stop --task "+task.ID) {
		t.Fatalf("the quiet alert doesn't name the stop op: %s", alert)
	}
	if _, err := callOp(m, leadRun, "stop", `{"task":"`+task.ID+`","why":"quiet for hours","fresh":true}`); err != nil {
		t.Fatal(err)
	}
	tick(m)
	got, _ = m.Get(task.ID)
	if got.Stage != StageWorking || got.Worker == "" || got.Worker == first {
		t.Fatalf("no fresh worker after the stop: %s worker %q (first %s) %q", got.Stage, got.Worker, first, got.Reason)
	}
	if r, _ := m.C.Runs.Get(got.Worker); r.Terminal() || r.Status == core.StatusQuiet {
		t.Fatalf("the fresh worker isn't going: %s %q", r.Status, r.Reason)
	}
}
