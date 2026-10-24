package core

import (
	"math"
	"testing"
	"time"

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

// Claude Code's total_cost_usd is its session's running total, carried on
// when the session is resumed. These are the lead's real result lines from
// three turns of one session (trimmed): it spent $0.4700508 in all, and the
// per-turn spend the CLI reports beside them (model_usage_since_last_result)
// is $0.4259718, $0.0343042 and $0.0097748. A fresh session starts again.
func TestCostOfResumedSession(t *testing.T) {
	c, _ := testHive(t)
	c.J.MustAppend("run.created", RunSpec{ID: "lead", Harness: "claude"})
	lines := []string{
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","num_turns":16,"session_id":"a6da7b50-b725-4955-b359-b722a926f945","total_cost_usd":0.4259718}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","num_turns":2,"session_id":"a6da7b50-b725-4955-b359-b722a926f945","total_cost_usd":0.460276}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok","num_turns":1,"session_id":"a6da7b50-b725-4955-b359-b722a926f945","total_cost_usd":0.4700508}`,
	}
	turn := func(n int, session string, resume bool, line string) {
		c.J.MustAppend("run.turn_started", turnStarted{ID: "lead", Session: session, Turn: n, Resume: resume})
		var res *harness.Result
		for _, e := range (harness.Claude{}).Parse([]byte(line)) {
			if e.Result != nil {
				res = e.Result
			}
		}
		c.J.MustAppend("run.turn_ended", turnEnded{ID: "lead", Turn: n, SessionLive: true, Session: session, Result: res, Status: StatusIdle})
	}
	for i, l := range lines {
		turn(i+1, "a6da7b50-b725-4955-b359-b722a926f945", i > 0, l)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	r, _ := c.Runs.Get("lead")
	if !near(r.Cost, 0.4700508) {
		t.Fatalf("three turns of one resumed session cost $%.7f, want $0.4700508 (summing the running totals gives $1.3562986)", r.Cost)
	}
	turn(4, "b1", false, `{"type":"result","subtype":"success","result":"ok","session_id":"b1","total_cost_usd":0.1}`)
	turn(5, "b1", true, `{"type":"result","subtype":"success","result":"ok","session_id":"b1","total_cost_usd":0.15}`)
	r, _ = c.Runs.Get("lead")
	if !near(r.Cost, 0.6200508) {
		t.Fatalf("a fresh session's spend adds from zero: $%.7f, want $0.6200508", r.Cost)
	}
	if month := c.Runs.Spent(time.Now().UTC().Format("2006-01")); !near(month, 0.6200508) {
		t.Fatalf("the month's spend is $%.7f, want $0.6200508", month)
	}
}
