package work

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// GitHub's secondary rate limit holds a task until the pause ends: it waits,
// never counts a failure or stops, and the log and dashboard say why.
func TestRateLimitWaits(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	until := time.Now().Add(5 * time.Minute)
	for range 20 {
		m.settle(task, &core.RateLimited{Until: until, Text: "pull request create failed: GraphQL: was submitted too quickly (createPullRequest)"})
	}
	if task.Stage != StageLanding || task.Failures != 0 || !task.Retry.Equal(until) || m.due(*task) {
		t.Fatalf("stage %q, failures %d, retry %v, due %v", task.Stage, task.Failures, task.Retry, m.due(*task))
	}
	if !logged(task, "GitHub asked the Hive to slow down") || !logged(task, "submitted too quickly") || !logged(task, "the task waits") {
		t.Fatalf("log %v", task.Log)
	}
	if o := m.Offline(); !strings.Contains(o, "GitHub asked the Hive to slow down") || !strings.Contains(o, until.UTC().Format("15:04Z")) {
		t.Fatalf("dashboard says %q", o)
	}
	m.limit = time.Now().Add(-time.Second)
	if o := m.Offline(); o != "" {
		t.Fatalf("dashboard after the pause: %q", o)
	}
}

// A task the rate limit stopped before the Hive waited it out comes back on
// its own once the pause is over; one the person stopped stays theirs.
func TestRateLimitStopsComeBack(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Failures = 8
	m.giveUp(task, "gave up after repeated errors: gh pr create --head hive/t1 --base main --title x --body ...: exit status 1: pull request create failed: GraphQL: was submitted too quickly (createPullRequest)")
	held := &Task{ID: "t2", Project: "p", Kind: "work", Stage: StageStopped, ByPerson: true, Reason: "was submitted too quickly"}
	m.save(held, "stopped")
	other := &Task{ID: "t3", Project: "p", Kind: "work", Stage: StageStopped, Reason: "gave up after repeated errors: HTTP 422"}
	m.save(other, "stopped")
	m.limit = time.Now().Add(time.Minute)
	m.revive()
	if got, _ := m.Get(task.ID); got.Stage != StageStopped {
		t.Fatalf("brought back during the pause: %q", got.Stage)
	}
	m.limit = time.Time{}
	m.revive()
	got, _ := m.Get(task.ID)
	if got.Stage != StageLanding || got.Failures != 0 || got.Reason != "" || !logged(&got, "rate-limited") {
		t.Fatalf("not brought back: %+v", got)
	}
	if h, _ := m.Get("t2"); h.Stage != StageStopped {
		t.Fatal("a task the person stopped came back")
	}
	if o, _ := m.Get("t3"); o.Stage != StageStopped {
		t.Fatal("a task stopped for another error came back")
	}
}

// Findings quoted into a stop's reason can say anything the worker's code
// printed, "secondary rate limit" too: that never resets the review rounds.
func TestPlantedRateLimitDoesNotRevive(t *testing.T) {
	m, _, task, _ := noCommitRig(t, 0)
	task.Round = 3
	m.giveUp(task, "stopped after 3 rounds of fixes; what remains:\n\nthe log says: You have exceeded a secondary rate limit (was submitted too quickly)")
	m.limit = time.Time{}
	m.revive()
	if got, _ := m.Get(task.ID); got.Stage != StageStopped || got.Round != 3 {
		t.Fatalf("a planted rate limit brought the task back: stage %q, round %d", got.Stage, got.Round)
	}
}

func testComments(f *core.Local, pr int) (n int) {
	for _, c := range f.Comments("me/p", pr) {
		if strings.HasPrefix(c, "Tests on ") {
			n++
		}
	}
	return n
}

// One test comment per head; a catch-up with main that leaves the PR's own
// diff and the result as they were posts none, and a changed result does.
func TestOneTestCommentPerHead(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	task.Stage, task.Recheck = StageTesting, true // tested again on the same head
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if n := testComments(forge, task.PR.Number); n != 1 {
		t.Fatalf("%d test comments on one head", n)
	}
	pushMain(t, forge, map[string]string{"other.txt": "z"})
	main, _ := m.C.Effects.Sync(p)
	old := task.SHA
	if ok, err := m.catchUp(task, p, main, "main moved"); !ok || err != nil || task.SHA == old {
		t.Fatalf("catch-up: %v %v", ok, err)
	}
	if err := m.test(task, p); err != nil {
		t.Fatal(err)
	}
	if n := testComments(forge, task.PR.Number); n != 1 {
		t.Fatalf("%d test comments after a catch-up that left the diff alone", n)
	}
}

func TestChangedResultComments(t *testing.T) {
	m, task, p, forge := gateRig(t, true, map[string]string{"feature.txt": "y"})
	if err := m.test(task, p); err != nil { // fails with main
		t.Fatal(err)
	}
	pushMain(t, forge, map[string]string{"BROKEN": ""})
	if err := m.test(task, p); err != nil { // main green: caught up
		t.Fatal(err)
	}
	if err := m.test(task, p); err != nil { // passes now
		t.Fatal(err)
	}
	if n := testComments(forge, task.PR.Number); n != 2 {
		t.Fatalf("%d test comments, want fail then pass", n)
	}
}

// A new commit after a round edits the PR's one status comment.
func TestStatusCommentEditedEachRound(t *testing.T) {
	m, task, p, forge := gateRig(t, false, map[string]string{"feature.txt": "y"})
	sha := task.SHA
	for round := 1; round <= 3; round++ {
		task.SHA, task.Round = "0000000"+string(rune('0'+round)), round
		if err := m.open(task, p, sha, "work"); err != nil {
			t.Fatal(err)
		}
	}
	var status []string
	for _, c := range forge.Comments("me/p", task.PR.Number) {
		if strings.Contains(c, "<!-- hive-status:t1 -->") {
			status = append(status, c)
		}
	}
	if len(status) != 1 || !strings.Contains(status[0], "after round 3") {
		t.Fatalf("status comments %q", status)
	}
}

// A task stopped because the token was refused its head's checks comes back
// once those checks read (a repo with no workflows reads as none); while they
// are still refused it stays stopped, and its head is asked only once.
func TestChecksRefusedStopsComeBack(t *testing.T) {
	m, forge, task, _ := noCommitRig(t, 0)
	// The deck task's reason as the journal records it (task 261024-154353-5c4bec3bc6).
	sha := "e3b0815699da94aede6f3d3a1c8b60e5ea532c87"
	refused := "gave up after repeated errors: gh api repos/Hive-RSI-Lab/hive-deck/commits/e3b0815699da94aede6f3d3a1c8b60e5ea532c87/check-runs?per_page=100: gh: Resource not accessible by personal access token (HTTP 403)"
	task.SHA = sha
	m.giveUp(task, refused)
	other := &Task{ID: "t2", Project: "p", Kind: "work", SHA: sha, Stage: StageStopped, ByPerson: true, Reason: refused}
	m.save(other, "stopped")
	planted := &Task{ID: "t3", Project: "p", Kind: "work", SHA: sha, Stage: StageStopped, Reason: "stopped after 3 rounds of fixes: " + refused}
	m.save(planted, "stopped")

	forge.checksRefused = true
	m.revive()
	m.revive()
	if got, _ := m.Get(task.ID); got.Stage != StageStopped {
		t.Fatalf("came back while still refused: %q", got.Stage)
	}
	if forge.checksAsked != 1 {
		t.Fatalf("asked the forge %d times, want once", forge.checksAsked)
	}

	// An hour on it asks again; this time the checks read.
	forge.checksRefused = false
	m.refusedAsked[task.ID+"@"+sha] = time.Now().Add(-2 * time.Hour)
	m.revive()
	got, _ := m.Get(task.ID)
	if got.Stage != StageLanding || got.Failures != 0 || got.Reason != "" || !logged(&got, "read now") {
		t.Fatalf("not brought back: %+v", got)
	}
	if o, _ := m.Get("t2"); o.Stage != StageStopped {
		t.Fatal("a task the person stopped came back")
	}
	if o, _ := m.Get("t3"); o.Stage != StageStopped {
		t.Fatal("a refusal quoted in another reason brought the task back")
	}
}
