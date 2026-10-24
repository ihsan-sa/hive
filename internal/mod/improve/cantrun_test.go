package improve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A merge whose tests can't find their tool is no result: not kept, not red,
// no revert, and measured again; once the tool is there it is kept.
func TestMissingToolIsNoResult(t *testing.T) {
	m, merge := scored(t)
	defer func(d time.Duration) { retryNoResult = d }(retryNoResult)
	retryNoResult = time.Hour // the test measures again itself
	ms := merge(map[string]string{".hive/project.json": `{"test": "hive-test-tool-xyz", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
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
}

// A test that really fails is still red, and the merge is reverted.
func TestRealFailureIsStillRed(t *testing.T) {
	m, merge := scored(t)
	ms := merge(map[string]string{".hive/project.json": `{"test": "echo broken; exit 1", "score": {"command": "sh ./score.sh", "direction": "higher"}}`})
	if ms.NoResult || ms.Kept || ms.TestsOK || !strings.Contains(ms.Note, "tests red") {
		t.Fatalf("a failing test should be red: %+v", ms)
	}
	if n := len(m.Work.List(func(t work.Task) bool { return t.Kind == "revert" })); n != 1 {
		t.Fatalf("a red merge filed %d reverts, want 1", n)
	}
}
