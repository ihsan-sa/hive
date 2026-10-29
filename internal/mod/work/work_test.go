package work

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// flakyForge fails the first `fail` closes, the way GitHub does on a 503, and
// keeps the body of each pull request it opens.
type flakyForge struct {
	*core.Local
	fail   int
	down   bool
	users  int
	refuse bool
	// checksRefused refuses the token every read of checks, as GitHub does
	// a token without Checks: read on a repo with workflows, in the words
	// GitHub.Checks gives (TestChecksRefusedWithoutWorkflows pins them).
	checksRefused bool
	checksAsked   int
	hold          chan struct{} // the first User call waits for it to close
	closed        []int
	bodies        []string
}

func (f *flakyForge) CreatePR(repo, head, base, title, body string) (core.PR, error) {
	f.bodies = append(f.bodies, body)
	return f.Local.CreatePR(repo, head, base, title, body)
}

func (f *flakyForge) Checks(repo, sha string) ([]core.CheckRun, error) {
	f.checksAsked++
	if f.checksRefused {
		return nil, errors.New("gh api repos/" + repo + "/commits/" + sha + "/check-runs?per_page=100: gh: Resource not accessible by personal access token (HTTP 403)")
	}
	return f.Local.Checks(repo, sha)
}

func (f *flakyForge) User() (string, error) {
	f.users++
	if f.hold != nil && f.users == 1 {
		<-f.hold
	}
	if f.down {
		return "", errors.New("dial tcp: lookup api.github.com: no such host")
	}
	if f.refuse {
		return "", errors.New("HTTP 403: Resource not accessible by integration")
	}
	return f.Local.User()
}

func (f *flakyForge) CloseIssue(repo string, number int) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("HTTP 503")
	}
	f.closed = append(f.closed, number)
	return f.Local.CloseIssue(repo, number)
}

// noCommitRig is a work module with one project and a task whose worker
// finished with no commit.
func noCommitRig(t *testing.T, fail int) (*Module, *flakyForge, *Task, core.Project) {
	dir := t.TempDir()
	forge := &flakyForge{Local: &core.Local{Dir: filepath.Join(dir, "forge")}, fail: fail}
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	p, err := c.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	if err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c}
	m := New(c, a, chat.New(c, a))
	t.Cleanup(m.Wait) // a task's checkouts go off the save's path
	task := &Task{ID: "t1", Project: "p", Title: "restart the box", Kind: "work", Stage: StageLanding, Issue: core.Issue{Number: 7}}
	return m, forge, task, p
}

func logged(t *Task, s string) bool {
	for _, e := range t.Log {
		if strings.Contains(e.Text, s) {
			return true
		}
	}
	return false
}

// With no step summary wired, a worker with no commit blocks the task as it
// did before outside steps existed, instead of panicking.
func TestNoCommitsWithoutSteps(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 0)
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}, Result: "gave up"})
	if task.Stage != StageBlocked || !strings.Contains(task.Reason, "without any commits: gave up") {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	if strings.Contains(task.Reason, "outside the checkout") || len(forge.closed) != 0 {
		t.Fatalf("reason %q, closed %v", task.Reason, forge.closed)
	}
}

// A worker that finished done with no commit and no step outside the
// checkout still blocks: nothing came of the ask.
func TestNoCommitsIsBlocked(t *testing.T) {
	m, _, task, p := noCommitRig(t, 0)
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}, Finish: &core.Finish{Status: core.StatusDone, Account: "nothing to do"}})
	if task.Stage != StageBlocked {
		t.Fatalf("a plain ask: stage %q", task.Stage)
	}
}

// A step still pending when the worker finishes blocks the task and is named
// as pending, not as a failure.
func TestNoCommitsWithPendingStep(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 0)
	m.Steps = func(string) (string, bool) { return "- restart: still pending (tribunal)", false }
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}})
	if task.Stage != StageBlocked || !strings.Contains(task.Reason, "not all done:\n- restart: still pending (tribunal)") {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
	if len(forge.closed) != 0 || task.IssueOpen {
		t.Fatalf("closed %v, issue open %v", forge.closed, task.IssueOpen)
	}
}

// When every outside step ran, the task is done and says so on its issue
// first; a close that fails is journaled and the next pass closes it.
func TestNoCommitsDoneRetriesClose(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 1)
	m.Steps = func(string) (string, bool) { return "- restart: ran", true }
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}})
	if task.Stage != StageDone || !task.IssueOpen || !logged(task, "closing the issue failed (will retry): HTTP 503") {
		t.Fatalf("stage %q, issue open %v, log %v", task.Stage, task.IssueOpen, task.Log)
	}
	if c := forge.Comments("me/p", 7); len(c) != 1 || !strings.Contains(c[0], "Done with no commit") {
		t.Fatalf("comments %q", c)
	}
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageDone || task.IssueOpen || len(forge.closed) != 1 || forge.closed[0] != 7 {
		t.Fatalf("stage %q, issue open %v, closed %v", task.Stage, task.IssueOpen, forge.closed)
	}
}

// A close that keeps failing backs off and gives up once, leaving the task done.
func TestCloseIssueGivesUp(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 100)
	task.Stage, task.IssueOpen = StageDone, true
	for i := 0; i < 20 && task.IssueOpen; i++ {
		m.closeIssue(task, p)
	}
	if task.Stage != StageDone || task.IssueOpen || forge.fail != 92 || !logged(task, "gave up closing the issue, left open on the forge: HTTP 503") {
		t.Fatalf("stage %q, issue open %v, attempts %d, log %v", task.Stage, task.IssueOpen, 100-forge.fail, task.Log)
	}
}

// While GitHub is unreachable a task's gaps grow to a ceiling and it's never
// stopped; the first step that gets through wakes every waiting task at once.
func TestOutageBacksOffAndWakes(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	var gaps []time.Duration
	for range 20 {
		// From just before the step: a slow save (Windows) mustn't read 30s as 29s.
		before := time.Now()
		m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
		gaps = append(gaps, task.Retry.Sub(before).Truncate(time.Second))
	}
	why := "can't reach GitHub: no internet connection"
	if task.Stage != StageLanding || task.Failures != 0 || m.Offline() != why || !logged(task, why+"; retrying") {
		t.Fatalf("stage %q, failures %d, offline %q, log %v", task.Stage, task.Failures, m.Offline(), task.Log)
	}
	if gaps[0] != 30*time.Second || gaps[1] != time.Minute || gaps[4] != 8*time.Minute || gaps[5] != 10*time.Minute || gaps[19] != 10*time.Minute {
		t.Fatalf("gaps %v", gaps)
	}
	if m.due(*task) {
		t.Fatal("due inside its gap while offline")
	}
	forge.down = true
	m.settle(&Task{ID: "t2"}, nil) // a step that never touched GitHub
	if m.Offline() != why || m.due(*task) {
		t.Fatalf("offline %q, due %v after a step that proves nothing", m.Offline(), m.due(*task))
	}
	forge.down = false
	passGap(m)                     // the shared check's gap is over
	m.settle(&Task{ID: "t2"}, nil) // and once GitHub answers
	if m.Offline() != "" || !m.due(*task) {
		t.Fatalf("offline %q, due %v once back", m.Offline(), m.due(*task))
	}
	// A real error after the outage counts from zero and keeps its own gap.
	m.settle(task, errors.New("HTTP 422"))
	if task.Failures != 1 || task.Stage != StageLanding || m.due(*task) {
		t.Fatalf("failures %d, stage %q, due %v", task.Failures, task.Stage, m.due(*task))
	}
}

// Idle tasks during an outage share one GitHub check per backoff step.
func TestOutageChecksOncePerStep(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
	forge.down = true
	for step, gap := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute} {
		for range 50 {
			m.settle(&Task{ID: "idle"}, nil)
		}
		if forge.users != step+1 || m.Offline() == "" {
			t.Fatalf("step %d: %d checks, offline %q", step, forge.users, m.Offline())
		}
		if got := nextCheck(m); got != gap {
			t.Fatalf("step %d: next check in %v, want %v", step, got, gap)
		}
		passGap(m)
	}
}

// A second outage right after a recovery gets its first check at once.
func TestOutageAfterRecoveryChecksAtOnce(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
	m.settle(&Task{ID: "idle"}, nil) // GitHub answers
	if m.Offline() != "" {
		t.Fatalf("offline %q after recovery", m.Offline())
	}
	m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
	forge.down = true
	m.settle(&Task{ID: "idle"}, nil)
	if forge.users != 2 {
		t.Fatalf("%d checks, want the new outage checked at once", forge.users)
	}
}

// One check is out at a time: idle tasks don't start another while it hangs
// past its gap, and when it answers every waiting task wakes.
func TestOutageOneCheckAtATime(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
	release := holdCheck(m, forge)
	passGap(m)
	for range 50 {
		m.settle(&Task{ID: "idle"}, nil)
	}
	release()
	if forge.users != 1 || m.Offline() != "" || !m.due(*task) {
		t.Fatalf("%d checks, offline %q, due %v", forge.users, m.Offline(), m.due(*task))
	}
}

// A step that fails while a check is out isn't wiped when the check answers.
func TestOutageDuringCheckSticks(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
	release := holdCheck(m, forge)
	m.settle(task, errors.New("dial tcp: i/o timeout"))
	release()
	if why := "can't reach GitHub: the connection failed"; m.Offline() != why || m.due(*task) {
		t.Fatalf("offline %q, due %v", m.Offline(), m.due(*task))
	}
}

// A check refused for a reason other than the network says GitHub is back.
func TestOutageCheckOtherErrorClears(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	m.settle(task, errors.New("dial tcp: lookup api.github.com: no such host"))
	forge.refuse = true
	m.settle(&Task{ID: "idle"}, nil)
	if m.Offline() != "" || !m.due(*task) {
		t.Fatalf("offline %q, due %v", m.Offline(), m.due(*task))
	}
}

// holdCheck starts an idle task's GitHub check and leaves it hanging; release
// lets it answer and waits for that settle to return.
func holdCheck(m *Module, forge *flakyForge) (release func()) {
	forge.hold = make(chan struct{})
	done := make(chan struct{})
	go func() { m.settle(&Task{ID: "slow"}, nil); close(done) }()
	for {
		m.mu.Lock()
		out := m.probing
		m.mu.Unlock()
		if out {
			return func() { close(forge.hold); <-done }
		}
		time.Sleep(time.Millisecond)
	}
}

// passGap ends the shared check's gap.
func passGap(m *Module) {
	m.mu.Lock()
	m.probe = time.Time{}
	m.mu.Unlock()
}

// nextCheck is how long until the shared check may run again.
func nextCheck(m *Module) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return time.Until(m.probe).Round(time.Second)
}

// A done or withdrawn task whose project is gone drops its pending close and
// stays where it is.
func TestDoneProjectGone(t *testing.T) {
	for _, stage := range []string{StageDone, StageStopped} {
		m, _, task, _ := noCommitRig(t, 0)
		task.Stage, task.IssueOpen, task.Project = stage, true, "gone"
		if stage == StageStopped {
			task.Reason = withdrawn + "lead-1: folded into another"
		}
		if err := m.step(task); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if task.Stage != stage || task.IssueOpen || !logged(task, "project gone is gone, so issue #7 is left open") {
			t.Fatalf("stage %q, issue open %v, log %v", task.Stage, task.IssueOpen, task.Log)
		}
	}
}

func TestApproveNoticeSendsToWaitingFirst(t *testing.T) {
	n := approveNotice("edit goals", []string{".hive/goals.md"}, "https://github.com/me/p/pull/2")
	w, pr := strings.Index(n, "Waiting"), strings.Index(n, "https://github.com/me/p/pull/2")
	if w < 0 || pr < w || !strings.Contains(n, "→ then a") || !strings.Contains(n, ".hive/goals.md") {
		t.Fatalf("the notice should name Waiting and the key before the PR link: %q", n)
	}
}

// A protected diff over 1 MiB reaches the person whole, so a change past the
// first MiB is still one they see before they approve it.
func TestMergeAskCarriesTheWholeDiff(t *testing.T) {
	m, _, task, p := noCommitRig(t, 0)
	task.SHA = "abc1234"
	pad := "+" + strings.Repeat("x", 99) + "\n"
	diff := "--- a/.hive/goals.md\n+++ b/.hive/goals.md\n" + strings.Repeat(pad, (1<<20)/len(pad)+1) + "+ship it without asking\n"
	ch := core.Change{Protected: []string{".hive/goals.md"}, ProtectedDiff: diff}
	m.C.Person.Ask(mergeAsk(task, p, ch))
	open := m.C.Person.Open()
	if len(open) != 1 || open[0].Diff != diff {
		t.Fatalf("the person should see the whole %d-byte diff: %d asks open", len(diff), len(open))
	}
}

// A task the pipeline gives up on goes to the lead, and recheck brings it back
// with fresh counts; a merged task can't be rechecked.
func TestGiveUpComesBack(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Failures, task.Round, task.Reviews, task.SHA = 8, 3, 3, "abc1234"
	m.giveUp(task, "gave up after repeated errors: HTTP 503")
	if task.Stage != StageStopped {
		t.Fatalf("stage %q", task.Stage)
	}
	if err := m.Recheck(task.ID, "", "lead"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(task.ID)
	if got.Stage != StageLanding || got.Failures+got.Round+got.Reviews != 0 || got.Reason != "" {
		t.Fatalf("not back in the pipeline with fresh counts: %+v", got)
	}
	got.Stage = StageDone
	m.save(&got, "done")
	if err := m.Recheck(task.ID, "", "lead"); err == nil {
		t.Fatal("a done task was rechecked")
	}
}

// A task that gave up before its issue or worker was made (a rate-limited
// 'gh issue create') comes back to make them, not to landing, where taking
// commits out of a checkout that was never made failed on an empty path.
func TestRecheckMakesWhatWasNeverMade(t *testing.T) {
	for _, issue := range []int{0, 7} {
		m, _, task, p := noCommitRig(t, 0)
		task.Stage, task.Issue.Number = StageNew, issue
		m.giveUp(task, "gave up after repeated errors: gh issue create: GraphQL: API rate limit already exceeded")
		if err := m.Recheck(task.ID, "", "lead"); err != nil {
			t.Fatal(err)
		}
		got, _ := m.Get(task.ID)
		want := StageNew
		if issue != 0 {
			want = StageQueued
		}
		if got.Stage != want || got.Reason != "" {
			t.Fatalf("issue %d: stage %q, want %q", issue, got.Stage, want)
		}
		got.Stage = StageLanding // one already sent to landing before the fix
		m.save(&got, "landing")
		if err := m.land(&got, p); err != nil || got.Stage != want {
			t.Fatalf("issue %d: landing a task with no checkout: stage %q, err %v", issue, got.Stage, err)
		}
	}
}

// A task stuck on its way to the merge (here waiting on a stale commit's
// checks) is the lead's to retry: recheck takes it back to landing with fresh
// counts, a stuck checkpoint is dropped while its worker goes on, and a step
// running on the task refuses it.
func TestRecheckRetriesAStuckMerge(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Stage, task.SHA, task.Round, task.Failures = StageMerging, "2125f74e", 2, 4
	task.Reason = "waiting for the forge's checks on 2125f74e"
	m.save(task, task.Reason)
	m.mu.Lock()
	m.busy[task.ID] = true
	m.mu.Unlock()
	if err := m.Recheck(task.ID, "", "lead"); err == nil || !strings.Contains(err.Error(), "step running") {
		t.Fatalf("retried under a running step: %v", err)
	}
	m.mu.Lock()
	delete(m.busy, task.ID)
	m.mu.Unlock()
	task.Retry = time.Now().Add(time.Hour) // backed off before the retry
	m.save(task, "rate limited")
	m.mu.Lock()
	m.later[task.ID] = time.Now().Add(time.Hour)
	m.mu.Unlock()
	if err := m.Recheck(task.ID, "", "lead"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(task.ID)
	m.mu.Lock()
	due := m.due(got)
	m.mu.Unlock()
	if !due {
		t.Fatalf("the retry still waits out the backoff from before it: retry %v", got.Retry)
	}
	if got.Stage != StageLanding || got.Failures+got.Round != 0 || got.Reason != "" || !logged(&got, "retried from merging") {
		t.Fatalf("not back to landing with fresh counts: %+v", got)
	}

	got.Stage, got.Checkpoint = StageTesting, "2125f74e"
	m.save(&got, "a checkpoint")
	if err := m.Recheck(task.ID, "try again", "lead"); err == nil {
		t.Fatal("sent back a worker still working past its checkpoint")
	}
	if err := m.Recheck(task.ID, "", "lead"); err != nil {
		t.Fatal(err)
	}
	if got, _ = m.Get(task.ID); got.Stage != StageWorking || got.Checkpoint != "" {
		t.Fatalf("the checkpoint should be dropped: %+v", got)
	}
}

// A task the person stopped, from the control center or by leaving its PR
// open, stays stopped: the lead can neither recheck it nor send its worker back.
func TestPersonStopHolds(t *testing.T) {
	for _, how := range []string{"stop", "leave open"} {
		m, _, task, _ := noCommitRig(t, 0)
		task.Stage = StagePerson
		m.save(task, "waiting on the person")
		if how == "stop" {
			m.Stop(task.ID, "stopped by the person")
		} else {
			m.onPersonMerge(core.Ask{Ref: task.ID + "@abc", Answer: "leave open"})
		}
		for _, back := range []string{"", "try again"} {
			if err := m.Recheck(task.ID, back, "lead"); err == nil || !strings.Contains(err.Error(), "by the person") {
				t.Fatalf("%s: recheck %q got %v", how, back, err)
			}
		}
		if got, _ := m.Get(task.ID); got.Stage != StageStopped {
			t.Fatalf("%s: stage %q", how, got.Stage)
		}
	}
}

// A worker the person stops in the control center (/api/run/stop) holds its
// task: the stop marks it the person's, and a task stopped before that mark
// existed holds too, because its worker's run says it was stopped.
func TestPersonStoppedWorkerHolds(t *testing.T) {
	for _, marked := range []bool{true, false} {
		m, _, task, p := noCommitRig(t, 0)
		if _, err := m.C.Runs.Start(core.RunSpec{ID: "w1", Role: "worker", Project: p.Name, Checkout: t.TempDir(), Harness: "fake"}); err != nil {
			t.Fatal(err)
		}
		task.Worker, task.Stage = "w1", StageWorking
		m.save(task, "working")
		m.C.Runs.Stop("w1", "stopped by the person")
		if marked {
			if err := m.checkWorker(task, p); err != nil {
				t.Fatal(err)
			}
			if task.Stage != StageStopped || !task.ByPerson {
				t.Fatalf("stage %q, by person %v", task.Stage, task.ByPerson)
			}
		} else { // stopped before the fix: no mark on the task
			task.Stage, task.Reason = StageStopped, "worker stopped: stopped by the person"
			m.save(task, task.Reason)
		}
		for _, back := range []string{"", "try again"} {
			if err := m.Recheck(task.ID, back, "lead"); err == nil || !strings.Contains(err.Error(), "by the person") {
				t.Fatalf("marked %v: recheck %q got %v", marked, back, err)
			}
		}
		if r, _ := m.C.Runs.Get("w1"); r.Status != core.StatusStopped {
			t.Fatalf("marked %v: the worker was reopened: %s", marked, r.Status)
		}
		if err := m.C.Runs.Reopen("w1", "try again"); err == nil {
			t.Fatal("a stopped run was reopened")
		}
		if err := m.Retry(task.ID); err != nil {
			t.Fatal(err)
		}
		if got, _ := m.Get(task.ID); got.ByPerson || got.Stage != StageQueued {
			t.Fatalf("the person's retry should hand the task back: %+v", got)
		}
	}
}

// A lead can't recheck a task in a project walled off from it.
// A task retried from reviewing has its reader stopped, so a hung one
// doesn't keep its slot, and the next read is a new run, not the stopped one.
func TestRecheckFromReviewingStopsTheReader(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Stage, task.SHA, task.Reviewer = StageReviewing, "2125f74e", "r-hung"
	m.C.J.MustAppend("run.created", core.RunSpec{ID: "r-hung", Role: "reviewer", Project: task.Project, Harness: "fake"})
	m.save(task, "reviewer r-hung reading 2125f74e")
	if err := m.Recheck(task.ID, "", "the stall watcher"); err != nil {
		t.Fatal(err)
	}
	if r, _ := m.C.Runs.Get("r-hung"); !r.Terminal() {
		t.Fatalf("the hung reader is still %s after the retry", r.Status)
	}
	if got, _ := m.Get(task.ID); got.Stage != StageLanding || got.Reviews != 1 {
		t.Fatalf("after the retry: %s with %d reviews, want landing with the stopped read counted", got.Stage, got.Reviews)
	}
}

func TestRecheckWalled(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	m.C.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	task := &Task{ID: "t9", Project: "secret", Title: "x", Kind: "work"}
	m.giveUp(task, "gave up")
	if _, err := m.opRecheck(core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead", Project: "p"}}, []byte(`{"task":"t9"}`)); err == nil {
		t.Fatal("a walled lead rechecked another project's task")
	}
	if got, _ := m.Get("t9"); got.Stage != StageStopped {
		t.Fatalf("stage %q", got.Stage)
	}
}

// helperRig is a work module on a project with one commit on main, in bare
// sandboxes so a checkout's commits can be extracted.
func helperRig(t *testing.T) (*Module, *flakyForge, core.Project) {
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0 // a loaded machine mustn't hold the queue
	ws.SaveConfig(cfg)
	forge := &flakyForge{Local: &core.Local{Dir: filepath.Join(dir, "forge")}}
	if _, err := forge.EnsureRepo("me/p", "", false); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(dir, "seed")
	mustGit(t, "", "clone", "-q", forge.RepoPath("me/p"), seed)
	os.WriteFile(filepath.Join(seed, "README.md"), []byte("# p\n"), 0o644)
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-q", "-m", "seed")
	mustGit(t, seed, "push", "-q", "origin", "HEAD:main")
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	p, err := c.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	if err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c, Mod: modules.New(ws, c.J)}
	return New(c, a, chat.New(c, a)), forge, p
}

// A helper whose run fails to start leaves no task behind, so nothing points
// at a run that doesn't exist and no second worker takes up its brief.
func TestHelperFailedStartLeavesNoTask(t *testing.T) {
	m, _, _ := helperRig(t)
	cfg := m.C.WS.LoadConfig()
	cfg.Harness = "no-such-harness"
	m.C.WS.SaveConfig(cfg)
	if _, err := m.Spawn(core.Run{RunSpec: core.RunSpec{ID: "parent", Role: "worker"}}, "worker", "p", "write the helper part", ""); err == nil {
		t.Fatal("the spawn started on a harness that doesn't exist")
	}
	if tasks := m.List(nil); len(tasks) != 0 {
		t.Fatalf("a failed start left tasks %+v", tasks)
	}
}

// A helper worker on a project with nothing on main yet is refused, since no
// task could land its commits; the parent files a brief instead.
func TestHelperOnEmptyProject(t *testing.T) {
	m, forge, _ := helperRig(t)
	if _, err := forge.EnsureRepo("me/e", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.C.Projects.Add(core.Project{Name: "e", Repo: "me/e"}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Spawn(core.Run{RunSpec: core.RunSpec{ID: "parent", Role: "worker"}}, "worker", "e", "start the project", "")
	if err == nil || !strings.Contains(err.Error(), "file a brief") || len(m.List(nil)) != 0 || len(m.C.Runs.List(nil)) != 0 {
		t.Fatalf("err %v, tasks %+v", err, m.List(nil))
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := core.Git(dir, args...); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// A spawned helper's commits are never left in its checkout: its task pushes
// them to a branch of its own and opens a pull request, with no issue to close.
func TestHelperCommitsGetTheirOwnPR(t *testing.T) {
	m, forge, p := helperRig(t)
	spec, err := m.Spawn(core.Run{RunSpec: core.RunSpec{ID: "parent", Role: "worker"}}, "worker", "p", "write the helper part", "")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := m.C.Runs.Get(spec.ID); !ok || r.Parent != "parent" {
		t.Fatalf("the helper's run wasn't started: %+v", r)
	}
	task, ok := m.Get(spec.Task)
	if !ok || task.Kind != "helper" || task.Worker != spec.ID || task.Stage != StageWorking || task.From != "parent" || task.Branch != "hive/"+task.ID {
		t.Fatalf("spec %+v, task %+v", spec, task)
	}
	os.WriteFile(filepath.Join(spec.Checkout, "helper.txt"), []byte("helped\n"), 0o644)
	mustGit(t, spec.Checkout, "add", "-A")
	mustGit(t, spec.Checkout, "commit", "-q", "-m", "helper work")
	if err := m.land(&task, p); err != nil {
		t.Fatal(err)
	}
	pr, found, err := forge.FindPR("me/p", task.Branch)
	if err != nil || !found || pr.HeadSHA != task.SHA || task.Stage != StageTesting {
		t.Fatalf("pr %+v found %v err %v, task %+v", pr, found, err, task)
	}
	if forge.Issues("me/p") != 0 || len(forge.bodies) != 1 || strings.Contains(forge.bodies[0], "Closes #") {
		t.Fatalf("issues %d, bodies %q", forge.Issues("me/p"), forge.bodies)
	}
}

// A helper that finishes done with commits goes on like any task, to a pull
// request in testing, and the worker that spawned it hears how its task went
// as a message, as it has no thread of its own; a lead hears in its thread.
func TestHelperLandsAndItsSpawnerHears(t *testing.T) {
	m, forge, p := helperRig(t)
	parent, err := m.C.Runs.Start(m.Agents.Spec("w-parent", "worker", "p", t.TempDir(), "split the work"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := m.Spawn(parent, "worker", "p", "write the helper part", "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(spec.Checkout, "helper.txt"), []byte("helped\n"), 0o644)
	mustGit(t, spec.Checkout, "add", "-A")
	mustGit(t, spec.Checkout, "commit", "-q", "-m", "helper work")
	m.C.Runs.SetStatus(spec.ID, core.StatusDone, "")
	for _, want := range []string{StageLanding, StageTesting} {
		task, _ := m.Get(spec.Task)
		if err := m.step(&task); err != nil {
			t.Fatal(err)
		}
		if task.Stage != want {
			t.Fatalf("stage %q, want %q: %v", task.Stage, want, task.Log)
		}
	}
	task, _ := m.Get(spec.Task)
	if pr, found, _ := forge.FindPR("me/p", task.Branch); !found || pr.HeadSHA != task.SHA {
		t.Fatalf("no pull request for the helper's commit: %+v", pr)
	}
	before := len(mustRun(t, m, parent.ID).Pending) // the helper's finish account
	toMerging(t, m, &task, p)
	mergeOnce(t, m, &task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q; want merged", task.Stage, task.Reason)
	}
	r := mustRun(t, m, parent.ID)
	if len(r.Pending) != before+1 || !strings.Contains(r.Pending[before], "Merged: write the helper part") || !strings.Contains(r.Pending[before], task.ID) {
		t.Fatalf("the spawner wasn't told: %+v", r.Pending)
	}
	lead := &Task{ID: "t-lead", Kind: "helper", From: "lead"}
	m.C.Runs.Start(core.RunSpec{ID: "lead", Role: "lead", Persistent: true, Checkout: t.TempDir()})
	m.notify(lead, "Merged: the lead's helper")
	if r, _ := m.C.Runs.Get("lead"); len(r.Pending) != 0 {
		t.Fatalf("a lead was messaged besides its thread: %+v", r.Pending)
	}
}

// A helper that finishes blocked tells its spawner once, by its own finish
// account; the task's "Blocked:" line goes to the threads, not again to it.
// One the Hive ended, which sent no account, is told by that line.
func TestBlockedHelperTellsItsSpawnerOnce(t *testing.T) {
	for _, finished := range []bool{true, false} {
		m, _, _ := helperRig(t)
		parent, err := m.C.Runs.Start(m.Agents.Spec("w-parent", "worker", "p", t.TempDir(), "split the work"))
		if err != nil {
			t.Fatal(err)
		}
		spec, err := m.Spawn(parent, "worker", "p", "write the helper part", "")
		if err != nil {
			t.Fatal(err)
		}
		if finished {
			m.C.J.MustAppend("run.finish", map[string]any{"id": spec.ID, "finish": core.Finish{Status: core.StatusBlocked, Account: "can't reach the API"}})
		}
		m.C.Runs.SetStatus(spec.ID, core.StatusBlocked, "can't reach the API")
		before := len(mustRun(t, m, parent.ID).Pending)
		task, _ := m.Get(spec.Task)
		if err := m.step(&task); err != nil {
			t.Fatal(err)
		}
		if task.Stage != StageBlocked {
			t.Fatalf("stage %q, want blocked: %v", task.Stage, task.Log)
		}
		heard := mustRun(t, m, parent.ID).Pending[before:]
		if want := map[bool]int{true: 0, false: 1}[finished]; len(heard) != want {
			t.Fatalf("finished by itself %v: the spawner heard %+v, want %d messages", finished, heard, want)
		}
	}
}

// A helper with no commits ends its task done, with nothing said on the forge:
// its account already went to the agent that spawned it.
func TestHelperNoCommitsIsDone(t *testing.T) {
	m, forge, task, p := noCommitRig(t, 0)
	task.Kind, task.Issue = "helper", core.Issue{}
	m.noCommits(task, p, core.Run{RunSpec: core.RunSpec{ID: "w1"}, Result: "answered"})
	if task.Stage != StageDone || task.IssueOpen || len(forge.closed) != 0 || !logged(task, "the helper made no commits") {
		t.Fatalf("stage %q, issue open %v, closed %v, log %v", task.Stage, task.IssueOpen, forge.closed, task.Log)
	}
}

// A lead with no project of its own must name one even when only one exists,
// since the ask may be a module or about the Hive itself; a sweep files to the
// only project, and a project's own agents file to their project. An isolated
// project it can't reach goes unnamed.
func TestBriefNamesItsProject(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	m.C.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	_, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}, []byte(`{"title":"a cost ledger"}`))
	if err == nil || !strings.Contains(err.Error(), "one of: p)") || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "create_project") {
		t.Fatalf("want a refusal naming the projects and the module path, got %v", err)
	}
	if len(m.List(nil)) != 0 {
		t.Fatal("a refused brief filed a task")
	}
	if _, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "s", Role: "sweep"}}, []byte(`{"title":"a sweep finding"}`)); err != nil {
		t.Fatalf("a sweep with one project to file in was refused: %v", err)
	}
	if _, err := m.opBrief(core.Run{RunSpec: core.RunSpec{ID: "w", Role: "worker", Project: "p"}}, []byte(`{"title":"a follow-up"}`)); err != nil {
		t.Fatal(err)
	}
	if ts := m.List(nil); len(ts) != 2 || ts[0].Project != "p" || ts[1].Project != "p" {
		t.Fatalf("want two tasks in p, got %+v", ts)
	}
}

// Nothing files a brief into the only project unasked: the ask may be a
// module or about the Hive.
func TestBriefWithoutProjectRefused(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	if _, err := m.File(Brief{Title: "a cost ledger", From: "person"}); err == nil || !strings.Contains(err.Error(), "one of: p") {
		t.Fatalf("want a refusal naming the projects, got %v", err)
	}
}

// A revert the Hive made has no worker, so findings on it stop the task
// cleanly instead of reopening a run that doesn't exist.
func TestRevertFindingsStop(t *testing.T) {
	m, _, _, p := noCommitRig(t, 0)
	task := &Task{ID: "t2", Project: "p", Title: "Revert: x", Kind: "revert", Stage: StageReviewing}
	if err := m.sendBack(task, p, "it reverts too much"); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageStopped || task.Round != 0 || !strings.Contains(task.Reason, "it reverts too much") {
		t.Fatalf("stage %q, round %d, reason %q", task.Stage, task.Round, task.Reason)
	}
}

// briefAt renders a brief for a project whose main holds config.
func briefAt(t *testing.T, config string, task *Task) string {
	m, p := rigAt(t, config)
	return m.renderBrief(task, p, "HEAD")
}

// rigAt is a rig whose project's main holds config.
func rigAt(t *testing.T, config string) (*Module, core.Project) {
	m, _, _, p := noCommitRig(t, 0)
	dir := m.C.WS.Project(p.Name)
	os.MkdirAll(filepath.Join(dir, ".hive"), 0o755)
	os.WriteFile(filepath.Join(dir, ".hive/project.json"), []byte(config), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "seed"}} {
		if out, err := core.Git(dir, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	return m, p
}

// A scored project's worker gets the person's goal as written, with no bar an
// agent set; the Hive's setup brief and an unscored brief keep their done.
func TestScoredBriefCarriesTheGoal(t *testing.T) {
	goal := "The kernel is slow. Make it faster."
	task := func() *Task {
		return &Task{ID: "t1", Project: "p", Title: goal, Done: "score above 3.139", Known: "the kernel is in k.c"}
	}
	b := briefAt(t, `{"test":"true","score":{"command":"cat s","direction":"higher"}}`, task())
	if !strings.Contains(b, "## The ask\n\n"+goal+"\n") || strings.Contains(b, "3.139") || !strings.Contains(b, "as far as you can take it") {
		t.Fatalf("scored brief:\n%s", b)
	}
	setup := task()
	setup.From = "hive"
	if b := briefAt(t, `{"test":"true","score":{"command":"cat s","direction":"higher"}}`, setup); !strings.Contains(b, "score above 3.139") {
		t.Fatalf("the Hive's own setup brief lost its done line:\n%s", b)
	}
	b = briefAt(t, `{"test":"true"}`, task())
	if !strings.Contains(b, "## What done looks like\n\nscore above 3.139\n") {
		t.Fatalf("unscored brief:\n%s", b)
	}
	m, p := rigAt(t, `{"test":"true","score":{"command":"cat s","direction":"higher"}}`)
	if b := m.issueBody(task(), p); strings.Contains(b, "3.139") || !strings.Contains(b, "as far as you can take it") {
		t.Fatalf("scored issue body:\n%s", b)
	}
	m, p = rigAt(t, `{"test":"true"}`)
	if b := m.issueBody(task(), p); !strings.Contains(b, "score above 3.139") {
		t.Fatalf("unscored issue body:\n%s", b)
	}
}

// A review's brief ends with main's standing rules, goals and lessons
// included: the seat's checkout is the change's head, where a diff may have
// edited them (hive-eval #26); a project with none of the files gets nothing
// appended.
func TestReviewRulesFromMain(t *testing.T) {
	m, p := rigAt(t, `{"test":"true"}`)
	if r := m.reviewRules(p, "HEAD"); r != "" {
		t.Fatalf("no rule files, yet: %q", r)
	}
	dir := m.C.WS.Project(p.Name)
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs/REVIEW.md"), []byte("# Review\n\nsimple wins\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hive/goals.md"), []byte("# Goals\n\nship it\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hive/lessons.md"), []byte("# Lessons\n\n- don't do that again\n"), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "rules"}} {
		if out, err := core.Git(dir, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	r := m.reviewRules(p, "HEAD")
	i, j, k := strings.Index(r, "## `docs/REVIEW.md` on main\n\n# Review\n\nsimple wins"), strings.Index(r, "## `.hive/goals.md` on main\n\n# Goals\n\nship it"), strings.Index(r, "## `.hive/lessons.md` on main\n\n# Lessons\n\n- don't do that again")
	if i < 0 || j < i || k < j {
		t.Fatalf("rules:\n%s", r)
	}
}

// The lead ends a task whose work is already on main, whatever stage it is
// stuck in: not while its PR is open and main lacks its change; with the main
// commit named when it was squashed there; at once when its PR was merged on
// the forge. The merge is recorded, OnMerged runs, and the brief that waited
// on it starts.
func TestLandedEndsATaskOnMain(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	task.Stage, task.Known = StageMerging, "Changes .hive/project.json."
	task.Base, _ = m.C.Effects.Sync(p)
	m.save(task, "")
	var merged []Task
	m.OnMerged = func(t Task) { merged = append(merged, t) }
	call := func(args string) error {
		_, err := m.C.Runs.Ops().Call(lead, "landed", json.RawMessage(args))
		return err
	}
	if err := call(`{"task":"t1"}`); err == nil || !strings.Contains(err.Error(), "why") {
		t.Fatalf("ended a task with no reason: %v", err)
	}
	if err := call(`{"task":"t1","why":"merged by hand"}`); err == nil || !strings.Contains(err.Error(), "isn't landed") {
		t.Fatalf("ended a task whose PR is open and isn't on main: %v", err)
	}
	if err := call(`{"task":"t1","why":"x","merge":"HEAD"}`); err == nil || !strings.Contains(err.Error(), "from before") {
		t.Fatalf("took a main commit from before the task's work: %v", err)
	}

	waiting := file(t, m, "tune the tests", "See .hive/project.json.")
	waiting.Stage = StageQueued
	m.save(&waiting, "")
	if _, wait := m.admit(m.List(nil)); strings.Contains(wait[waiting.ID], "task t1") { // t1 has pushed: a conflict goes back to it
		t.Fatalf("the brief on the same file waits for t1, which is merging: %q", wait[waiting.ID])
	}
	// Squashed onto main by hand: the lead names the commit.
	pushMain(t, f, map[string]string{"feature.txt": "y"})
	if err := call(`{"task":"t1","why":"x"}`); err == nil || !strings.Contains(err.Error(), "say which") {
		t.Fatalf("a squashed change wasn't asked for its commit: %v", err)
	}
	mainSHA, _ := m.C.Effects.Sync(p)
	// A checkpoint behind a head that main holds: nothing past it is lost.
	cp, _ := core.Git(m.C.WS.Project("p"), "rev-parse", mainSHA+"^")
	task.Checkpoint, task.Chat = strings.TrimSpace(cp), m.Chat.EnsureProject("p").ID
	m.save(task, "")
	if err := call(`{"task":"t1","why":"squashed by the person","merge":"` + mainSHA + `"}`); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get("t1")
	if got.Stage != StageMerged || got.MergeSHA != mainSHA || len(merged) != 1 || !logged(&got, "squashed by the person") ||
		!logged(&got, "checkpoint "+core.Short(task.Checkpoint)+" is what merged; the worker had committed nothing past it") {
		t.Fatalf("stage %q, merge %q, OnMerged %d, log %v", got.Stage, got.MergeSHA, len(merged), got.Log)
	}
	if msgs := m.Chat.Messages("project:p"); len(msgs) == 0 || msgs[len(msgs)-1].Text != "Merged: work "+task.PR.URL {
		t.Fatalf("chat %v", msgs)
	}
	if r, _ := m.C.Runs.Get("w1"); !r.Terminal() && !r.StopAsked {
		t.Fatal("its worker still runs")
	}
	if err := call(`{"task":"t1","why":"again"}`); err == nil {
		t.Fatal("ended a merged task twice")
	}
	if err := m.step(&got); err != nil || got.Stage != StageDone {
		t.Fatalf("stage %q: %v", got.Stage, err)
	}
	if start, _ := m.admit(m.List(nil)); !start[waiting.ID] {
		t.Fatal("the brief behind it didn't start")
	}

	// Merged on the forge while the Hive held it: no commit to name.
	m2, t2, p2, f2 := gateRig(t, false, map[string]string{"other.txt": "z"})
	t2.Stage, t2.Checkpoint = StageReviewing, t2.SHA // its checkpoint PR, merged by hand
	m2.save(t2, "")
	sha, err := f2.MergePR(p2.Repo, t2.PR.Number, t2.SHA, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Landed("t2", "", "lead", "x"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("no such task: %v", err)
	}
	// The worker went on past the checkpoint; those commits aren't on main.
	r, _ := m2.C.Runs.Get("w1")
	if err := os.WriteFile(filepath.Join(r.Checkout, "later.txt"), []byte("later"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, r.Checkout, "add", "-A")
	must(t, r.Checkout, "commit", "-q", "-m", "later")
	later := strings.TrimSpace(must(t, r.Checkout, "rev-parse", "HEAD")) // only in its checkout, as the real flow leaves it
	// A revert ended this way reads as the merge path words it.
	t2.Kind, t2.Title, t2.Lesson, t2.Chat = "revert", "Revert: work", "it broke the tests", m2.Chat.EnsureProject("p").ID
	m2.save(t2, "")
	got, err = m2.Landed("t1", "", "lead", "merged on GitHub at 07:53Z")
	if err != nil || got.Stage != StageMerged || got.MergeSHA != sha || !logged(&got, "merged on the forge") || got.Checkpoint != "" ||
		!logged(&got, "checkpoint "+core.Short(t2.Checkpoint)+" is what merged; the worker's commits past it, up to "+core.Short(later)+", are not on main") {
		t.Fatalf("stage %q, merge %q (want %s), log %v: %v", got.Stage, got.MergeSHA, sha, got.Log, err)
	}
	if msgs := m2.Chat.Messages("project:p"); len(msgs) == 0 || msgs[len(msgs)-1].Text != "Reverted: work. it broke the tests" {
		t.Fatalf("chat %v", msgs)
	}
}

// queue now puts a brief first in line, only with a reason, put on its
// record; the max_workers cap still holds it.
func TestQueueNowGoesFirst(t *testing.T) {
	m := queueRig(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead", Role: "lead"}}
	stuck := file(t, m, "the stuck one", "In internal/core/effects.go.")
	other := file(t, m, "an earlier idea", "")
	fix := file(t, m, "the queue fix", "In internal/core/effects.go too.")
	stuck.Stage, other.Stage, fix.Stage = StageWorking, StageQueued, StageQueued
	m.save(&stuck, "")
	m.save(&other, "")
	m.save(&fix, "")
	if start, wait := m.admit(m.List(nil)); !start[other.ID] || start[fix.ID] {
		t.Fatalf("the earlier brief didn't take the slot: %q", wait[fix.ID])
	}
	if _, err := m.C.Runs.Ops().Call(lead, "queue", json.RawMessage(`{"now":"`+fix.ID+`"}`)); err == nil {
		t.Fatal("started one now with no reason")
	}
	if _, err := m.C.Runs.Ops().Call(lead, "queue", json.RawMessage(`{"now":"`+fix.ID+`","why":"it fixes what holds the queue"}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(fix.ID)
	if !got.Now || !logged(&got, "it fixes what holds the queue") {
		t.Fatalf("now %v, log %v", got.Now, got.Log)
	}
	if start, wait := m.admit(m.List(nil)); !start[fix.ID] || start[other.ID] {
		t.Fatalf("the fix still waits: %q", wait[fix.ID])
	}
	cfg := m.C.WS.LoadConfig()
	cfg.MaxWorkers = 1 // the stuck one's worker counts toward the cap
	m.C.WS.SaveConfig(cfg)
	if _, wait := m.admit(m.List(nil)); !strings.Contains(wait[fix.ID], "max_workers") {
		t.Fatalf("now went past max_workers: %q", wait[fix.ID])
	}
}

// A PR merged on the forge outside the Hive (by the person, or the lead
// through gh) while forgeChecks holds it on a check main fails too ends as
// merged at the forge's merge commit, as the Hive's own merge does: the chat
// hears, OnMerged runs, and the brief queued behind it starts.
func TestMergedOnForgeWhileHeld(t *testing.T) {
	m, task, p, f := gateRig(t, false, map[string]string{"feature.txt": "y"})
	toMerging(t, m, task, p)
	task.Chat = m.Chat.EnsureProject("p").ID
	m.save(task, "")
	var merged []Task
	m.OnMerged = func(t Task) { merged = append(merged, t) }
	main, _ := m.C.Effects.Sync(p)
	f.SetChecks(p.Repo, task.SHA, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail}, core.CheckRun{Name: "sandboxed-e2e", State: core.CheckPending})
	f.SetChecks(p.Repo, main, core.CheckRun{Name: "test (windows-latest)", State: core.CheckFail})
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerging || task.Reason == "" || len(merged) != 0 {
		t.Fatalf("not held: stage %q, reason %q", task.Stage, task.Reason)
	}
	waiting := file(t, m, "tune the feature", "See feature.txt.")
	waiting.Stage = StageQueued
	m.save(&waiting, "")

	sha, err := f.MergePR(p.Repo, task.PR.Number, task.SHA, "work") // by hand, on the forge
	if err != nil {
		t.Fatal(err)
	}
	if err := m.step(task); err != nil {
		t.Fatal(err)
	}
	if task.Stage != StageMerged || task.MergeSHA != sha || task.Reason != "" || len(merged) != 1 || merged[0].MergeSHA != sha ||
		!logged(task, "merged on the forge outside the Hive") {
		t.Fatalf("stage %q, merge %q (want %s), reason %q, OnMerged %d, log %v", task.Stage, task.MergeSHA, sha, task.Reason, len(merged), task.Log)
	}
	if msgs := m.Chat.Messages("project:p"); len(msgs) == 0 || msgs[len(msgs)-1].Text != "Merged: work "+task.PR.URL {
		t.Fatalf("chat %v", msgs)
	}
	if err := m.step(task); err != nil || task.Stage != StageDone {
		t.Fatalf("stage %q: %v", task.Stage, err)
	}
	if start, _ := m.admit(m.List(nil)); !start[waiting.ID] {
		t.Fatal("the brief behind it didn't start")
	}
}

// A task held waiting on main is stepped again every tick to see whether main
// went green, so a step nearly always holds it: a recheck sending it back to
// its worker goes ahead over that step, and the step's older copy doesn't
// save over it (issue #2082).
func TestRecheckSendsBackATaskHeldOnMainUnderItsStep(t *testing.T) {
	m, _, task, p := noCommitRig(t, 0)
	if _, err := m.C.Runs.Start(core.RunSpec{ID: "w1", Role: "worker", Project: p.Name, Task: task.ID, Checkout: t.TempDir(), Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	task.Stage, task.SHA, task.Worker = StageTesting, "2125f74e", "w1"
	task.Reason = "waiting on main: its own tests fail at 7606c7f7, so this failure isn't the worker's"
	m.save(task, task.Reason)
	step, _ := m.Get(task.ID) // the tick's copy
	m.mu.Lock()
	m.busy[task.ID] = true
	m.stepAt = map[string]time.Time{task.ID: time.Now()}
	m.mu.Unlock()
	if err := m.Recheck(task.ID, "fix the red test", "lead"); err != nil {
		t.Fatalf("refused under the hold's step: %v", err)
	}
	m.save(&step, "the step's older copy")
	got, _ := m.Get(task.ID)
	if got.Stage != StageWorking || got.Reason != "" || !logged(&got, "sent back to its worker") {
		t.Fatalf("not sent back, or the step saved over it: stage %q reason %q", got.Stage, got.Reason)
	}
	if !m.busy[task.ID] {
		t.Fatal("the retry freed the task under the step still running on it")
	}
}

// The person's stop stands over a step running on an older copy of the task:
// the step's later saves are dropped and it doesn't merge, and a tick doesn't
// start a step on a copy taken before a save.
func TestStopStandsOverARunningStep(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Stage = StageMerging
	m.save(task, "on to the merge")
	step, _ := m.Get(task.ID) // the tick's copy
	if err := m.Stop(task.ID, "stopped by the person"); err != nil {
		t.Fatal(err)
	}
	if !m.overtaken(&step) {
		t.Fatal("a copy from before the stop isn't seen as overtaken")
	}
	step.Stage = StageLanding
	m.save(&step, "the step's older copy")
	if got, _ := m.Get(task.ID); got.Stage != StageStopped || !got.ByPerson {
		t.Fatalf("the step saved over the person's stop: %+v", got)
	}
	cur, _ := m.Get(task.ID)
	if m.overtaken(&cur) {
		t.Fatal("a copy taken after the stop is overtaken")
	}
	m.mu.Lock()
	ok := m.fresh(step)
	m.mu.Unlock()
	if ok {
		t.Fatal("a copy from before a save reads as fresh")
	}
}

// A step past its limit is let go with a journalled steer: its later saves
// are dropped, and its late end leaves the step that took over holding the task.
func TestReapStuckLetsAStepGoAndKeepsTheNewOne(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Stage = StageMerging
	m.save(task, "on to the merge")
	old := time.Now().Add(-2 * stepLimit)
	m.mu.Lock()
	m.busy[task.ID] = true
	m.stepAt = map[string]time.Time{task.ID: old}
	m.mu.Unlock()
	stale, _ := m.Get(task.ID)
	m.reapStuck()
	if m.busy[task.ID] || !m.overtaken(&stale) {
		t.Fatalf("busy %v, overtaken %v: want freed and the old copy overtaken", m.busy[task.ID], m.overtaken(&stale))
	}
	got, _ := m.Get(task.ID)
	if !logged(&got, "ran past") {
		t.Fatalf("the reap isn't in the task's log: %v", got.Log)
	}
	stale.Stage = StageLanding
	m.save(&stale, "the hung step's late save")
	if got, _ := m.Get(task.ID); got.Stage != StageMerging {
		t.Fatalf("the hung step saved over the task: %q", got.Stage)
	}
	// A newer step takes the task; the hung one ending frees nothing.
	began := time.Now()
	m.mu.Lock()
	m.busy[task.ID] = true
	m.stepAt[task.ID] = began
	m.mu.Unlock()
	m.endStep(task.ID, old)
	if !m.busy[task.ID] {
		t.Fatal("the hung step's end freed the newer step's mark")
	}
	m.endStep(task.ID, began)
	if m.busy[task.ID] {
		t.Fatal("the current step's end left the task held")
	}
}

func TestReapStuckLeavesAStepWithinItsLimit(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	m.mu.Lock()
	m.busy[task.ID] = true
	m.stepAt = map[string]time.Time{task.ID: time.Now()}
	m.mu.Unlock()
	m.reapStuck()
	if !m.busy[task.ID] || len(m.stepAt) != 1 {
		t.Fatal("a step within its limit was let go")
	}
}

func mustRun(t *testing.T, m *Module, id string) core.Run {
	t.Helper()
	r, ok := m.C.Runs.Get(id)
	if !ok {
		t.Fatalf("no run %s", id)
	}
	return r
}

// A parent looping on spawn gets no second helper for the same brief, live or
// just finished, and says which sibling has it; a different brief still goes,
// up to maxLiveHelpers live at once.
func TestSpawnRefusesARepeatedHelper(t *testing.T) {
	m, _, _ := helperRig(t)
	parent := core.Run{RunSpec: core.RunSpec{ID: "parent", Role: "worker"}}
	first, err := m.Spawn(parent, "worker", "p", "Run the word counter task (wc) to completion\nand report", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Spawn(parent, "worker", "p", "  run the word counter task (WC)   to completion\nand report back with numbers", "")
	if err == nil || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("a repeat of a live helper: err %v, want one naming %s", err, first.ID)
	}
	m.C.Runs.Stop(first.ID, "test")
	if _, err = m.Spawn(parent, "worker", "p", "Run the word counter task (wc) to completion\nand report", ""); err == nil || !strings.Contains(err.Error(), "finished") {
		t.Fatalf("a repeat of a just-finished helper: err %v", err)
	}
	if _, err = m.Spawn(core.Run{RunSpec: core.RunSpec{ID: "other", Role: "worker"}}, "worker", "p", "Run the word counter task (wc) to completion\nand report", ""); err != nil {
		t.Fatalf("another parent's helper with the same brief: %v", err)
	}
	for i := 0; i < maxLiveHelpers; i++ {
		_, err = m.Spawn(parent, "worker", "p", "helper part "+strconv.Itoa(i), "")
		if err != nil {
			t.Fatalf("helper %d: %v", i, err)
		}
	}
	if _, err = m.Spawn(parent, "worker", "p", "one more part", ""); err == nil || !strings.Contains(err.Error(), "helpers live") {
		t.Fatalf("a helper past the cap: err %v", err)
	}
}

// A worker's "parent" is whoever filed its task while that run takes
// messages, and its own project's lead once it doesn't; "lead" is that lead
// (issue #2040).
func TestWorkerParentIsItsFiler(t *testing.T) {
	m, _, _ := helperRig(t)
	sweep, err := m.C.Runs.Start(m.Agents.Spec("sweep-1", "sweep", "", t.TempDir(), "look around"))
	if err != nil {
		t.Fatal(err)
	}
	lead := m.Agents.Spec("lead-project-p-1", "lead", "p", t.TempDir(), "hi")
	lead.Name, lead.Persistent = "lead:p", true
	if _, err := m.C.Runs.Start(lead); err != nil {
		t.Fatal(err)
	}
	task := &Task{ID: core.NewID(), Project: "p", Title: "fix it", From: sweep.ID, Stage: StageWorking, Created: time.Now().UTC()}
	task.Worker = "w-" + task.ID
	m.save(task, "")
	w := core.Run{RunSpec: core.RunSpec{ID: task.Worker, Role: "worker", Project: "p", Task: task.ID}}
	pending := func(id string) int {
		r, _ := m.C.Runs.Get(id)
		return len(r.Pending)
	}
	if _, err := m.C.Runs.Ops().Deliver(w, "parent", "done?"); err != nil || pending(sweep.ID) != 1 || pending(lead.ID) != 0 {
		t.Fatalf("parent went elsewhere than the filer: %v, sweep %d, lead %d", err, pending(sweep.ID), pending(lead.ID))
	}
	if _, err := m.C.Runs.Ops().Deliver(w, "lead", "done?"); err != nil || pending(lead.ID) != 1 {
		t.Fatalf("lead didn't reach the project's lead: %v", err)
	}
	m.C.Runs.Stop(sweep.ID, "done")
	if _, err := m.C.Runs.Ops().Deliver(w, "parent", "done?"); err != nil || pending(lead.ID) != 2 {
		t.Fatalf("parent with its filer ended didn't reach the project's lead: %v", err)
	}
}
