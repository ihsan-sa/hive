package improve

import (
	"encoding/json"
	"strings"
	"testing"
)

// Any agent's pivot is filed as a fix, first in line, carrying its case and
// evidence, and refused without either.
func TestPivotIsFiledFirstInLine(t *testing.T) {
	m := rig(t)
	r := said(t, m, "w1", "hive", "working")
	pivot := func(a map[string]string) (any, error) {
		raw, _ := json.Marshal(a)
		return m.opPivot(r, raw)
	}
	if _, err := pivot(map[string]string{"case": "drop the gate"}); err == nil {
		t.Fatal("a pivot with no evidence was filed")
	}
	if _, err := pivot(map[string]string{"case": strings.Repeat("x", pivotCase+1), "evidence": "e"}); err == nil {
		t.Fatal("a pivot with an essay for a case was filed")
	}
	out, err := pivot(map[string]string{"case": "Drop the recursive gate\nIt rolls back most changes on noise.", "evidence": "32 of 59 verdicts were rollbacks on noise"})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := m.Work.Get(out.(map[string]string)["task"])
	if !ok {
		t.Fatal("no task filed")
	}
	if !task.Now || !task.Fix() || task.Project != "hive" || task.From != "w1" || task.Title != "Pivot: Drop the recursive gate" {
		t.Fatalf("filed as %+v", task)
	}
	if !strings.Contains(task.Known, "32 of 59 verdicts") || !strings.Contains(task.Known, "It rolls back most changes on noise.") {
		t.Fatalf("its brief lacks the case or evidence: %s", task.Known)
	}
}
