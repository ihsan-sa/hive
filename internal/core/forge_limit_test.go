package core

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// stubGH replaces gh with reply for one test and records each call's args.
func stubGH(t *testing.T, reply func(args []string) (string, string, error)) *[][]string {
	t.Helper()
	run, p := ghRun, ghPause
	t.Cleanup(func() { ghRun, ghPause = run, p })
	ghPause = &pause{}
	logins.Clear()
	var calls [][]string
	ghRun = func(_ string, args []string) (string, string, error) {
		calls = append(calls, args)
		return reply(args)
	}
	return &calls
}

// A failed gh call's error is gh's own stderr under the command's name, not
// its arguments, so a long body can't push the reason out of the task log.
func TestGhErrorIsStderr(t *testing.T) {
	stubGH(t, func([]string) (string, string, error) {
		return "", "GraphQL: Head sha can't be blank (createPullRequest)", errors.New("exit status 1")
	})
	body := strings.Repeat("a long account ", 200)
	_, err := gh("o/r", "pr", "create", "-R", "o/r", "--head", "hive/x", "--title", "t", "--body", body)
	if err == nil || err.Error() != "gh pr create: GraphQL: Head sha can't be blank (createPullRequest)" {
		t.Fatalf("error %v", err)
	}
	if strings.Contains(err.Error(), "long account") {
		t.Fatal("the body is in the error")
	}
}

func TestGhWrites(t *testing.T) {
	for args, want := range map[string]bool{
		"pr create -R o/r":                                   true,
		"issue comment 3 -R o/r":                             true,
		"pr merge 3 -R o/r":                                  true,
		"release create v1 -R o/r":                           true,
		"pr view 3 -R o/r":                                   false,
		"issue list -R o/r":                                  false,
		"api user -q .login":                                 false,
		"api repos/o/r/forks -X POST":                        true,
		"api repos/o/r/issues/3/comments --paginate -q .":    false,
		"api -X PATCH repos/o/r/issues/comments/9 -f body=x": true,
		"api repos/o/r/issues/3/comments -f body=x":          true,
		"run rerun 7 --failed -R o/r":                        true,
		"pr ready 3 -R o/r --undo":                           true,
		"api -X DELETE repos/o/r/git/refs/heads/hive/x":      true,
	} {
		if got := ghWrites(strings.Fields(args)); got != want {
			t.Errorf("%s: writes %v, want %v", args, got, want)
		}
	}
}

func TestRateLimitRead(t *testing.T) {
	for text, want := range map[string]bool{
		"pull request create failed: GraphQL: was submitted too quickly (createPullRequest)": true,
		"You have exceeded a secondary rate limit. Please wait a few minutes. (HTTP 403)":    true,
		"HTTP 429: Too Many Requests (https://api.github.com/...)\nRetry-After: 30":          true,
		"HTTP 403: API rate limit exceeded for user ID 1.":                                   true,
		"HTTP 403: Resource not accessible by integration":                                   false,
		"GraphQL: Head sha can't be blank (createPullRequest)":                               false,
		"dial tcp: lookup api.github.com: no such host":                                      false,
	} {
		if _, got := rateLimit(text); got != want {
			t.Errorf("%q: limited %v, want %v", text, got, want)
		}
	}
	if d, _ := rateLimit("HTTP 429\nRetry-After: 30"); d != 30*time.Second {
		t.Errorf("retry-after read as %v", d)
	}
}

// On a secondary rate limit every write waits out one shared pause without
// being sent; reads still go; the pause doubles while hits keep coming and
// honours a retry-after; a write that goes through resets it.
func TestRateLimitPausesWrites(t *testing.T) {
	limited := true
	calls := stubGH(t, func(args []string) (string, string, error) {
		if limited && ghWrites(args) {
			return "", "pull request create failed: GraphQL: was submitted too quickly (createPullRequest)", errors.New("exit status 1")
		}
		return "[]", "", nil
	})
	_, err := (GitHub{}).CreatePR("o/r", "hive/x", "main", "t", "body")
	var rl *RateLimited
	if !errors.As(err, &rl) || !strings.Contains(err.Error(), "submitted too quickly") {
		t.Fatalf("error %v", err)
	}
	if d := time.Until(rl.Until); d < 55*time.Second || d > time.Minute {
		t.Fatalf("first pause %v, want a minute", d)
	}
	n := len(*calls)
	if err := (GitHub{}).Comment("o/r", 3, "hi"); !errors.As(err, &rl) {
		t.Fatalf("a write during the pause: %v", err)
	}
	if len(*calls) != n {
		t.Fatalf("a write was sent during the pause: %q", (*calls)[n:])
	}
	if _, err := (GitHub{}).GetPR("o/r", 3); errors.As(err, &rl) || len(*calls) != n+1 {
		t.Fatalf("a read during the pause: %v, calls %d", err, len(*calls)-n)
	}
	ghPause.until = time.Time{} // the pause is over; GitHub still says too quickly
	err = (GitHub{}).Comment("o/r", 3, "hi")
	if !errors.As(err, &rl) || time.Until(rl.Until) < 115*time.Second {
		t.Fatalf("second pause %v, want two minutes", err)
	}
	ghPause.until = time.Time{}
	limited = false
	if err := (GitHub{}).Comment("o/r", 3, "hi"); err != nil {
		t.Fatal(err)
	}
	if ghPause.hits != 0 {
		t.Fatalf("hits %d after a write went through", ghPause.hits)
	}
	ghPause.hit("HTTP 429: Too Many Requests\nRetry-After: 300")
	if err := ghPause.wait(); !errors.As(err, &rl) || time.Until(rl.Until) < 295*time.Second {
		t.Fatalf("retry-after pause %v", err)
	}
}

// One status comment per PR: posted once, then edited in place.
func TestGitHubStatusCommentEdits(t *testing.T) {
	list := ""
	calls := stubGH(t, func(args []string) (string, string, error) {
		switch {
		case strings.Join(args[:2], " ") == "api user":
			return "hive-bot\n", "", nil
		case args[0] == "api" && args[1] == "--paginate":
			return list, "", nil
		}
		return "", "", nil
	})
	if err := (GitHub{}).StatusComment("o/r", 3, "<!-- s -->", "round 1"); err != nil {
		t.Fatal(err)
	}
	list = "990 someone-else false\n991 hive-bot true\n"
	if err := (GitHub{}).StatusComment("o/r", 3, "<!-- s -->", "round 2"); err != nil {
		t.Fatal(err)
	}
	c := *calls
	if len(c) != 5 || strings.Join(c[2][:2], " ") != "issue comment" || !strings.Contains(strings.Join(c[4], " "), "-X PATCH repos/o/r/issues/comments/991 -f body=round 2\n\n<!-- s -->") {
		t.Fatalf("calls %q", c)
	}
}

// Anyone can write the marker into a comment: only the Hive's own is edited.
func TestGitHubStatusCommentEditsOnlyItsOwn(t *testing.T) {
	calls := stubGH(t, func(args []string) (string, string, error) {
		switch {
		case strings.Join(args[:2], " ") == "api user":
			return "hive-bot\n", "", nil
		case args[0] == "api" && args[1] == "--paginate":
			return "990 someone-else true\n", "", nil
		}
		return "", "", nil
	})
	if err := (GitHub{}).StatusComment("o/r", 3, "<!-- s -->", "round 2"); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if strings.Contains(strings.Join(c, " "), "PATCH") {
			t.Fatalf("edited another's comment: %q", *calls)
		}
	}
	if c := *calls; strings.Join(c[len(c)-1][:2], " ") != "issue comment" {
		t.Fatalf("no comment of its own posted: %q", c)
	}
}

func TestLocalStatusCommentEdits(t *testing.T) {
	l := &Local{Dir: t.TempDir()}
	l.Comment("o/r", 3, "Tests on a: pass")
	l.StatusComment("o/r", 3, "<!-- s -->", "round 1")
	l.StatusComment("o/r", 3, "<!-- s -->", "round 2")
	if c := l.Comments("o/r", 3); len(c) != 2 || !strings.HasPrefix(c[1], "round 2") {
		t.Fatalf("comments %q", c)
	}
}

// RerunFailed re-runs only the finished runs on the head that failed, timed
// out or were cancelled, each with --failed under the repo's own token.
func TestRerunFailedRunsOnlyFailedRuns(t *testing.T) {
	calls := stubGH(t, func(args []string) (string, string, error) {
		if args[0] == "api" {
			return `[{"id":1,"status":"completed","conclusion":"failure"},{"id":2,"status":"completed","conclusion":"success"},
				{"id":3,"status":"in_progress","conclusion":""},{"id":4,"status":"completed","conclusion":"cancelled"}]`, "", nil
		}
		return "", "", nil
	})
	n, err := GitHub{}.RerunFailed("o/r", "abc")
	if err != nil || n != 2 {
		t.Fatalf("n %d, err %v", n, err)
	}
	var reran []string
	for _, c := range *calls {
		if c[0] == "run" {
			reran = append(reran, strings.Join(c, " "))
		}
	}
	if strings.Join(reran, "; ") != "run rerun 1 --failed -R o/r; run rerun 4 --failed -R o/r" {
		t.Fatalf("re-ran %q", reran)
	}
}

// A re-run's attempt still queued has no start yet; its larger id makes it
// the one that counts, not the failed attempt before it.
func TestChecksNewestAttemptByID(t *testing.T) {
	stubGH(t, func(args []string) (string, string, error) {
		if strings.Contains(args[2], "check-runs") {
			return `{"id":5,"name":"test","status":"completed","conclusion":"failure","started_at":"2026-10-24T10:00:00Z"}` + "\n" + `{"id":9,"name":"test","status":"queued"}`, "", nil
		}
		return "", "", nil
	})
	runs, err := GitHub{}.Checks("o/r", "abc")
	if err != nil || len(runs) != 1 || runs[0].State != CheckPending {
		t.Fatalf("runs %+v, err %v", runs, err)
	}
}

// ReadyPR runs gh pr ready, a write, only while the draft's head is the
// commit it names; a moved head or a PR already ready runs nothing.
func TestGitHubReadyPRAtItsHead(t *testing.T) {
	head, draft := "aaaa1111", true
	calls := stubGH(t, func(args []string) (string, string, error) {
		if args[0] == "pr" && args[1] == "view" {
			return `{"number":7,"state":"OPEN","headRefOid":"` + head + `","isDraft":` + map[bool]string{true: "true", false: "false"}[draft] + `}`, "", nil
		}
		return "", "", nil
	})
	readies := func() (n int) {
		for _, c := range *calls {
			if c[0] == "pr" && c[1] == "ready" {
				n++
			}
		}
		return n
	}
	if err := (GitHub{}).ReadyPR("o/r", 7, "bbbb2222"); err == nil || readies() != 0 {
		t.Fatalf("readied a moved head: %v, %d", err, readies())
	}
	if err := (GitHub{}).ReadyPR("o/r", 7, head); err != nil || readies() != 1 {
		t.Fatalf("ready at its head: %v, %d", err, readies())
	}
	draft = false
	if err := (GitHub{}).ReadyPR("o/r", 7, head); err != nil || readies() != 1 {
		t.Fatalf("readied one already ready: %v, %d", err, readies())
	}
	if !ghWrites([]string{"pr", "ready", "7"}) {
		t.Fatal("gh pr ready isn't counted a write")
	}
}

// GitHub's refusal to merge a draft reads as ErrDraft, so the task waits.
func TestDraftRefusedReadsAsDraft(t *testing.T) {
	if !draftRefused.MatchString("gh pr merge: GraphQL: Pull Request is still a draft (mergePullRequest)") {
		t.Fatal("GitHub's draft refusal isn't recognised")
	}
	if draftRefused.MatchString("gh pr merge: Pull request is not mergeable") {
		t.Fatal("another refusal reads as a draft")
	}
}

// Checks reads every page of check runs, statuses and suites (a head past
// 100 check runs still shows its 101st), and takes gh's items whether it
// printed them compact or indented. The replies are the item shapes GitHub's
// check-runs, combined status and check-suites calls return.
func TestChecksPaginates(t *testing.T) {
	recent := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	calls := stubGH(t, func(args []string) (string, string, error) {
		var b strings.Builder
		switch {
		case strings.Contains(args[2], "check-runs"):
			for i := 1; i <= 100; i++ {
				fmt.Fprintf(&b, `{"id":%d,"name":"job-%d","status":"completed","conclusion":"success","started_at":"2026-10-24T10:00:00Z","html_url":"https://github.com/o/r/actions/runs/1/job/%d"}`+"\n", i, i, i)
			}
			b.WriteString("{\n  \"id\": 101,\n  \"name\": \"test (ubuntu-latest)\",\n  \"status\": \"completed\",\n  \"conclusion\": \"failure\"\n}\n")
		case strings.Contains(args[2], "/status"):
			b.WriteString(`{"context":"ci/legacy","state":"pending","description":"waiting","target_url":"https://ci.example/1"}`)
		default:
			fmt.Fprintf(&b, `{"status":"queued","latest_check_runs_count":0,"created_at":%q,"app":{"slug":"github-actions"}}`+"\n", recent)
			b.WriteString(`{"status":"completed","latest_check_runs_count":101,"created_at":"2026-10-24T10:00:00Z","app":{"slug":"github-actions"}}`)
		}
		return b.String(), "", nil
	})
	runs, err := GitHub{}.Checks("o/r", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 103 {
		t.Fatalf("%d checks, want 101 runs, a status and a young suite", len(runs))
	}
	if r := runs[100]; r.Name != "test (ubuntu-latest)" || r.State != CheckFail {
		t.Errorf("the 101st run reads %+v", r)
	}
	if r := runs[101]; r.Name != "ci/legacy" || r.State != CheckPending {
		t.Errorf("the status reads %+v", r)
	}
	if r := runs[102]; r.Name != "github-actions" || r.State != CheckPending {
		t.Errorf("the young suite reads %+v", r)
	}
	for _, c := range *calls {
		if len(c) < 5 || c[1] != "--paginate" || !strings.Contains(c[2], "per_page=100") || c[3] != "-q" {
			t.Errorf("gh %q doesn't read every page", c)
		}
	}
}

// A token refused the checks (HTTP 403) on a head with no workflows reads as
// no checks, which ChecksState passes; with workflows there the refusal
// stands, since there is CI the Hive can't see.
func TestChecksRefusedWithoutWorkflows(t *testing.T) {
	refused := "gh: Resource not accessible by personal access token (HTTP 403)"
	for _, workflows := range []bool{false, true} {
		calls := stubGH(t, func(args []string) (string, string, error) {
			switch {
			case strings.Contains(args[1], "contents/.github/workflows"):
				if workflows {
					return `[{"name":"ci.yml"}]`, "", nil
				}
				return "", "gh: Not Found (HTTP 404)", errors.New("exit status 1")
			case strings.Contains(args[2], "check-runs"), strings.Contains(args[2], "check-suites"):
				return "", refused, errors.New("exit status 1")
			}
			return "", "", nil
		})
		runs, err := GitHub{}.Checks("o/r", "abc")
		if workflows {
			// The stopped task's reason is this text, which revive matches.
			want := "gh api repos/o/r/commits/abc/check-runs?per_page=100: " + refused
			if err == nil || err.Error() != want || !ChecksRefused(err.Error()) {
				t.Errorf("with workflows: runs %+v, err %v", runs, err)
			}
			continue
		}
		if err != nil || len(runs) != 0 {
			t.Fatalf("no workflows: runs %+v, err %v", runs, err)
		}
		if st, _ := ChecksState(runs, nil); st != CheckPass {
			t.Errorf("no workflows reads %s", st)
		}
		asked := 0
		for _, c := range *calls {
			if strings.Contains(c[1], "workflows") {
				asked++
				if !strings.Contains(c[1], "ref=abc") {
					t.Errorf("asked for workflows off the head: %q", c)
				}
			}
		}
		if asked != 1 {
			t.Errorf("asked for workflows %d times, want once", asked)
		}
	}
}

// The four tasks stopped on a checks refusal, their reasons as the journal
// records them (after "gave up after repeated errors: "), read as one; a
// refusal of another call doesn't.
func TestChecksRefusedReasons(t *testing.T) {
	const tail = "?per_page=100: gh: Resource not accessible by personal access token (HTTP 403)"
	for _, r := range []string{
		"gh api repos/Hive-RSI-Lab/hive-deck/commits/e3b0815699da94aede6f3d3a1c8b60e5ea532c87/check-runs" + tail,
		"gh api repos/Hive-RSI-Lab/hive-dispatch/commits/23925cb03db4544ed170451a28aa905c00d66783/check-runs" + tail,
		"gh api repos/Hive-RSI-Lab/hive-trials/commits/e5789b39d9d40ee34abf3b53886ee38529028feb/check-runs" + tail,
		"gh api repos/Hive-RSI-Lab/hive-trials/commits/fa2ec42bff45ca0e5d059e4a6429e8c0495ebd36/check-runs" + tail,
	} {
		if !ChecksRefused(r) {
			t.Errorf("not read as a checks refusal: %s", r)
		}
	}
	for _, r := range []string{
		"gh api: gh: Resource not accessible by personal access token (HTTP 403)",
		"gh api repos/o/r/issues/3/comments: gh: Resource not accessible by personal access token (HTTP 403)",
		"gh api repos/o/r/commits/abc/check-runs?per_page=100: gh: Bad credentials (HTTP 401)",
	} {
		if ChecksRefused(r) {
			t.Errorf("read as a checks refusal: %s", r)
		}
	}
}

// An API read goes through gh's login as a plain GET, and a rate-limited one
// comes back as RateLimited; the quota's reset is read from rate_limit.
func TestAPIGetOnTheLogin(t *testing.T) {
	limited := false
	calls := stubGH(t, func(args []string) (string, string, error) {
		switch {
		case args[1] == "rate_limit":
			return "1792900000\n", "", nil
		case limited:
			return "", "HTTP 403: API rate limit exceeded for user ID 1.", errors.New("exit status 1")
		}
		return `{"number":7}`, "", nil
	})
	var r APIReader = GitHub{}
	if out, err := r.APIGet("o/r", "repos/o/r/pulls/7"); err != nil || out != `{"number":7}` {
		t.Fatalf("read: %q, %v", out, err)
	}
	if got := strings.Join((*calls)[0], " "); got != "api repos/o/r/pulls/7" || ghWrites((*calls)[0]) {
		t.Fatalf("gh called as %q", got)
	}
	if _, err := r.APIGet("o/r", "-X"); err == nil {
		t.Fatal("a flag passed as the path")
	}
	limited = true
	var rl *RateLimited
	if _, err := r.APIGet("o/r", "repos/o/r/pulls/7"); !errors.As(err, &rl) {
		t.Fatalf("rate-limited read: %v", err)
	}
	if at, err := r.RateReset("o/r"); err != nil || at.Unix() != 1792900000 {
		t.Fatalf("reset %v, %v", at, err)
	}
}

// A gh call killed by its timeout says so even when gh had written to stderr.
func TestGhKilledSaysTimedOut(t *testing.T) {
	old := ghRun
	defer func() { ghRun = old }()
	ghRun = func(string, []string) (string, string, error) {
		return "", "partial output", errors.New("timed out after 2m0s, signal: killed")
	}
	_, err := gh("o/r", "pr", "view", "430")
	if err == nil || !TimedOut(err.Error()) || !strings.Contains(err.Error(), "partial output") {
		t.Fatalf("err %v", err)
	}
}

// A host program past its timeout is killed, and its error says why.
func TestRunCmdTimeoutSaysSo(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep")
	}
	_, _, err := runCmd(100*time.Millisecond, "", nil, "sleep", "5")
	if err == nil || !TimedOut(err.Error()) || !strings.Contains(err.Error(), "timed out after 100ms") {
		t.Fatalf("err %v", err)
	}
}
