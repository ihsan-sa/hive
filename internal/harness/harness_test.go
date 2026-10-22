package harness

import "testing"

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

func TestClaudeMaxOutput(t *testing.T) {
	if e := join(Claude{}.Env(Turn{MaxOutput: 16000})); !contains(e, "CLAUDE_CODE_MAX_OUTPUT_TOKENS=16000") {
		t.Fatalf("env %q has no output cap", e)
	}
	if e := join(Claude{}.Env(Turn{})); contains(e, "MAX_OUTPUT") {
		t.Fatalf("env %q caps output with none set", e)
	}
}
