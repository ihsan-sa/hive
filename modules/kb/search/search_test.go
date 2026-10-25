package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes a small knowledge base: a task with a PR, its worker's run,
// its revert, notes that link, a chat that names everything many times, and
// a line every brief repeats.
func fixture(t *testing.T) string {
	dir := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	task, run := "261024-015726-45d2d27ed7", "w-261024-015726-45d2d27ed7"
	write("record/task/"+task+".md", "# task "+task+"\n\n### 2026-10-24 01:57 · task.saved · seq 13\n- title: Set up the Hive files\n- stage: working\n\n### 2026-10-24 01:58 · task.saved · seq 28\n- pr.number: 111\n- pr.url: https://github.com/ihsan-sa/hive/pull/111\n\n### 2026-10-24 02:00 · task.saved · seq 131\n- stage: done\n")
	write("record/run/"+run+".md", "# run "+run+"\n\n### 2026-10-24 01:57 · run.created · seq 14\n- brief: write the goals file\n\n### 2026-10-24 01:59 · run.finish · seq 30\n- finish.account: wrote .hive/goals.md and the test command\n")
	write("record/task/261024-020043-5f804b138f.md", "# task 261024-020043-5f804b138f\n\n### 2026-10-24 02:00 · task.saved · seq 136\n- kind: revert\n- revert_of: "+task+"\n- lesson: the tests went red after it merged: go: not found\n- title: Revert: Set up the Hive files\n")
	var chat strings.Builder
	chat.WriteString("# chat lead\n")
	for i := range 40 {
		fmt.Fprintf(&chat, "\n### 2026-10-24 03:%02d · chat.posted · seq %d\n- text: the zebra gate was reverted again, see #111 and the walrus\n", i, 200+i)
	}
	write("record/chat/lead.md", chat.String())
	write("gate-ties.md", "---\ntitle: Gate ties roll changes back\ntags: gate\ndate: 2026-10-24\n---\n\nA tie at the gate rolls the change back. See [[own-check-rule]].\n")
	write("own-check-rule.md", "---\ntitle: The own-check rule\ntags: decision\ndate: 2026-10-24\n---\n\nThe person decided a change's own check that the old Hive fails counts.\n")
	write("lonely.md", "---\ntitle: Nothing related\ndate: 2026-10-20\n---\n\nPenguins.\n")
	for i := range boilerFiles {
		write(fmt.Sprintf("record/run/w-261024-0000%02d-aaaaaaaaaa.md", i), fmt.Sprintf("# run %d\n\n### 2026-10-24 04:00 · run.created · seq %d\n- brief: Lessons: the walrus rolled back a parser, quoted in every brief\n", i, 300+i))
	}
	write("isolated/secret/hidden.md", "---\ntitle: walrus secret\n---\n\nwalrus walrus\n")
	return dir
}

func search(t *testing.T, dir, q string, hide ...string) []*Hit {
	ix := Load(dir, hide)
	return ix.Search(ParseQuery(q, ix))
}

func paths(hits []*Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Doc.Path)
	}
	return out
}

func rank(hits []*Hit, path string) int {
	for i, h := range hits {
		if h.Doc.Path == path {
			return i + 1
		}
	}
	return 0
}

// A PR number finds the task it is the PR of first, then what is tied to that
// task: its run (no word of the question in it) and its revert.
func TestEntityAndConnections(t *testing.T) {
	dir := fixture(t)
	hits := search(t, dir, "why was #111 reverted")
	if len(hits) == 0 || hits[0].Doc.Path != "record/task/261024-015726-45d2d27ed7.md" {
		t.Fatalf("the PR's task should come first: %v", paths(hits))
	}
	if w := why(hits[0], ParseQuery("why was #111 reverted", nil)); !strings.Contains(w, "is about #111") {
		t.Fatalf("why = %q, want it to say the file is about #111", w)
	}
	revert := rank(hits, "record/task/261024-020043-5f804b138f.md")
	run := rank(hits, "record/run/w-261024-015726-45d2d27ed7.md")
	if revert == 0 || run == 0 || revert > 4 {
		t.Fatalf("the revert (%d) and the run (%d) should follow the task: %v", revert, run, paths(hits))
	}
	if h := hits[run-1]; !strings.Contains(h.Via, "id:261024-015726-45d2d27ed7") {
		t.Fatalf("the run came in by %q, want the task id", h.Via)
	}
}

// A file comes back once however many of its entries match, with its best
// two entries quoted.
func TestOneHitPerFile(t *testing.T) {
	dir := fixture(t)
	hits := search(t, dir, "zebra gate reverted")
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.Doc.Path] {
			t.Fatalf("%s came back twice: %v", h.Doc.Path, paths(hits))
		}
		seen[h.Doc.Path] = true
	}
	out := Render(ParseQuery("zebra gate reverted", nil), hits, 10)
	if strings.Count(out, ". record/chat/lead.md") != 1 || strings.Count(out, "chat.posted") > 2 {
		t.Fatalf("the chat should show once with at most two entries:\n%s", out)
	}
}

// Links are followed both ways: a note the best hit links to comes along.
func TestLinksAreFollowed(t *testing.T) {
	dir := fixture(t)
	hits := search(t, dir, "tie at the gate")
	i := rank(hits, "own-check-rule.md")
	if rank(hits, "gate-ties.md") != 1 || i == 0 || !strings.Contains(hits[i-1].Via, "linked from gate-ties.md") {
		t.Fatalf("the linked note should come in by its link: %v", paths(hits))
	}
	if rank(hits, "lonely.md") != 0 {
		t.Fatal("an unrelated note matched")
	}
}

// A line that many files repeat (the lessons every brief quotes) matches
// nothing, and a hidden folder is never read.
func TestBoilerplateAndHidden(t *testing.T) {
	dir := fixture(t)
	for _, h := range search(t, dir, "walrus", "isolated/secret") {
		if strings.HasPrefix(h.Doc.Path, "record/run/w-261024-0000") || strings.HasPrefix(h.Doc.Path, "isolated/") {
			t.Fatalf("%s matched: boilerplate or hidden", h.Doc.Path)
		}
	}
	if rank(search(t, dir, "walrus"), "isolated/secret/hidden.md") == 0 {
		t.Fatal("with nothing hidden the note should match")
	}
}

// The op reads its arguments on stdin and the knowledge base and what the
// caller can't see from its environment.
func TestOp(t *testing.T) {
	dir := fixture(t)
	t.Setenv("HIVE_KB", dir)
	t.Setenv("HIVE_KB_HIDE", "isolated/secret\nrecord")
	var out bytes.Buffer
	if code := op(strings.NewReader(`{"query":"walrus","limit":"all"}`), &out); code != 0 {
		t.Fatalf("op exit %d", code)
	}
	if s := out.String(); strings.Contains(s, "isolated/") || strings.Contains(s, "record/") || !strings.Contains(s, "doesn't cover it") {
		t.Fatalf("op showed what the caller can't see:\n%s", s)
	}
	out.Reset()
	t.Setenv("HIVE_KB_HIDE", "")
	op(strings.NewReader(`{"query":"#111"}`), &out)
	if s := out.String(); !strings.HasPrefix(s, "1. record/task/261024-015726-45d2d27ed7.md") || !strings.Contains(s, "why: is about #111") || !strings.Contains(s, "now: stage done") {
		t.Fatalf("op answer:\n%s", s)
	}
}

func TestEntities(t *testing.T) {
	got := entities("PR #111 (pr.number: 112, https://github.com/o/r/pull/113) for task 261024-015726-45d2d27ed7, run r-261024-015726-45d2d27ed7-101e8531-0, gate-1792869795-a693e1a9 at 1e83d08bc9f4, in internal/mod/kb/kb.go and TestModuleSeam, see [[own-check-rule]]; the kb module; - project: hive")
	for _, want := range []string{"#111", "#112", "#113", "id:261024-015726-45d2d27ed7", "run:r-261024-015726-45d2d27ed7-101e8531-0",
		"run:gate-1792869795-a693e1a9", "commit:1e83d08", "file:internal/mod/kb/kb.go", "file:kb.go", "file:internal/mod/kb", "test:TestModuleSeam",
		"note:own-check-rule", "module:kb"} {
		if got[want] == 0 {
			t.Errorf("no %s in %v", want, got)
		}
	}
	if got["commit:45d2d27"] > 0 {
		t.Error("an id's tail read as a commit")
	}
}

func TestMeasure(t *testing.T) {
	qs := []Question{{Q: "a", Expect: []string{"x", "y"}, Also: []string{"z"}}}
	s := Measure(qs, 3, "t", func(string) []string { return []string{"z", "x", "x", "w"} })
	if s.Recall != 0.5 || s.Precision != 1 || s.MRR != 0.5 || s.Distinct != 2.0/3 {
		t.Fatalf("%+v", s)
	}
}
