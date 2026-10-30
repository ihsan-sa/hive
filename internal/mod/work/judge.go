package work

import (
	"encoding/json"
	"fmt"

	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
)

// The judge (docs/judge.md): a model's judgement decides whether a change is
// kept, and the numbers are one thing it weighs, since a change can help
// without moving them. It judges a recursive gate verdict that measured both
// sides here, and a merge the score says made main worse (improve module).

// JudgeTries is how many unusable answers a judge may give before the numbers
// decide alone.
const JudgeTries = 3

// JudgeWait is how long one judge run may take before it is stopped and
// counts as a try with no usable answer.
var JudgeWait = 3 * time.Hour

// JudgeCase is one change put to the judge.
type JudgeCase struct {
	ID      string // the run: named by the change, so a restart finds it
	Project core.Project
	SHA     string // the change: a gated head, or a merge on main
	Main    string // what a gated head was measured against; "" after a merge
	Numbers string // what the numbers alone say: keep | rollback
	What    string // what is being decided, one line
	Brief   string // the task's brief and its worker's account
	Diff    string
	Report  string // the numbers in full: the gate's report, the score's history
	Author  core.Run
	// Unjudged says why no judge is asked: the numbers' rollback stands (Unjudged).
	Unjudged string
}

// Unjudged says why the numbers decide a rollback without a judge: a change
// to files on the security list (or in the root class) keeps a measured
// regression, since the judge reads the diff a prompt injection could be in.
func Unjudged(ch core.Change) string {
	if len(ch.Security) == 0 {
		return ""
	}
	return "it touches security-listed files (" + core.Clip(strings.Join(ch.Security, ", "), 200) + "), where no judge overrides the numbers' rollback"
}

// StartJudge starts the judge's read-only run on c, on a model that isn't
// the author's where the config has one; it raises an alert when none is.
func StartJudge(cc *core.Core, a *agents.Agents, c JudgeCase) (core.RunSpec, error) {
	dir, err := cc.Effects.NewCheckout(c.Project, c.ID, c.SHA)
	if err != nil {
		return core.RunSpec{}, err
	}
	cfg := cc.WS.LoadConfig()
	pick, off := cfg.JudgePick(c.Project.Name, core.Pick{Harness: c.Author.Harness, Model: c.Author.Model})
	if !off {
		cc.J.MustAppend("alert.raised", map[string]string{"kind": "seat", "ref": "seat:" + c.Project.Name + ":judge", "project": c.Project.Name,
			"text": "The judge of " + c.Project.Name + " runs on the model that wrote the change (" + pick.Model + "), since no judge route, reviewer model or tribunal_models seat has another; add one so a change isn't judged by its author."})
	}
	spec := a.Spec(c.ID, core.JudgeRole, c.Project.Name, dir, fmt.Sprintf("Judge %s. Everything is in $HIVE_RUN (brief.md, diff.patch, numbers.md). Give your verdict.", c.What))
	spec.Harness, spec.Model = pick.Harness, pick.Model
	if len(cfg.Routed(core.JudgeRole, c.Project.Name)) == 0 {
		spec.Route = nil // a check's model doesn't move at a limit unless the person routed it
	}
	spec.ReadOnly, spec.Schema, spec.Task = true, core.JudgeSchema, c.Author.Task
	base := c.Main
	if base == "" { // a merge on main: the rules it was judged by are its first parent's
		base = c.SHA + "^"
	}
	spec.Inputs = map[string]string{"sha": c.SHA, "main": c.Main, "diff.patch": c.Diff, "numbers.md": c.Report,
		"brief.md": fmt.Sprintf("Judge token: %s\n\nYou decide: %s.\n\nThe numbers alone say: %s.\n\n## The change\n\n%s", core.JudgeToken(c.ID), c.What, c.Numbers, c.Brief) + rulesOn(cc, c.Project, base)}
	_, err = cc.Runs.Start(spec)
	return spec, err
}

// ReadJudgement is a finished judge run's answer; ok is false for an answer
// that isn't keep or rollback with the run's token.
func ReadJudgement(r core.Run) (verdict, summary string, ok bool) {
	var v struct{ Verdict, Summary, Token string }
	if json.Unmarshal(r.Structured, &v) != nil || (v.Verdict != core.GateKeep && v.Verdict != core.GateRollback) || v.Token != core.JudgeToken(r.ID) {
		return "", "", false
	}
	return v.Verdict, v.Summary, true
}
