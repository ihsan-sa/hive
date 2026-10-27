package work

import (
	"fmt"
	"slices"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A measured keep goes first (issue #1021). A gate takes hours and a slot
// on a loaded machine, and its verdict stands only while main's commits
// leave the change and what the gate measures with alone
// (core.Effects.StandingGate). So while a gain's keep stands at main (its
// judge yet to call it, or keeping it), a merge in its project that would
// unseat it waits behind it; one that leaves it standing goes on. Reverts
// don't wait: undoing a merge judged worse comes first. A keep holds the
// others at most keptFirstFor from its measure, so a judge that never answers
// doesn't stop a project's merges.
var keptFirstFor = 6 * time.Hour

// KeptFirstWait opens the reason of a task waiting behind a kept gain.
const KeptFirstWait = "waiting behind a measured gain"

// keptFirst says why t's merge waits behind another task's measured keep at
// main, or "".
func (m *Module) keptFirst(t *Task, p core.Project, main string) string {
	if !p.Self || t.Kind == "revert" {
		return ""
	}
	type kept struct {
		task *Task
		gate core.GateResult
	}
	var ahead []kept
	mine, mineOK := m.liveKeep(t, p, main)
	m.mu.Lock()
	var others []*Task
	for _, u := range m.tasks {
		if u.ID != t.ID && u.Project == p.Name && u.Stage == StageMerging && u.Gain() && u.SHA != "" && u.SHA != t.SHA {
			others = append(others, u)
		}
	}
	m.mu.Unlock()
	for _, u := range others {
		g, ok := m.liveKeep(u, p, main)
		if !ok {
			continue
		}
		// Two keeps that would unseat each other: the one measured first
		// goes first, so neither waits forever.
		if mineOK && (mine.At.Before(g.At) || mine.At.Equal(g.At) && t.ID < u.ID) {
			continue
		}
		ahead = append(ahead, kept{u, g})
	}
	slices.SortFunc(ahead, func(a, b kept) int { return a.gate.At.Compare(b.gate.At) })
	for _, k := range ahead {
		if hit, err := m.C.Effects.Unseats(p, k.gate, main, t.SHA); err != nil || !hit {
			continue // git can't tell: as before, it doesn't wait
		}
		return fmt.Sprintf("%s: #%d (%s) was kept on %s against main at %s, and this merge would change what that verdict measured; it merges first (or until %s)",
			KeptFirstWait, k.task.PR.Number, core.Clip(k.task.Label(), 80), core.Short(k.gate.Head), core.Short(k.gate.Main), k.gate.At.Add(keptFirstFor).Format("15:04Z"))
	}
	return ""
}

// liveKeep is u's measured keep standing at main, while its judge hasn't
// rolled it back and it is fresh enough to hold others.
func (m *Module) liveKeep(u *Task, p core.Project, main string) (core.GateResult, bool) {
	g, ok := m.C.Effects.StandingGate(p, u.SHA, main)
	if !ok || !g.Measured || g.Outcome != core.GateKeep || time.Since(g.At) > keptFirstFor {
		return core.GateResult{}, false
	}
	if j, ok := m.C.Effects.Judgement(p.Name, g.Head, g.Main); ok && j.At.After(g.At) && j.Verdict != core.GateKeep {
		return core.GateResult{}, false
	}
	return g, true
}
