package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A deep search starts one researcher per asker, on read tools and a budget,
// with the plain search's results; its answer goes to the asker once, even
// after a restart, and a researcher can't start another.
func TestDeepSearch(t *testing.T) {
	c, m := newKB(t)
	os.WriteFile(filepath.Join(c.WS.KB(), "gate.md"), []byte("---\ntitle: Gate\n---\n\nThe gate rolls back ties.\n"), 0o644)
	asker, err := c.Runs.Start(core.RunSpec{ID: "w-asker", Role: "worker", Harness: "fake", Checkout: t.TempDir(), Brief: "work"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Runs.Ops().Call(asker, "search", []byte(`{"deep":"why does the gate roll back ties"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s := out.(string); !strings.Contains(s, "comes to you as a message") || !strings.Contains(s, "gate.md") {
		t.Errorf("deep search said: %s", s)
	}
	rs := c.Runs.List(func(r *core.Run) bool { return r.Role == Researcher })
	if len(rs) != 1 {
		t.Fatalf("researchers: %+v", rs)
	}
	r := rs[0]
	if r.Budget != DeepBudget || r.Schema == "" || strings.Join(r.Tools, ",") != "Read,Grep,Glob" || r.ReadOnly || r.Inputs["asker"] != "w-asker" || !strings.Contains(r.Inputs["search.md"], "gate.md") || !strings.Contains(r.Brief, "knowledge base researcher") {
		t.Errorf("researcher spec: %+v", r.RunSpec)
	}
	if a, _ := c.Runs.Get("w-asker"); a.Waiting == "" {
		t.Error("the asker isn't marked waiting for the answer")
	}
	if _, err := c.Runs.Ops().Call(asker, "search", []byte(`{"deep":true,"query":"again"}`)); err == nil || !strings.Contains(err.Error(), "still at work") {
		t.Errorf("a second deep search while one runs: %v", err)
	}
	if _, err := c.Runs.Ops().Call(r, "search", []byte(`{"deep":"deeper"}`)); err == nil {
		t.Error("a researcher started a deep search")
	}
	c.Runs.Stop(r.ID, "test")
	m.Tick()
	m.Tick()
	msgs := 0
	for _, p := range mustRun(t, c, "w-asker").Pending {
		if strings.Contains(p, "[deep search "+r.ID) {
			msgs++
		}
	}
	if msgs != 1 {
		t.Errorf("the asker got %d answers, want 1: %q", msgs, mustRun(t, c, "w-asker").Pending)
	}
	// A restart reads what was sent from the journal.
	m2 := New(c, m.Agents)
	m2.Tick()
	if n := len(mustRun(t, c, "w-asker").Pending); n != msgs {
		t.Errorf("a restart sent the answer again: %d pending", n)
	}
}

func mustRun(t *testing.T, c *core.Core, id string) core.Run {
	r, ok := c.Runs.Get(id)
	if !ok {
		t.Fatalf("no run %s", id)
	}
	return r
}
