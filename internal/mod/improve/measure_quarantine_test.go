package improve

import (
	"strings"
	"testing"
)

// Main red only on a quarantined test is still scored, as the PRs' checks
// read it, unless the merge touched that test's package: then the red counts.
func TestQuarantinedRedIsScored(t *testing.T) {
	m, merge := scored(t)
	const key = "example.com/p/e2e::TestStale"
	m.C.Effects.Quarantined = func(project, task string) []string { return []string{key} }
	red := "--- FAIL: TestStale (1.0s)\nFAIL\nFAIL\texample.com/p/e2e\t3.1s\nok  \texample.com/p/core\t1.2s\n"
	ms := merge(map[string]string{
		"go.mod":             "module example.com/p\n",
		"red.txt":            red,
		".hive/project.json": `{"test": "test ! -f red.txt || { cat red.txt; exit 1; }", "score": {"command": "sh ./score.sh", "direction": "higher"}}`,
	})
	if !ms.TestsOK || !ms.Kept || ms.Score == nil || *ms.Score != 5 || !strings.Contains(ms.Note, "quarantined "+key) {
		t.Fatalf("a red only on a quarantined test: %+v", ms)
	}
	if ms = merge(map[string]string{"e2e/stale_test.go": "package e2e\n"}); ms.TestsOK || ms.Kept {
		t.Fatalf("a merge touching the quarantined test's package: %+v", ms)
	}
}
