package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveGitHubPath walks the real GitHub path the Hive takes on itself,
// with the GitHub forge and whatever login gh has (GH_TOKEN if set): fork the
// upstream into an owner, open a pull request on the fork from a new branch,
// merge it at its checked head, cut a release at the merge, and look up a
// fork-headed pull request on the upstream the way an upstream offer does.
// It writes nothing to the upstream. Each step's link goes to the log and to
// $HIVE_LIVE_GITHUB_OUT.
//
//	GH_TOKEN="$(cat /home/hive/h0-org.token)" HIVE_LIVE_GITHUB_FORK_OWNER=Hive-RSI-Lab \
//	  go test ./internal/core/ -run TestLiveGitHubPath -v -count=1 -timeout 15m
func TestLiveGitHubPath(t *testing.T) {
	owner := os.Getenv("HIVE_LIVE_GITHUB_FORK_OWNER")
	if testing.Short() || owner == "" {
		t.Skip("set HIVE_LIVE_GITHUB_FORK_OWNER (e.g. Hive-RSI-Lab) to run against real GitHub")
	}
	up := os.Getenv("HIVE_LIVE_GITHUB_UP")
	if up == "" {
		up = "ihsan-sa/hive"
	}
	var links []string
	note := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		t.Log(line)
		links = append(links, line)
		if out := os.Getenv("HIVE_LIVE_GITHUB_OUT"); out != "" {
			os.WriteFile(out, []byte(strings.Join(links, "\n")+"\n"), 0o644)
		}
	}
	g := GitHub{}
	stamp := time.Now().UTC().Format("20060102-150405")

	fork, err := g.Fork(up, owner)
	if err != nil {
		t.Fatalf("fork %s into %s: %v", up, owner, err)
	}
	if !strings.EqualFold(strings.SplitN(fork, "/", 2)[0], owner) {
		t.Fatalf("fork %s is not under %s", fork, owner)
	}
	note("fork: https://github.com/%s (of %s)", fork, up)

	dir := filepath.Join(t.TempDir(), "fork")
	if err := g.Clone(fork, dir); err != nil {
		t.Fatalf("clone %s: %v", fork, err)
	}
	base := g.DefaultBranch(fork)
	branch := "hive/probe-" + stamp
	file := filepath.Join(dir, "probes", stamp+".md")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte("A live probe of the GitHub path, "+stamp+".\n"), 0o644)
	for _, args := range [][]string{
		{"checkout", "-q", "-b", branch},
		{"add", "probes"},
		{"-c", "user.name=Hive", "-c", "user.email=hive@localhost", "commit", "-q", "-m", "Live probe " + stamp},
	} {
		if out, err := Git(dir, args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if out, err := GitT(5*time.Minute, dir, "push", "-q", "origin", branch); err != nil {
		t.Fatalf("push %s to %s: %v %s", branch, fork, err, out)
	}
	head, _ := Git(dir, "rev-parse", "HEAD")
	head = strings.TrimSpace(head)

	pr, err := g.CreatePR(fork, branch, base, "Live probe "+stamp, "The Hive's live probe of fork, PR, merge and release.")
	if err != nil {
		t.Fatalf("open PR on %s: %v", fork, err)
	}
	note("PR: %s", pr.URL)
	if again, err := g.CreatePR(fork, branch, base, "Live probe "+stamp, "again"); err != nil || again.Number != pr.Number {
		t.Fatalf("opening the PR again gave #%d, %v; want #%d", again.Number, err, pr.Number)
	}
	if pr.HeadSHA != head {
		t.Fatalf("PR head %s, pushed %s", pr.HeadSHA, head)
	}

	merged, err := g.MergePR(fork, pr.Number, head, "Live probe "+stamp)
	if err != nil {
		t.Fatalf("merge #%d: %v", pr.Number, err)
	}
	if again, err := g.MergePR(fork, pr.Number, head, "Live probe "+stamp); err != nil || again != merged {
		t.Fatalf("merging again gave %s, %v; want %s", again, err, merged)
	}
	note("merged: https://github.com/%s/commit/%s", fork, merged)

	tag := "probe-" + stamp
	if err := g.CreateRelease(fork, tag, merged, "Cut by the Hive's live probe at "+Short(merged)+"."); err != nil {
		t.Fatalf("release %s: %v", tag, err)
	}
	rel, err := g.LatestRelease(fork)
	if err != nil || rel.Tag != tag {
		t.Fatalf("latest release on %s is %q, %v; want %s", fork, rel.Tag, err, tag)
	}
	note("release: https://github.com/%s/releases/tag/%s", fork, tag)

	// An upstream offer looks for the fork's open pull request by
	// "owner:branch"; none was opened, so none is found.
	forkOwner := strings.SplitN(fork, "/", 2)[0]
	if p, found, err := g.FindPR(up, forkOwner+":"+branch); err != nil || found {
		t.Fatalf("FindPR(%s, %s:%s) = %+v, %v, %v; want none", up, forkOwner, branch, p, found, err)
	}
}
