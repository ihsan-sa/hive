package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

const guardrail404 = `{"type":"result","subtype":"error","is_error":true,"result":"the model server answered 404: 0 endpoints out of 1 requested are available matching your guardrail restrictions and data policy. Free model training violation (guardrail): 1 endpoint excluded; configurable at https://openrouter.ai/workspaces/default/guardrails","api_error_status":404}`

// endOn ends turn n of run id on open:model with the given result line.
func endOn(t *testing.T, c *Core, id, model string, n int, line string) Run {
	t.Helper()
	return endOnH(t, c, id, "open", model, n, line)
}

// endOnH is endOn on harness h.
func endOnH(t *testing.T, c *Core, id, h, model string, n int, line string) Run {
	t.Helper()
	c.J.MustAppend("run.turn_started", turnStarted{ID: id, Turn: n, Harness: h, Model: model, Login: h + ":" + model})
	r, _ := c.Runs.Get(id)
	out := c.WS.TurnOut(id, n)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out, []byte(line+"\n"), 0o600)
	c.Runs.endTurn(r)
	got, _ := c.Runs.Get(id)
	return got
}

// A model that answers 404 is marked down: the run moves at once to the next
// pick with room, or waits for the first reset as at a usage limit, with no
// retry backoff; it comes back when its config changes or it answers.
func TestModelThatCantAnswerIsSkipped(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"router": {Endpoint: "https://openrouter.ai/api/v1", Model: "free:free", Key: true}, "local": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"}}
	c.WS.SaveConfig(cfg)
	c.Runs.tickDown(cfg) // the config Downs are fingerprinted against
	reset := time.Now().Add(20 * time.Minute)
	c.Runs.append("plan.usage", Plan{Harness: "fake", Limit: harness.RateLimit{Status: "rejected", ResetsAt: reset}})
	route := []Pick{{Harness: "fake", Model: "opus"}, {Harness: "open", Model: "router"}, {Harness: "open", Model: "local"}}
	for _, m := range []string{"router", "local"} { // a worker falls back only to a model that has finished here
		c.J.MustAppend("run.created", RunSpec{ID: "trial-" + m, Role: "worker", Harness: "open", Model: m})
		c.J.MustAppend("run.finish", map[string]any{"id": "trial-" + m, "finish": Finish{Status: "done"}})
	}
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "open", Model: "router", Route: route})

	got := endOn(t, c, "w1", "router", 1, guardrail404)
	if got.Status != StatusParked || got.Retries != 0 || got.ParkedUntil.After(time.Now()) || !strings.Contains(got.Reason, "moving to open:local") {
		t.Fatalf("after the 404: status %s, retries %d, until %v, reason %q", got.Status, got.Retries, got.ParkedUntil, got.Reason)
	}
	down := c.Runs.Down()
	if len(down) != 1 || down[0].Status != 404 || !strings.Contains(down[0].Text, "guardrail") || down[0].Retry.Before(time.Now().Add(50*time.Minute)) {
		t.Fatalf("down: %+v", down)
	}
	if fix := down[0].Fix(cfg); !strings.Contains(fix, "https://openrouter.ai/workspaces/default/guardrails so") {
		t.Fatalf("fix: %s", fix)
	}
	// The other free model can't answer either: it waits for Claude's reset, not a backoff.
	got = endOn(t, c, "w1", "local", 2, `{"type":"result","subtype":"error","is_error":true,"result":"the model server answered 400: model 'qwen' not found","api_error_status":400}`)
	if got.Retries != 0 || got.ParkedUntil.Before(reset) || got.ParkedUntil.After(reset.Add(time.Minute)) {
		t.Fatalf("with every pick limited or down: retries %d, until %v (reset %v), reason %q", got.Retries, got.ParkedUntil, reset, got.Reason)
	}
	if p, _ := c.Runs.next(got); !p.Same(route[0]) {
		t.Fatalf("it resumes on %v", p)
	}
	// The same 404 again keeps its spell, so the person hears once.
	first := c.Runs.Down()[1].ToldID()
	endOn(t, c, "w1", "router", 3, guardrail404)
	if again := c.Runs.Down()[1].ToldID(); again != first {
		t.Fatalf("a second 404 started a new spell: %s, %s", first, again)
	}
	// A changed model config brings it back; an answer brings the other back.
	cfg.Models["router"] = OpenModel{Endpoint: "https://openrouter.ai/api/v1", Model: "other:free", Key: true}
	c.Runs.tickDown(cfg)
	if down := c.Runs.Down(); len(down) != 1 || down[0].Pick.Model != "local" {
		t.Fatalf("after router's config changed: %+v", down)
	}
	endOn(t, c, "w1", "local", 4, `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`)
	if down := c.Runs.Down(); len(down) != 0 {
		t.Fatalf("after local answered: %+v", down)
	}
}

// An error that doesn't say the model can't answer (a 500) is retried with
// backoff: one run failing again and again is that run's trouble; the same
// error three turns running, from more than one run, marks the model down.
func TestSameErrorThreeTurnsIsDown(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"m": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"}}
	c.WS.SaveConfig(cfg)
	c.Runs.tickDown(cfg)
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "open", Model: "m"})
	c.J.MustAppend("run.created", RunSpec{ID: "w2", Role: "worker", Harness: "open", Model: "m"})
	line := `{"type":"result","subtype":"error","is_error":true,"result":"the model server answered 500: boom","api_error_status":500}`
	for n := 1; n <= 4; n++ {
		if got := endOn(t, c, "w1", "m", n, line); got.Retries != n || len(c.Runs.Down()) != 0 {
			t.Fatalf("w1 turn %d: retries %d, down %+v", n, got.Retries, c.Runs.Down())
		}
	}
	got := endOn(t, c, "w2", "m", 1, line)
	if len(c.Runs.Down()) != 1 || got.Retries != 0 || got.ParkedUntil.Before(time.Now().Add(50*time.Minute)) {
		t.Fatalf("w2's turn: retries %d, until %v, down %+v", got.Retries, got.ParkedUntil, c.Runs.Down())
	}
}

// Claude's errors are each run's own (often with no text): however many runs
// fail alike, claude:opus isn't marked down, nor by a 401; only an answer
// that it has no such model marks it.
func TestClaudeErrorsDontMarkItDown(t *testing.T) {
	c, _ := testHive(t)
	c.Runs.tickDown(c.WS.LoadConfig())
	for _, line := range []string{
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":""}`,
		`{"type":"result","subtype":"error_max_turns","is_error":true,"result":"","api_error_status":500}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"API Error: 500 Internal server error","api_error_status":500}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"API Error: 400 prompt is too long: 210000 tokens > 200000 maximum for this model","api_error_status":400}`,
	} {
		for i := 1; i <= 4; i++ {
			id := fmt.Sprintf("w%d-%d", i, len(line))
			c.J.MustAppend("run.created", RunSpec{ID: id, Role: "worker", Harness: "claude", Model: "opus"})
			endOnH(t, c, id, "claude", "opus", 1, line)
		}
		if d := c.Runs.Down(); len(d) != 0 {
			t.Fatalf("after %s on four runs: %+v", line, d)
		}
	}
	c.J.MustAppend("run.created", RunSpec{ID: "nm", Role: "worker", Harness: "claude", Model: "opus"})
	endOnH(t, c, "nm", "claude", "opus", 1, `{"type":"result","subtype":"success","is_error":true,"result":"API Error: 404 {\"type\":\"error\",\"error\":{\"type\":\"not_found_error\",\"message\":\"model: claude-opus-9 not found\"}}","api_error_status":404}`)
	if d := c.Runs.Down(); len(d) != 1 || d[0].Pick.String() != "claude:opus" {
		t.Fatalf("after a model-not-found 404: %+v", d)
	}
}

// A 400 for one run's context overflowing is that run's, not the model's.
func TestContextLength400IsntDown(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"m": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"}}
	c.WS.SaveConfig(cfg)
	c.Runs.tickDown(cfg)
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "open", Model: "m"})
	got := endOn(t, c, "w1", "m", 1, `{"type":"result","subtype":"error","is_error":true,"result":"the model server answered 400: This model's maximum context length is 32768 tokens. However, your messages resulted in 40100 tokens.","api_error_status":400}`)
	if d := c.Runs.Down(); len(d) != 0 || got.Retries != 1 {
		t.Fatalf("a context-length 400: retries %d, down %+v", got.Retries, d)
	}
}

// A run waiting on the only pick it has, which can't answer, wakes as soon
// as that pick's config changes, not at its retry an hour on.
func TestParkedRunWakesWhenThePickComesBack(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"router": {Endpoint: "https://openrouter.ai/api/v1", Model: "free:free"}}
	c.WS.SaveConfig(cfg)
	c.Runs.tickDown(cfg)
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "open", Model: "router", Route: []Pick{{Harness: "open", Model: "router"}}})
	got := endOn(t, c, "w1", "router", 1, guardrail404)
	if !got.ParkedUntil.After(time.Now().Add(50 * time.Minute)) {
		t.Fatalf("parked until %v", got.ParkedUntil)
	}
	cfg.Models["router"] = OpenModel{Endpoint: "https://openrouter.ai/api/v1", Model: "other:free"}
	c.Runs.tickDown(cfg)
	got, _ = c.Runs.Get("w1")
	if got.Status != StatusParked || got.ParkedUntil.After(time.Now()) {
		t.Fatalf("after the config changed: %s until %v (%s)", got.Status, got.ParkedUntil, got.Reason)
	}
}
