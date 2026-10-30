package problems

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func open(t *testing.T) (*core.Core, *Module) {
	t.Helper()
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, New(c)
}

func call(t *testing.T, c *core.Core, r core.Run, op string, args map[string]string) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := c.Runs.Ops().Call(r, op, raw)
	s, _ := out.(string)
	return s, err
}

// A problem is posted under the run that met it, whatever the arguments say;
// it shows in other agents' briefs until someone solves it, and others add
// to it on the way.
func TestBoardCarriesProblemsIntoBriefs(t *testing.T) {
	c, m := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Project: "hive"}}
	other := core.Run{RunSpec: core.RunSpec{ID: "w-2", Role: "worker", Project: "textkit"}}
	if m.Brief("worker", "hive") != "" {
		t.Fatal("an empty board added to the brief")
	}
	if _, err := call(t, c, w, "problem", map[string]string{"text": " "}); err == nil {
		t.Fatal("an empty problem was posted")
	}
	if _, err := call(t, c, w, "problem", map[string]string{"text": "main's telemetry test fails on a fresh clone", "where": "internal/core", "run": "someone-else"}); err != nil {
		t.Fatal(err)
	}
	list := m.List(nil)
	if len(list) != 1 || list[0].Run != "w-1" || list[0].Project != "hive" || list[0].Where != "internal/core" {
		t.Fatalf("posted as %+v", list)
	}
	id := list[0].ID

	brief := m.Brief("worker", "textkit")
	if !strings.Contains(brief, id) || !strings.Contains(brief, "w-1") || !strings.Contains(brief, "telemetry") {
		t.Fatalf("another project's worker's brief misses the problem:\n%s", brief)
	}
	if m.Brief("judge", "hive") != "" || m.Brief("warden", "hive") != "" {
		t.Fatal("a judge or warden was handed the board")
	}

	out, err := call(t, c, other, "problem", map[string]string{"text": "telemetry test fails on fresh clone for me too"})
	if err != nil || !strings.Contains(out, id) {
		t.Fatalf("a look-alike post didn't point at %s: %q %v", id, out, err)
	}
	if _, err := call(t, c, other, "problem", map[string]string{"id": id, "also": "the cause is a missing testdata dir"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := m.Get(id); len(p.Notes) != 1 || p.Notes[0].Run != "w-2" {
		t.Fatalf("note recorded as %+v", p.Notes)
	}
	if !strings.Contains(m.Brief("lead", "hive"), "missing testdata") {
		t.Fatal("the latest note isn't in the brief")
	}

	if _, err := call(t, c, other, "problem", map[string]string{"id": id, "solved": "testdata committed"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Brief("worker", "hive"), id) {
		t.Fatal("a solved problem stayed in briefs")
	}
	if out, _ := call(t, c, w, "problems", map[string]string{"all": "yes"}); !strings.Contains(out, "testdata committed") {
		t.Fatalf("the full board misses the solved one: %q", out)
	}
	if _, err := call(t, c, w, "problem", map[string]string{"id": "p-none", "also": "x"}); err == nil {
		t.Fatal("added to a problem that isn't there")
	}
}

// The board is rebuilt from the journal on restart.
func TestBoardSurvivesRestart(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := New(c)
	if _, err := m.Post(core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker"}}, "", "flaky e2e"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c2, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got := New(c2).List(nil); len(got) != 1 || got[0].Text != "flaky e2e" {
		t.Fatalf("after restart: %+v", got)
	}
}

// Solving needs a word on how.
func TestSolveNeedsHow(t *testing.T) {
	c, m := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker"}}
	p, err := m.Post(w, "", "flaky e2e")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, c, w, "problem", map[string]string{"id": p.ID, "solved": "  "}); err == nil {
		t.Fatal("solved with no word on how")
	}
	if got, _ := m.Get(p.ID); !got.Open() {
		t.Fatalf("closed anyway: %+v", got)
	}
}

// A standing problem is one entry: posted once, rewritten only when its text
// changes, and solved when it is cleared.
func TestStandingIsOneEntry(t *testing.T) {
	_, m := open(t)
	r := core.Run{ID: "improve", Role: "module", Project: "p"}
	m.Standing(r, "quarantined tests", "")
	m.Standing(r, "quarantined tests", "TestA")
	m.Standing(r, "quarantined tests", "TestA")
	m.Standing(r, "quarantined tests", "TestA, TestB")
	list := m.List(nil)
	if len(list) != 1 || list[0].Text != "TestA, TestB" || !list[0].Open() {
		t.Fatalf("board %+v", list)
	}
	m.Standing(r, "quarantined tests", "")
	m.Standing(r, "quarantined tests", "TestC")
	list = m.List(nil)
	if len(list) != 2 || list[1].Open() || !list[0].Open() || list[0].Text != "TestC" {
		t.Fatalf("board %+v", list)
	}
}
