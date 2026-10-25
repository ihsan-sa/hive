package stats

import (
	"path/filepath"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Passing the monthly figure tells the person once; work is not touched.
func TestSpendToldOnce(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cost := func(id string, usd float64) {
		c.J.MustAppend("run.created", core.RunSpec{ID: id, Harness: "fake"})
		c.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone, "result": map[string]any{"cost_usd": usd}})
	}
	n := SpendNotice{C: c}
	cost("a", 3)
	n.tell(5)
	if k := len(c.Person.Told(5)); k != 0 {
		t.Fatalf("told at $3 of $5: %d notices", k)
	}
	cost("b", 4)
	n.tell(5)
	n.tell(5)
	if told := c.Person.Told(5); len(told) != 1 || len(c.Person.Open()) != 0 {
		t.Fatalf("told past the figure: %+v, want one notice in the feed and nothing waiting", told)
	}
	cost("c", 1)
	n.tell(5)
	if k := len(c.Person.Told(5)); k != 1 {
		t.Fatalf("told again in the same month: %d notices", k)
	}
}
