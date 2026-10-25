package improve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A merge whose tests can't find their tool is no result: not kept, not red,
// no revert, and measured again; once the tool is there it is kept.
func TestMissingToolIsNoResult(t *testing.T) {
	m, merge := scored(t)
	defer func(d time.Duration) { retryNoResult = d }(retryNoResult)
	retryNoResult = time.Hour // the test measures again itself
	ms := merge(map[string]string{"t.sh": "hive-test-tool-xyz\n", ".hive/project.json": `{"test": "sh ./t.sh", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
	if !ms.NoResult || ms.Kept || !strings.Contains(ms.Note, "hive-test-tool-xyz is not installed") {
		t.Fatalf("a missing tool should be no result naming it: %+v", ms)
	}
	if n := len(m.Work.List(func(t work.Task) bool { return t.Kind == "revert" })); n != 0 {
		t.Fatalf("a missing tool filed %d reverts", n)
	}
	if m.measured("p", ms.SHA) {
		t.Fatal("a merge with no result counts as measured, so it won't be measured again")
	}
	alerted := false
	for _, e := range m.C.J.Since(0) {
		var a struct{ Kind, Text string }
		if e.Kind == "alert.raised" && e.Decode(&a) == nil && a.Kind == "main-red" && strings.Contains(a.Text, "hive-test-tool-xyz") {
			alerted = true
		}
	}
	if !alerted {
		t.Fatal("no alert says which tool is missing")
	}

	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "hive-test-tool-xyz"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m.afterMerge(work.Task{ID: ms.Task, Project: "p", MergeSHA: ms.SHA})
	all := m.Scores("p")
	if last := all[len(all)-1]; !last.Kept || last.NoResult || last.SHA != ms.SHA {
		t.Fatalf("with the tool there the merge should be measured and kept: %+v", last)
	}
	if cantRunAlertOpen(m) {
		t.Fatal("the can't-run alert stays open with the tool there")
	}
}

// A merge whose tests keep faking a missing tool (a new name each time) is
// held only work.CantRunTries measures: then it counts as red, the person is
// asked for the tool, and it is reverted as any red merge is.
func TestFakedCantRunMergeTurnsRed(t *testing.T) {
	m, merge := scored(t)
	defer func(d time.Duration) { retryNoResult = d }(retryNoResult)
	retryNoResult = time.Hour // the test measures again itself
	ms := merge(map[string]string{".hive/project.json": `{"test": "echo \"sh: 1: tool$$: not found\"; exit 127", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
	if !ms.NoResult {
		t.Fatalf("the first measure should be no result: %+v", ms)
	}
	for i := 1; i < work.CantRunTries; i++ {
		if m.measured("p", ms.SHA) {
			t.Fatalf("judged after %d tries, want %d", i, work.CantRunTries)
		}
		m.afterMerge(work.Task{ID: ms.Task, Project: "p", MergeSHA: ms.SHA})
	}
	all := m.Scores("p")
	if last := all[len(all)-1]; last.NoResult || last.Kept || last.TestsOK || !strings.Contains(last.Note, "counts as red") {
		t.Fatalf("after %d tries the merge should be red: %+v", work.CantRunTries, last)
	}
	if n := len(m.Work.List(func(t work.Task) bool { return t.Kind == "revert" })); n != 1 {
		t.Fatalf("%d reverts filed, want 1", n)
	}
	asked := false
	for _, a := range m.C.Person.Open() {
		asked = asked || a.Ref == "cant-run:"+ms.SHA && strings.Contains(a.Title, "Install tool")
	}
	if !asked {
		t.Fatal("the person isn't asked for the tool")
	}
	counted := 0
	for _, e := range m.C.J.Since(0) {
		if e.Kind == "score.cant-run" {
			counted++
		}
	}
	if counted != work.CantRunTries {
		t.Fatalf("%d tries counted over %d measures, want one each", counted, work.CantRunTries)
	}

	// Once main measures with a result again, the ask is stale and closed.
	merge(map[string]string{".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
	for _, a := range m.C.Person.Open() {
		if a.Ref == "cant-run:"+ms.SHA {
			t.Fatalf("the ask stays open after main measures again: %+v", a)
		}
	}
}

// A full disk or quota is the machine's fault, which a commit can't fake: it
// never counts toward red, however long it lasts.
func TestFullDiskNeverTriesOut(t *testing.T) {
	m, _ := scored(t)
	full := core.Check{Kind: "test", CantRun: "can't run: the disk or a quota is full where checks run"}
	for i := 0; i < 2*work.CantRunTries; i++ {
		if m.triedOut("p", "abc", full) {
			t.Fatalf("a full disk counted as red after %d measures", i+1)
		}
	}
	tool := core.Check{Kind: "test", CantRun: "can't run: x is not installed where checks run", Tool: "x"}
	for i := 1; i < work.CantRunTries; i++ {
		m.triedOut("p", "abc", tool)
	}
	if !m.triedOut("p", "abc", tool) {
		t.Fatalf("a missing tool isn't red after %d measures", work.CantRunTries)
	}
}

// cantRunAlertOpen says the journal leaves p's can't-run alert open.
func cantRunAlertOpen(m *Module) (open bool) {
	for _, e := range m.C.J.Since(0) {
		var a struct{ Ref string }
		if e.Decode(&a) == nil && a.Ref == "cant-run:p" {
			open = e.Kind == "alert.raised"
		}
	}
	return open
}

// Several merges waiting on one missing tool raise one alert, and only the
// newest waits; once the tool is there it is measured, and an older merge it
// superseded is no longer measured.
func TestOnlyTheNewestMergeWaits(t *testing.T) {
	m, merge := scored(t)
	defer func(d time.Duration) { retryNoResult = d }(retryNoResult)
	retryNoResult = time.Hour
	a := merge(map[string]string{"t.sh": "hive-test-tool-xyz\n", ".hive/project.json": `{"test": "sh ./t.sh", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
	b := merge(map[string]string{"n.txt": "6\n"})
	if !a.NoResult || !b.NoResult {
		t.Fatalf("both merges should have no result: %+v %+v", a, b)
	}
	n := 0
	for _, e := range m.C.J.Since(0) {
		var x struct{ Ref string }
		if e.Kind == "alert.raised" && e.Decode(&x) == nil && x.Ref == "cant-run:p" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d can't-run alerts for one missing tool, want 1", n)
	}
	m.mu.Lock()
	h, ok := m.pending["p"]
	m.mu.Unlock()
	if !ok || h.t.MergeSHA != b.SHA {
		t.Fatalf("held %+v, want only the newest merge %s", h, b.SHA)
	}
	m.afterMerge(work.Task{ID: a.Task, Project: "p", MergeSHA: a.SHA}) // an older one comes back (a restart's Recover)
	m.mu.Lock()
	h = m.pending["p"]
	m.mu.Unlock()
	if h.t.MergeSHA != b.SHA {
		t.Fatalf("an older merge took the newest one's place: %+v", h)
	}

	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "hive-test-tool-xyz"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m.afterMerge(h.t)
	before := len(m.Scores("p"))
	if last := m.Scores("p")[before-1]; !last.Kept || last.SHA != b.SHA {
		t.Fatalf("the newest merge should be kept with the tool there: %+v", last)
	}
	m.afterMerge(work.Task{ID: a.Task, Project: "p", MergeSHA: a.SHA})
	if after := len(m.Scores("p")); after != before {
		t.Fatalf("a superseded merge was measured: %+v", m.Scores("p")[after-1])
	}
}

// A merge that itself names a tool that isn't there in its test command (a
// typo) is the change's fault: red and reverted, not a wait.
func TestMisspeltToolIsRed(t *testing.T) {
	m, merge := scored(t)
	ms := merge(map[string]string{".hive/project.json": `{"test": "hive-test-tool-xyz ./...", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
	if ms.NoResult || ms.Kept || !strings.Contains(ms.Note, "red") || !strings.Contains(ms.Note, "hive-test-tool-xyz") {
		t.Fatalf("a misspelt tool should be red naming it: %+v", ms)
	}
	if n := len(m.Work.List(func(t work.Task) bool { return t.Kind == "revert" })); n != 1 {
		t.Fatalf("a misspelt tool filed %d reverts, want 1", n)
	}
}

// A red whose rerun can't find its tool is no result, not red.
func TestRetestThatCantRunIsNoResult(t *testing.T) {
	m, merge := scored(t)
	defer func(d time.Duration) { retryNoResult = d }(retryNoResult)
	retryNoResult = time.Hour
	mark := filepath.Join(t.TempDir(), "first")
	ms := merge(map[string]string{
		"t.sh":               "if [ -f '" + mark + "' ]; then hive-test-tool-xyz; else touch '" + mark + "'; echo broken; exit 1; fi\n",
		".hive/project.json": `{"test": "sh ./t.sh", "score": {"command": "sh ./score.sh", "direction": "higher"}}`,
	})
	if !ms.NoResult || ms.Kept || !strings.Contains(ms.Note, "ran again: ") || !strings.Contains(ms.Note, "hive-test-tool-xyz") {
		t.Fatalf("a rerun that can't start should be no result: %+v", ms)
	}
	if n := len(m.Work.List(func(t work.Task) bool { return t.Kind == "revert" })); n != 0 {
		t.Fatalf("a rerun that can't start filed %d reverts", n)
	}
}

// A test that really fails is still red, and the merge is reverted.
func TestRealFailureIsStillRed(t *testing.T) {
	m, merge := scored(t)
	ms := merge(map[string]string{".hive/project.json": `{"test": "echo broken; exit 1", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
	if ms.NoResult || ms.Kept || ms.TestsOK || !strings.Contains(ms.Note, "red") {
		t.Fatalf("a failing test should be red: %+v", ms)
	}
	if n := len(m.Work.List(func(t work.Task) bool { return t.Kind == "revert" })); n != 1 {
		t.Fatalf("a red merge filed %d reverts, want 1", n)
	}
}
