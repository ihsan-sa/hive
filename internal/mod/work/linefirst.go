package work

import (
	"fmt"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The merge line's front can't be starved (issue #1794). An older PR whose
// tests passed merged with main keeps that pass while main's commits miss
// its files (mergedTests), but on the Hive's own repository it then waits
// hours for a gate slot and the gate, and every merge elsewhere that touched
// its files sent it back to test, until main moved again before its slot
// freed. So a merge the Hive makes that would end such a pass waits behind
// it; one that leaves it standing goes on, as does one its batch tested.
// Reverts don't wait, nor a measured keep (keptFirst orders those), and a
// pass holds the others at most lineFirstFor from its test, so a front that
// never gets through doesn't stop a project's merges. A front holds the
// others only if it can merge on what it has now (issue #1825): a fix, or a
// gain the gate already kept. A gate takes up to GateTimeout and its slots
// shrink under load, so some twenty passed fixes sat behind one gain still
// in its gate. They go ahead, and the gain's verdict counts as main moves
// while main's commits miss its files and the gate's, else it gates again.
var lineFirstFor = 6 * time.Hour

// LineFirstWait opens the reason of a task waiting behind an older PR's
// passed merged test.
const LineFirstWait = "waiting behind the merge line's front"

// lineFirst says why t's merge waits behind an older PR whose merged test
// stands at main, or "".
func (m *Module) lineFirst(t *Task, p core.Project, main string) string {
	if t.Kind == "revert" {
		return ""
	}
	type front struct {
		task *Task
		was  string
	}
	var ahead []front
	m.mu.Lock()
	for _, u := range m.tasks {
		if u.ID == t.ID || u.Project != p.Name || u.Stage != StageMerging || u.SHA == "" || u.SHA == t.SHA || !lineBefore(u, t) {
			continue
		}
		was, at, ok := strings.Cut(u.MergedOK, "+")
		if !ok || was != u.SHA || time.Since(u.MergedAt) > lineFirstFor || m.inBatchLocked(t, u, main) {
			continue
		}
		ahead = append(ahead, front{&Task{ID: u.ID, Kind: u.Kind, Aim: u.Aim, PR: u.PR, SHA: u.SHA, Title: u.Title, MergedAt: u.MergedAt}, at})
	}
	m.mu.Unlock()
	if len(ahead) == 0 {
		return ""
	}
	if _, kept := m.liveKeep(t, p, main); kept {
		return "" // a measured keep goes first (keptFirst)
	}
	var first *front
	for i := range ahead {
		f := &ahead[i]
		if !m.canMerge(f.task, p, main) {
			continue // a gain still queued for or in its gate holds nothing back
		}
		if f.was != main && !m.C.Effects.MainLeaves(p, f.task.SHA, f.was, main) {
			continue // its pass no longer stands: this merge can't end it
		}
		g := core.GateResult{Head: f.task.SHA, Main: main}
		if hit, err := m.C.Effects.Unseats(p, g, main, t.SHA); err != nil || !hit {
			continue // git can't tell: as with a keep, it doesn't wait
		}
		if first == nil || lineBefore(f.task, first.task) {
			first = f
		}
	}
	if first == nil {
		return ""
	}
	return fmt.Sprintf("%s: #%d (%s) passed its tests merged with main, and this merge would change its files or what the gate measures with, so it merges first (or until %s)",
		LineFirstWait, first.task.PR.Number, core.Clip(first.task.Label(), 80), first.task.MergedAt.Add(lineFirstFor).Format("15:04Z"))
}

// canMerge says whether u, a front in the line, can merge at main on what
// it has now: the gate's rule in Effects.Merge for a gain on the Hive's own
// repository.
func (m *Module) canMerge(u *Task, p core.Project, main string) bool {
	return !p.Self || !u.Gain() || m.C.Effects.Kept(p, u.SHA, main) || m.C.Effects.Approved("gate", p.Name, u.SHA+"@"+main)
}

// inBatchLocked says whether t's head was in u's passed batch at main: its
// merge moves the batch's tip, which u's pass follows. m.mu is held.
func (m *Module) inBatchLocked(t, u *Task, main string) bool {
	for _, b := range m.batches[t.Project] {
		if b.ok && b.front == u.ID && b.tip == main && b.heads[t.ID] == t.SHA {
			return true
		}
	}
	return false
}
