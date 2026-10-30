package work

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// limitsList puts guard/** on main's limits: code that enforces the hard limits.
var limitsList = map[string]string{core.SecurityList: "limits = [\n  \"guard/**\", # the warden\n]\n"}

// A change to the limits code waits in merging for one read, asked once per
// head through Weigh; a land lets it merge, and a no sends it back.
func TestLimitsWaitForAWarden(t *testing.T) {
	for _, answer := range []string{"merge", "send back"} {
		m, task, p, f := gateRigSeeded(t, false, limitsList, map[string]string{"guard/warden.go": "package guard\n"})
		var asked []core.Ask
		m.Weigh = func(a core.Ask) string { asked = append(asked, a); return "warden" }
		toMerging(t, m, task, p)
		mergeOnce(t, m, task, p)
		mergeOnce(t, m, task, p)
		if task.Stage != StageMerging || prState(t, f, task.PR.Number) != "OPEN" || len(asked) != 1 {
			t.Fatalf("stage %q, reason %q, asked %d", task.Stage, task.Reason, len(asked))
		}
		if a := asked[0]; a.Kind != core.LimitsRead || a.Ref != task.ID+"@"+task.SHA || a.Diff == "" {
			t.Fatalf("ask %+v", a)
		}
		a := m.C.Person.Ask(asked[0])
		if _, err := m.C.Person.Answer(a.ID, answer, "the warden: "+answer); err != nil {
			t.Fatal(err)
		}
		m.C.Person.Handled()
		got, _ := m.Get(task.ID)
		task = &got
		if answer == "send back" {
			if task.Stage != StageWorking || task.Round != 1 {
				t.Fatalf("a no didn't send it back: stage %q, reason %q", task.Stage, task.Reason)
			}
			continue
		}
		mergeOnce(t, m, task, p)
		if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
			t.Fatalf("a read change didn't merge: stage %q, reason %q", task.Stage, task.Reason)
		}
	}
}

// Everything off the list still merges with nothing between.
func TestLimitsLeaveTheRestAlone(t *testing.T) {
	m, task, p, f := gateRigSeeded(t, false, limitsList, map[string]string{"plain/x.go": "package plain\n"})
	m.Weigh = func(a core.Ask) string { t.Fatalf("asked %+v", a); return "" }
	toMerging(t, m, task, p)
	mergeOnce(t, m, task, p)
	if task.Stage != StageMerged || prState(t, f, task.PR.Number) != "MERGED" {
		t.Fatalf("stage %q, reason %q", task.Stage, task.Reason)
	}
}
