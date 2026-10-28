package core

import (
	"path/filepath"
	"testing"
)

// An approval bound to a diff carries to another head with that diff, after
// a restart too, and to no head with another diff or none named.
func TestApprovalBindsToDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	open := func() (*Effects, *Journal) {
		j, err := OpenJournal(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { j.Close() })
		return NewEffects(Workspace{}, j, nil, nil, nil, nil), j
	}
	e, j := open()
	e.ApproveDiff("merge", "hive", "h1", "d1")
	e.Approve("merge", "hive", "h9")
	j.Close()
	e, _ = open()
	for _, c := range []struct {
		sha, diff string
		want      bool
	}{{"h1", "", true}, {"h2", "d1", true}, {"h2", "d2", false}, {"h2", "", false}, {"h9", "d9", true}, {"h3", "d9", false}} {
		if got := e.ApprovedDiff("merge", "hive", c.sha, c.diff); got != c.want {
			t.Errorf("%s with diff %q: approved %v, want %v", c.sha, c.diff, got, c.want)
		}
	}
	if e.ApprovedDiff("release", "hive", "h2", "d1") || e.ApprovedDiff("merge", "other", "h2", "d1") {
		t.Error("an approval carried to another kind or project")
	}
}
