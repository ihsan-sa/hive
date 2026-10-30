package work

import (
	"strings"
	"testing"
)

// Search shows the recent tasks like the query in the asker's project, with
// their runs, and none of another project's.
func TestRelatedTasksForSearch(t *testing.T) {
	m := queueRig(t)
	brief(t, m, "w-one", map[string]any{"title": "Speed up the effects reader", "done": "internal/core/effects.go reads once"})
	brief(t, m, "w-two", map[string]any{"title": "Runs keep their exit code", "done": "internal/core/runs.go records it"})
	tick(m)
	var first, second Task
	for _, o := range m.List(nil) {
		if o.Title == "Speed up the effects reader" {
			first = o
		} else {
			second = o
		}
	}
	if first.ID == "" || second.ID == "" {
		t.Fatalf("tasks: %+v", m.List(nil))
	}
	if got := m.Related("p", "effects reader slow"); !strings.Contains(got, first.ID) || strings.Contains(got, second.ID) {
		t.Fatalf("search's related tasks: %q", got)
	}
	if got := m.Related("other", "effects reader slow"); got != "" {
		t.Fatalf("another project's run saw p's tasks: %q", got)
	}
}
