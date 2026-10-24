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
// measured against main at Main. A change to the Hive itself merges only on
// a keep for that head and main, or one carried to it on the same own diff
// (GateKept, Effects.Merge).
type GateResult struct {
	Project string    `json:"project"`
	Head    string    `json:"head"`
	Main    string    `json:"main"`
	Diff    string    `json:"diff,omitempty"` // the head's own diff (DiffID), which a keep carries along
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason"`
	Report  string    `json:"report,omitempty"`  // the run's report.md on this machine
	Sandbox string    `json:"sandbox,omitempty"` // what the children ran in
	Note    string    `json:"note,omitempty"`    // how the run was set up, when the person should hear it (the org leg skipped)
	At      time.Time `json:"at"`
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

// gateApply is the replay of a gate.judged event: the verdict on its head and
// main, and, when the gate decided, the latest decided verdict on its diff.
func (e *Effects) gateApply(g GateResult) {
	e.gates[g.Project+"@"+g.Head+"@"+g.Main] = g
	if g.Diff != "" && g.Outcome != GateCantRun {
		e.gateDiffs[g.Project+"#"+g.Diff] = g
	}
}

// GateResult is the newest gate verdict on head against main.
func (e *Effects) GateResult(project, head, main string) (GateResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.gates[project+"@"+head+"@"+main]
	return g, ok
}

// GateKept is the keep that lets head merge against main, if there is one:
// the verdict on exactly that head and main, or else the latest decided
// verdict on the same own diff (diff, DiffID), carried as reads are, so a
// main that moved without changing the change itself doesn't send it round
// the two-hour gate again. A rollback on exactly this head and main, or a
// later rollback of the same diff, stops a keep carrying. A stand-in's keep
// (sandbox "fake") counts only while the harness is still the fake one, so
// switching the harness to fake can't skip the gate for real work.
func (e *Effects) GateKept(project, head, main, diff string) (GateResult, bool) {
	e.mu.Lock()
	g, exact := e.gates[project+"@"+head+"@"+main]
	if (!exact || g.Outcome == GateCantRun) && diff != "" {
		g, exact = e.gateDiffs[project+"#"+diff]
	}
	e.mu.Unlock()
	if !exact || g.Outcome != GateKeep {
		return g, false
	}
	switch g.Sandbox {
	case "bwrap":
		return g, true
	case "fake":
		return g, e.ws.LoadConfig().Harness == "fake"
	}
	return g, false
}

// GateWaived says the person let a revert the Hive made itself merge without
// the gate's keep: a revert undoes a merge already judged, and fixing a bad
// merge must not hang on a gate that can't run or won't keep it.
func (e *Effects) GateWaived(project, sha string) bool {
	h, ok := e.HostMade(project, sha)
	return ok && h.Kind == "revert" && e.Approved("gate", project, sha)
}
