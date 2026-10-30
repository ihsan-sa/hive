package work

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// toMerging takes a rig's task through its tests to the merge.
func toMerging(t *testing.T, m *Module, task *Task, p core.Project) {
	t.Helper()
	if err := testStep(m, task, p); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageMerging {
		t.Fatalf("stage %q, want merging (reason %q)", task.Stage, task.Reason)
	}
}

func mergeOnce(t *testing.T, m *Module, task *Task, p core.Project) {
	t.Helper()
	if err := m.merge(task, p); err != nil {
		t.Fatal(err)
	}
}

func prState(t *testing.T, f *core.Local, n int) string {
	t.Helper()
	pr, err := f.GetPR("me/p", n)
	if err != nil {
		t.Fatal(err)
	}
	return pr.State
}

// rerunSpent marks the automatic re-run of task's head (a red main has no
// answer for) as done and past its grace, for tests of what comes after.
func rerunSpent(task *Task) {
	task.Reruns, task.RerunAt = map[string]int{task.SHA: 1}, time.Now().Add(-2*rerunGrace)
}

// logHas reports whether a line of task's log contains text.
func logHas(task *Task, text string) bool {
	for _, e := range task.Log {
		if strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}

// Failing exactly the checks main fails is no worse than main either.
func TestMergeForgeCheckSameFailuresAsMain(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckFail})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// A PR with its security read merges as it is when main moved and the forge
// says it still merges cleanly: main isn't merged in, so the head stays the
// one that was read and checked.
func TestMergeAsItIsWhenClean(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m.save(task, "")
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	read := task.SHA
	if _, err := m.RecordRead("t1", read, "perms", "land", ""); err != nil {
		t.Fatal(err)
	}
	pushMain(t, f, map[string]string{"other.txt": "z"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.SHA != read {
		t.Fatalf("stage %q at %s (read %s), reason %q", task.Stage, core.Short(task.SHA), core.Short(read), task.Reason)
	}
	main, _ := m.C.Effects.Sync(p)
	if ref := must(t, m.C.WS.Project(p.Name), "show", main+":other.txt"); ref != "z" {
		t.Fatalf("main lost its own change: %q", ref)
	}
}

// A clean merge whose tests pass merges as it is, tested once per head and main.
func TestMergedWithMainGreenMergesAsItIs(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	head := task.SHA
	pushMain(t, f, map[string]string{"other.txt": "z"})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.SHA != head {
		t.Fatalf("stage %q at %s (head %s), reason %q", task.Stage, core.Short(task.SHA), core.Short(head), task.Reason)
	}
}

// unknownForge is a forge still working out whether a PR merges cleanly.
type unknownForge struct{ *core.Local }

func (u unknownForge) GetPR(repo string, n int) (core.PR, error) {
	pr, err := u.Local.GetPR(repo, n)
	pr.Mergeable, pr.MergeState = "UNKNOWN", "UNKNOWN"
	return pr, err
}

func TestMergeAfterUnknownResolves(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	pushMain(t, f, map[string]string{"other.txt": "z"})
	m.C.Effects.Forge = unknownForge{f}
	mergeOnce(t, m, task, p)
	m.C.Effects.Forge = f
	m.later = map[string]time.Time{}
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// Main is brought in only when the PR actually conflicts with it; then the
// worker resolves it, and the new head needs a new read.
func TestCatchUpOnlyOnConflict(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	m.save(task, "")
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	m.RecordRead("t1", task.SHA, "perms", "land", "")
	read := task.SHA
	pushMain(t, f, map[string]string{"secure/box.go": "package secure\n\n// main's own\n"})
	if pr, _ := f.GetPR(p.Repo, task.PR.Number); pr.Mergeable != "CONFLICTING" {
		t.Fatalf("the local forge should see the conflict: %+v", pr)
	}
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || task.SHA != read || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// securityList puts only secure/** on main's security list.
var securityList = map[string]string{core.SecurityList: "# paths that need a security read\npaths = [\n  \"secure/**\", # the boundary\n  \"tests/LANDING-security.toml\",\n]\n"}

// reviewerLand records the Hive's own reviewer's land on the task's head.
func reviewerLand(t *testing.T, m *Module, task *Task, p core.Project, id string) {
	t.Helper()
	main, _ := m.C.Effects.Sync(p)
	ch, _ := m.C.Effects.Classify(p, main, task.SHA)
	if _, err := m.C.Runs.Start(core.RunSpec{ID: id, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": task.SHA, "diff.patch": ch.Diff}}); err != nil {
		t.Fatal(err)
	}
	if err := m.C.Effects.RecordVerdict(core.Verdict{Project: p.Name, SHA: task.SHA, Run: id, Verdict: "land"}); err != nil {
		t.Fatal(err)
	}
}

// A change to paths not on the list merges on the Hive's own read.
func TestSecurityListUnlistedPasses(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, securityList, map[string]string{"plain/x.go": "package plain\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}

// seatSays ends the task's security seat run on its head with a verdict, as
// the seat would; the gate must have started it.
func seatSays(t *testing.T, m *Module, task *Task, seat, verdict string) core.Run {
	t.Helper()
	id := "s-" + task.ID + "-" + core.Short(task.Diff) + "-" + seat + "-0"
	r, ok := m.C.Runs.Get(id)
	if !ok || !r.ReadOnly || r.Role != "reviewer" || r.Inputs["seat"] != seat {
		t.Fatalf("no read-only %s seat run %s: %+v", seat, id, r)
	}
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone,
		"result": harness.Result{Structured: []byte(`{"verdict":"` + verdict + `","summary":"seat ` + seat + ` says ` + verdict + `","findings":[{"file":"secure/box.go","issue":"widens a mount"}],"token":"` + reviewToken(id) + `"}`)}})
	return r
}

// rootPanelRig is a task on a root path of the list whose security seat
// said land and whose panel is seated.
func rootPanelRig(t *testing.T) (*Module, *Task, core.Project, *core.Local) {
	t.Helper()
	list := map[string]string{core.SecurityList: "paths = [\"secure/**\"]\nroot = [\"ops/**\"]\n"}
	m, task, p, f := gateRigSeeded(t, false, list, map[string]string{"ops/deploy.sh": "echo hi\n"})
	reviewerLand(t, m, task, p, "r1")
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	seatSays(t, m, task, core.SeatSecurity, "land")
	mergeOnce(t, m, task, p) // records the seat's land
	mergeOnce(t, m, task, p) // seats the panel
	return m, task, p, f
}

// refusingForge is GitHub with a required check red on main: its branch rule
// refuses the merge.
type refusingForge struct{ *core.Local }

func (refusingForge) MergePR(repo string, number int, sha, subject string) (string, error) {
	return "", errors.New(`gh pr merge: GraphQL: Required status check "test" is failing. (mergePullRequest)`)
}

// timedOutForge merges but answers with an error, as GitHub's 504 did.
type timedOutForge struct{ *core.Local }

func (f timedOutForge) MergePR(repo string, number int, sha, subject string) (string, error) {
	if _, err := f.Local.MergePR(repo, number, sha, subject); err != nil {
		return "", err
	}
	return "", errors.New("gh pr merge: HTTP 504: Gateway Timeout")
}

// mergedEvents are the journal's effect.merged events for pr.
func mergedEvents(m *Module, pr int) (shas []string) {
	m.C.J.Replay(func(e core.Event) {
		var x struct {
			PR       int    `json:"pr"`
			MergeSHA string `json:"merge_sha"`
		}
		if e.Kind == "effect.merged" && e.Decode(&x) == nil && x.PR == pr {
			shas = append(shas, x.MergeSHA)
		}
	})
	return shas
}

// A merge whose forge call errored but went through is the Hive's merge all
// the same: the next look finds it merged at the head the Hive was merging
// and journals its effect.merged once, so a release can cut at it.
func TestMergeThatErroredButWentThrough(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.C.Effects.Forge = timedOutForge{f}
	if err := m.merge(task, p); err == nil || task.Stage != StageMerging {
		t.Fatalf("err %v, stage %q", err, task.Stage)
	}
	if got := mergedEvents(m, task.PR.Number); len(got) != 0 {
		t.Fatalf("effect.merged before the merge was seen: %q", got)
	}
	m.C.Effects.Forge = f
	mergeOnce(t, m, task, p)
	pr, _ := f.GetPR(p.Repo, task.PR.Number)
	if task.Stage != StageMerged || task.MergeSHA != pr.MergeSHA || logged(task, "outside the Hive") || !logged(task, "it went through") {
		t.Fatalf("stage %q, merge %q, log %v", task.Stage, task.MergeSHA, task.Log)
	}
	m.closedPR(task, p, pr) // a second look journals nothing more
	if got := mergedEvents(m, task.PR.Number); len(got) != 1 || got[0] != pr.MergeSHA {
		t.Fatalf("effect.merged %q, want one at %s", got, pr.MergeSHA)
	}
}

// One the Hive's merge errored on but someone merged at another head is
// theirs: it stays outside the Hive and journals no effect.merged.
func TestMergeErroredThenMergedElsewhere(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	m.C.Effects.Forge = refusingForge{f}
	if err := m.merge(task, p); err == nil {
		t.Fatal("the refused merge said nothing")
	}
	m.C.Effects.Forge = f
	pr, _ := f.GetPR(p.Repo, task.PR.Number)
	pr.State, pr.MergeSHA, pr.HeadSHA = "MERGED", "abc123", "def456"
	m.closedPR(task, p, pr)
	if !logged(task, "outside the Hive") || len(mergedEvents(m, task.PR.Number)) != 0 {
		t.Fatalf("log %v, effect.merged %q", task.Log, mergedEvents(m, task.PR.Number))
	}
}

// queued is the newest message queued for run, as the worker reads it.
func queued(m *Module, run string) (text string) {
	m.C.J.Replay(func(e core.Event) {
		var n struct{ ID, Text string }
		if e.Kind == "run.queued" && e.Decode(&n) == nil && n.ID == run {
			text = n.Text
		}
	})
	return text
}

// While merges keep coming, main's newest commits may have only cancelled or
// unfinished runs. A red PR doesn't wait on them: it takes main's answer from
// the newest commit whose run of the check finished, and a cancelled run is
// never that answer. Here main fails the check two commits back, so the PR is
// no worse than main and merges.
func TestMergeForgeCheckUsesLastFinishedMainRun(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	base, _ := m.C.Effects.Sync(p)
	pushMain(t, f, map[string]string{"a.txt": "1"})
	cancelled, _ := m.C.Effects.Sync(p)
	pushMain(t, f, map[string]string{"b.txt": "2"})
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test", State: core.CheckFail})
	f.SetChecks(p.Repo, base, core.CheckRun{Name: "test", State: core.CheckFail})
	f.SetChecks(p.Repo, cancelled, core.CheckRun{Name: "test", State: core.CheckCantStart, Detail: "cancelled"})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test", State: core.CheckPending})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || task.Round != 0 || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

// A security seat left on the reviewer's model, or a seat on a harness the
// Hive doesn't have, reads on the reviewer's and says so in an alert.
func TestSeatModelFallsBack(t *testing.T) {
	m, _, p, _ := gateRigSeeded(t, false, securityList, map[string]string{"secure/box.go": "package secure\n"})
	h, model := m.Agents.Model("reviewer", p.Name)
	alerts := func() string {
		var b strings.Builder
		for _, e := range m.C.J.Since(0) {
			if e.Kind == "alert.raised" {
				b.Write(e.Data)
			}
		}
		return b.String()
	}
	cfg := m.C.WS.LoadConfig()
	cfg.TribunalModels = []core.TribunalSeat{{Model: model}}
	m.C.WS.SaveConfig(cfg)
	if gh, gm, _ := m.seatModel(p, core.SeatSecurity); gh != h || gm != model || !strings.Contains(alerts(), "ordinary reviewer's model") {
		t.Fatalf("security seat with no other model: %s/%s, alerts %s", gh, gm, alerts())
	}
	cfg.TribunalModels = []core.TribunalSeat{{Harness: "no-such-harness", Model: "other"}}
	m.C.WS.SaveConfig(cfg)
	if gh, gm, _ := m.seatModel(p, "panel-0"); gh != h || gm != model || !strings.Contains(alerts(), "no-such-harness") {
		t.Fatalf("seat on a missing harness: %s/%s, alerts %s", gh, gm, alerts())
	}
}

// Seat tries are counted per own diff: a head whose diff moved without a
// send-back starts its seats fresh.
func TestSeatTriesFollowTheDiff(t *testing.T) {
	task := &Task{Diff: "a", SeatTries: map[string]int{"a#security": 2, "b#security": 1}}
	task.setDiff("b")
	if task.Diff != "b" || len(task.SeatTries) != 1 || task.SeatTries["b#security"] != 1 {
		t.Fatalf("tries after the diff moved: %+v", task.SeatTries)
	}
}

// advisoryList is a security list on main naming the Windows job advisory.
var advisoryList = map[string]string{core.SecurityList: "paths = []\nadvisory_checks = [\"test (windows-latest)\"]\n"}

// testStep runs the test step and, when it queued the head's check behind
// it, waits for the check and runs the step again to take the result.
func testStep(m *Module, task *Task, p core.Project) error {
	if err := m.test(task, p); err != nil {
		return err
	}
	m.mu.Lock()
	_, queued := m.testRuns[task.ID]
	m.mu.Unlock()
	if !queued {
		return nil
	}
	m.Wait()
	m.mu.Lock()
	delete(m.later, task.ID)
	m.mu.Unlock()
	return m.test(task, p)
}
