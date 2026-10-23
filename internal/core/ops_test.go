package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// A run with no parent messaging "parent" fails, rather than reaching
// whichever run Find("") happens to match.
func TestDeliverToParentWithNoParentFails(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	rs.runs["other"] = &Run{RunSpec: RunSpec{ID: "other"}}
	_, err = rs.ops.Deliver(Run{RunSpec: RunSpec{ID: "w1"}}, "parent", "hi")
	if err == nil || !strings.Contains(err.Error(), "no parent") {
		t.Fatalf("Deliver to parent with no Parent: %v, want a no-parent error", err)
	}
}
