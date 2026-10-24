package harness

import (
	"slices"
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
	// Codex prints an error line for each retry; only turn.failed ends the turn.
	if evs := c.Parse([]byte(`{"type":"error","message":"Reconnecting... 2/5 (stream disconnected before completion)"}`)); len(evs) != 0 {
		t.Fatalf("a retry ended the turn: %+v", evs)
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
	// Codex's own sandbox can't start inside the Hive's, which is the boundary there.
	for _, ro := range []bool{false, true} {
		in := strings.Join(c.Args(Turn{Sandboxed: true, ReadOnly: ro, Writable: []string{"/hive/box"}}), " ")
		if !strings.Contains(in, "--dangerously-bypass-approvals-and-sandbox") || strings.Contains(in, "sandbox_mode") {
			t.Fatalf("sandboxed args: %s", in)
		}
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
	args := OpenClaude{}.Args(Turn{SessionID: "s", Model: "deepseek-chat", MaxBudgetUSD: 2, KeyCommand: "hive open-key /k", Endpoint: "https://api.deepseek.com/anthropic/"})
	a := strings.Join(args, " ")
	if strings.Contains(a, "--max-budget-usd") || !strings.Contains(a, "--model deepseek-chat") ||
		!strings.Contains(a, `--settings {"apiKeyHelper":"hive open-key /k","disableAllHooks":true,"env":{"ANTHROPIC_BASE_URL":"https://api.deepseek.com/anthropic"}}`) {
		t.Errorf("args %s", a)
	}
	// No settings file loads, so a hook or env in the checkout's
	// .claude/settings.json can't run before the helper or redirect the key.
	if i := slices.Index(args, "--setting-sources"); i < 0 || args[i+1] != "" || slices.Contains(args, "project") {
		t.Errorf("setting sources %q", args)
	}
	if APIHosts(OpenClaude{}) != nil {
		t.Error("a model on another vendor's endpoint reaches Anthropic's API")
	}
	// Its config folder is the run's own, so no Claude login is found.
	if env := (OpenClaude{}).Env(Turn{ConfigDir: "/hive/home/.claude-open"}); !slices.Contains(env, "CLAUDE_CONFIG_DIR=/hive/home/.claude-open") {
		t.Errorf("env %q", env)
	}
	// A vendor's 429, as Claude Code 2.1 reports it, is a usage limit.
	limited := []byte(`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"API Error: Request rejected (429) · Your account org-x request reached organization max RPM: 3"}`)
	if evs := (OpenClaude{}).Parse(limited); len(evs) != 1 || !evs[0].Result.Limited {
		t.Errorf("a vendor's 429: %+v", evs)
	}
	refused := []byte(`{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"invalid x-api-key"}`)
	if evs := (OpenClaude{}).Parse(refused); len(evs) != 1 || evs[0].Result.Limited {
		t.Errorf("a 401 is no limit: %+v", evs)
	}
}

// Codex's usage readings come from its session file: the last one since the
// turn began, its windows under Claude's names, the fullest binding.
func TestCodexUsage(t *testing.T) {
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	rollout := []byte(`{"timestamp":"2026-10-24T09:59:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":100,"window_minutes":300,"resets_at":1}}}}
not json
{"timestamp":"2026-10-24T10:01:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1234}},"rate_limits":{"primary":{"used_percent":30,"window_minutes":300,"resets_at":1793000000},"secondary":{"used_percent":85,"window_minutes":10080,"resets_at":1793500000}}}}
{"timestamp":"2026-10-24T10:02:00Z","type":"response_item","payload":{"type":"message"}}
`)
	rl, n := Codex{}.Usage(rollout, start)
	if n != 1234 || rl == nil {
		t.Fatalf("usage %+v, context %d", rl, n)
	}
	if rl.Status != "allowed_warning" || rl.Window != "seven_day" || rl.Utilization != 0.85 || rl.ResetsAt.Unix() != 1793500000 || rl.Windows["five_hour"].Utilization != 0.3 {
		t.Fatalf("usage %+v", rl)
	}
	if rl, _ := (Codex{}).Usage(rollout, start.Add(time.Hour)); rl != nil {
		t.Fatalf("a reading from before the turn: %+v", rl)
	}
	full := []byte(`{"timestamp":"2026-10-24T10:01:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":100,"window_minutes":300,"resets_at":1793000000},"secondary":{"used_percent":20,"window_minutes":10080,"resets_at":1793500000}}}}`)
	if rl, _ := (Codex{}).Usage(full, start); rl == nil || rl.Status != "rejected" || rl.Window != "five_hour" {
		t.Fatalf("at the limit: %+v", rl)
	}
}

// A turn's result carries its token use, for the dashboard's totals.
func TestResultTokens(t *testing.T) {
	r := Claude{}.Parse([]byte(`{"type":"result","subtype":"success","result":"ok","total_cost_usd":0.5,"usage":{"input_tokens":12,"output_tokens":340,"cache_creation_input_tokens":1000,"cache_read_input_tokens":20000}}`))[0].Result
	if r.Tokens == nil || *r.Tokens != (Tokens{Input: 12, Output: 340, CacheRead: 20000, CacheWrite: 1000}) {
		t.Errorf("claude: %+v", r.Tokens)
	}
	r = Codex{}.Parse([]byte(`{"type":"turn.completed","usage":{"input_tokens":5000,"cached_input_tokens":4000,"output_tokens":70}}`))[0].Result
	if r.Tokens == nil || *r.Tokens != (Tokens{Input: 1000, Output: 70, CacheRead: 4000}) {
		t.Errorf("codex: %+v", r.Tokens)
	}
}
