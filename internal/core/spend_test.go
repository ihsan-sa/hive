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
	cost("a", 3)
	c.tellSpend(5)
	if n := len(c.Person.Told(5)); n != 0 {
		t.Fatalf("told at $3 of $5: %d notices", n)
	}
	cost("b", 4)
	c.tellSpend(5)
	c.tellSpend(5)
	if told := c.Person.Told(5); len(told) != 1 || len(c.Person.Open()) != 0 {
		t.Fatalf("told past the figure: %+v, want one notice in the feed and nothing waiting", told)
	}
	cost("c", 1)
	c.tellSpend(5)
	if n := len(c.Person.Told(5)); n != 1 {
		t.Fatalf("told again in the same month: %d notices", n)
	}
}
