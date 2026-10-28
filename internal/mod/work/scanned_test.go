package work

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// A self project's change that adds a deny word to a published file goes
// back to its worker before it merges, named by file, line and kind only,
// never the word; one already on main isn't its to fix (issue #1897).
func TestAChangeTheReleaseScanBlocksGoesBack(t *testing.T) {
	t.Setenv(scan.HandlesEnv, "somebody")
	t.Setenv(scan.DenyEnv, "zorblat")
	onMain := map[string]string{".hive/public.txt": "**\n!secret/**\n", "old.md": "a zorblat from before\n", "secret/x.md": "zorblat\n"}
	m, task, p, f, _ := selfRigSeeded(t, onMain, map[string]string{"docs/goals.md": "one\nthe ZORBLAT runner\n", "secret/y.md": "zorblat\n", "old.md": "a zorblat from before, kept\n"}, core.GateKeep)
	task.Aim, task.AimWhy = AimFix, "TestX fails"
	mergeOnce(t, m, task, p)
	if task.Stage != StageWorking || prState(t, f, task.PR.Number) != "OPEN" {
		t.Fatalf("stage %q, reason %q: want it sent back unmerged", task.Stage, task.Reason)
	}
	if !strings.Contains(task.Findings, "docs/goals.md:2: word 1 of the deny list") || strings.Contains(task.Findings, "old.md") || strings.Contains(task.Findings, "secret/") || strings.Contains(strings.ToLower(task.Findings), "zorblat") {
		t.Fatalf("findings %q: want the added line named, not main's, not an unpublished file, never the word", task.Findings)
	}
}

// A change that adds nothing the scan blocks merges as before.
func TestAChangeTheReleaseScanPassesMerges(t *testing.T) {
	t.Setenv(scan.HandlesEnv, "somebody")
	t.Setenv(scan.DenyEnv, "zorblat")
	m, task, p, _, _ := selfRigSeeded(t, map[string]string{".hive/public.txt": "**\n"}, map[string]string{"docs/goals.md": "the second machine's runner\n"}, core.GateKeep)
	task.Aim, task.AimWhy = AimFix, "TestX fails"
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged {
		t.Fatalf("stage %q, reason %q: want merged", task.Stage, task.Reason)
	}
}
