package openagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// stub is an OpenAI-compatible endpoint whose replies a test scripts: each
// request gets the next reply, and the requests are kept.
type stub struct {
	replies []func(w http.ResponseWriter)
	got     []map[string]any
	auth    []string
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	s.got = append(s.got, body)
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	if r.URL.Path != "/v1/chat/completions" || len(s.replies) == 0 {
		http.Error(w, `{"error": {"message": "no reply scripted"}}`, 500)
		return
	}
	next := s.replies[0]
	s.replies = s.replies[1:]
	next(w)
}

func call(name string, args any) func(w http.ResponseWriter) {
	raw, _ := json.Marshal(args)
	return func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant",
			"tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}}},
			"usage": map[string]any{"prompt_tokens": 1234}})
	}
}

func say(text string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}}}})
	}
}

func agent(t *testing.T, s *stub) (*Agent, *bytes.Buffer) {
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	out := &bytes.Buffer{}
	dir := t.TempDir()
	return &Agent{Endpoint: srv.URL + "/v1", Model: "qwen", Key: "sk-test", Cwd: dir, Dirs: []string{dir}, MaxSteps: 10, Out: out,
		Client: srv.Client(), Sleep: func(time.Duration) {}}, out
}

// events reads what the agent wrote the way the run manager does.
func events(out *bytes.Buffer) []harness.Event {
	var evs []harness.Event
	sc := bufio.NewScanner(bytes.NewReader(out.Bytes()))
	for sc.Scan() {
		evs = append(evs, harness.Open{}.Parse(sc.Bytes())...)
	}
	return evs
}

func result(t *testing.T, a *Agent, res map[string]any) *harness.Result {
	t.Helper()
	a.emit(res)
	for _, e := range events(a.Out.(*bytes.Buffer)) {
		if e.Kind == "result" {
			return e.Result
		}
	}
	t.Fatal("no result")
	return nil
}

// A worker turn: the model edits through the tools and runs a command, and
// the turn ends on its answer, all in Claude's stream-json.
func TestWorkerTurn(t *testing.T) {
	s := &stub{replies: []func(http.ResponseWriter){
		call("write_file", map[string]string{"path": "hello.txt", "content": "hi there\n"}),
		call("edit_file", map[string]string{"path": "hello.txt", "old": "there", "new": "hive"}),
		call("bash", map[string]any{"command": "cat hello.txt; echo $HIVE_OPEN_KEY-gone"}),
		say("Wrote hello.txt."),
	}}
	a, out := agent(t, s)
	var saved []Message
	res := result(t, a, a.Turn(context.Background(), []Message{{Role: "system", Content: a.preamble()}, {Role: "user", Content: "say hi"}}, func(m []Message) { saved = m }))
	if res.IsError || res.Text != "Wrote hello.txt." {
		t.Fatalf("result %+v\n%s", res, out)
	}
	if raw, _ := os.ReadFile(filepath.Join(a.Cwd, "hello.txt")); string(raw) != "hi hive\n" {
		t.Fatalf("hello.txt: %q", raw)
	}
	last := saved[len(saved)-2] // the bash call's output, before the answer
	if last.Role != "tool" || !strings.Contains(last.Content, "hi hive") || !strings.Contains(last.Content, "[exit 0]") {
		t.Fatalf("the bash output the model read: %+v", last)
	}
	tools, ctx := 0, 0
	for _, e := range events(out) {
		if e.Kind == "tool" {
			tools++
		}
		if e.Kind == "context" && ctx == 0 {
			ctx = e.Tokens
		}
	}
	if ctx != 1234 {
		t.Errorf("context %d, want the endpoint's prompt_tokens", ctx)
	}
	if tools != 3 || s.auth[0] != "Bearer sk-test" {
		t.Fatalf("%d tool events, auth %q", tools, s.auth[0])
	}
	if s.got[0]["model"] != "qwen" || len(s.got[0]["tools"].([]any)) != 4 {
		t.Fatalf("request: %v", s.got[0])
	}
}

// A model that writes its tool call as text, as small local models do, is
// read as the call.
func TestToolCallAsText(t *testing.T) {
	s := &stub{replies: []func(http.ResponseWriter){
		say("```json\n{\"name\": \"bash\", \"arguments\": {\"command\": \"echo ok > f\"}}\n```"),
		say("done"),
	}}
	a, _ := agent(t, s)
	if res := result(t, a, a.Turn(context.Background(), []Message{{Role: "user", Content: "go"}}, func([]Message) {})); res.Text != "done" {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(a.Cwd, "f")); err != nil {
		t.Fatal("the call written as text didn't run")
	}
}

// A long rate limit ends the turn as a usage limit with its reset, which
// the run manager parks or moves on; a short one is waited out.
func TestRateLimit(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	s := &stub{replies: []func(http.ResponseWriter){
		func(w http.ResponseWriter) { w.Header().Set("Retry-After", "20"); w.WriteHeader(429) },
		func(w http.ResponseWriter) {
			w.Header().Set("X-RateLimit-Reset", strings.TrimSpace(func() string { b, _ := json.Marshal(reset.UnixMilli()); return string(b) }()))
			w.WriteHeader(429)
			io.WriteString(w, `{"error": {"message": "Rate limit exceeded: free-models-per-day"}}`)
		},
	}}
	a, out := agent(t, s)
	slept := time.Duration(0)
	a.Sleep = func(d time.Duration) { slept += d }
	res := result(t, a, a.Turn(context.Background(), []Message{{Role: "user", Content: "go"}}, func([]Message) {}))
	if !res.IsError || !res.Limited || res.APIStatus != 429 {
		t.Fatalf("result %+v", res)
	}
	if slept < 19*time.Second || slept > 21*time.Second {
		t.Fatalf("the short limit wasn't waited out: slept %v", slept)
	}
	var rl *harness.RateLimit
	for _, e := range events(out) {
		if e.Kind == "ratelimit" {
			rl = e.RateLimit
		}
	}
	if rl == nil || rl.Status != "rejected" || !rl.ResetsAt.Equal(reset) {
		t.Fatalf("rate limit %+v, want rejected until %v", rl, reset)
	}
}

// A read-only turn reads its folders and nothing else, has no shell, and
// gives its structured answer through the answer tool.
func TestReadOnlyAnswer(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s3cret"), 0o644)
	s := &stub{replies: []func(http.ResponseWriter){
		call("read_file", map[string]string{"path": filepath.Join(outside, "secret")}),
		call("bash", map[string]string{"command": "touch made"}),
		call("grep", map[string]string{"pattern": "land"}),
		call("answer", map[string]string{"verdict": "land", "findings": ""}),
	}}
	a, _ := agent(t, s)
	a.ReadOnly, a.Schema = true, json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"},"findings":{"type":"string"}}}`)
	os.WriteFile(filepath.Join(a.Cwd, "notes.md"), []byte("we land this\n"), 0o644)
	var saved []Message
	res := result(t, a, a.Turn(context.Background(), []Message{{Role: "user", Content: "review"}}, func(m []Message) { saved = m }))
	if string(res.Structured) != `{"findings":"","verdict":"land"}` {
		t.Fatalf("structured %s", res.Structured)
	}
	var tools []string
	for _, m := range saved {
		if m.Role == "tool" {
			tools = append(tools, m.Content)
		}
	}
	if len(tools) != 3 || strings.Contains(tools[0], "s3cret") || !strings.Contains(tools[1], "no tool bash") || !strings.Contains(tools[2], "notes.md:1: we land this") {
		t.Fatalf("tool results: %q", tools)
	}
	if _, err := os.Stat(filepath.Join(a.Cwd, "made")); err == nil {
		t.Fatal("a read-only turn ran a shell command")
	}
	for _, tool := range s.got[0]["tools"].([]any) {
		if name := tool.(map[string]any)["function"].(map[string]any)["name"]; name == "bash" || name == "write_file" {
			t.Fatalf("a read-only turn was offered %s", name)
		}
	}
}

// The conversation is trimmed to the model's context: old tool output goes
// first, and the system prompt, the ask and the latest steps stay whole.
func TestFit(t *testing.T) {
	a := &Agent{Context: 4000}
	big := strings.Repeat("x", 6000)
	msgs := []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "ask"}}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, Message{Role: "assistant", Content: "step"}, Message{Role: "tool", Content: big})
	}
	msgs = a.fit(msgs)
	if size(msgs) > 4000*4 || msgs[0].Content != "sys" || msgs[1].Content != "ask" || msgs[len(msgs)-1].Content != big {
		t.Fatalf("fit left %d chars; first %q %q, last kept %v", size(msgs), msgs[0].Content, msgs[1].Content, msgs[len(msgs)-1].Content == big)
	}
}

// `hive open-agent` as the run manager starts it: settings from the
// environment, which it clears, and a second turn resumes the first one's
// conversation from the run's home.
func TestMainResumes(t *testing.T) {
	s := &stub{replies: []func(http.ResponseWriter){say("first answer"), say("second answer")}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	sys := filepath.Join(t.TempDir(), "system.md")
	os.WriteFile(sys, []byte("THE HIVE'S PROMPT"), 0o644)
	turn := func(prompt string, args ...string) string {
		t.Setenv("HIVE_OPEN_ENDPOINT", srv.URL+"/v1")
		t.Setenv("HIVE_OPEN_MODEL", "qwen")
		t.Setenv("HIVE_OPEN_KEY", "sk-main")
		keyFile := filepath.Join(t.TempDir(), "key")
		if strings.Contains(prompt, "message") { // the second turn gets its key as the run manager hands it over
			t.Setenv("HIVE_OPEN_KEY", "")
			os.WriteFile(keyFile, []byte("sk-file"), 0o600)
			t.Setenv("HIVE_OPEN_KEY_FILE", keyFile)
		}
		in, out := filepath.Join(t.TempDir(), "in"), filepath.Join(t.TempDir(), "out")
		os.WriteFile(in, []byte(prompt), 0o644)
		stdin, _ := os.Open(in)
		stdout, _ := os.Create(out)
		oldIn, oldOut := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = stdin, stdout
		code := Main(append([]string{"--session", "s1", "--model", "local", "--system-file", sys}, args...))
		os.Stdin, os.Stdout = oldIn, oldOut
		stdin.Close()
		stdout.Close()
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		if os.Getenv("HIVE_OPEN_KEY") != "" || os.Getenv("HIVE_OPEN_KEY_FILE") != "" {
			t.Fatal("the key is still in the environment tools inherit")
		}
		if _, err := os.Stat(keyFile); err == nil {
			t.Fatal("the key file is still there for a tool to read")
		}
		raw, _ := os.ReadFile(out)
		return string(raw)
	}
	if out := turn("the brief"); !strings.Contains(out, `"subtype":"init"`) || !strings.Contains(out, "first answer") {
		t.Fatalf("first turn: %s", out)
	}
	turn("a message", "--resume")
	msgs := s.got[1]["messages"].([]any)
	var roles, texts []string
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
		texts = append(texts, m.(map[string]any)["content"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,user" || !strings.Contains(texts[0], "THE HIVE'S PROMPT") || texts[1] != "the brief" || texts[3] != "a message" {
		t.Fatalf("the resumed turn sent %q %q", roles, texts)
	}
	if s.auth[0] != "Bearer sk-main" || s.auth[1] != "Bearer sk-file" {
		t.Fatalf("auth %q", s.auth)
	}
}

// `hive open-key` prints the key once and leaves no file; a second call
// finds nothing.
func TestKeyMainOnce(t *testing.T) {
	f := filepath.Join(t.TempDir(), "key")
	os.WriteFile(f, []byte("sk-once\n"), 0o600)
	if code := KeyMain([]string{f}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("the key file outlived the helper")
	}
	if code := KeyMain([]string{f}); code == 0 {
		t.Fatal("a second call handed out a key")
	}
}

// costly is a reply calling a tool, with what OpenRouter says it cost.
func costly(usd float64) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant",
			"tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "bash", "arguments": `{"command":"true"}`}}}}}},
			"usage": map[string]any{"prompt_tokens": 10, "cost": usd}})
	}
}

// A turn's result carries the session's cost so far, from what the endpoint
// reported, and a turn with a budget stops once it has spent it.
func TestCostAndBudget(t *testing.T) {
	s := &stub{replies: []func(http.ResponseWriter){costly(0.01), costly(0.02), say("done")}}
	a, _ := agent(t, s)
	a.Spent = 0.5 // earlier turns of the session
	res := result(t, a, a.Turn(context.Background(), []Message{{Role: "user", Content: "go"}}, func([]Message) {}))
	if res.IsError || res.CostUSD < 0.5299 || res.CostUSD > 0.5301 {
		t.Fatalf("cost %v, want the session's running total 0.53: %+v", res.CostUSD, res)
	}
	if _, ok := s.got[0]["usage"]; ok {
		t.Error("asked a server that isn't OpenRouter for its usage accounting")
	}
	s = &stub{replies: []func(http.ResponseWriter){costly(0.01), costly(0.02), costly(0.04), say("done")}}
	a, _ = agent(t, s)
	a.Budget = 0.025
	res = result(t, a, a.Turn(context.Background(), []Message{{Role: "user", Content: "go"}}, func([]Message) {}))
	if len(s.got) != 2 || !strings.Contains(res.Text, "stopped: this turn spent $0.0300") {
		t.Fatalf("a budget of $0.025 stops after the request that spends it: %d requests, %q", len(s.got), res.Text)
	}
	for ep, want := range map[string]bool{"https://openrouter.ai/api/v1": true, "https://eu.openrouter.ai/api/v1": true,
		"https://openrouter.ai.evil.example/v1": false, "http://127.0.0.1:11434/v1": false} {
		if openRouter(ep) != want {
			t.Errorf("openRouter(%s) != %v", ep, want)
		}
	}
}
