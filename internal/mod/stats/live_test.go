package stats

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// A running Claude turn's cost shows before its result line: its replies'
// tokens, each counted once though Claude Code repeats them per content
// block, at the list price of the model the harness began on, read on from
// where the last read stopped; the turn's end drops it.
func TestLiveEstimate(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tally := New(c.J)
	l := &Live{C: c, T: tally}
	c.J.MustAppend("run.created", core.RunSpec{ID: "r", Harness: "claude", Model: "opus"})
	c.J.MustAppend("run.turn_started", map[string]any{"id": "r", "turn": 1})
	out := c.WS.TurnOut("r", 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out, []byte(`{"type":"system","subtype":"init","session_id":"s","model":"claude-sonnet-5-5"}
{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":1000000,"output_tokens":10}}}
{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","name":"Bash"}],"usage":{"input_tokens":1000000,"output_tokens":100000}}}
{"type":"assistant","message":{"id":"m2","content":[],"usa`), 0o644)
	live := func() float64 { return tally.View(time.Now()).Live["r"] }
	l.Tick()
	// sonnet 5.5, as the harness said it began: $2 in, $1 out; m2 is half written
	if got := live(); math.Abs(got-3) > 1e-9 {
		t.Fatalf("estimate %v", got)
	}
	f, _ := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`ge":{"input_tokens":0,"cache_read_input_tokens":1000000,"output_tokens":0}}}` + "\n")
	f.Close()
	l.Tick()
	if got := live(); math.Abs(got-3) > 1e-9 {
		t.Fatalf("read again within ten seconds: %v", got)
	}
	l.reads["r"].at = time.Time{}
	l.Tick()
	if got := live(); math.Abs(got-3.2) > 1e-9 { // and $0.20 a cache read
		t.Fatalf("estimate read on: %v", got)
	}
	c.J.MustAppend("run.turn_ended", map[string]any{"id": "r", "turn": 1, "status": core.StatusIdle, "result": map[string]any{"cost_usd": 3.3}})
	if v := tally.View(time.Now()); v.Live != nil {
		t.Fatalf("an ended turn keeps no estimate: %v", v.Live)
	}
	tally.live("r", 1, 9)
	if v := tally.View(time.Now()); v.Live != nil {
		t.Fatalf("a late estimate of an ended turn is dropped: %v", v.Live)
	}
	l.Tick()
	if len(l.reads) != 0 {
		t.Fatalf("a run no longer running keeps no reading: %v", l.reads)
	}
}

// A named model's set price comes first; a harness with no list price shows none.
func TestEstimatePrices(t *testing.T) {
	var u harness.Usage
	u.Add(harness.Event{Kind: "usage", Usage: &harness.Tokens{Input: 1e6}})
	cfg := core.Config{Models: map[string]core.OpenModel{"kimi": {Price: &core.Price{Input: 0.5}}}}
	if got := estimate(harness.Open{}, core.Run{RunSpec: core.RunSpec{Model: "kimi"}}, u, cfg); got != 0.5 {
		t.Fatalf("named model: %v", got)
	}
	if got := estimate(harness.Open{}, core.Run{RunSpec: core.RunSpec{Model: "qwen"}}, u, cfg); got != 0 {
		t.Fatalf("unpriced: %v", got)
	}
	if got := estimate(harness.Codex{}, core.Run{RunSpec: core.RunSpec{Model: "gpt-5"}}, u, cfg); got != 0 {
		t.Fatalf("codex: %v", got)
	}
}
