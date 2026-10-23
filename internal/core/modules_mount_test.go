package core

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

type isolatedBox struct{}

func (isolatedBox) Name() string   { return "isolated" }
func (isolatedBox) Isolated() bool { return true }
func (isolatedBox) Check() error   { return nil }
func (isolatedBox) Start(sandbox.Spec) (sandbox.Proc, error) {
	return sandbox.Proc{}, errors.New("not started")
}
func (isolatedBox) Run(sandbox.Spec, time.Duration) (int, error) { return 0, errors.New("not run") }

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
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	h, _ := harness.Get("fake")
	spec, err := rs.sandboxSpec(rs.ws.LoadConfig(), isolatedBox{}, h, Run{RunSpec: RunSpec{ID: "r1", Checkout: t.TempDir(), Isolated: true}}, 1, false, "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range spec.Mounts {
		if m.Inside == "/hive/modules" && (m.Host == ws.Modules() || !Exists(m.Host)) {
			t.Fatalf("an isolated run with no grants doesn't get an empty modules dir: %+v", m)
		}
	}
}
