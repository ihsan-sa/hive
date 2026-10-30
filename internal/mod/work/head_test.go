package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// pushHead puts a commit with files on top of the branch's head on the
// forge, past the Hive, as someone with the repo's rights would.
func pushHead(t *testing.T, f *core.Local, branch string, files map[string]string) string {
	t.Helper()
	return commitTo(t, f.RepoPath("me/p"), branch, branch, "outside", files)
}

// staleHead is the incident of PR #462: the Hive pushed a newer head (here a
// catch-up with main, or a new diff), but the task kept the one before, on
// which the forge will never report a check.
func staleHead(t *testing.T, newDiff bool) (*Module, *Task, core.Project, *core.Local, string) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	pushMain(t, f, map[string]string{"other.txt": "z"})
	main, _ := m.C.Effects.Sync(p)
	next, err := m.C.Effects.UpdateBranch(p, task.SHA, main, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.C.Effects.Push(p, task.Branch, next); err != nil {
		t.Fatal(err)
	}
	if newDiff {
		next = pushHead(t, f, task.Branch, map[string]string{"more.txt": "w"})
		if err := m.C.Effects.Push(p, task.Branch, next); err != nil { // as if the Hive had pushed it
			t.Fatal(err)
		}
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPending})
	f.SetChecks(p.Repo, next, core.CheckRun{Name: "test", State: core.CheckPass})
	return m, task, p, f, next
}

// The Hive's own newest push is adopted and tested again, and then merges
// on the checks of that head.
func TestMergeAdoptsTheHivesNewerHead(t *testing.T) {
	m, task, p, f, next := staleHead(t, false)
	mergeOnce(t, m, task, p)
	if task.SHA != next || task.Stage != StageTesting || task.Round != 0 || !logHas(task, "adopted it") {
		t.Fatalf("sha %s (head %s), stage %q, reason %q", core.Short(task.SHA), core.Short(next), task.Stage, task.Reason)
	}
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A forge still showing an earlier push of the Hive's hasn't caught up: the
// task waits, and keeps its newer head.
func TestMergeWaitsForTheForgeToShowItsHead(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	old := task.SHA
	newer := pushHead(t, f, task.Branch, map[string]string{"x.txt": "1"})
	if err := m.C.Effects.Push(p, task.Branch, newer); err != nil { // as if the Hive had pushed it
		t.Fatal(err)
	}
	task.SHA = newer
	must(t, f.RepoPath("me/p"), "update-ref", "refs/heads/"+task.Branch, old) // the forge lags
	mergeOnce(t, m, task, p)
	if task.SHA != newer || task.Stage != StageMerging || !strings.Contains(task.Reason, "waiting for the forge to show") {
		t.Fatalf("sha %s, stage %q, reason %q", core.Short(task.SHA), task.Stage, task.Reason)
	}
}

// A head the Hive didn't push goes back to the worker, saying it moved.
func TestMergeForeignHeadGoesBack(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	head := pushHead(t, f, task.Branch, map[string]string{"theirs.txt": "q"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.Round != 1 || !strings.Contains(task.Findings, "moved to "+core.Short(head)) || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, round %d, findings %q", task.Stage, task.Round, task.Findings)
	}
}

// waitedLong backdates the task's wait for its head's first check run past
// the 30 minutes it waits. It goes through the task's saved form, so this file
// builds on a Hive without the wait, where the test then fails (the gate's
// fixes step runs it there).
func waitedLong(t *testing.T, task *Task) {
	t.Helper()
	setJSON(t, task, map[string]any{"no_checks": time.Now().Add(-31 * time.Minute)})
}

// A task already holding for checks that never came, with its reason
// unchanged from tick to tick, keeps the start of its wait across ticks,
// each of which loads it afresh: else the wait restarts every tick.
func TestMergeNoCheckRunsWaitSurvivesReload(t *testing.T) {
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nrequired_checks = [\"sandboxed-e2e\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	setJSON(t, task, map[string]any{"no_checks": nil, "no_checks_on": nil}) // as saved before the wait was bounded
	m.save(task, "")
	reload := func() *Task {
		got, ok := m.Get(task.ID)
		if !ok {
			t.Fatal("task gone")
		}
		m.later = map[string]time.Time{}
		return &got
	}
	again := reload()
	mergeOnce(t, m, again, p)
	again = reload()
	raw, _ := json.Marshal(again)
	var saved map[string]any
	json.Unmarshal(raw, &saved)
	if saved["no_checks_on"] != again.SHA {
		t.Fatalf("the wait's start wasn't saved: %v", saved["no_checks_on"])
	}
	waitedLong(t, again)
	mergeOnce(t, m, again, p)
	if !strings.Contains(again.Reason, "the CI the Hive started") || alerts(m, "no-checks") != 0 || f.CIStarts(p.Repo, again.SHA) != 1 {
		t.Fatalf("reason %q, %d alerts, %d CI starts", again.Reason, alerts(m, "no-checks"), f.CIStarts(p.Repo, again.SHA))
	}
	again = reload()
	if !logHas(again, "the Hive started its CI: dispatched") {
		t.Fatalf("the start isn't on the task's record: %q", again.Reason)
	}
	waitedLong(t, again)
	mergeOnce(t, m, again, p)
	if !strings.Contains(again.Reason, "lead has been told") || !strings.Contains(again.Reason, "even after the Hive started its CI") || alerts(m, "no-checks") != 1 || f.CIStarts(p.Repo, again.SHA) != 1 {
		t.Fatalf("reason %q, %d alerts, %d CI starts", again.Reason, alerts(m, "no-checks"), f.CIStarts(p.Repo, again.SHA))
	}
}

// A head whose CI the Hive started merges on the checks that start brings
// (the incident of hive-slack's PRs #9, #10 and #15, opened before ci.yml
// landed on main).
func TestMergeStartsCIOnAHeadNeverRun(t *testing.T) {
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nrequired_checks = [\"sandboxed-e2e\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	waitedLong(t, task)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if f.CIStarts(p.Repo, task.SHA) != 1 || task.Stage != StageMerging {
		t.Fatalf("%d CI starts, stage %q, reason %q", f.CIStarts(p.Repo, task.SHA), task.Stage, task.Reason)
	}
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPass}, core.CheckRun{Name: "test", State: core.CheckPass})
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || alerts(m, "no-checks") != 0 {
		t.Fatalf("stage %q, reason %q, %d alerts", task.Stage, task.Reason, alerts(m, "no-checks"))
	}
}

// A head behind main that gets no forge check run has main merged into it
// rather than its CI started, as a branch cut before its project's workflow
// landed has no workflow to start (hive-chip-flow #36, hifi-receiver-2 #88,
// dispatch #45 on 10-30); the caught-up head is tested and waited on. That is
// once per task: a caught-up head that still gets none has its CI started and
// then goes to the lead, however far main moves on meanwhile.
func TestMergeNoChecksBehindMainCatchesUp(t *testing.T) {
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nrequired_checks = [\"sandboxed-e2e\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	pushMain(t, f, map[string]string{".github/workflows/ci.yml": "on: pull_request\n"})
	stale := task.SHA
	waitedLong(t, task)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	main, err := core.Git(f.RepoPath("me/p"), "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	dir := m.C.WS.Project(p.Name)
	if task.SHA == stale || task.Stage != StageTesting || f.CIStarts(p.Repo, stale) != 0 || alerts(m, "no-checks") != 0 || !logHas(task, "merged main into it") {
		t.Fatalf("head %s (was %s), stage %q, %d CI starts, %d alerts, reason %q", core.Short(task.SHA), core.Short(stale), task.Stage, f.CIStarts(p.Repo, stale), alerts(m, "no-checks"), task.Reason)
	}
	if _, err := core.Git(dir, "merge-base", "--is-ancestor", strings.TrimSpace(main), task.SHA); err != nil {
		t.Fatal("the caught-up head doesn't hold main")
	}
	if pr, err := f.GetPR(p.Repo, task.PR.Number); err != nil || pr.HeadSHA != task.SHA {
		t.Fatalf("the PR's head is %s (%v), want the caught-up %s", core.Short(pr.HeadSHA), err, core.Short(task.SHA))
	}
	caught := task.SHA
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	pushMain(t, f, map[string]string{"later.txt": "main moves on"})
	waitedLong(t, task)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.SHA != caught || f.CIStarts(p.Repo, caught) != 1 {
		t.Fatalf("head %s (want %s), %d CI starts, reason %q", core.Short(task.SHA), core.Short(caught), f.CIStarts(p.Repo, caught), task.Reason)
	}
	waitedLong(t, task)
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.SHA != caught || alerts(m, "no-checks") != 1 || !strings.Contains(task.Reason, "lead has been told") {
		t.Fatalf("head %s, %d alerts, reason %q", core.Short(task.SHA), alerts(m, "no-checks"), task.Reason)
	}
}

// setJSON sets fields of the task's saved form, nil removing one, through
// JSON so that this file builds on a Hive without them.
func setJSON(t *testing.T, task *Task, set map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(task)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	for k, v := range set {
		if v == nil {
			delete(fields, k)
		} else {
			fields[k] = v
		}
	}
	raw, _ = json.Marshal(fields)
	*task = Task{}
	if err := json.Unmarshal(raw, task); err != nil {
		t.Fatal(err)
	}
}

// Checks cancelled because a newer push replaced their head broke nothing
// (the incident of PR #414): the task waits for the newer head's own run,
// with no re-run and no "can't start" alert.
func TestForgeChecksCancelledBySupersedingPushRaiseNothing(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	cancelled := core.CheckRun{Name: "test", State: core.CheckCantStart, Detail: "cancelled"}
	f.SetChecks(p.Repo, task.SHA, cancelled)
	newer := pushHead(t, f, task.Branch, map[string]string{"x.txt": "1"}) // pushed while the merge step ran
	main, _ := m.C.Effects.Sync(p)
	wait, err := m.forgeChecks(task, p, main)
	if err != nil || !wait {
		t.Fatalf("wait %v, err %v", wait, err)
	}
	if task.Reruns[task.SHA] != 0 || alerts(m, "main-red") != 0 || !strings.Contains(task.Reason, "a newer push replaced it") || !strings.Contains(task.Reason, core.Short(newer)) {
		t.Fatalf("reruns %v, %d alerts, reason %q", task.Reruns, alerts(m, "main-red"), task.Reason)
	}
}

// The forge showing an earlier push of the Hive's is waited on only so long:
// the journal's order of two pushes may not be the forge's, so past
// noChecksMax the forge's head is adopted. The wait's start survives a
// reload, as the hold saves nothing new from tick to tick.
func TestMergeForgeLagIsBounded(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	old := task.SHA
	if err := m.C.Effects.Push(p, task.Branch, old); err != nil {
		t.Fatal(err)
	}
	newer := pushHead(t, f, task.Branch, map[string]string{"x.txt": "1"})
	if err := m.C.Effects.Push(p, task.Branch, newer); err != nil {
		t.Fatal(err)
	}
	task.SHA = newer
	m.save(task, "")
	must(t, f.RepoPath("me/p"), "update-ref", "refs/heads/"+task.Branch, old) // the forge's order
	mergeOnce(t, m, task, p)
	again, _ := m.Get(task.ID)
	if saved := savedJSON(&again); saved["head_wait_on"] != old || saved["head_wait"] == nil || !strings.Contains(again.Reason, "waiting for the forge to show") {
		t.Fatalf("the wait's start wasn't saved: %v, reason %q", saved["head_wait_on"], again.Reason)
	}
	setJSON(t, &again, map[string]any{"head_wait": time.Now().Add(-noChecksMax - time.Minute)})
	m.later = map[string]time.Time{}
	mergeOnce(t, m, &again, p)
	if again.SHA != old || again.Stage != StageTesting || savedJSON(&again)["head_wait_on"] != nil || !logHas(&again, "the forge's order of the pushes") {
		t.Fatalf("sha %s, stage %q, reason %q", core.Short(again.SHA), again.Stage, again.Reason)
	}
}

// savedJSON is the task's saved form.
func savedJSON(task *Task) map[string]any {
	raw, _ := json.Marshal(task)
	var saved map[string]any
	json.Unmarshal(raw, &saved)
	return saved
}

// A task whose own head isn't the Hive's newest push either doesn't wait for
// the forge to show it: the forge's head, a push of the Hive's, is adopted.
func TestMergeAdoptsTheForgesHeadWhenNeitherIsNewest(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	old := task.SHA
	if err := m.C.Effects.Push(p, task.Branch, old); err != nil {
		t.Fatal(err)
	}
	mid := pushHead(t, f, task.Branch, map[string]string{"x.txt": "1"})
	if err := m.C.Effects.Push(p, task.Branch, mid); err != nil {
		t.Fatal(err)
	}
	newest := pushHead(t, f, task.Branch, map[string]string{"y.txt": "2"})
	if err := m.C.Effects.Push(p, task.Branch, newest); err != nil {
		t.Fatal(err)
	}
	must(t, f.RepoPath("me/p"), "update-ref", "refs/heads/"+task.Branch, mid)
	mergeOnce(t, m, task, p)
	if task.SHA != mid || task.Stage != StageTesting {
		t.Fatalf("sha %s (forge %s), stage %q, reason %q", core.Short(task.SHA), core.Short(mid), task.Stage, task.Reason)
	}
}

// A foreign head goes back with its diff, which the worker can't fetch.
func TestMergeForeignHeadSendsItsDiff(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	pushHead(t, f, task.Branch, map[string]string{"theirs.txt": "kept by hand"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || !strings.Contains(task.Findings, "theirs.txt") || !strings.Contains(task.Findings, "+kept by hand") {
		t.Fatalf("stage %q, findings %q", task.Stage, task.Findings)
	}
}

// A push the Hive made to another branch doesn't make a commit the Hive's on
// this one.
func TestMergeHeadPushedToAnotherBranchIsForeign(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	head := pushHead(t, f, task.Branch, map[string]string{"theirs.txt": "q"})
	m.C.Effects.Sync(p)
	if err := m.C.Effects.Push(p, "hive/other", head); err != nil {
		t.Fatal(err)
	}
	mergeOnce(t, m, task, p)
	if task.SHA == head || task.Stage != StageWorking || task.Round != 1 {
		t.Fatalf("sha %s, stage %q, round %d", core.Short(task.SHA), task.Stage, task.Round)
	}
}

// A draft whose head moved to the Hive's newest push adopts it rather than
// waiting as a draft.
func TestMergeDraftAdoptsTheHivesNewerHead(t *testing.T) {
	m, task, p, f, next := staleHead(t, false)
	f.SetDraft(p.Repo, task.PR.Number, true)
	mergeOnce(t, m, task, p)
	if task.SHA != next || task.Stage != StageTesting || !task.DraftSince.IsZero() {
		t.Fatalf("sha %s (head %s), stage %q, reason %q", core.Short(task.SHA), core.Short(next), task.Stage, task.Reason)
	}
}

// An adopted head whose own diff changed a path on the security list needs
// its own security read: the read of the head before doesn't carry.
func TestMergeAdoptedHeadNeedsItsOwnSecurityRead(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckPass})
	mergeOnce(t, m, task, p)
	seatSays(t, m, task, core.SeatSecurity, "land")
	mergeOnce(t, m, task, p) // records the read
	main, _ := m.C.Effects.Sync(p)
	read, _ := m.C.Effects.Classify(p, main, task.SHA)
	if !m.C.Effects.SecurityOK(p.Name, read) {
		t.Fatal("the first head's read didn't count")
	}
	next := pushHead(t, f, task.Branch, map[string]string{"secure/box.go": "package secure\n\nvar Open = true\n"})
	if err := m.C.Effects.Push(p, task.Branch, next); err != nil {
		t.Fatal(err)
	}
	mergeOnce(t, m, task, p)
	if task.SHA != next {
		t.Fatalf("not adopted: %q", task.Reason)
	}
	ch, _ := m.C.Effects.Classify(p, main, next)
	if m.C.Effects.SecurityOK(p.Name, ch) {
		t.Fatal("the adopted head's new diff on a listed path took the old head's security read")
	}
	if _, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: task.PR.Number, SHA: next, Main: main}); err == nil {
		t.Fatal("Effects.Merge took the adopted head unread")
	}
}

// A task held waiting on main's run of a red check keeps the start of that
// wait across ticks, each of which loads it afresh.
func TestMergeMainWaitSurvivesReload(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	rerunSpent(task)
	m.save(task, "")
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail, URL: "head/test"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckPending})
	reload := func() *Task {
		got, ok := m.Get(task.ID)
		if !ok {
			t.Fatal("task gone")
		}
		m.later = map[string]time.Time{}
		return &got
	}
	again := reload()
	mergeOnce(t, m, again, p)
	again = reload()
	if again.MainWaitOn != again.SHA || again.MainWait.IsZero() || !strings.HasPrefix(again.Reason, "waiting on main: ") {
		t.Fatalf("the wait's start wasn't saved: on %q at %v, reason %q", again.MainWaitOn, again.MainWait, again.Reason)
	}
	start := again.MainWait
	mergeOnce(t, m, again, p)
	if again = reload(); !again.MainWait.Equal(start) {
		t.Fatalf("the wait restarted: %v, then %v", start, again.MainWait)
	}
}
