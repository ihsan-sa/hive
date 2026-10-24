package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// freeModel is an OpenAI-compatible endpoint standing in for a free model: it
// answers a conversation with one shell call, then with its last word once
// the call's output is back.
type freeModel struct {
	command string
	mu      sync.Mutex
	auth    map[string]bool
	models  map[string]bool
}

func (f *freeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.auth[r.Header.Get("Authorization")] = true
	f.models[body.Model] = true
	f.mu.Unlock()
	msg := map[string]any{"role": "assistant", "content": "Done: free.txt is committed and I finished."}
	if n := len(body.Messages); n > 0 && body.Messages[n-1].Role != "tool" {
		args, _ := json.Marshal(map[string]string{"command": f.command})
		msg = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "c1", "type": "function",
			"function": map[string]any{"name": "bash", "arguments": string(args)}}}}
	}
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}}, "usage": map[string]int{"prompt_tokens": 900}})
}

// When a worker's harness hits its usage limit, the worker moves to the next
// model on its route, here a free model on an OpenAI-compatible endpoint
// with a stored key, through the open harness, and its work lands; nothing
// else moves, and the dashboard's state says which model each ran on.
func TestFallbackToAFreeModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the open harness's shell tool is bash; the sandboxed Windows path runs it in WSL, the bare one has none")
	}
	t.Parallel()
	free := &freeModel{auth: map[string]bool{}, models: map[string]bool{},
		command: `printf 'from a free model\n' > free.txt && git add free.txt && git commit -qm "free.txt" && hive agent finish --status done --account "free.txt from the free model" --tested "none"`}
	srv := httptest.NewServer(free)
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	h := startWith(t, bin, func(c *core.Config) {
		c.Models = map[string]core.OpenModel{"free": {Endpoint: srv.URL + "/v1", Model: "free-coder:7b", Key: true}}
		c.Projects = map[string]core.ProjectSettings{"lab": {Routes: map[string][]string{"worker": {"fake:opus", "free"}}}}
		c.Egress.Local = append(c.Egress.Local, u.Host) // what `hive egress grant` opens for a model server here
	})
	if err := h.ws.SaveOpenKey("free", "sk-free"); err != nil {
		t.Fatal(err)
	}
	h.seed("me/lab", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "lab", "repo": "me/lab"}, nil)
	h.post("/api/ask", map[string]string{"project": "lab", "title": "work through a limit", "known": "FAKE:limit"}, nil)
	st := h.wait("the limited worker to move to the free model and land", 3*minute, taskStage("lab", "work through a limit", work.StageDone))
	h.wait("free.txt on main", minute, h.onMain("me/lab", "free.txt", "from a free model"))
	tk, _ := task(st, "lab", "work through a limit")
	var worker *core.Run
	for i, r := range st.Runs {
		switch {
		case r.Task == tk.ID && r.Role == "worker":
			worker = &st.Runs[i]
		case r.Harness != "fake":
			t.Errorf("%s (%s) left its model for %s:%s; only the project's workers route there", r.ID, r.Role, r.Harness, r.Model)
		}
	}
	if worker == nil || worker.Harness != "open" || worker.Model != "free" || worker.Login != "open:free" {
		t.Fatalf("the worker: %+v", worker)
	}
	free.mu.Lock()
	defer free.mu.Unlock()
	if !free.auth["Bearer sk-free"] || !free.models["free-coder:7b"] || len(free.auth) != 1 {
		t.Fatalf("the endpoint saw auth %v, models %v", free.auth, free.models)
	}
}

// A live probe: a worker turn on a model on this machine's Ollama (say
// qwen2.5-coder, or a Kimi, DeepSeek or GLM build), which lands on a test
// project. It runs only when asked, as the other live probes do:
// HIVE_E2E_OPEN_MODEL=<model> (or "any" for the first Ollama lists), and
// Ollama answering on 127.0.0.1:11434.
func TestLiveLocalModelWorker(t *testing.T) {
	model := os.Getenv("HIVE_E2E_OPEN_MODEL")
	if model == "" {
		t.Skip("set HIVE_E2E_OPEN_MODEL=<an Ollama model> (or any) to run a worker on a local model")
	}
	if model == "any" {
		model = ""
	}
	resp, err := http.Get("http://127.0.0.1:11434/api/tags")
	if err != nil {
		t.Skip("no Ollama on 127.0.0.1:11434: " + err.Error())
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	json.NewDecoder(resp.Body).Decode(&tags)
	resp.Body.Close()
	if model == "" && len(tags.Models) > 0 {
		model = tags.Models[0].Name
	}
	if model == "" {
		t.Skip("Ollama has no model; `ollama pull qwen2.5-coder:7b`")
	}
	h := startWith(t, bin, func(c *core.Config) {
		c.Models = map[string]core.OpenModel{"local": {Endpoint: "http://127.0.0.1:11434/v1", Model: model, Context: 32000}}
		c.Projects = map[string]core.ProjectSettings{"lab": {Routes: map[string][]string{"worker": {"local"}}}}
		c.Egress.Local = append(c.Egress.Local, "127.0.0.1:11434")
	})
	h.seed("me/lab", map[string]string{".hive/project.json": `{"test": "test -f hello.txt"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "lab", "repo": "me/lab"}, nil)
	h.post("/api/ask", map[string]string{"project": "lab", "title": "Add hello.txt",
		"done": "hello.txt at the repository's root says hello; it is committed, and you finished with `hive agent finish --status done`"}, nil)
	st := h.wait(fmt.Sprintf("a worker on %s to land", model), 45*minute, func(st api.State) bool {
		t, ok := task(st, "lab", "Add hello.txt")
		return ok && t.Terminal()
	})
	tk, _ := task(st, "lab", "Add hello.txt")
	for _, r := range st.Runs {
		if r.Task == tk.ID && r.Role == "worker" && r.Harness != "open" {
			t.Errorf("the worker ran on %s:%s", r.Harness, r.Model)
		}
	}
	if tk.Stage != work.StageDone {
		t.Fatalf("the task ended %s: %s\n%s", tk.Stage, tk.Reason, logLines(tk.Log))
	}
	if !h.onMain("me/lab", "hello.txt", "")(st) {
		t.Fatal("hello.txt isn't on main")
	}
}

// anthropicModel is an Anthropic-compatible endpoint (as Kimi's, DeepSeek's
// and GLM's are) standing in for a vendor's model: the main conversation
// gets one Bash call, then its last word once the call's output is back;
// Claude Code's side requests get a short text.
type anthropicModel struct {
	command string
	mu      sync.Mutex
	keys    map[string]bool
	models  map[string]bool
	outputs []string
}

func (f *anthropicModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string            `json:"model"`
		Stream   bool              `json:"stream"`
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.keys[r.Header.Get("X-Api-Key")] = true
	f.models[body.Model] = true
	answered := false
	for _, m := range body.Messages {
		var blocks []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(m.Content, &blocks)
		for _, b := range blocks {
			if b.Type == "tool_result" {
				answered = true
				f.outputs = append(f.outputs, string(b.Content))
			}
		}
	}
	f.mu.Unlock()
	content := []any{map[string]any{"type": "text", "text": "Done: anthropic.txt is committed and I finished."}}
	stop := "end_turn"
	if len(body.Tools) > 0 && !answered {
		content = []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]string{"command": f.command, "description": "commit and finish"}}}
		stop = "tool_use"
	}
	msg := map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": body.Model, "content": content,
		"stop_reason": stop, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 900, "output_tokens": 20}}
	if !body.Stream {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msg)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ev := func(kind string, data any) {
		raw, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
	}
	start := map[string]any{}
	for k, v := range msg {
		start[k] = v
	}
	start["content"], start["stop_reason"] = []any{}, nil
	ev("message_start", map[string]any{"type": "message_start", "message": start})
	for i, c := range content {
		c := c.(map[string]any)
		if c["type"] == "text" {
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": c["text"]}})
		} else {
			in, _ := json.Marshal(c["input"])
			ev("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": c["id"], "name": c["name"], "input": map[string]any{}}})
			ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(in)}})
		}
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 20}})
	ev("message_stop", map[string]any{"type": "message_stop"})
}

// A worker routed to a named model whose API is Anthropic-compatible runs
// on Claude Code itself, pointed at the endpoint: the endpoint sees the
// model's key and its own model id on every request, its tools find the key
// neither in their environment nor in the file it came in, and the work
// lands. It needs Claude Code installed (no Claude login: none is used).
//
// With HIVE_E2E_ANTHROPIC="<endpoint> <model id>" and the key in
// HIVE_E2E_ANTHROPIC_KEY, it is a live probe instead: a real worker on that
// vendor's model (say https://api.moonshot.ai/anthropic kimi-k2-0905-preview).
func TestAnthropicModelWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bare Windows path has no shell for the fake model's command")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude Code isn't installed")
	}
	live := strings.Fields(os.Getenv("HIVE_E2E_ANTHROPIC"))
	if len(live) != 0 && (len(live) != 2 || os.Getenv("HIVE_E2E_ANTHROPIC_KEY") == "") {
		t.Fatal(`HIVE_E2E_ANTHROPIC="<endpoint> <model id>", with the key in HIVE_E2E_ANTHROPIC_KEY`)
	}
	t.Parallel()
	fake := &anthropicModel{keys: map[string]bool{}, models: map[string]bool{},
		command: `env | grep -q sk-anthropic-e2e && echo LEAK-ENV; cat "$HIVE_STATE/private/runs/$HIVE_ID/home/.hive-open/key" 2>/dev/null && echo LEAK-FILE; ` +
			`printf 'from a vendor model\n' > anthropic.txt && git add anthropic.txt && git commit -qm "anthropic.txt" && hive agent finish --status done --account "anthropic.txt from the vendor model" --tested "none" && echo landed`}
	endpoint, id, key := "", "vendor-coder-1", "sk-anthropic-e2e"
	if len(live) == 2 {
		endpoint, id, key = live[0], live[1], os.Getenv("HIVE_E2E_ANTHROPIC_KEY")
	} else {
		srv := httptest.NewServer(fake)
		defer srv.Close()
		endpoint = srv.URL
	}
	u, _ := url.Parse(endpoint)
	// Claude Code keeps its sessions under HOME on a bare run; this one's
	// is the test's, with no Claude login in it. Claude Code would send even
	// a loopback request through a proxy the test's environment names.
	env := []string{"HOME=" + t.TempDir(), "PATH=" + filepath.Dir(claude) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"ANTHROPIC_API_KEY=sk-not-the-model", "CLAUDE_CODE_OAUTH_TOKEN=not-the-model"}
	if len(live) == 0 {
		env = append(env, "NO_PROXY="+u.Hostname(), "no_proxy="+u.Hostname())
	}
	h := startWith(t, bin, func(c *core.Config) {
		c.Models = map[string]core.OpenModel{"vendor": {Endpoint: endpoint, Model: id, API: "anthropic", Key: true}}
		c.Projects = map[string]core.ProjectSettings{"lab": {Routes: map[string][]string{"worker": {"vendor"}}}}
		c.Egress.Local = append(c.Egress.Local, u.Host)
	}, env...)
	if err := h.ws.SaveOpenKey("vendor", key); err != nil {
		t.Fatal(err)
	}
	h.seed("me/lab", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "lab", "repo": "me/lab"}, nil)
	title := "Add anthropic.txt"
	h.post("/api/ask", map[string]string{"project": "lab", "title": title,
		"done": "anthropic.txt at the repository's root says 'from a vendor model'; it is committed, and you finished with `hive agent finish --status done`"}, nil)
	wait := 3 * minute
	if len(live) == 2 {
		wait = 45 * minute
	}
	st := h.wait("the worker on the vendor model to land", wait, func(st api.State) bool {
		t, ok := task(st, "lab", title)
		return ok && t.Terminal()
	})
	tk, _ := task(st, "lab", title)
	if tk.Stage != work.StageDone {
		t.Fatalf("the task ended %s: %s\n%s", tk.Stage, tk.Reason, logLines(tk.Log))
	}
	for _, r := range st.Runs {
		if r.Task == tk.ID && r.Role == "worker" {
			if r.Harness != "open" || r.Model != "vendor" || r.Login != "open:vendor" {
				t.Errorf("the worker ran on %s:%s (login %s)", r.Harness, r.Model, r.Login)
			}
			if core.Exists(filepath.Join(h.ws.RunHome(r.ID), ".hive-open", "key")) {
				t.Error("the key file outlived the turn")
			}
		}
	}
	if len(live) == 2 {
		return
	}
	h.wait("anthropic.txt on main", minute, h.onMain("me/lab", "anthropic.txt", "from a vendor model"))
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.keys[key] || len(fake.keys) != 1 || !fake.models[id] || len(fake.models) != 1 {
		t.Fatalf("the endpoint saw keys %v, models %v", fake.keys, fake.models)
	}
	out := strings.Join(fake.outputs, "\n")
	if strings.Contains(out, "LEAK") || !strings.Contains(out, "landed") {
		t.Fatalf("the tool's output: %s", out)
	}
}
