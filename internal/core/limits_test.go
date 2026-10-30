package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A change to the code that enforces the hard limits (the list's limits,
// read from main) merges only once a warden's read approved its diff; a
// revert needs none, and everything else still merges with nothing between.
func TestLimitsWaitForAWardensRead(t *testing.T) {
	c, f, p, _ := setupProject(t)
	seedRepo(t, f, p.Repo, map[string]string{SecurityList: "limits = [\n  \"guard/**\", # the warden\n]\n"})
	main, err := c.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	pr := func(id, file string) (MergeRequest, Change) {
		r, _ := commitInCheckout(t, c, p, id, main, file, "package guard // "+id+"\n")
		sha, err := c.Effects.Extract(p, r, main)
		if err != nil {
			t.Fatal(err)
		}
		c.Effects.Push(p, "hive/"+id, sha)
		x, err := f.CreatePR(p.Repo, "hive/"+id, "main", id, "")
		if err != nil {
			t.Fatal(err)
		}
		ch, err := c.Effects.Classify(p, main, sha)
		if err != nil {
			t.Fatal(err)
		}
		return MergeRequest{Project: p, PR: x.Number, SHA: sha, Main: main, Subject: id}, ch
	}
	req, ch := pr("w1", "guard/warden.go")
	if len(ch.Limits) != 1 || ch.Limits[0] != "guard/warden.go" {
		t.Fatalf("limits %v", ch.Limits)
	}
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "no warden has read") {
		t.Fatalf("merged a change to the limits code unread: %v", err)
	}
	c.Effects.ApproveDiff(LimitsRead, p.Name, "", ch.DiffID) // read by its diff, as after main merged in
	if merged, err := c.Effects.Merge(req); err != nil || merged == "" {
		t.Fatalf("a read change didn't merge: %q %v", merged, err)
	}
	main, _ = c.Effects.Sync(p)
	req, _ = pr("w2", "guard/tribunal.go")
	req.Revert = true
	if _, err := c.Effects.Merge(req); err != nil {
		t.Fatalf("a revert waited on a read: %v", err)
	}
	main, _ = c.Effects.Sync(p)
	req, ch = pr("w3", "other/code.go")
	if len(ch.Limits) != 0 {
		t.Fatalf("limits %v", ch.Limits)
	}
	if _, err := c.Effects.Merge(req); err != nil {
		t.Fatalf("a change outside the limits waited: %v", err)
	}
}

// The Hive's own list names the warden, the ceiling, the scan and itself,
// and not the core at large.
func TestLimitsList(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", SecurityList))
	if err != nil {
		t.Fatal(err)
	}
	list := ParseList(string(raw), "limits")
	for _, f := range []string{SecurityList, "internal/core/limits.go", "internal/mod/work/limits.go", "internal/core/ceiling.go", "internal/mod/risk/risk.go", "internal/mod/scan/scan.go", "internal/sandbox/linux/x.go"} {
		if !matchAny(f, list) {
			t.Errorf("%s should be on limits (%v)", f, list)
		}
	}
	for _, f := range []string{"internal/core/effects.go", "internal/mod/work/work.go", "internal/core/runs.go", "docs/status.md"} {
		if matchAny(f, list) {
			t.Errorf("%s shouldn't be on limits", f)
		}
	}
}
