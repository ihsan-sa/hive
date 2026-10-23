package core

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// Passing the monthly figure tells the person once; work is not touched.
func TestSpendToldOnce(t *testing.T) {
	c, _ := testHive(t)
	cost := func(id string, usd float64) {
		c.J.MustAppend("run.created", RunSpec{ID: id, Harness: "fake"})
		c.J.MustAppend("run.turn_ended", turnEnded{ID: id, Turn: 1, Status: StatusDone, Result: &harness.Result{CostUSD: usd}})
	}
	cfg := c.WS.LoadConfig()
	cfg.MonthlyUSD = 5
	c.WS.SaveConfig(cfg)
	cost("a", 3)
	c.tellSpend()
	if n := len(c.Person.Open()); n != 0 {
		t.Fatalf("told at $3 of $5: %d asks", n)
	}
	cost("b", 4)
	c.tellSpend()
	c.tellSpend()
	open := c.Person.Open()
	if len(open) != 1 || open[0].Kind != "told" {
		t.Fatalf("asks past the figure: %+v, want one notice", open)
	}
	c.Person.Answer(open[0].ID, "seen", "")
	cost("c", 1)
	c.tellSpend()
	if n := len(c.Person.Open()); n != 0 {
		t.Fatalf("told again in the same month: %d asks", n)
	}
}
