package work

import (
	"encoding/json"
	"fmt"
	"os"
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

// JudgeParkWait is the longest a clear keep (clearKeep) waits on a parked
// judge: a judge that has stayed parked this long (at a usage limit, on a
// model that can't answer) is stopped and the numbers decide, since a gain
// measured beyond the gate's noise with no regression needs no judge to
// merge. On 10-26 #868's keep waited 8 hours on a judge parked at every
// login's limit while main stood still for 12 (issue #983). Any other
// verdict still waits: the judge may keep what the numbers missed, or roll
// back a keep won on the control's flake. A var so a test needn't wait.
var JudgeParkWait = 30 * time.Minute

// clearKeep says whether g is a keep the numbers make clear: its gain is a
// metric measured beyond the noise (the gate names it with its noise), not a
// pass rate the control's flake could have given it, nor a tie.
func clearKeep(g core.GateResult) bool {
	return g.Outcome == core.GateKeep && strings.Contains(g.Reason, "(noise ±")
}

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
	spec.Inputs = map[string]string{"sha": c.SHA, "main": c.Main, "diff.patch": c.Diff, "numbers.md": c.Report,
		"brief.md": fmt.Sprintf("Judge token: %s\n\nYou decide: %s.\n\nThe numbers alone say: %s.\n\n## The change\n\n%s", core.JudgeToken(c.ID), c.What, c.Numbers, c.Brief)}
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

// judgeGate is the judge's call on a gate verdict that measured both sides:
// keep says it merges, wait that the task is held for the judge. A verdict
// that measured nothing (the candidate failed on its own first) stands as is,
// as do the numbers once the judge gave no usable answer JudgeTries times,
// and a clear keep whose judge has been parked JudgeParkWait.
func (m *Module) judgeGate(t *Task, p core.Project, g core.GateResult) (keep, wait bool, err error) {
	numbers := g.Outcome == core.GateKeep
	if !g.Measured {
		return numbers, false, nil
	}
	if j, ok := m.C.Effects.Judgement(p.Name, g.Head, g.Main); ok && j.At.After(g.At) {
		return j.Verdict == core.GateKeep, false, nil
	}
	key := core.Short(g.Head) + "@" + core.Short(g.Main)
	if t.JudgeTries[key] >= JudgeTries {
		return numbers, false, nil
	}
	id := fmt.Sprintf("j-%s-%s-%d", t.ID, strings.ReplaceAll(key, "@", "-"), t.JudgeTries[key])
	r, ok := m.C.Runs.Get(id)
	if !ok {
		ch, err := m.C.Effects.Classify(p, g.Main, g.Head)
		if err != nil {
			return false, true, err
		}
		if why := Unjudged(ch); why != "" && !numbers {
			if t.JudgeTries == nil {
				t.JudgeTries = map[string]int{}
			}
			t.JudgeTries[key] = JudgeTries
			m.save(t, "the gate's numbers decide unjudged: "+why)
			return false, false, nil
		}
		w, _ := m.C.Runs.Get(t.Worker)
		report, _ := os.ReadFile(g.Report)
		spec, err := StartJudge(m.C, m.Agents, JudgeCase{ID: id, Project: p, SHA: g.Head, Main: g.Main, Numbers: g.Outcome,
			What:  fmt.Sprintf("whether task %s (%q), a change to the Hive aimed at a gain, merges; the recursive gate said %s: %s", t.ID, t.Label(), GateWord(g.Outcome), g.Reason),
			Brief: w.Brief + "\n\n## The worker's account\n\n" + finishText(w), Diff: ch.Diff, Report: core.Clip(string(report), 60000), Author: w})
		if err != nil {
			return false, true, err
		}
		m.hold(t, p, fmt.Sprintf("%s: the gate said %s on %s, and its judge (%s) is weighing that with the change", GateWait, GateWord(g.Outcome), core.Short(g.Head), spec.Model))
		return false, true, nil
	}
	if !r.Terminal() && clearKeep(g) && r.Status == core.StatusParked && !r.ParkedSince.IsZero() && time.Since(r.ParkedSince) >= JudgeParkWait {
		m.C.Runs.Stop(r.ID, "the gate's clear keep merges on its numbers rather than wait for this judge")
		if t.JudgeTries == nil {
			t.JudgeTries = map[string]int{}
		}
		t.JudgeTries[key] = JudgeTries
		why := fmt.Sprintf("the judge (%s) has been parked since %s (%s), so the gate's clear keep merges on its numbers", r.Model, r.ParkedSince.UTC().Format("15:04Z"), core.Clip(r.Reason, 200))
		// Said on the PR, so a later read knows no judge weighed this keep.
		m.comment(p.Repo, t.PR.Number, fmt.Sprintf("**Judge bypassed:** %s against main at %s merges unjudged: %s. The gate said: %s", core.Short(g.Head), core.Short(g.Main), why, g.Reason))
		m.save(t, why)
		return true, false, nil
	}
	verdict, summary, ok := "", "", false
	switch {
	case !r.Terminal() && time.Since(r.Created) < JudgeWait:
		m.hold(t, p, fmt.Sprintf("%s: the gate said %s on %s, and its judge (%s) is weighing that with the change", GateWait, GateWord(g.Outcome), core.Short(g.Head), r.Model))
		return false, true, nil
	case !r.Terminal():
		// A judge that hangs counts as a try, so the task never waits on it
		// past JudgeTries of them (the stall watcher skips GateWait).
		m.C.Runs.Stop(id, "the judge took longer than "+JudgeWait.String()+"; another is asked")
	default:
		verdict, summary, ok = ReadJudgement(r)
	}
	if !ok {
		if t.JudgeTries == nil {
			t.JudgeTries = map[string]int{}
		}
		t.JudgeTries[key]++
		if t.JudgeTries[key] >= JudgeTries {
			m.save(t, fmt.Sprintf("the judge gave no usable answer %d times, so the gate's numbers decide: %s", JudgeTries, GateWord(g.Outcome)))
			return numbers, false, nil
		}
		m.save(t, "the judge gave no usable answer; asking again")
		return false, true, nil
	}
	j := core.Judgement{Project: p.Name, SHA: g.Head, Main: g.Main, Run: r.ID, Model: r.Model, Numbers: g.Outcome, Verdict: verdict, Summary: summary}
	if err := m.C.Effects.RecordJudgement(j); err != nil {
		return false, true, err
	}
	word := map[string]string{core.GateKeep: "KEEP", core.GateRollback: "ROLL BACK"}
	m.comment(p.Repo, t.PR.Number, fmt.Sprintf("**Judge (%s): %s** %s against main at %s; the gate's numbers said %s.\n\n%s", r.Model, word[verdict], core.Short(g.Head), core.Short(g.Main), GateWord(g.Outcome), summary))
	m.save(t, fmt.Sprintf("the judge (%s): %s; the gate's numbers said %s", r.Model, word[verdict], GateWord(g.Outcome)))
	return verdict == core.GateKeep, false, nil
}
