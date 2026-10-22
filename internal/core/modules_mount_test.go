package core

import (
	"path/filepath"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

type isolatedBox struct{ sandbox.Sandbox }

func (isolatedBox) Isolated() bool { return true }

// An isolated run sees only its granted modules, so none granted means none,
// even after the empty list is dropped on its way through the journal.
func TestIsolatedRunWithNoGrantsSeesNoModules(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, func(string) ([]string, error) { return nil, nil }, func() []string { return nil })
	h, _ := harness.Get("fake")
	spec, err := rs.sandboxSpec(isolatedBox{}, h, Run{RunSpec: RunSpec{ID: "r1", Checkout: t.TempDir(), Isolated: true}}, 1, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range spec.Mounts {
		if m.Inside == "/hive/modules" && (m.Host == ws.Modules() || !Exists(m.Host)) {
			t.Fatalf("an isolated run with no grants doesn't get an empty modules dir: %+v", m)
		}
	}
}
