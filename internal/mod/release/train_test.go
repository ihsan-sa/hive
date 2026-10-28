package release

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// canaryStub answers each canary with the next outcome sent on it, and
// records the jobs it was given.
type canaryStub struct {
	outcome chan string
	jobs    chan CanaryJob
}

func newCanary(m *Module) *canaryStub {
	s := &canaryStub{outcome: make(chan string, 1), jobs: make(chan CanaryJob, 4)}
	m.Canary = func(ctx context.Context, j CanaryJob) core.GateResult {
		s.jobs <- j
		return core.GateResult{Outcome: <-s.outcome, Reason: "stub"}
	}
	return s
}

// verdicts waits for the journal to hold n canary verdicts.
func verdicts(t *testing.T, m *Module, n int) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); len(m.canaries()) < n; {
		if time.Now().After(end) {
			t.Fatalf("%d canary verdicts, want %d", len(m.canaries()), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		m.pmu.Lock()
		idle := m.canarying == ""
		m.pmu.Unlock()
		if idle {
			return
		}
		if time.Now().After(end) {
			t.Fatal("the canary never ended")
		}
	}
}

// The Hive that releases the Hive rides the daily train: the newest
// pre-release safe here runs as a canary against the running release, and
// only its keep moves the Hive; the next canary waits a day; a rollback keeps
// the Hive where it is and is said; a canary that couldn't run tries again
// within the hour.
func TestTrainMovesOnlyOnTheCanarysKeep(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if m.Channel() != Train {
		t.Fatalf("the Hive that releases the Hive follows %q", m.Channel())
	}
	s := newCanary(m)
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "already on v0.1.12") || !strings.Contains(msg, "v0.1.13: its canary started against v0.1.12") || string(raw) != "old" {
		t.Fatalf("before the canary: %q, %v, binary %q", msg, err, raw)
	}
	j := <-s.jobs
	if j.Tag != "v0.1.13" || j.Head != "sha-v0.1.13" || j.Base != "sha-v0.1.12" || j.Project.Name != "hive" {
		t.Fatalf("canary job %+v", j)
	}
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "the canary is running on v0.1.13") {
		t.Fatalf("while the canary runs: %q", msg)
	}
	s.outcome <- core.GateKeep
	verdicts(t, m, 1)
	msg, err = m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") || string(raw) != "v13" {
		t.Fatalf("after the keep: %q, %v, binary %q", msg, err, raw)
	}

	// On v0.1.13, v0.1.15 passes here and is published: the train still
	// waits for the next day.
	core.Version = "v0.1.13"
	g.setJobs("v0.1.15", gatesFor("success", "success"))
	g.f.PublishRelease("me/hive", "v0.1.15")
	msg, err = m.AutoUpdate()
	if err != nil || !strings.HasPrefix(msg, "already on v0.1.13") || !strings.Contains(msg, "v0.1.15: safe here; the next train leaves after") {
		t.Fatalf("within the day: %q, %v", msg, err)
	}

	// A day on: the canary couldn't run, then rolls it back.
	every := trainEvery
	t.Cleanup(func() { trainEvery = every })
	trainEvery = 0
	m.AutoUpdate()
	<-s.jobs
	s.outcome <- core.GateCantRun
	verdicts(t, m, 2)
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "v0.1.15: its canary couldn't run (stub)") {
		t.Fatalf("after a can't-run: %q", msg)
	}
	retry := canaryRetry
	t.Cleanup(func() { canaryRetry = retry })
	canaryRetry = 0
	m.AutoUpdate()
	<-s.jobs
	s.outcome <- core.GateRollback
	verdicts(t, m, 3)
	msg, err = m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "already on v0.1.13") || !strings.Contains(msg, "v0.1.15: its canary rolled it back") || string(raw) != "v13" {
		t.Fatalf("after the rollback: %q, %v, binary %q", msg, err, raw)
	}
	alerted := false
	m.C.J.Replay(func(e core.Event) {
		var a struct{ Ref string }
		if e.Kind == "alert.raised" && e.Decode(&a) == nil && a.Ref == "release:canary@v0.1.15" {
			alerted = true
		}
	})
	if !alerted {
		t.Fatal("the rollback raised no alert")
	}
	select {
	case j := <-s.jobs:
		t.Fatalf("a rolled-back candidate ran again: %+v", j)
	default:
	}
}

// Without a canary runner the train never moves, even to a newer latest
// release.
func TestTrainWithoutACanaryStays(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true})
	g.f.PublishRelease("me/hive", "v0.1.13")
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "already on v0.1.12") || !strings.Contains(msg, "no canary runner") || string(raw) != "old" {
		t.Fatalf("no canary: %q, %v, binary %q", msg, err, raw)
	}
}
