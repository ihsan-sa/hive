package harness

import (
	"strings"
	"testing"
	"time"
)

func TestParseClaudeStream(t *testing.T) {
	c := Claude{}
	cases := []struct {
		line string
		kind string
	}{
		{`{"type":"system","subtype":"init","session_id":"s1","model":"m"}`, "init"},
		{`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`, "text"},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`, "tool"},
		{`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1793160000,"rateLimitType":"five_hour","utilization":1,"unifiedWindows":{"five_hour":{"utilization":1,"resetsAt":1793160000}}}}`, "ratelimit"},
		{`{"type":"result","subtype":"success","is_error":false,"result":"{\"verdict\":\"land\"}","structured_output":{"verdict":"land"},"total_cost_usd":0.1,"session_id":"s1"}`, "result"},
	}
	for _, tc := range cases {
		evs := c.Parse([]byte(tc.line))
		if len(evs) != 1 || evs[0].Kind != tc.kind {
			t.Fatalf("%s: got %+v", tc.line, evs)
		}
	}
	r := c.Parse([]byte(cases[4].line))[0].Result
	if string(r.Structured) != `{"verdict":"land"}` || r.CostUSD != 0.1 {
		t.Fatalf("result: %+v", r)
	}
	rl := c.Parse([]byte(cases[3].line))[0].RateLimit
	if rl.Status != "rejected" || rl.ResetsAt.Unix() != 1793160000 || rl.Windows["five_hour"].Utilization != 1 {
		t.Fatalf("rate limit: %+v", rl)
	}
	lim := c.Parse([]byte(`{"type":"result","is_error":true,"result":"Claude AI usage limit reached|1793160000"}`))[0].Result
	if !lim.Limited {
		t.Fatal("usage limit not recognized")
	}
}

// A rate_limit_event as Claude Code 2.x sent it on 2026-10-24 (ids scrubbed):
// no top-level utilization, the number only under unifiedWindows.
const realRateLimitEvent = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1792820400,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.28,"resetsAt":1792820400,"status":"allowed"},"seven_day":{"utilization":0.36,"resetsAt":1792861200,"status":"allowed"}}},"uuid":"00000000-0000-0000-0000-000000000000","session_id":"00000000-0000-0000-0000-000000000000"}`

func TestParseRealRateLimitEvent(t *testing.T) {
	evs := Claude{}.Parse([]byte(realRateLimitEvent))
	if len(evs) != 1 || evs[0].RateLimit == nil {
		t.Fatalf("got %+v", evs)
	}
	rl := evs[0].RateLimit
	if rl.Status != "allowed" || rl.Window != "five_hour" || rl.Utilization != 0.28 {
		t.Fatalf("want allowed, five_hour at 0.28; got %+v", rl)
	}
	if !rl.ResetsAt.Equal(time.Date(2026, 10, 24, 5, 40, 0, 0, time.UTC)) {
		t.Fatalf("resets at %v, want 05:40Z", rl.ResetsAt)
	}
	if rl.Windows["seven_day"].Utilization != 0.36 {
		t.Fatalf("seven_day: %+v", rl.Windows)
	}
}

// The context figure is what one reply was sent, as Claude Code 2.1 prints it.
func TestParseContext(t *testing.T) {
	evs := Claude{}.Parse([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":12035,"cache_read_input_tokens":13792,"output_tokens":87}}}`))
	if len(evs) != 2 || evs[0].Kind != "context" || evs[0].Tokens != 25829 || evs[1].Kind != "text" {
		t.Fatalf("got %+v", evs)
	}
}

func TestClaudeArgs(t *testing.T) {
	a := Claude{}.Args(Turn{SessionID: "u", ReadOnly: true, RunDir: "/hive/run", Schema: "{}"})
	joined := ""
	for _, x := range a {
		joined += x + " "
	}
	for _, want := range []string{"--session-id u", "--tools Read,Grep,Glob --add-dir /hive/run --permission-mode dontAsk", "--json-schema {}"} {
		if !contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if b := (Claude{}).Args(Turn{SessionID: "u", Resume: true}); !contains(join(b), "--resume u") {
		t.Fatalf("resume args: %v", b)
	}
}

func join(a []string) string {
	s := ""
	for _, x := range a {
		s += x + " "
	}
	return s
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestCodex(t *testing.T) {
	c := Codex{}
	evs := c.Parse([]byte(`{"type":"thread.started","thread_id":"th1"}`))
	if len(evs) != 1 || evs[0].Kind != "init" || evs[0].SessionID != "th1" {
		t.Fatalf("init: %+v", evs)
	}
	if evs := c.Parse([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`)); len(evs) != 1 || evs[0].Text != "done" {
		t.Fatalf("message: %+v", evs)
	}
	if evs := c.Parse([]byte(`{"type":"item.completed","item":{"type":"command_execution","command":"go test"}}`)); len(evs) != 1 || evs[0].Kind != "tool" {
		t.Fatalf("command: %+v", evs)
	}
	if r := c.Parse([]byte(`{"type":"turn.completed","usage":{}}`))[0].Result; r == nil || r.IsError {
		t.Fatalf("completed: %+v", r)
	}
	if r := c.Parse([]byte(`{"type":"turn.failed","error":{"message":"You've hit your usage limit. Try again later."}}`))[0].Result; !r.IsError || !r.Limited {
		t.Fatalf("failed: %+v", r)
	}
	if r := c.Parse([]byte(`{"type":"error","message":"stream disconnected before completion"}`))[0].Result; !r.IsError || r.Text != "stream disconnected before completion" {
		t.Fatalf("error line: %+v", r)
	}
	if h := APIHosts(c); len(h) == 0 {
		t.Fatal("Codex names no API host for the egress list")
	}
	if e := c.Env(Turn{ConfigDir: "/hive/codex"}); len(e) != 1 || e[0] != "CODEX_HOME=/hive/codex" {
		t.Fatalf("env: %v", e)
	}
	ro := strings.Join(c.Args(Turn{ReadOnly: true, Resume: true, SessionID: "th1"}), " ")
	if !strings.Contains(ro, `exec resume`) || !strings.Contains(ro, `sandbox_mode="read-only"`) || !strings.HasSuffix(ro, "th1 -") {
		t.Fatalf("read-only args: %s", ro)
	}
	if a := strings.Join(c.Args(Turn{Model: "sonnet"}), " "); strings.Contains(a, "-m ") {
		t.Fatalf("a Claude model went to Codex: %s", a)
	}
	rw := strings.Join(c.Args(Turn{Model: "a-codex-model", SystemText: "be brief", Writable: []string{"/hive/box"}, SchemaFile: "/hive/run/schema.json"}), " ")
	if strings.Contains(rw, "be brief") {
		t.Fatalf("instructions went on the command line, which has a size cap: %s", rw)
	}
	for _, want := range []string{`sandbox_mode="workspace-write"`, `writable_roots=["/hive/box"]`, "--output-schema /hive/run/schema.json", "-m a-codex-model"} {
		if !strings.Contains(rw, want) {
			t.Fatalf("args %s lack %s", rw, want)
		}
	}
}

func TestClaudeMaxOutput(t *testing.T) {
	if e := join(Claude{}.Env(Turn{MaxOutput: 16000})); !contains(e, "CLAUDE_CODE_MAX_OUTPUT_TOKENS=16000") {
		t.Fatalf("env %q has no output cap", e)
	}
	if e := join(Claude{}.Env(Turn{})); contains(e, "MAX_OUTPUT") {
		t.Fatalf("env %q caps output with none set", e)
	}
}

type uncapped struct{ Harness } // a harness with no CapsSpend

func (uncapped) Name() string { return "uncapped" }

// A run with a budget starts only on a harness that holds a turn to it.
func TestCapped(t *testing.T) {
	if Capped(Claude{}, 5) != nil || Capped(Fake{}, 5) != nil || Capped(uncapped{}, 0) != nil {
		t.Fatal("a harness that caps spend, or a run with no budget, was refused")
	}
	if Capped(uncapped{}, 5) == nil {
		t.Fatal("a budgeted run started on a harness that can't cap its spend")
	}
}

// A turn on a model the open harness runs never counts a cost, though Claude
// Code driving another vendor's model prices its tokens as Claude's, and its
// budget flag is left off for the same reason.
func TestOpenClaude(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","result":"ok","total_cost_usd":0.5}`)
	for _, h := range []Harness{Open{}, OpenClaude{}} {
		if evs := h.Parse(line); len(evs) != 1 || evs[0].Result.CostUSD != 0 {
			t.Errorf("%T: %+v", h, evs)
		}
	}
	a := strings.Join(OpenClaude{}.Args(Turn{SessionID: "s", Model: "deepseek-chat", MaxBudgetUSD: 2, KeyCommand: "hive open-key /k"}), " ")
	if strings.Contains(a, "--max-budget-usd") || !strings.Contains(a, `--settings {"apiKeyHelper":"hive open-key /k"}`) || !strings.Contains(a, "--model deepseek-chat") {
		t.Errorf("args %s", a)
	}
	if APIHosts(OpenClaude{}) != nil {
		t.Error("a model on another vendor's endpoint reaches Anthropic's API")
	}
}
