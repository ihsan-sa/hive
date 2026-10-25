package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	// A named model's host widens only its own harness's runs: a run on
	// another harness, even on a model of the same name, keeps its list.
	for _, hn := range []string{"claude", "fake", "codex"} {
		oh, _ := harness.Get(hn)
		r := Run{RunSpec: RunSpec{ID: "x-" + hn, Role: "worker", Harness: hn, Model: "free"}}
		spec, err := c.Runs.sandboxSpec(cfg, isolatedSB{}, oh, r, 1, false, "p", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(spec.Allow, "openrouter.ai") {
			t.Errorf("a %s run reaches the open model's host: %q", hn, spec.Allow)
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
		!strings.Contains(argv, `--settings {"apiKeyHelper":"'hive' open-key '/hive/home/.hive-open/key'","disableAllHooks":true`) {
		t.Errorf("argv %q", spec.Argv)
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL=https://api.moonshot.ai/anthropic", "ANTHROPIC_DEFAULT_HAIKU_MODEL=kimi-k2-0905-preview",
		"ANTHROPIC_SMALL_FAST_MODEL=kimi-k2-0905-preview", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000",
		"CLAUDE_CONFIG_DIR=/hive/home/.claude-open"} {
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

// The key helper's words are shell-quoted: a path holding $, a backtick or
// a quote reaches the helper as written, never expanded.
func TestShellWord(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("the POSIX shell")
	}
	for _, s := range []string{"/w/hive", "/w/$HOME/x", "/w/`id`/x", "/w/it's/x", `/w/a"b\c`} {
		out, err := exec.Command("sh", "-c", "printf %s "+shellWord(s, false)).Output()
		if err != nil || string(out) != s {
			t.Errorf("%q came through as %q (%v)", s, out, err)
		}
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

	// Claude Code's config folder planted as a link to a Claude login
	// elsewhere is replaced by a real, empty one.
	cfgDir := filepath.Join(home, ".claude-open")
	os.Symlink(elsewhere, cfgDir)
	if err := realDir(cfgDir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(cfgDir); !fi.IsDir() || Exists(filepath.Join(cfgDir, "key")) {
		t.Fatal("the config folder is still a link")
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

// An endpoint never carries a credential, which agents would see; a key
// would cross a public network unencrypted only on plain http to a public host.
func TestEndpointHoldsNoKey(t *testing.T) {
	c := DefaultConfig()
	for _, ep := range []string{"https://me:sk-1@api.example.com/v1", "https://api.example.com/v1?key=sk-1", "https://api.example.com/v1#sk-1"} {
		c.Models = map[string]OpenModel{"m": {Endpoint: ep, Model: "x"}}
		if err := c.Check(); err == nil || strings.Contains(err.Error(), "sk-1") {
			t.Errorf("%s: %v", ep, err)
		}
	}
	for ep, want := range map[string]bool{"http://api.example.com/v1": true, "https://api.example.com/v1": false,
		"http://127.0.0.1:11434/v1": false, "http://192.168.1.2:8000/v1": false, "http://localhost:8000/v1": false} {
		c.Models = map[string]OpenModel{"m": {Endpoint: ep, Model: "x"}}
		if c.Check() != nil || c.Plaintext("m") != want {
			t.Errorf("%s: plaintext %v, want %v (%v)", ep, c.Plaintext("m"), want, c.Check())
		}
	}
}

// A named model's price costs its turns, and a run with a budget starts on
// it only if that budget can be held: a free model, or one the open agent
// counts at a price.
func TestModelPriceAndBudget(t *testing.T) {
	p := Price{Input: 2, Output: 8}
	if got := p.Cost(harness.Tokens{Input: 400_000, CacheRead: 100_000, Output: 250_000}); got != 3 {
		t.Fatalf("cost %v, want 3", got)
	}
	for _, tc := range []struct {
		m  OpenModel
		ok bool
	}{
		{OpenModel{}, true}, // on this machine
		{OpenModel{Key: true}, false},
		{OpenModel{Key: true, Price: &Price{}}, true}, // a free tier, said so
		{OpenModel{Key: true, Price: &p}, true},
		{OpenModel{Key: true, Price: &p, API: "anthropic"}, false},
		{OpenModel{Key: true, Price: &Price{}, API: "anthropic"}, true},
	} {
		if err := tc.m.Holds(1); (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc.m, err)
		}
		if tc.m.Holds(0) != nil {
			t.Errorf("%+v refuses no budget", tc.m)
		}
	}
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"paid": {Endpoint: "https://api.example.com/v1", Model: "x", Key: true}}
	c.WS.SaveConfig(cfg)
	if _, err := c.Runs.Start(RunSpec{Role: "worker", Harness: "open", Model: "paid", Budget: 1, Checkout: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "price") {
		t.Fatalf("a budget on an unpriced paid model started: %v", err)
	}
	// A priced model's turn costs its tokens at the price, whatever the line says.
	cfg.Models["paid"] = OpenModel{Endpoint: "https://api.example.com/v1", Model: "x", Key: true, Price: &Price{Input: 2, Output: 8}}
	c.WS.SaveConfig(cfg)
	r, err := c.Runs.Start(RunSpec{ID: "p1", Role: "worker", Harness: "open", Model: "paid", Brief: "b", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r.Turn = 1
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out, []byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","total_cost_usd":0,"usage":{"input_tokens":500000,"output_tokens":125000},"session_id":"s1"}`+"\n"), 0o600)
	c.Runs.endTurn(r)
	if got, _ := c.Runs.Get("p1"); got.Cost != 2 {
		t.Fatalf("cost %v, want 2", got.Cost)
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
	c.Runs.SetStatus("w1", StatusIdle, "") // the first turn ended
	r, _ = c.Runs.Get("w1")
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
}

// A vendor's 429, as Claude Code reports it on an Anthropic-compatible model
// (no rate_limit_info, and words that needn't say "limit"), parks that model
// and moves the run to the next on its route; a key refused mid-turn says so
// plainly and retries afresh, without marking the model down.
func TestVendorLimitMovesToFallback(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{
		"kimi": {Endpoint: "https://api.moonshot.ai/anthropic", Model: "kimi-k2", API: "anthropic", Key: true},
		"free": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"},
	}
	c.WS.SaveConfig(cfg)
	_, err := c.Runs.Start(RunSpec{ID: "v1", Role: "worker", Harness: "open", Model: "kimi", Brief: "b", Checkout: t.TempDir(),
		Route: []Pick{{Harness: "open", Model: "kimi"}, {Harness: "open", Model: "free"}}})
	if err != nil {
		t.Fatal(err)
	}
	end := func(turn int, line string) Run {
		r, _ := c.Runs.Get("v1")
		r.Turn, r.Login = turn, "open:kimi"
		out := c.WS.TurnOut(r.ID, turn)
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte(line+"\n"), 0o600)
		c.Runs.endTurn(r)
		got, _ := c.Runs.Get("v1")
		return got
	}
	got := end(1, `{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Your apiKeyHelper script is failing · This usually means you need to re-authenticate with your provider · Run /status to see the script's error output","session_id":"s1"}`)
	if got.Status != StatusParked || !strings.Contains(got.Reason, "refused kimi's key mid-turn") || !strings.Contains(got.Reason, "model key kimi") {
		t.Fatalf("a key refused mid-turn: %s %q", got.Status, got.Reason)
	}
	if d := c.Runs.Down(); len(d) != 0 {
		t.Fatalf("a key refused mid-turn marked the model down: %+v", d)
	}
	got = end(2, `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"API Error: Request rejected (429) · Your account org-x request reached organization max RPM: 3","session_id":"s1"}`)
	if got.Status != StatusParked || got.Reason != "usage limit; moving to open:free" {
		t.Fatalf("a vendor's 429: %s %q", got.Status, got.Reason)
	}
	if !c.Runs.freeAt("open", "kimi").After(time.Now().Add(30 * time.Minute)) {
		t.Fatal("the vendor's model isn't parked")
	}
	if d := c.Runs.Down(); len(d) != 0 {
		t.Fatalf("a vendor's 429 is a limit, not a model that can't answer: %+v", d)
	}
	if p, at := c.Runs.next(got); p.Model != "free" || at.After(time.Now()) {
		t.Fatalf("next after a vendor's 429: %v at %v", p, at)
	}
}

// A key the endpoint won't take at all (refused on two turns in a row, the
// second with the key handed over afresh) marks the model down as any 401
// does, and the run moves at once to the next pick on its route.
func TestKeyRefusedTwiceMovesToFallback(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{
		"kimi": {Endpoint: "https://api.moonshot.ai/anthropic", Model: "kimi-k2", API: "anthropic", Key: true},
		"free": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"},
	}
	c.WS.SaveConfig(cfg)
	if _, err := c.Runs.Start(RunSpec{ID: "v2", Role: "worker", Harness: "open", Model: "kimi", Brief: "b", Checkout: t.TempDir(),
		Route: []Pick{{Harness: "open", Model: "kimi"}, {Harness: "open", Model: "free"}}}); err != nil {
		t.Fatal(err)
	}
	refused := `{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Your apiKeyHelper script is failing · This usually means you need to re-authenticate with your provider · Run /status to see the script's error output","session_id":"s1"}`
	end := func(turn int) Run {
		r, _ := c.Runs.Get("v2")
		r.Turn, r.Login = turn, "open:kimi"
		out := c.WS.TurnOut(r.ID, turn)
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte(refused+"\n"), 0o600)
		c.Runs.endTurn(r)
		got, _ := c.Runs.Get("v2")
		return got
	}
	if got := end(1); !strings.Contains(got.Reason, "refused kimi's key mid-turn") || len(c.Runs.Down()) != 0 {
		t.Fatalf("the first refusal: %q, down %+v", got.Reason, c.Runs.Down())
	}
	got := end(2)
	if d := c.Runs.Down(); len(d) != 1 || d[0].Pick.Model != "kimi" || d[0].Status != 401 {
		t.Fatalf("the second refusal left the model up: %+v", d)
	}
	if got.Status != StatusParked || !strings.Contains(got.Reason, "moving to open:free") {
		t.Fatalf("the second refusal: %s %q", got.Status, got.Reason)
	}
	if p, at := c.Runs.next(got); p.Model != "free" || at.After(time.Now()) {
		t.Fatalf("next after a refused key: %v at %v", p, at)
	}
}

// A run's turn starts at most once. On 2026-10-24 a run moving off a model
// at its limit got two live sessions on one checkout: two launches of the
// same run, one beside the other and one from a copy older than the turn
// the first started. Each has to start nothing.
func TestTurnStartsOnceWhenARunMoves(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"free": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"}}
	c.WS.SaveConfig(cfg)
	var args []string
	var mu sync.Mutex
	starts := 0
	in, release := make(chan struct{}, 1), make(chan struct{})
	block := false
	c.Runs.sandbox = func() (sandbox.Sandbox, error) {
		mu.Lock()
		b := block
		block = false
		mu.Unlock()
		if b {
			in <- struct{}{}
			<-release
		}
		return countSB{startSB{args: &args}, &mu, &starts}, nil
	}
	r, err := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "fake", Model: "opus", Brief: "the brief", Checkout: t.TempDir(),
		Route: []Pick{{Harness: "fake", Model: "opus"}, {Harness: "open", Model: "free"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, c.WS.LoadConfig())
	c.Runs.SetStatus("w1", StatusIdle, "") // the first turn ended
	c.Runs.append("plan.usage", Plan{Harness: "fake", Login: "fake", Limit: harness.RateLimit{Status: "rejected", ResetsAt: time.Now().Add(time.Hour)}})
	r, _ = c.Runs.Get("w1")

	mu.Lock()
	block = true
	mu.Unlock()
	done := make(chan struct{})
	go func() { c.Runs.launch(r, c.WS.LoadConfig()); close(done) }()
	<-in
	c.Runs.launch(r, c.WS.LoadConfig()) // beside the first
	close(release)
	<-done
	c.Runs.launch(r, c.WS.LoadConfig()) // from the copy before the turn started

	got, _ := c.Runs.Get("w1")
	if starts != 2 || got.Turn != 2 || got.Harness != "open" {
		t.Fatalf("%d turns started (want 2: the first and one on the new route); run at turn %d on %s", starts, got.Turn, got.Harness)
	}
	started := 0
	for _, e := range c.J.Since(0) {
		if e.Kind == "run.turn_started" {
			started++
		}
	}
	if started != 2 {
		t.Fatalf("%d run.turn_started journalled, want 2", started)
	}
}

type countSB struct {
	startSB
	mu *sync.Mutex
	n  *int
}

func (s countSB) Start(spec sandbox.Spec) (sandbox.Proc, error) {
	s.mu.Lock()
	*s.n++
	s.mu.Unlock()
	return s.startSB.Start(spec)
}

// Tick steps a run as it is when the step begins, not as it was listed: a
// run listed idle whose turn started since is stepped as running, so it is
// watched rather than launched again.
func TestTickStepsTheRunAsItIsNow(t *testing.T) {
	c, _ := testHive(t)
	var args []string
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return startSB{args: &args}, nil }
	r, err := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "fake", Model: "opus", Brief: "the brief", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, c.WS.LoadConfig())
	c.Runs.SetStatus("w1", StatusIdle, "") // the first turn ended
	listed, _ := c.Runs.Get("w1")
	c.Runs.launch(listed, c.WS.LoadConfig()) // a turn starts after the list was read
	got, ok := c.Runs.claim(listed)
	if !ok || got.Status != StatusRunning || got.Turn != 2 {
		t.Fatalf("claimed %v: %s at turn %d, want running at turn 2", ok, got.Status, got.Turn)
	}
	if _, again := c.Runs.claim(listed); again {
		t.Fatal("a run claimed twice before its step ended")
	}
}

// A watch from a copy older than the run's last turn ends nothing: that
// turn ended already, and ending it again would clear the live one after it.
func TestWatchFromAStaleCopyEndsNothing(t *testing.T) {
	c, _ := testHive(t)
	var args []string
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return startSB{args: &args}, nil }
	r, err := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "fake", Model: "opus", Brief: "the brief", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, c.WS.LoadConfig())
	stale, _ := c.Runs.Get("w1") // running turn 1
	c.Runs.SetStatus("w1", StatusIdle, "")
	cur, _ := c.Runs.Get("w1")
	c.Runs.launch(cur, c.WS.LoadConfig()) // turn 2 is live
	c.Runs.watch(stale)
	got, _ := c.Runs.Get("w1")
	if got.Status != StatusRunning || got.Turn != 2 {
		t.Fatalf("after a stale watch: %s at turn %d, want running at turn 2", got.Status, got.Turn)
	}
	for _, e := range c.J.Since(0) {
		if e.Kind == "run.turn_ended" {
			t.Fatalf("a stale watch ended a turn: %s", e.Data)
		}
	}
	c.Runs.watch(got) // the live copy still ends its turn
	if got, _ = c.Runs.Get("w1"); got.Status == StatusRunning {
		t.Fatal("the current copy's watch didn't end the turn")
	}
}
