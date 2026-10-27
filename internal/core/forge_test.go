package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeGH puts a gh on PATH that logs its arguments, one call per line, and
// prints reply. It returns the log's path.
func fakeGH(t *testing.T, reply string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a sh script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\nprintf '%s\\n' '" + reply + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// A fork lands in the organization asked for (child Hives and test repos
// fork into Hive-RSI-Lab), and in the person's account when none is named.
func TestGitHubForkOwner(t *testing.T) {
	ready := forkReady
	t.Cleanup(func() { forkReady = ready })
	forkReady = func(string) bool { return true }

	log := fakeGH(t, "Hive-RSI-Lab/hive")
	fork, err := GitHub{}.Fork("ihsan-sa/hive", "Hive-RSI-Lab")
	if err != nil || fork != "Hive-RSI-Lab/hive" {
		t.Fatalf("fork %q, %v", fork, err)
	}
	if _, err := (GitHub{}).Fork("ihsan-sa/hive", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(calls) != 2 {
		t.Fatalf("calls: %q", calls)
	}
	if !strings.Contains(calls[0], "repos/ihsan-sa/hive/forks") || !strings.Contains(calls[0], "-f organization=Hive-RSI-Lab") {
		t.Errorf("org fork called gh %q", calls[0])
	}
	if strings.Contains(calls[1], "organization") {
		t.Errorf("a fork to the person's account named an organization: %q", calls[1])
	}
}

func TestLocalForkOwner(t *testing.T) {
	l := &Local{Dir: t.TempDir()}
	if _, err := l.EnsureRepo("me/p", "", false); err != nil {
		t.Fatal(err)
	}
	for owner, want := range map[string]string{"": "local/p", "lab": "lab/p"} {
		fork, err := l.Fork("me/p", owner)
		if err != nil || fork != want || !Exists(l.RepoPath(fork)) {
			t.Errorf("Fork(me/p, %q) = %q, %v", owner, fork, err)
		}
	}
}

// TagCommit asks for the full ref, which GitHub peels to the commit; the
// short commits/tags/<tag> form is a 422 there.
func TestGitHubTagCommitPath(t *testing.T) {
	calls := stubGH(t, func([]string) (string, string, error) { return "e156455", "", nil })
	sha, err := GitHub{}.TagCommit("o/r", "v1.2.3")
	if err != nil || sha != "e156455" {
		t.Fatalf("%q %v", sha, err)
	}
	want := []string{"api", "repos/o/r/commits/refs/tags/v1.2.3", "-q", ".sha"}
	if got := (*calls)[len(*calls)-1]; !slices.Equal(got, want) {
		t.Fatalf("gh %q, want %q", got, want)
	}
}

// GitHub's check runs map to a verdict on the commit only when they ran it.
func TestGitHubCheckStates(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		r    ghCheckRun
		want string
	}{
		{ghCheckRun{Status: "in_progress", StartedAt: now.Add(-3 * time.Hour)}, CheckPending},
		{ghCheckRun{Status: "queued", StartedAt: now.Add(-time.Minute)}, CheckPending},
		{ghCheckRun{Status: "queued", StartedAt: now.Add(-2 * time.Hour)}, CheckCantStart},
		{ghCheckRun{Status: "completed", Conclusion: "success"}, CheckPass},
		{ghCheckRun{Status: "completed", Conclusion: "skipped"}, CheckSkipped},
		{ghCheckRun{Status: "completed", Conclusion: "neutral"}, CheckSkipped},
		{ghCheckRun{Status: "completed", Conclusion: "failure"}, CheckFail},
		{ghCheckRun{Status: "completed", Conclusion: "timed_out"}, CheckFail},
		{ghCheckRun{Status: "completed", Conclusion: "startup_failure"}, CheckCantStart},
		{ghCheckRun{Status: "completed", Conclusion: "cancelled"}, CheckCantStart},
	} {
		if got := c.r.check(now).State; got != c.want {
			t.Errorf("%+v: %s, want %s", c.r, got, c.want)
		}
	}
	if st, which := ChecksState([]CheckRun{{Name: "a", State: CheckPending}, {Name: "b", State: CheckFail}, {Name: "c", State: CheckCantStart}}, nil); st != CheckFail || CheckNames(which) != "b" {
		t.Fatalf("%s %v", st, which)
	}
	if st, _ := ChecksState(nil, nil); st != CheckPass {
		t.Fatal("no checks should pass")
	}
}

// Only a young GitHub Actions suite with no jobs listed yet holds: a stale
// queued one, or another app's, never does.
func TestGitHubSuitePending(t *testing.T) {
	now := time.Now()
	suite := func(app, status string, runs int, age time.Duration) ghSuite {
		s := ghSuite{Status: status, Runs: runs, CreatedAt: now.Add(-age)}
		s.App.Slug = app
		return s
	}
	for _, c := range []struct {
		s    ghSuite
		want bool
	}{
		{suite("github-actions", "queued", 0, time.Minute), true},
		{suite("github-actions", "queued", 0, 3*time.Hour), false},
		{suite("dependabot", "queued", 0, time.Minute), false},
		{suite("github-actions", "queued", 2, time.Minute), false},
		{suite("github-actions", "completed", 0, time.Minute), false},
		{ghSuite{Status: "queued", App: struct {
			Slug string `json:"slug"`
		}{"github-actions"}}, false},
	} {
		if got := c.s.pending(now); got != c.want {
			t.Errorf("%+v: %v, want %v", c.s, got, c.want)
		}
	}
}

// EnsureRepo asks gh for the visibility it was given, and never touches a
// repository that already exists.
func TestGitHubEnsureRepoVisibility(t *testing.T) {
	log := fakeGH(t, "")
	// The fake gh finds no repository: "repo view" fails.
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n[ \"$1 $2\" = \"repo view\" ] && exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(log), "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, public := range []bool{false, true} {
		if created, err := (GitHub{}).EnsureRepo("Hive-RSI-Lab/m", "d", public); err != nil || !created {
			t.Fatalf("public %v: created %v, %v", public, created, err)
		}
	}
	raw, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(calls) != 4 || !strings.Contains(calls[1], "repo create Hive-RSI-Lab/m --private") || !strings.Contains(calls[3], "repo create Hive-RSI-Lab/m --public") {
		t.Fatalf("calls: %q", calls)
	}
}

// The squash body credits nobody but the Hive: no commit's Co-authored-by
// trailer, in any case, reaches main, and the rest of each message does.
func TestSquashBodyDropsCoAuthors(t *testing.T) {
	got := SquashBody([]string{
		"* Fix the thing\n\nWhy it broke.\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>",
		"* Follow up\n\nco-authored-by: Hive worker <hive@localhost>\n",
		"Co-authored-by: Someone <s@example.com>",
	})
	if want := "* Fix the thing\n\nWhy it broke.\n\n* Follow up"; got != want {
		t.Fatalf("squash body %q, want %q", got, want)
	}
}

// A body with nothing left is still given, or gh would skip it and GitHub
// write its default with the co-author lines; one past the argv cap is
// trimmed to headlines.
func TestSquashBodyNeverEmptyAndCapped(t *testing.T) {
	if got := SquashBody([]string{"Co-authored-by: Claude <noreply@anthropic.com>"}); got != noSquashBody {
		t.Fatalf("empty body %q", got)
	}
	if got := SquashBody(nil); got != noSquashBody {
		t.Fatalf("no messages %q", got)
	}
	long := strings.Repeat("x", 1000) + "\n"
	var msgs []string
	for i := 0; i < 200; i++ {
		msgs = append(msgs, fmt.Sprintf("* Commit %d\n\n%s", i, strings.Repeat(long, 5)))
	}
	got := SquashBody(msgs)
	if len(got) > squashBodyMax+100 || !strings.HasPrefix(got, "* Commit 0\n* Commit 1\n") || strings.Contains(got, "xxx") {
		t.Fatalf("long body %d bytes: %.80q", len(got), got)
	}
	var many []string
	for i := 0; i < 20000; i++ {
		many = append(many, fmt.Sprintf("* Commit %d", i))
	}
	if got := SquashBody(many); len(got) > squashBodyMax+100 || !strings.HasSuffix(got, "pull request)") {
		t.Fatalf("many headlines %d bytes", len(got))
	}
}

// GitHub's merge is given its body, so its default (which turns commit
// authors and trailers into co-authors) never reaches main.
func TestGitHubMergeBodyHasNoCoAuthor(t *testing.T) {
	merged := false
	calls := stubGH(t, func(args []string) (string, string, error) {
		a := strings.Join(args, " ")
		switch {
		case strings.Contains(a, "--json commits"):
			return `{"commits":[{"messageHeadline":"Fix","messageBody":"Why.\n\nCo-authored-by: Claude Opus 5.5 <noreply@anthropic.com>"}]}`, "", nil
		case strings.HasPrefix(a, "pr merge"):
			merged = true
		case strings.HasPrefix(a, "pr view"):
			if merged {
				return `{"number":3,"state":"MERGED","headRefOid":"abc","mergeCommit":{"oid":"def"}}`, "", nil
			}
			return `{"number":3,"state":"OPEN","headRefOid":"abc"}`, "", nil
		}
		return "", "", nil
	})
	if m, err := (GitHub{}).MergePR("o/r", 3, "abc", "Fix (#3)"); err != nil || m != "def" {
		t.Fatalf("merge %q %v", m, err)
	}
	for _, c := range *calls {
		if c[0] == "pr" && c[1] == "merge" {
			if i := slices.Index(c, "--body"); i < 0 || c[i+1] != "Why." {
				t.Fatalf("merge args %q", c)
			}
			return
		}
	}
	t.Fatal("no merge")
}
