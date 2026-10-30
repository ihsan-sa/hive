package problems

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A failing test one task met says nothing to it; a second task meeting it
// is told who met it first and puts it on the board by itself, and a third
// is pointed at the same board item and added to it. A task's own later
// rounds aren't others.
func TestSeenMarksAndPostsTheSecondTime(t *testing.T) {
	_, m := open(t)
	if got := m.Seen("hive", "w-a", "t-a", []string{"x::TestFlaky"}); got != "" {
		t.Fatalf("first sighting said %q", got)
	}
	if got := m.Seen("hive", "w-a", "t-a", []string{"x TestFlaky"}); got != "" {
		t.Fatalf("the same task's next round counted as another: %q", got)
	}
	if got := m.Seen("textkit", "w-z", "t-z", []string{"x TestFlaky"}); got != "" {
		t.Fatalf("another project's test counted: %q", got)
	}
	got := m.Seen("hive", "w-b", "t-b", []string{"x TestFlaky", "x TestOther"})
	board := m.List(func(p Problem) bool { return p.Key != "" })
	if len(board) != 1 || board[0].Key != "x TestFlaky" || board[0].Run != "w-a" || !board[0].Open() {
		t.Fatalf("board: %+v", board)
	}
	if !strings.Contains(got, "`x TestFlaky`: also seen by w-a (task t-a") || !strings.Contains(got, "board "+board[0].ID) || strings.Contains(got, "TestOther") {
		t.Fatalf("second sighting said %q", got)
	}
	if !strings.Contains(m.Brief("worker", "hive"), board[0].ID) {
		t.Fatal("the posted failure isn't in new workers' prompts")
	}
	got = m.Seen("hive", "w-c", "t-c", []string{"x TestFlaky"})
	again, _ := m.Get(board[0].ID)
	if !strings.Contains(got, "w-b") || !strings.Contains(got, "w-a") || !strings.Contains(got, "board "+board[0].ID) || len(again.Notes) != 1 || again.Notes[0].Run != "w-c" {
		t.Fatalf("third sighting said %q; item %+v", got, again)
	}
	if n := len(m.List(func(p Problem) bool { return p.Key != "" })); n != 1 {
		t.Fatalf("%d board items for one test", n)
	}
}

// A check that fails dozens of tests at once is broken by its environment:
// none of them is recorded or posted.
func TestSeenIgnoresMassFailures(t *testing.T) {
	_, m := open(t)
	var keys []string
	for _, c := range "abcdefghij" {
		keys = append(keys, "x Test"+string(c))
	}
	m.Seen("hive", "w-a", "t-a", keys)
	if got := m.Seen("hive", "w-b", "t-b", keys[:1]); got != "" || len(m.List(nil)) != 0 {
		t.Fatalf("a mass failure was recorded: %q", got)
	}
}

// Search shows the board items sharing words with the query, open first.
func TestMatchFindsBoardItems(t *testing.T) {
	c, m := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Project: "hive"}}
	call(t, c, w, "problem", map[string]string{"text": "the sandboxed e2e times out on a loaded machine"})
	call(t, c, w, "problem", map[string]string{"text": "the dashboard freezes on update"})
	got := m.Match("hive", "sandboxed e2e on a loaded runner")
	if !strings.Contains(got, "sandboxed e2e") || strings.Contains(got, "dashboard") {
		t.Fatalf("match: %q", got)
	}
	if m.Match("hive", "a b") != "" {
		t.Fatal("a query of short words matched")
	}
}
