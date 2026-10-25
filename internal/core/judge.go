package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// A model's judgement decides whether a change is kept; the numbers (the
// recursive gate's report, the score measured after a merge) are one thing
// it weighs, since a change can help without moving them (docs/judge.md).
// The judge is a read-only run of role JudgeRole that the daemon starts, on
// a model other than the change's author's where the config has one.

// JudgeRole is the judge's role (modules/agents/judge.md).
const JudgeRole = "judge"

// Judgement is the judge's call on a change.
type Judgement struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`            // the change judged: a gated head, or a merge on main
	Main    string    `json:"main,omitempty"` // what a gated head was measured against; "" after a merge
	Run     string    `json:"run"`
	Model   string    `json:"model,omitempty"`
	Numbers string    `json:"numbers"` // what the numbers alone said: keep | rollback
	Verdict string    `json:"verdict"` // keep | rollback
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
}

// JudgeSchema is the judge's structured answer.
const JudgeSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["keep","rollback"]},"summary":{"type":"string"},"token":{"type":"string"}},"required":["verdict","summary","token"]}`

// JudgeToken is what the judge must echo from its brief: an answer that
// lacks it wasn't written by a reader of the brief.
func JudgeToken(run string) string {
	sum := sha256.Sum256([]byte("judge " + run))
	return hex.EncodeToString(sum[:6])
}

// RecordJudgement accepts a judgement only from a read-only judge run the
// Hive started on exactly that change.
func (e *Effects) RecordJudgement(j Judgement) error {
	r, ok := e.runs.Get(j.Run)
	if !ok || r.Role != JudgeRole || !r.ReadOnly || r.Inputs["sha"] != j.SHA || r.Inputs["main"] != j.Main || r.Project != j.Project {
		return fmt.Errorf("judgement from %s is not bound to %s", j.Run, short(j.SHA))
	}
	if (j.Verdict != GateKeep && j.Verdict != GateRollback) || (j.Numbers != GateKeep && j.Numbers != GateRollback) {
		return fmt.Errorf("a judgement and the numbers are each keep or rollback")
	}
	if j.At.IsZero() {
		j.At = time.Now().UTC()
	}
	e.j.MustAppend("judge.decided", j)
	return nil
}

// Judgement is the newest judgement on sha against main ("" after a merge).
func (e *Effects) Judgement(project, sha, main string) (Judgement, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, ok := e.judged[project+"@"+sha+"@"+main]
	return j, ok
}

// Kept is the gate's verdict on head against main as it decides a merge: a
// measured verdict as the judge called it, once it has; else the numbers.
func (e *Effects) Kept(project, head, main string) bool {
	g, ok := e.GateResult(project, head, main)
	if !ok {
		return false
	}
	if j, ok := e.Judgement(project, head, main); ok && g.Measured && j.At.After(g.At) {
		return j.Verdict == GateKeep
	}
	return g.Outcome == GateKeep
}

// JudgePick is the harness and model that judge a change written on author:
// the judge route the person set, else the reviewer's model, else a tribunal
// seat's, taking the first that isn't the author's, so a change isn't judged
// by its author. off is false when every choice is the author's.
func (c Config) JudgePick(project string, author Pick) (pick Pick, off bool) {
	var all []Pick
	if len(c.Routed(JudgeRole, project)) > 0 {
		all = append(all, c.Route(JudgeRole, project)...)
	}
	all = append(all, c.Route("reviewer", project)[0])
	for _, s := range c.Seats(project) {
		h := s.Harness
		if h == "" {
			h = c.Harness
		}
		all = append(all, Pick{Harness: HarnessName(h), Model: s.Model})
	}
	for _, p := range all {
		if p.Model != "" && p.Model != author.Model {
			return p, true
		}
	}
	return all[0], false
}
