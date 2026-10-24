package core

import (
	"os"
	"path/filepath"
	"runtime"
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
	if _, err := l.EnsureRepo("me/p", ""); err != nil {
		t.Fatal(err)
	}
	for owner, want := range map[string]string{"": "local/p", "lab": "lab/p"} {
		fork, err := l.Fork("me/p", owner)
		if err != nil || fork != want || !Exists(l.RepoPath(fork)) {
			t.Errorf("Fork(me/p, %q) = %q, %v", owner, fork, err)
		}
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
