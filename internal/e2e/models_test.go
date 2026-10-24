package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
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

// A live probe: a worker turn on a model on this machine's Ollama, which
// lands on a test project. It runs where Ollama answers on 127.0.0.1:11434
// with a model (HIVE_E2E_OPEN_MODEL picks one, else the first it lists).
func TestLiveLocalModelWorker(t *testing.T) {
	model := os.Getenv("HIVE_E2E_OPEN_MODEL")
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
