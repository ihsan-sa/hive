package core

import (
	"os"
	"path/filepath"
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
	// The key reaches the turn as a file in its home, never in its environment.
	r := Run{RunSpec: RunSpec{ID: "o-key", Role: "worker", Harness: "open", Model: "free"}}
	os.MkdirAll(c.WS.RunHome(r.ID), 0o755)
	spec, err := c.Runs.sandboxSpec(cfg, isolatedSB{}, h, r, 1, false, "p", env, "")
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(spec.Env, "\n")
	raw, _ := os.ReadFile(c.Runs.openKeyFile(r.ID))
	if strings.Contains(all, "sk-or-test") || !strings.Contains(all, "HIVE_OPEN_KEY_FILE=/hive/home/.hive-open/key") || string(raw) != "sk-or-test" {
		t.Fatalf("env %q, key file %q", spec.Env, raw)
	}
	c.Runs.dropOpenKey(r.ID)
	if Exists(c.Runs.openKeyFile(r.ID)) {
		t.Fatal("the key file outlived its turn")
	}
}

// A named model on an Anthropic-compatible API runs on Claude Code: --model
// is the endpoint's id, ANTHROPIC_BASE_URL its endpoint, no Claude login or
// credential reaches it, the key goes in a file a helper prints once (never
// in the environment or the command line), and the run may reach the
// endpoint's host but not Anthropic's.
func TestAnthropicModelTurn(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{
		"kimi":  {Endpoint: "https://api.moonshot.ai/anthropic", Model: "kimi-k2-0905-preview", API: "anthropic", Key: true, Context: 200000},
		"local": {Endpoint: "http://127.0.0.1:4000", Model: "glm-4.6", API: "anthropic"},
	}
	cfg.Egress.Default = []string{"github.com"}
	if err := cfg.Check(); err != nil {
		t.Fatal(err)
	}
	if err := c.WS.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := c.WS.SaveOpenKey("kimi", "sk-kimi-test"); err != nil {
		t.Fatal(err)
	}
	if p, _ := cfg.Pick("kimi"); p.Harness != "open" || cfg.Models["kimi"].Harness().Program() != "claude" {
		t.Fatalf("pick %v runs on %s", p, cfg.Models["kimi"].Harness().Program())
	}
	tok, err := NewTokens(c.WS).AgentEnv(Login{Harness: "open", Name: "kimi"}, "")
	if err != nil {
		t.Fatal(err)
	}
	r := Run{RunSpec: RunSpec{ID: "a-kimi", Role: "worker", Harness: "open", Model: "kimi"}}
	os.MkdirAll(c.WS.RunHome(r.ID), 0o755)
	h := cfg.Models["kimi"].Harness()
	spec, err := c.Runs.sandboxSpec(cfg, isolatedSB{}, h, r, 1, false, "p", tok, "")
	if err != nil {
		t.Fatal(err)
	}
	argv, env := strings.Join(spec.Argv, " "), strings.Join(spec.Env, "\n")
	if spec.Argv[0] != "claude" || !strings.Contains(argv, "--model kimi-k2-0905-preview") ||
		!strings.Contains(argv, `--settings {"apiKeyHelper":"\"hive\" open-key \"/hive/home/.hive-open/key\"","disableAllHooks":true`) {
		t.Errorf("argv %q", spec.Argv)
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL=https://api.moonshot.ai/anthropic", "ANTHROPIC_DEFAULT_HAIKU_MODEL=kimi-k2-0905-preview",
		"ANTHROPIC_SMALL_FAST_MODEL=kimi-k2-0905-preview", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000"} {
		if !slices.Contains(spec.Env, want) {
			t.Errorf("env lacks %s: %q", want, spec.Env)
		}
	}
	if strings.Contains(argv+env, "sk-kimi-test") || strings.Contains(env, "HIVE_OPEN_") {
		t.Errorf("the key or the open agent's settings are in the turn's command or environment: %q %q", spec.Argv, spec.Env)
	}
	// The daemon's own Claude credentials can't win over the model's key:
	// each is blanked last.
	last := map[string]string{}
	for _, kv := range spec.Env {
		k, v, _ := strings.Cut(kv, "=")
		last[k] = v
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if v, ok := last[k]; !ok || v != "" {
			t.Errorf("%s is %q, not blank", k, v)
		}
	}
	if raw, _ := os.ReadFile(c.Runs.openKeyFile(r.ID)); string(raw) != "sk-kimi-test" {
		t.Errorf("key file %q", raw)
	}
	if !slices.Contains(spec.Allow, "api.moonshot.ai") || slices.Contains(spec.Allow, "api.anthropic.com") || !slices.Contains(spec.Tools, "claude") {
		t.Errorf("egress %q, tools %q", spec.Allow, spec.Tools)
	}
	c.Runs.dropOpenKey(r.ID)

	// A model with no key gets a placeholder and no helper; one on this
	// machine adds no public host.
	r = Run{RunSpec: RunSpec{ID: "a-local", Role: "worker", Harness: "open", Model: "local"}}
	tok, _ = NewTokens(c.WS).AgentEnv(Login{Harness: "open", Name: "local"}, "")
	spec, err = c.Runs.sandboxSpec(cfg, isolatedSB{}, cfg.Models["local"].Harness(), r, 1, false, "p", tok, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(spec.Argv, " "), "apiKeyHelper") || !slices.Contains(spec.Env, "ANTHROPIC_API_KEY=none") || len(spec.Allow) != 1 {
		t.Errorf("argv %q, env %q, egress %q", spec.Argv, spec.Env, spec.Allow)
	}

	// It routes and falls back as any other pick.
	cfg.Routes = map[string][]string{"worker": {"claude:opus"}, "fallback": {"kimi", "local"}}
	if got := picks(cfg.Route("worker", "")); got != "claude:opus open:kimi open:local" {
		t.Errorf("worker route %s", got)
	}

	cfg.Models["bad"] = OpenModel{Endpoint: "https://x.example/v1", Model: "m", API: "gemini"}
	if err := cfg.Check(); err == nil || !strings.Contains(err.Error(), "openai, anthropic") {
		t.Errorf("an unknown api: %v", err)
	}
}

// The daemon writes a model's key into the run's home, which the agent can
// write: a link an earlier turn planted at the key's folder or file is
// replaced, never followed, so the key lands nowhere else and nothing
// outside is deleted.
func TestOpenKeyFollowsNoLink(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("links need privileges on Windows")
	}
	c, _ := testHive(t)
	elsewhere := t.TempDir()
	os.WriteFile(filepath.Join(elsewhere, "key"), []byte("theirs"), 0o644)
	keyOf := func(id string) string { raw, _ := os.ReadFile(c.Runs.openKeyFile(id)); return string(raw) }

	// The folder is a link.
	home := c.WS.RunHome("l-dir")
	os.MkdirAll(home, 0o755)
	os.Symlink(elsewhere, filepath.Join(home, ".hive-open"))
	if _, err := c.Runs.openKey("l-dir", []string{"HIVE_OPEN_KEY=sk-x"}, "/k"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(elsewhere, "key")); string(raw) != "theirs" || keyOf("l-dir") != "sk-x" {
		t.Fatalf("outside %q, key %q", raw, keyOf("l-dir"))
	}
	if fi, _ := os.Lstat(filepath.Join(home, ".hive-open")); !fi.IsDir() {
		t.Fatal("the key's folder is still a link")
	}
	c.Runs.dropOpenKey("l-dir")

	// The file is a link.
	home = c.WS.RunHome("l-file")
	os.MkdirAll(filepath.Join(home, ".hive-open"), 0o700)
	os.Symlink(filepath.Join(elsewhere, "key"), filepath.Join(home, ".hive-open", "key"))
	if _, err := c.Runs.openKey("l-file", []string{"HIVE_OPEN_KEY=sk-y"}, "/k"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(elsewhere, "key")); string(raw) != "theirs" || keyOf("l-file") != "sk-y" {
		t.Fatalf("outside %q, key %q", raw, keyOf("l-file"))
	}

	// Dropping a key through a folder planted as a link after the turn
	// began deletes nothing outside.
	os.RemoveAll(filepath.Join(home, ".hive-open"))
	os.Symlink(elsewhere, filepath.Join(home, ".hive-open"))
	c.Runs.dropOpenKey("l-file")
	if !Exists(filepath.Join(elsewhere, "key")) {
		t.Fatal("dropping the key deleted a file outside the run's home")
	}
}

// Only a public host is added to a run's egress list for its model: an
// endpoint on this machine or a private network opens only through the
// person's local grant.
func TestOpenHostIsPublicOnly(t *testing.T) {
	c := DefaultConfig()
	c.Models = map[string]OpenModel{}
	for ep, want := range map[string]string{
		"https://api.moonshot.ai/v1": "api.moonshot.ai", "https://api.deepseek.com/v1": "api.deepseek.com",
		"http://localhost:11434/v1": "", "http://127.0.0.2:8080/v1": "", "http://0.0.0.0:11434/v1": "",
		"http://[::1]:8000/v1": "", "http://[::ffff:127.0.0.1]:8000/v1": "", "http://192.168.1.20:11434/v1": "",
		"http://10.0.0.5/v1": "", "http://169.254.169.254/v1": "", "http://[fd00::1]/v1": "", "http://100.64.0.1/v1": "",
	} {
		c.Models["m"] = OpenModel{Endpoint: ep, Model: "x"}
		if got := c.openHost("m"); got != want {
			t.Errorf("%s: %q, want %q", ep, got, want)
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
	// No run has finished on the free model yet, so it waits for the reset.
	if p, at := c.Runs.next(r); p.Harness != "fake" || !at.After(time.Now()) {
		t.Fatalf("a worker fell back to a model with no finishes: %v at %v", p, at)
	}
	// Once one has (a trial, or real work), it may go there.
	if _, err := c.Runs.Start(RunSpec{ID: "trial", Role: "worker", Harness: "open", Model: "free", Checkout: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	c.Runs.append("run.finish", map[string]any{"id": "trial", "finish": Finish{Status: "blocked"}})
	if p, _ := c.Runs.next(r); p.Harness != "fake" {
		t.Fatalf("a blocked finish proves nothing: %v", p)
	}
	c.Runs.append("run.finish", map[string]any{"id": "trial", "finish": Finish{Status: "done"}})
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
	if got.HarnessAt(1) != "fake" || got.HarnessAt(2) != "open" || len(got.Moves) != 1 {
		t.Fatalf("each turn is read with the harness it ran on: %+v", got.Moves)
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
	// A worker already on a model with no finishes goes back to its route's
	// first and waits there, rather than taking another turn where it is.
	r3, _ := c.Runs.Start(RunSpec{ID: "w3", Role: "worker", Harness: "open", Model: "unproven", Checkout: t.TempDir(),
		Route: []Pick{{Harness: "fake", Model: "opus"}, {Harness: "open", Model: "unproven"}}})
	if p, at := c.Runs.next(r3); p.Harness != "fake" || !at.After(time.Now()) {
		t.Fatalf("a worker stayed on a model with no finishes: %v at %v", p, at)
	}
}
