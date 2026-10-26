package work

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Tree search over candidate Hives (issue #829, docs/tree.md). A change the
// recursive gate rolls back for fallout of its own (it fails its own tests,
// or the child Hive built from it does worse) goes back to its worker as
// before, and also branches: up to treeWidth-1 sibling tasks start from the
// rolled-back commit, each told the fallout and a different way to fix it.
// Each branch goes through the gate and the judge like any gain, so it is a
// child Hive built and run on real work, and one that is rolled back for its
// own fallout branches again, down to treeDepth. The first node of a tree to
// be kept and merged is its stable best (kept on the gate's repeated rounds
// and by the judge); every other open node of that tree is then archived:
// stopped, its branch and pull request left on the forge as they are.
//
// The branches share the gate's queue and the pace with every other change,
// so the tree adds workers, never gates at once (the machine's share is
// ~8 GB; one soft-layer run of 28 jobs swamped it), and treeLive caps the
// nodes one tree keeps open.
var (
	treeWidth = 2 // children per rollback: the sent-back worker and one branch
	treeDepth = 3 // levels below the root that may branch again
	treeLive  = 3 // open nodes one tree may hold at once
	treeTotal = 6 // branches one tree may ever file, so flaky rollbacks of a sent-back root don't keep costing workers
)

// treeOf names t's tree: its root's task id.
func (t Task) treeOf() string {
	if t.Tree != "" {
		return t.Tree
	}
	return t.ID
}

// sameTree says t and o are nodes of one search tree.
func sameTree(t, o Task) bool {
	return (t.Tree != "" || o.Tree != "") && t.treeOf() == o.treeOf()
}

// fallout says a rollback's reason is the candidate's own doing, which a fix
// on top of it may undo: not "nothing moved", which no fix of fallout helps.
func fallout(reason string) bool {
	return strings.Contains(reason, "fails its own vet or short tests") || strings.Contains(reason, "worse than the current Hive")
}

// treeAngles are the ways the branches are told to try, one each.
var treeAngles = []string{
	"Take the smallest step that removes the failure while keeping what the change is for; if the failure is a flake of the child Hive's run, not the change, show that (a test, a rerun) instead of changing code.",
	"Rethink the part of the change that caused the failure: keep its aim, but reach it another way that doesn't set the failure off.",
	"Drop or narrow whatever part of the change the failure comes from, and keep only the part that still gains.",
}

// branchOut opens the sibling branches of t, whose head the gate rolled
// back: g names the fallout. A head branches once; the head is saved as
// branched before any branch is filed, so a restart never files one twice.
func (m *Module) branchOut(t *Task, p core.Project, g core.GateResult, judged string) {
	if !p.Self || t.Checkpoint != "" || t.Kind != "work" || g.Head == "" || !fallout(g.Reason) || t.Depth >= treeDepth || slices.Contains(t.Branched, g.Head) {
		return
	}
	open, filed := 0, 0
	for _, o := range m.List(func(o Task) bool { return sameTree(*t, o) || o.ID == t.treeOf() }) {
		if !o.Terminal() && o.Stage != StageMerged {
			open++
		}
		if o.Tree != "" {
			filed++
		}
	}
	if t.Tree == "" {
		open++ // t itself, not yet named a tree's root
	}
	n := min(treeWidth-1, treeLive-open, treeTotal-filed)
	if n <= 0 {
		return
	}
	t.Branched = append(t.Branched, g.Head)
	m.save(t, fmt.Sprintf("the gate rolled back %s for its own fallout: branching %d sibling(s) from it (tree %s, depth %d)", core.Short(g.Head), n, t.treeOf(), t.Depth+1))
	for i := range n {
		angle := treeAngles[(t.Depth+i+1)%len(treeAngles)]
		known := fmt.Sprintf("A branch of task %s (tree %s, depth %d of %d). The recursive gate rolled back its commit %s against main at %s: %s.%s\n\nYour checkout starts at that commit, with its changes in: fix that fallout on top of it. Task %s's own worker is fixing it too, its own way, and the first node of this tree to be kept and merged is kept; the others are archived. %s\n\nThe change as briefed: %s\n\nWhat was known then: %s",
			t.ID, t.treeOf(), t.Depth+1, treeDepth, core.Short(g.Head), core.Short(g.Main), g.Reason, judged, t.ID, angle, t.Title, orNone(t.Known))
		b, err := m.File(Brief{Project: t.Project, Title: core.Clip("Branch of: "+strings.TrimPrefix(t.Title, "Branch of: "), 120), Done: t.Done, Known: known,
			From: "tree:" + t.ID, Chat: t.Chat, Thread: t.Thread, Aim: AimGain, Start: g.Head, Tree: t.treeOf(), Parent: t.ID, Depth: t.Depth + 1})
		if err != nil {
			m.save(t, "a branch couldn't be filed: "+err.Error())
			return
		}
		m.save(t, "branch "+b.ID+" filed from "+core.Short(g.Head))
	}
}

// archived stops t, an open node of a tree another node of which has
// merged: the tree's stable best. It runs at t's own step, so nothing else
// is saving t meanwhile. t's branch and pull request stay on the forge as
// they are, for anyone who wants to look again (recheck brings it back).
func (m *Module) archived(t *Task) bool {
	if t.Terminal() || t.Stage == StageMerged || t.Stage == StageDone || t.Kind != "work" {
		return false
	}
	if t.Tree == "" && len(t.Branched) == 0 {
		return false // in no tree
	}
	won := m.List(func(o Task) bool {
		return o.ID != t.ID && (sameTree(*t, o) || o.ID == t.treeOf()) && o.MergeSHA != "" && (o.Stage == StageMerged || o.Stage == StageDone)
	})
	if len(won) == 0 {
		return false
	}
	why := fmt.Sprintf("%s task %s of its tree (%s) merged as the tree's best", treeArchived, won[0].ID, t.treeOf())
	// The worker ends blocked, as the stop op ends a quiet one, never with
	// Runs.Stop: a stopped run is the person's, and recheck couldn't bring
	// the node back (docs/log.md, #846). A turn in flight ends first.
	if w, ok := m.C.Runs.Get(t.Worker); ok && t.Worker != "" && !w.Terminal() {
		switch w.Status {
		case core.StatusRunning:
			return false // archived at a step after its turn ends
		case core.StatusQuiet:
			if m.C.Runs.EndQuiet(w.ID, core.StoppedQuiet+"the tree search: "+why) != nil {
				return false
			}
		default:
			m.C.Runs.SetStatus(w.ID, core.StatusBlocked, why)
		}
	}
	m.dropGate(t.ID)
	t.Stage, t.Reason = StageStopped, why
	m.save(t, why)
	return true
}

// treeArchived opens an archived node's reason.
const treeArchived = "archived:"
