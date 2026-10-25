package core

import (
	"fmt"
	"strings"
	"testing"
)

// Closing an issue that is already closed succeeds, as Forge promises.
func TestLocalCloseIssueTwice(t *testing.T) {
	l := &Local{Dir: t.TempDir()}
	is, err := l.CreateIssue("me/p", "x", "body", "<!-- m -->")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := l.CloseIssue("me/p", is.Number); err != nil {
			t.Fatalf("close %d: %v", i+1, err)
		}
	}
	if s := l.load().Issues["me/p"][0].State; s != "CLOSED" {
		t.Fatalf("state %q", s)
	}
}

// A merge killed after its squash reached main but before the PR was saved as
// merged reads as merged, at that squash, and merging it again adds nothing:
// the Hive doesn't take its own merge for work that landed another way.
func TestLocalMergeKilledAfterPushIsMerged(t *testing.T) {
	c, f, p, main := setupProject(t)
	r, _ := commitInCheckout(t, c, p, "w1", main, "feature.go", "package demo\n// feature\n")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Effects.Push(p, "hive/t1", sha); err != nil {
		t.Fatal(err)
	}
	pr, err := f.CreatePR(p.Repo, "hive/t1", "main", "feature", "body")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	mustGit(t, "", "clone", "-q", f.RepoPath(p.Repo), tmp)
	mustGit(t, tmp, "fetch", "-q", "origin", sha)
	mustGit(t, tmp, "merge", "--squash", sha)
	mustGit(t, tmp, "commit", "-q", "-m", LocalMergeMessage("feature", pr.Number, sha))
	mustGit(t, tmp, "push", "-q", "origin", "HEAD:main")
	squash := strings.TrimSpace(mustGit(t, tmp, "rev-parse", "HEAD"))
	if got, err := f.GetPR(p.Repo, pr.Number); err != nil || got.State != "MERGED" || got.MergeSHA != squash {
		t.Fatalf("PR %+v (want merged at %s): %v", got, squash, err)
	}
	if m, err := f.MergePR(p.Repo, pr.Number, sha, "feature"); err != nil || m != squash {
		t.Fatalf("merged again at %s (want %s): %v", m, squash, err)
	}
	if head, _ := Git(f.RepoPath(p.Repo), "rev-parse", "refs/heads/main"); strings.TrimSpace(head) != squash {
		t.Fatalf("main moved to %s", head)
	}
}

// A repo cloned from GitHub already has squashes titled "... (#N)": a new
// local PR with one of those numbers is open, not merged at that commit, and
// merges as its own squash. So is a #N whose number only prefixes another's.
func TestLocalImportedSubjectIsNotMerged(t *testing.T) {
	c, f, p, main := setupProject(t)
	r, _ := commitInCheckout(t, c, p, "w1", main, "feature.go", "package demo\n// feature\n")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Effects.Push(p, "hive/t1", sha); err != nil {
		t.Fatal(err)
	}
	pr, err := f.CreatePR(p.Repo, "hive/t1", "main", "feature", "body")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	mustGit(t, "", "clone", "-q", f.RepoPath(p.Repo), tmp)
	mustGit(t, tmp, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("from GitHub (#%d)", pr.Number))
	mustGit(t, tmp, "commit", "-q", "--allow-empty", "-m", LocalMergeMessage("another", pr.Number*10+1, sha))
	mustGit(t, tmp, "push", "-q", "origin", "HEAD:main")
	old := strings.TrimSpace(mustGit(t, tmp, "rev-parse", "HEAD"))
	if got, err := f.GetPR(p.Repo, pr.Number); err != nil || got.State != "OPEN" {
		t.Fatalf("PR %+v (want open): %v", got, err)
	}
	m, err := f.MergePR(p.Repo, pr.Number, sha, "feature")
	if err != nil || m == old {
		t.Fatalf("merged at %s (main was %s): %v", m, old, err)
	}
	if got, _ := f.GetPR(p.Repo, pr.Number); got.State != "MERGED" || got.MergeSHA != m {
		t.Fatalf("PR %+v (want merged at %s)", got, m)
	}
}
