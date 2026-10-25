package core

import (
	"errors"
	"time"
)

// The recursive gate's outcomes (docs/gate.md): kept, rolled back, or the
// gate could not run or measure it.
const (
	GateKeep     = "keep"
	GateRollback = "rollback"
	GateCantRun  = "cant_run"
)

// GateResult is the recursive gate's verdict on a self project's head,
// measured against main at Main. A change to the Hive itself aimed at a gain
// merges only on a keep for exactly that head and main (Effects.Merge).
type GateResult struct {
	Project   string    `json:"project"`
	Head      string    `json:"head"`
	Main      string    `json:"main"`
	Outcome   string    `json:"outcome"`
	Reason    string    `json:"reason"`
	Report    string    `json:"report,omitempty"`     // the run's report.md on this machine
	Sandbox   string    `json:"sandbox,omitempty"`    // what the children ran in
	Note      string    `json:"note,omitempty"`       // how the run was set up, when the person should hear it (the org leg skipped)
	Transient bool      `json:"transient,omitempty"`  // a can't-run whose cause may pass (the machine's load, a flake on both sides): tried again soon
	Alone     bool      `json:"alone,omitempty"`      // a can't-run tied to this head alone (preparing its build, out of time): other heads at this main don't wait on it
	MainFails bool      `json:"main_fails,omitempty"` // a can't-run whose cause is main itself (the control fails a step): every head at this main would hit it
	At        time.Time `json:"at"`
}

// RecordGate records a gate run's verdict. Only the daemon calls it, from the
// gate run it started; no agent op reaches it.
func (e *Effects) RecordGate(g GateResult) error {
	if g.Project == "" || g.Head == "" || g.Main == "" {
		return errors.New("a gate result names its project, head and main")
	}
	switch g.Outcome {
	case GateKeep, GateRollback, GateCantRun:
	default:
		return errors.New("a gate outcome is keep, rollback or cant_run")
	}
	if g.At.IsZero() {
		g.At = time.Now().UTC()
	}
	e.j.MustAppend("gate.judged", g)
	return nil
}

// GateAtMain is the newest run against main at main that couldn't run for a
// cause other than its own head, until a run there judges again: every
// other head at that main would fail the same way. It outlives a restart.
func (e *Effects) GateAtMain(project, main string) (GateResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.gateMains[project+"@"+main]
	return g, ok
}

// GateResult is the newest gate verdict on head against main.
func (e *Effects) GateResult(project, head, main string) (GateResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.gates[project+"@"+head+"@"+main]
	return g, ok
}
