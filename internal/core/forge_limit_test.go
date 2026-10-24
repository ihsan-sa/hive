package core

import (
	"errors"
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
		switch {
		case strings.Contains(args[1], "check-runs"):
			return `{"check_runs":[{"id":5,"name":"test","status":"completed","conclusion":"failure","started_at":"2026-10-24T10:00:00Z"},{"id":9,"name":"test","status":"queued"}]}`, "", nil
		case strings.HasSuffix(args[1], "/status"):
			return `{"statuses":[]}`, "", nil
		}
		return `{"check_suites":[]}`, "", nil
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
