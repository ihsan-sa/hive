package work

import (
	"fmt"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// noChecksMax is how long a head waits for its first forge check run before
// the task stops trusting that one will come.
var noChecksMax = 30 * time.Minute

// sameHead reconciles t with its PR's head on the forge before anything is
// waited on: checks are only ever reported on the commit the PR is at, so a
// task holding another would wait for them forever (two daemons once raced,
// and the slower one saved a head the faster had already replaced). The
// Hive's own newest push to the branch is adopted and tested again, as a
// merge needs a test of its exact head; its reads carry if its own diff is
// the same, as a catch-up's do. An older
// push of the Hive's is the forge not having caught up yet, so t waits. Any
// other commit isn't the Hive's, and goes back to the worker.
func (m *Module) sameHead(t *Task, p core.Project, head string) (bool, error) {
	if head == "" || head == t.SHA {
		return false, nil
	}
	pushed, last := m.C.Effects.Pushed(p.Name, t.Branch, head)
	pr := fmt.Sprintf("PR #%d", t.PR.Number)
	switch {
	case last:
		old := t.SHA
		t.SHA, t.Stage, t.Reason = head, StageTesting, ""
		m.save(t, pr+"'s head is "+core.Short(head)+", the Hive's newest push to "+t.Branch+", not "+core.Short(old)+"; adopted it and testing again")
		return true, nil
	case pushed:
		m.hold(t, p, "waiting for the forge to show "+pr+" at "+core.Short(t.SHA)+" (it still shows "+core.Short(head)+", an earlier push)")
		return true, nil
	}
	t.Reason = ""
	return true, m.sendBack(t, p, pr+"'s head on the forge moved to "+core.Short(head)+", a commit the Hive didn't push, so the checks waited for on "+core.Short(t.SHA)+
		" will never come. Your next finish pushes your branch over it: if that commit holds anything worth keeping, make it part of your work.")
}

// noChecks bounds the wait for a head on which the forge has reported no
// check run at all while required ones are awaited. Past noChecksMax a branch
// behind main is brought up to date, which gives the forge a new head to run
// on; one already up to date is raised once to the lead and waits.
func (m *Module) noChecks(t *Task, p core.Project, main string, runs []core.CheckRun, st string) (bool, error) {
	if st != core.CheckPending || len(runs) > 0 {
		return false, nil
	}
	if t.NoChecksOn != t.SHA {
		// Saved now: a hold whose reason didn't change saves nothing, and
		// the next tick loads the task afresh.
		t.NoChecks, t.NoChecksOn = time.Now().UTC(), t.SHA
		m.save(t, "no forge check run on "+core.Short(t.SHA)+" yet; waiting up to "+noChecksMax.String()+" for one")
	}
	if time.Since(t.NoChecks) < noChecksMax {
		return false, nil
	}
	none := "no forge check run has appeared on " + core.Short(t.SHA) + " in over " + noChecksMax.String()
	if ok, err := m.catchUp(t, p, main, none); ok || err != nil {
		return true, err
	}
	m.raiseOnce(p, t.SHA+"/nochecks", fmt.Sprintf("%s (PR #%d in %s), which is up to date with main; the forge may not have run CI on it", none, t.PR.Number, p.Name))
	m.hold(t, p, "waiting for the forge's checks on "+core.Short(t.SHA)+": "+none+"; the lead has been told")
	return true, nil
}
