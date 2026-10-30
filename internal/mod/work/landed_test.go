package work

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A task whose work landed on main through another task ends as landed when it
// next meets main: its PR and issue are closed naming the main commit that
// brought the work, it is done, not blocked or stopped, nothing more is run
// for it, and nothing is measured. So does one whose branch brought main in
// and has no own diff left, and the commit named is still the one that landed
// the work, not main's newest. One waiting for its read gets no reviewer.
func TestAlreadyLandedThroughAnotherTask(t *testing.T) {
	for _, stage := range []string{StageTesting, StageMerging} {
		t.Run(stage, func(t *testing.T) {
			m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
			is, err := f.CreateIssue(p.Repo, "work", "", marker(task.ID))
			if err != nil {
				t.Fatal(err)
			}
			task.Issue, task.Stage = is, stage
			m.save(task, "")
			var merged int
			m.OnMerged = func(Task) { merged++ }
			pushMain(t, f, map[string]string{"feature.txt": "y"}) // the other task's merge
			carried, _ := m.C.Effects.Sync(p)
			pushMain(t, f, map[string]string{"later.txt": "z"}) // main moves on
			main, _ := m.C.Effects.Sync(p)
			if stage == StageMerging { // brought up to date: its tree is main's
				next, err := m.C.Effects.UpdateBranch(p, task.SHA, main, "")
				if err != nil {
					t.Fatal(err)
				}
				dir := m.C.WS.Project(p.Name)
				if base, err := core.Git(dir, "merge-base", main, next); err != nil || core.OwnDiff(dir, base, next) != "" {
					t.Fatalf("the updated branch still has an own diff (%v)", err)
				}
				task.SHA = next
				m.save(task, "")
			}
			before := len(m.C.Runs.List(nil))
			if err := m.step(task); err != nil {
				t.Fatal(err)
			}
			if task.Stage != StageDone || task.Reason != "" || task.MergeSHA != carried || task.IssueOpen || merged != 0 {
				t.Fatalf("stage %q, reason %q, merge %s (want %s), issue open %v, measured %d", task.Stage, task.Reason, task.MergeSHA, carried, task.IssueOpen, merged)
			}
			if !logged(task, "already landed: main at "+core.Short(carried)) || !logged(task, fmt.Sprintf("closed issue #%d", is.Number)) {
				t.Fatalf("log %v", task.Log)
			}
			pr, _ := f.GetPR(p.Repo, task.PR.Number)
			cs := f.Comments(p.Repo, task.PR.Number)
			if pr.State != "CLOSED" || len(cs) == 0 || !strings.Contains(cs[len(cs)-1], carried) {
				t.Fatalf("PR %s, comments %q", pr.State, cs)
			}
			if cs := f.Comments(p.Repo, is.Number); len(cs) == 0 || !strings.Contains(cs[len(cs)-1], carried) {
				t.Fatalf("issue comments %q", cs)
			}
			if r, _ := m.C.Runs.Get("w1"); !r.Terminal() && !r.StopAsked {
				t.Fatal("its worker still runs")
			}
			if err := m.step(task); err != nil || task.Stage != StageDone || len(m.C.Runs.List(nil)) != before {
				t.Fatalf("stage %q, runs %d (were %d): %v", task.Stage, len(m.C.Runs.List(nil)), before, err)
			}
			for _, r := range m.C.Runs.List(nil) {
				if strings.HasPrefix(r.ID, "r-"+task.ID) {
					t.Fatalf("a reviewer %s was started", r.ID)
				}
			}
			if task.Reviewer != "" {
				t.Fatalf("reviewer %q", task.Reviewer)
			}
		})
	}
}

// A task whose work landed further back than carrier looks names the oldest
// commit it looked at, and its PR's comment says an older one may carry it.
func TestAlreadyLandedFarBackIsApproximate(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	task.Stage = StageTesting
	m.save(task, "")
	pushMain(t, f, map[string]string{"feature.txt": "y"})
	pushMain(t, f, map[string]string{"a.txt": "1"})
	oldest, _ := m.C.Effects.Sync(p)
	pushMain(t, f, map[string]string{"b.txt": "2"})
	main, _ := m.C.Effects.Sync(p)
	defer func(n int) { carrierBack = n }(carrierBack)
	carrierBack = 1
	if at, exact := carrier(m.C.WS.Project(p.Name), main, task.SHA); at != oldest || exact {
		t.Fatalf("carrier %s exact %v, want %s approximate", at, exact, oldest)
	}
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	cs := f.Comments(p.Repo, task.PR.Number)
	if task.Stage != StageDone || task.MergeSHA != oldest || len(cs) == 0 || !strings.Contains(cs[len(cs)-1], oldest+" or an older commit carries") {
		t.Fatalf("stage %q, merge %s, comments %q", task.Stage, task.MergeSHA, cs)
	}
}

// A worker that stops blocked because its branch is identical to main ends the
// task as landed rather than blocked.
func TestBlockedOnLandedWorkIsLanded(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	task.Stage = StageWorking
	pushMain(t, f, map[string]string{"feature.txt": "y"})
	m.C.Runs.SetStatus("w1", core.StatusBlocked, "this branch's tree is identical to main")
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageDone || task.MergeSHA == "" {
		t.Fatalf("stage %q, reason %q, merge %q", task.Stage, task.Reason, task.MergeSHA)
	}
	if pr, _ := f.GetPR(p.Repo, task.PR.Number); pr.State != "CLOSED" {
		t.Fatalf("PR %s", pr.State)
	}
}

// A task whose own PR was merged on the forge (by the person on GitHub, or the
// Hive's own merge before a restart) isn't taken for one that landed through
// another task: from testing, the merge path records it as merged and it is
// measured once, with no "already on main" comment. A checkpoint's merge is
// banked.
func TestOwnMergedPRIsNotAlreadyLanded(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(fmt.Sprint("checkpoint=", checkpoint), func(t *testing.T) {
			m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
			is, _ := f.CreateIssue(p.Repo, "work", "", marker(task.ID))
			task.Issue = is
			if checkpoint {
				task.Checkpoint = task.SHA
			}
			m.save(task, "")
			var merged int
			m.OnMerged = func(Task) { merged++ }
			pr := task.PR
			sha, err := f.MergePR(p.Repo, pr.Number, task.SHA, "work")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5 && merged == 0; i++ {
				if err := m.step(task); err != nil {
					t.Fatal(err)
				}
			}
			switch {
			case merged != 1:
				t.Fatalf("stage %q, measured %d, log %v", task.Stage, merged, task.Log)
			case checkpoint && (task.Banked != 1 || len(task.Banks) != 1 || task.Banks[0].MergeSHA != sha || task.Stage != StageWorking):
				t.Fatalf("checkpoint not banked: %+v", task)
			case !checkpoint && (task.Stage != StageMerged || task.MergeSHA != sha):
				t.Fatalf("stage %q, merge %s (want %s)", task.Stage, task.MergeSHA, sha)
			}
			for _, n := range []int{pr.Number, is.Number} {
				for _, c := range f.Comments(p.Repo, n) {
					if strings.Contains(c, "Already on main") {
						t.Fatalf("#%d told it was already on main: %q", n, c)
					}
				}
			}
		})
	}
}

// A task whose work another PR carried to main in its own shape (a squash
// merge of several branches, say) ends as landed when the lead names that
// merge: main need not hold its head. Its own open PR is closed naming the
// carrier. So does a task with no commits or PR of its own. A named commit
// from before the task's work started is refused.
func TestLandedOnACarrierThatSquashedItsWork(t *testing.T) {
	for _, own := range []bool{true, false} {
		t.Run(fmt.Sprint("own commits=", own), func(t *testing.T) {
			m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
			base, _ := m.C.Effects.Sync(p)
			task.Stage, task.Base = StageWorking, base
			if !own {
				task.SHA, task.PR = "", core.PR{}
			}
			m.save(task, "")
			var merged int
			m.OnMerged = func(Task) { merged++ }
			pushMain(t, f, map[string]string{"feature.txt": "y, reworked with another branch", "other.txt": "o"}) // #57's squash
			carried, _ := m.C.Effects.Sync(p)
			pushMain(t, f, map[string]string{"later.txt": "z"})
			if _, err := m.Landed(task.ID, base, "lead", "x"); err == nil || !strings.Contains(err.Error(), "from before") {
				t.Fatalf("took main from before its work: %v", err)
			}
			if _, err := m.Landed(task.ID, "", "lead", "x"); err == nil || !strings.Contains(err.Error(), "merge <sha>") {
				t.Fatalf("not told to name the carrier: %v", err)
			}
			got, err := m.Landed(task.ID, core.Short(carried), "lead", "carried by #57")
			if err != nil {
				t.Fatal(err)
			}
			if got.Stage != StageMerged || got.MergeSHA != carried || merged != 1 || !logged(&got, "on the caller's word") || !logged(&got, "carried by #57") {
				t.Fatalf("stage %q, merge %s (want %s), measured %d, log %v", got.Stage, got.MergeSHA, carried, merged, got.Log)
			}
			if own {
				pr, _ := f.GetPR(p.Repo, task.PR.Number)
				cs := f.Comments(p.Repo, task.PR.Number)
				if pr.State != "CLOSED" || len(cs) == 0 || !strings.Contains(cs[len(cs)-1], carried) {
					t.Fatalf("PR %s, comments %q", pr.State, cs)
				}
			}
		})
	}
}
