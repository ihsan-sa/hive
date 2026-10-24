package core

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

func picks(ps []Pick) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return strings.Join(out, " ")
}

// A role runs on its route, a project's own first; with none, on the model
// settings; the fallback list follows for every role but a check seat.
func TestRoute(t *testing.T) {
	c := DefaultConfig()
	c.Models = map[string]OpenModel{
		"qwen":   {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen2.5-coder:14b"},
		"or-gpt": {Endpoint: "https://openrouter.ai/api/v1", Model: "openai/gpt-oss-120b:free", Key: true},
	}
	c.Routes = map[string][]string{"worker": {"sonnet", "codex:gpt-5"}, "fallback": {"qwen", "or-gpt"}}
	c.Projects = map[string]ProjectSettings{"site": {Routes: map[string][]string{"worker": {"or-gpt"}}}, "app": {Model: "qwen"}}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ role, project, want string }{
		{"worker", "", "claude:sonnet codex:gpt-5 open:qwen open:or-gpt"},
		{"worker", "site", "open:or-gpt open:qwen"},
		{"lead", "", "claude:opus open:qwen open:or-gpt"},
		{"lead", "app", "open:qwen open:or-gpt"}, // a named model as a project's model setting
		{"reviewer", "", "claude:opus"},          // a check seat never takes the fallback list
		{"warden", "", "claude:opus"},
	} {
		if got := picks(c.Route(x.role, x.project)); got != x.want {
			t.Errorf("%s in %q: %s, want %s", x.role, x.project, got, x.want)
		}
	}
	c.Routes["reviewer"] = []string{"opus", "qwen"} // the person routes a check themselves
	if got := picks(c.Route("reviewer", "")); got != "claude:opus open:qwen" {
		t.Errorf("a routed reviewer: %s", got)
	}
	c.Routes["tribunal"] = []string{"opus", "qwen", "codex:gpt-5"}
	seats := c.Seats("")
	if len(seats) != 3 || seats[1] != (TribunalSeat{Harness: "open", Model: "qwen"}) {
		t.Errorf("tribunal route as seats: %+v", seats)
	}
	delete(c.Routes, "tribunal")
	c.TribunalModels = []TribunalSeat{{Model: "opus"}, {Model: "qwen"}}
	if seats := c.Seats(""); seats[1].Harness != "open" || seats[0].Harness != "" {
		t.Errorf("a tribunal seat on a named model sits on the open harness: %+v", seats)
	}
	for _, bad := range []func(*Config){
		func(c *Config) { c.Routes["worker"] = []string{"open:nosuch"} },
		func(c *Config) { c.Routes["janitor"] = []string{"opus"} },
		func(c *Config) { c.Models["x"] = OpenModel{Endpoint: "file:///etc/passwd", Model: "m"} },
		func(c *Config) { c.Models["a:b"] = OpenModel{Endpoint: "http://h/v1", Model: "m"} },
		func(c *Config) { c.Harness, c.Model = "open", "nosuch" },
		func(c *Config) {
			c.Projects["site"] = ProjectSettings{Routes: map[string][]string{"worker": {"open:gone"}}}
		},
	} {
		cc := c
		cc.Routes, cc.Models, cc.Projects = clone(c.Routes), cloneM(c.Models), map[string]ProjectSettings{}
		bad(&cc)
		if cc.Check() == nil {
			t.Errorf("Check took %+v %+v %+v", cc.Routes, cc.Models, cc.Projects)
		}
	}
}

func clone(m map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range m {
		out[k] = slices.Clone(v)
	}
	return out
}

func cloneM(m map[string]OpenModel) map[string]OpenModel {
	out := map[string]OpenModel{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// A named model's turn gets its endpoint and stored key, and its run may
// reach the endpoint's host; a key it needs and doesn't have stops it with
// what the person runs; one on this machine adds no public host.
func TestOpenModelTurn(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{
		"free":  {Endpoint: "https://openrouter.ai/api/v1", Model: "meta/llama:free", Key: true},
		"local": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"},
	}
	cfg.Egress.Default = []string{"github.com"}
	if err := c.WS.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	tok := NewTokens(c.WS)
	if _, err := tok.AgentEnv(Login{Harness: "open", Name: "free"}, ""); err == nil || !strings.Contains(err.Error(), "model key free") {
		t.Fatalf("a model with no stored key: %v", err)
	}
	if err := c.WS.SaveOpenKey("free", "sk-or-test\n"); err != nil {
		t.Fatal(err)
	}
	env, err := tok.AgentEnv(Login{Harness: "open", Name: "free"}, "")
	if err != nil || !slices.Contains(env, "HIVE_OPEN_KEY=sk-or-test") || !slices.Contains(env, "HIVE_OPEN_MODEL=meta/llama:free") {
		t.Fatalf("env %q, %v", env, err)
	}
	if fi, err := os.Stat(c.WS.OpenKey("free")); err != nil || fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("the key file is readable by others: %v", err)
	}
	h, _ := harness.Get("open")
	for model, host := range map[string]string{"free": "openrouter.ai", "local": ""} {
		r := Run{RunSpec: RunSpec{ID: "o-" + model, Role: "worker", Harness: "open", Model: model}}
		spec, err := c.Runs.sandboxSpec(cfg, isolatedSB{}, h, r, 1, false, "p", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if host != "" && !slices.Contains(spec.Allow, host) || host == "" && len(spec.Allow) != 1 {
			t.Errorf("%s: egress %q", model, spec.Allow)
		}
		if !strings.Contains(strings.Join(spec.Argv, " "), "open-agent --session") {
			t.Errorf("argv %q", spec.Argv)
		}
	}
}

// At a usage limit a run moves to the next model on its route that has
// room, as a fresh session told why, and the turn says which model it ran on;
// with no route it waits for the reset.
func TestFallbackAtALimit(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"free": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"}}
	c.WS.SaveConfig(cfg)
	var args []string
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return startSB{args: &args}, nil }
	limit := func(key string) {
		c.Runs.append("plan.usage", Plan{Harness: "fake", Login: key, Limit: harness.RateLimit{Status: "rejected", ResetsAt: time.Now().Add(time.Hour)}})
	}
	r, err := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "fake", Model: "opus", Brief: "the brief", Checkout: t.TempDir(),
		Route: []Pick{{Harness: "fake", Model: "opus"}, {Harness: "open", Model: "free"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, c.WS.LoadConfig())
	if got, _ := c.Runs.Get("w1"); got.Harness != "fake" {
		t.Fatalf("a run with room stays on its own model: %+v", got)
	}
	limit("fake")
	if p, at := c.Runs.next(r); p.Harness != "open" || at.After(time.Now()) {
		t.Fatalf("next at a limit: %v at %v", p, at)
	}
	r, _ = c.Runs.Get("w1")
	r.Status = StatusIdle
	c.Runs.launch(r, c.WS.LoadConfig())
	got, _ := c.Runs.Get("w1")
	if got.Harness != "open" || got.Model != "free" || got.Login != "open:free" {
		t.Fatalf("the run didn't move to the free model: %s:%s on %s", got.Harness, got.Model, got.Login)
	}
	prompt, _ := os.ReadFile(c.WS.RunHost("w1") + "/turn-002.prompt")
	if !strings.HasPrefix(string(prompt), "the brief") || !strings.Contains(string(prompt), "You now run on open:free: fake:opus is at its usage limit") {
		t.Fatalf("the moved turn's prompt: %q", prompt)
	}
	if !slices.Contains(args, "open-agent") || slices.Contains(args, "--resume") {
		t.Fatalf("the moved turn's argv: %q", args)
	}
	// The free model hits its own limit too: with nothing left it waits.
	c.Runs.append("plan.usage", Plan{Harness: "open", Login: "open:free", Limit: harness.RateLimit{Status: "rejected", ResetsAt: time.Now().Add(2 * time.Hour)}})
	got, _ = c.Runs.Get("w1")
	if p, at := c.Runs.next(got); !at.After(time.Now().Add(30*time.Minute)) || p.Harness != "fake" {
		t.Fatalf("with every pick limited it waits for the soonest reset: %v at %v", p, at)
	}
	// No route: it stays on its own and waits.
	r2, _ := c.Runs.Start(RunSpec{ID: "w2", Role: "worker", Harness: "fake", Model: "opus", Checkout: t.TempDir()})
	if p, at := c.Runs.next(r2); p.Harness != "fake" || !at.After(time.Now()) {
		t.Fatalf("a run with no route: %v at %v", p, at)
	}
}
