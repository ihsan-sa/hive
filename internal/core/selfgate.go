package core

import (
	"errors"
	"slices"
	"strings"
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
	Conflicts []string  `json:"conflicts,omitempty"`  // a can't-run because the head conflicts with main in these files: its worker merges main
	Alone     bool      `json:"alone,omitempty"`      // a can't-run tied to this head alone (preparing its build, out of time): other heads at this main don't wait on it
	MainFails bool      `json:"main_fails,omitempty"` // a can't-run whose cause is main itself (the control fails a step): every head at this main would hit it
	Repeats   int       `json:"-"`                    // how many MainFails at this main came before it in a row, counted on replay
	Started   time.Time `json:"started,omitempty"`    // when the run began: one that ran beside the last MainFails doesn't add a repeat
	// Measured says the outcome came from comparing both sides' results, so
	// the judge weighs it (judge.go); a candidate that failed before it was
	// measured (it doesn't build, fails its own tests) is rolled back as is.
	Measured bool `json:"measured,omitempty"`
	// MetricGain says a keep's gains include a metric beyond the noise
	// (gate.Verdict.MetricGain), so a parked judge needn't hold it (clearKeep).
	MetricGain bool      `json:"metric_gain,omitempty"`
	At         time.Time `json:"at"`
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

// gateMeasures are what gives a gate verdict its meaning besides the
// candidate itself: the gate's own code, the score's, the project's settings
// and the tests, among them main's whole e2e suite, which grades both sides.
// The score's evaluator paths join them (StandingGate).
var gateMeasures = []string{"internal/gate", "internal/app/selfgate.go", "cmd/hive/gate.go", "cmd/hive-score", "internal/e2e", ".hive/project.json", "tests"}

// StandingGate is the gate's verdict on head that holds at main: the one
// against main itself, else the newest measured keep or rollback against an
// older main, while main's commits since touch none of head's files and
// none of gateMeasures or the score's evaluator. A gate takes longer than
// main takes to move, so a verdict tied to the exact main was rarely used.
// The verdict keeps its own Main, so its judgement is looked up there.
func (e *Effects) StandingGate(p Project, head, main string) (GateResult, bool) {
	if g, ok := e.GateResult(p.Name, head, main); ok {
		return g, true
	}
	e.mu.Lock()
	var older []GateResult
	for _, g := range e.gates {
		if g.Project == p.Name && g.Head == head && g.Measured && g.Outcome != GateCantRun {
			older = append(older, g)
		}
	}
	e.mu.Unlock()
	slices.SortFunc(older, func(a, b GateResult) int { return b.At.Compare(a.At) })
	for _, g := range older {
		if e.gateStands(p, g, main) {
			return g, true
		}
	}
	return GateResult{}, false
}

// gateStands says whether g, measured against an older main, still means
// the same at main. An answer git gave is kept: the commits don't change.
func (e *Effects) gateStands(p Project, g GateResult, main string) bool {
	key := p.Name + "@" + g.Head + "@" + g.Main + "@" + main
	e.mu.Lock()
	v, ok := e.standing[key]
	e.mu.Unlock()
	if ok {
		return v
	}
	v, err := e.untouched(p, g, main)
	if err != nil {
		return false
	}
	e.mu.Lock()
	e.standing[key] = v
	e.mu.Unlock()
	return v
}

func (e *Effects) untouched(p Project, g GateResult, main string) (bool, error) {
	dir := e.ws.Project(p.Name)
	if base, err := Git(dir, "merge-base", g.Main, main); err != nil {
		return false, err
	} else if base != g.Main {
		return false, nil // main was rewritten, or the verdict's main is newer
	}
	moved, err := gitFiles(dir, g.Main, main)
	if err != nil {
		return false, err
	}
	hit, err := e.touches(p, g, main, moved)
	return !hit, err
}

// Unseats reports whether merging head into main would end g's standing
// there: head changes a file g's own change does, or one of what the gate
// measures with. A merge that leaves both alone keeps the verdict, so only
// such a merge need wait behind a measured keep (the work module's
// keptFirst).
func (e *Effects) Unseats(p Project, g GateResult, main, head string) (bool, error) {
	dir := e.ws.Project(p.Name)
	base, err := Git(dir, "merge-base", main, head)
	if err != nil {
		return false, err
	}
	moved, err := gitFiles(dir, base, head)
	if err != nil {
		return false, err
	}
	return e.touches(p, g, main, moved)
}

// touches says whether any of moved is one of g's own files or under what
// the gate measures with at main.
func (e *Effects) touches(p Project, g GateResult, main string, moved []string) (bool, error) {
	dir := e.ws.Project(p.Name)
	cbase, err := Git(dir, "merge-base", g.Main, g.Head)
	if err != nil {
		return false, err
	}
	own, err := gitFiles(dir, cbase, g.Head)
	if err != nil {
		return false, err
	}
	guard := gateMeasures
	if s := e.Config(p, main).Score; s != nil {
		guard = append(slices.Clone(guard), s.Evaluator()...)
	}
	for _, f := range moved {
		if under(f, guard) || slices.Contains(own, f) {
			return true, nil
		}
	}
	return false, nil
}

// gitFiles are the files that differ between from and to in dir.
func gitFiles(dir, from, to string) ([]string, error) {
	out, err := Git(dir, "-c", "core.quotePath=false", "diff", "--name-only", "-z", "--no-renames", from, to)
	return slices.DeleteFunc(strings.Split(out, "\x00"), func(f string) bool { return f == "" }), err
}
