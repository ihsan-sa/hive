package improve

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// judgePoll is how often a waiting judgement looks at its run; judgeWait is
// how long it waits on one run before asking again.
var (
	judgePoll = 5 * time.Second
	judgeWait = work.JudgeWait
	judgeHold = time.Minute // how long a later merge waits before looking again
	judgeLong = time.Hour   // past it, a merge with its judge raises an alert
)

// judgingMerge is a merge with its judge: running while judgeMerge decides
// it, or found after a restart with its judge run still going, which holds
// later merges until the merge is measured again and finds that run.
type judgingMerge struct {
	project string
	running bool
}

// judgingBefore says another merge of project is with its judge, so sha's
// measurement waits for the baseline it settles.
func (m *Module) judgingBefore(project, sha string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for s, j := range m.judging {
		if j.project == project && s != sha {
			return true
		}
	}
	return false
}

// judgeRunning says another merge of project is with a judge judgeMerge is
// running, not only marked after a restart.
func (m *Module) judgeRunning(project, sha string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for s, j := range m.judging {
		if j.project == project && s != sha && j.running {
			return true
		}
	}
	return false
}

// judgeMerge puts a merge whose score fell, tests green, to the judge
// (docs/judge.md): a model weighs the score with the change, its brief and
// the score's history, and decides whether main keeps it. A keep moves the
// baseline to this score, since main now stands there; a rollback, or no
// usable answer in work.JudgeTries runs, reverts as the numbers say.
func (m *Module) judgeMerge(p core.Project, t work.Task, ms Measurement, reason string) {
	defer m.judges.Done()
	started := time.Now().UTC()
	defer func() {
		m.mu.Lock()
		delete(m.judging, t.MergeSHA)
		m.mu.Unlock()
	}()
	j, ok := m.C.Effects.Judgement(p.Name, t.MergeSHA, "")
	if !ok {
		ask := m.askJudge
		if ask == nil {
			ask = m.runJudge
		}
		j, ok = ask(p, t, ms, reason)
	}
	if !ok {
		ms.Note += "; " + cmp.Or(j.Summary, "its judge gave no usable answer") + ", so the score decides"
		m.revert(p, t, ms, reason)
		return
	}
	if j.Verdict == core.GateRollback {
		ms.Note += fmt.Sprintf("; its judge (%s) agreed: %s", j.Model, core.Clip(j.Summary, 400))
		m.revert(p, t, ms, reason+"; its judge agreed")
		return
	}
	// Kept: main stands at this score now, so it is the baseline the next
	// merge is measured against.
	ms.Kept, ms.Best = true, ms.Score
	if k, ok := m.kept(p.Name); ok && k.At.After(started) && k.Best != nil {
		ms.Best = k.Best // a later merge was measured meanwhile: its baseline stands
	}
	ms.Note = fmt.Sprintf(judgedKeep+" (%s) though %s: %s", j.Model, reason, core.Clip(j.Summary, 400))
	m.record(ms)
	if m.OnKept != nil {
		m.OnKept(p.Name, t.MergeSHA)
	}
}

// runJudge asks the judge's runs for their call, up to work.JudgeTries of
// them, and records the first usable one. With none, j.Summary says why
// when it isn't that no answer was usable.
func (m *Module) runJudge(p core.Project, t work.Task, ms Measurement, reason string) (j core.Judgement, ok bool) {
	waited, alerted := time.Now(), false
	for i := 0; i < work.JudgeTries; i++ {
		id := fmt.Sprintf("j-%s-%s-%d", t.ID, core.Short(t.MergeSHA), i)
		r, found := m.C.Runs.Get(id)
		if !found {
			c, err := m.judgeCase(p, t, ms, reason, id)
			if err == nil && c.Unjudged != "" {
				j.Summary = c.Unjudged
				return j, false
			}
			if err == nil {
				_, err = work.StartJudge(m.C, m.Agents, c)
			}
			if err != nil {
				m.C.J.MustAppend("alert.raised", map[string]string{"kind": "judge", "project": p.Name, "text": "could not start the judge of " + core.Short(t.MergeSHA) + ", so the score decides: " + err.Error()})
				return j, false
			}
		}
		for start := time.Now(); time.Since(start) < judgeWait; time.Sleep(judgePoll) {
			if r, found = m.C.Runs.Get(id); found && r.Terminal() {
				break
			}
			if time.Since(waited) > judgeLong && !alerted {
				// Later merges of the project wait on this one: say so.
				alerted = true
				m.C.J.MustAppend("alert.raised", map[string]string{"kind": "judge", "ref": "judge:" + t.MergeSHA, "project": p.Name,
					"text": fmt.Sprintf("merge %s (%s) has been with its judge for over %s; later merges of %s aren't measured until it decides (up to %d runs of %s)", core.Short(t.MergeSHA), t.Label(), judgeLong, p.Name, work.JudgeTries, judgeWait)})
			}
		}
		if !r.Terminal() {
			m.C.Runs.Stop(id, "the judge took longer than "+judgeWait.String()+"; another is asked")
			continue
		}
		verdict, summary, good := work.ReadJudgement(r)
		if !good {
			continue
		}
		j = core.Judgement{Project: p.Name, SHA: t.MergeSHA, Run: r.ID, Model: r.Model, Numbers: core.GateRollback, Verdict: verdict, Summary: summary}
		if err := m.C.Effects.RecordJudgement(j); err != nil {
			return j, false
		}
		return j, true
	}
	return j, false
}

// judgeCase is what the judge of a merge reads: the task's brief and its
// worker's account, the merge's diff, and the score with its recent history.
func (m *Module) judgeCase(p core.Project, t work.Task, ms Measurement, reason, id string) (work.JudgeCase, error) {
	parent, err := core.Git(m.C.WS.Project(p.Name), "rev-parse", "-q", "--verify", t.MergeSHA+"^")
	if err != nil {
		return work.JudgeCase{}, err
	}
	ch, err := m.C.Effects.Classify(p, strings.TrimSpace(parent), t.MergeSHA)
	if err != nil {
		return work.JudgeCase{}, err
	}
	w, _ := m.C.Runs.Get(t.Worker)
	brief := w.Brief
	if w.Finish != nil {
		brief += "\n\n## The worker's account\n\n" + w.Finish.Account
	}
	if t.Worker == "" {
		brief = t.Label()
	}
	var b strings.Builder
	cfg := m.C.Effects.Config(p, t.MergeSHA)
	fmt.Fprintf(&b, "# The score after the merge\n\n%s.\n\n", reason)
	if cfg.Score != nil {
		fmt.Fprintf(&b, "The score: `%s` (%s is better, tolerance %g). Tests are green at this merge.\n\n", cfg.Score.Command, cfg.Score.Direction, cfg.Score.Tolerance)
	}
	b.WriteString("## Its recent history on main, newest last\n\n| merge | task | score | kept | note |\n|---|---|---|---|---|\n")
	m.mu.Lock()
	if n, drop, before := judgedDrift(m.scores[p.Name]); n > 0 {
		fmt.Fprintf(&b, "Judges have kept %d falls in a row on main, lowering the baseline by %g in all, from %g; the score above is measured against where they left it.\n\n", n, drop, before)
	}
	hist := m.scores[p.Name]
	if len(hist) > 20 {
		hist = hist[len(hist)-20:]
	}
	for _, x := range hist {
		sc := "-"
		if x.Score != nil {
			sc = fmt.Sprint(*x.Score)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %v | %s |\n", core.Short(x.SHA), x.Task, sc, x.Kept, strings.ReplaceAll(core.Clip(x.Note, 160), "|", "/"))
	}
	m.mu.Unlock()
	return work.JudgeCase{ID: id, Project: p, SHA: t.MergeSHA, Numbers: core.GateRollback, Unjudged: work.Unjudged(ch),
		What:  fmt.Sprintf("whether main keeps merge %s of task %s (%q), or reverts it because its score fell", core.Short(t.MergeSHA), t.ID, t.Label()),
		Brief: brief, Diff: ch.Diff, Report: b.String(), Author: w}, nil
}

// judgedDrift is how far judges' keeps of falls have lowered a project's
// baseline since the score last stood at a best of its own: how many in a
// row, by how much, and the best before them.
func judgedDrift(hist []Measurement) (n int, drop, before float64) {
	var last *float64
	for _, x := range hist {
		if !x.Kept || x.Best == nil {
			continue
		}
		switch {
		case strings.HasPrefix(x.Note, judgedKeep) && last != nil:
			if n == 0 {
				before = *last
			}
			n, drop = n+1, drop+(*last-*x.Best)
		case x.moved(last):
			n, drop = 0, 0 // a new best of the score's own
		}
		b := *x.Best
		last = &b
	}
	return n, drop, before
}

// judgedKeep opens the note of a merge a judge kept.
const judgedKeep = "kept by its judge"
