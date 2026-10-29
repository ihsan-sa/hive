package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// installWorkers puts the workers module's harness specs in ws's modules.
func installWorkers(t *testing.T, ws Workspace) {
	t.Helper()
	dir := filepath.Join(ws.Modules(), "workers")
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{"module.json", "cursor.json", "opencode.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "modules", "workers", f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), raw, 0o644)
	}
	harness.UseModules(ws.Modules())
}

// A turn on a module's harness: its own prompt (the system prompt ahead of
// it for Cursor, which takes none), the checkout as PWD, its API hosts, and
// no writable mailbox on a read-only turn, since it can run commands.
func TestModuleHarnessTurn(t *testing.T) {
	c, _ := testHive(t)
	installWorkers(t, c.WS)
	cfg := c.WS.LoadConfig()
	cfg.Egress.Roles = map[string][]string{"reviewer": {"example.com"}}
	h, err := harness.Get("cursor")
	if err != nil {
		t.Fatal(err)
	}
	r := Run{RunSpec: RunSpec{ID: "r1", Role: "reviewer", Harness: "cursor", ReadOnly: true, System: "You review.", Checkout: t.TempDir()}}
	r.Session = "s1"
	prompt := filepath.Join(t.TempDir(), "turn-001.prompt")
	os.WriteFile(prompt, []byte("Read the PR."), 0o644)
	spec, err := c.Runs.sandboxSpec(cfg, isolatedSB{}, h, r, 1, false, prompt, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if last := spec.Argv[len(spec.Argv)-1]; spec.Argv[0] != "cursor-agent" || last != "You review.\n\n---\n\nRead the PR." || !slices.Contains(spec.Argv, "ask") {
		t.Fatalf("argv: %q", spec.Argv)
	}
	if raw, _ := os.ReadFile(spec.Stdin); spec.Stdin == prompt || len(raw) != 0 {
		t.Fatalf("a prompt in the args leaves stdin empty: %s %q", spec.Stdin, raw)
	}
	for _, m := range spec.Mounts {
		if m.Inside == "/hive/box" && m.RW {
			t.Fatal("a read-only turn on a harness that runs commands got a writable mailbox")
		}
	}
	if !slices.Contains(spec.Env, "PWD=/work") || !slices.Contains(spec.Env, "NO_OPEN_BROWSER=1") {
		t.Fatalf("env: %q", spec.Env)
	}
	if !slices.Contains(spec.Allow, "cursor.sh") || !slices.Contains(spec.Allow, "example.com") {
		t.Fatalf("egress: %q", spec.Allow)
	}

	oc, _ := harness.Get("opencode")
	r.Harness, r.ReadOnly, r.Schema = "opencode", false, `{"type":"object"}`
	spec, err = c.Runs.sandboxSpec(cfg, isolatedSB{}, oc, r, 1, false, prompt, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(spec.Stdin); !strings.HasPrefix(string(raw), "Read the PR.") || !strings.HasSuffix(string(raw), r.Schema) {
		t.Fatalf("opencode's stdin carries the prompt and the schema: %q", raw)
	}
}

// A module harness's turn log: the steps' tokens summed into the result, the
// last message as its answer and, with a schema, the JSON it ends with.
func TestReadModuleHarnessTurn(t *testing.T) {
	c, _ := testHive(t)
	installWorkers(t, c.WS)
	r := Run{RunSpec: RunSpec{ID: "r2", Harness: "opencode"}}
	raw, err := os.ReadFile(filepath.Join("..", "harness", "testdata", "opencode-turn.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(c.WS.TurnOut(r.ID, 1)), 0o755)
	os.WriteFile(c.WS.TurnOut(r.ID, 1), raw, 0o644)
	tl := c.Runs.ReadTurn(r, 1)
	if !tl.SessionLive || tl.Session != "ses_e90208df1ffecRqrsum8lbHrhk" || tl.Result == nil || tl.Result.Text != "hello" {
		t.Fatalf("turn: %+v %+v", tl, tl.Result)
	}
	if tk := tl.Result.Tokens; tk == nil || tk.Input != 13132 || tk.Output != 50 || tk.CacheRead != 146 {
		t.Fatalf("tokens: %+v", tk)
	}
	if tl.Context != 6613 {
		t.Fatalf("context: %d", tl.Context)
	}

	answer := `{"type":"text","sessionID":"s","part":{"text":"I read it.\n` + "```json" + `\n{\"verdict\": \"land\"}\n` + "```" + `"}}` + "\n" +
		`{"type":"step_finish","sessionID":"s","part":{"reason":"stop","tokens":{"input":5,"output":1}}}` + "\n"
	r.ID, r.Schema = "r3", `{"type":"object"}`
	os.MkdirAll(filepath.Dir(c.WS.TurnOut(r.ID, 1)), 0o755)
	os.WriteFile(c.WS.TurnOut(r.ID, 1), []byte(answer), 0o644)
	if tl := c.Runs.ReadTurn(r, 1); tl.Result == nil || string(tl.Result.Structured) != `{"verdict": "land"}` {
		t.Fatalf("structured answer: %+v", tl.Result)
	}
}

func TestAnswerJSON(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1}`:                          `{"a":1}`,
		"Done.\n{\"a\":1}":                 `{"a":1}`,
		"Done.\n{\n  \"a\": {\"b\": 2}\n}": "{\n  \"a\": {\"b\": 2}\n}",
		"```json\n{\"a\":1}\n```":          `{"a":1}`,
		"Done. {\"a\":1}":                  "",
		"no json":                          "",
	} {
		if got := string(answerJSON(in)); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// A module's harness gets the key the person stored, in the variable its
// spec names; without one a sandboxed turn can't start (a bare one uses the
// host's login), unless the harness needs no key.
func TestModuleHarnessKey(t *testing.T) {
	c, _ := testHive(t)
	installWorkers(t, c.WS)
	tok := NewTokens(c.WS)
	if _, err := tok.AgentEnv(Login{Harness: "cursor"}, ""); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
	if err := c.WS.SaveHarnessKey("cursor", "key_123\n"); err != nil {
		t.Fatal(err)
	}
	if env, err := tok.AgentEnv(Login{Harness: "cursor"}, ""); err != nil || !slices.Equal(env, []string{"CURSOR_API_KEY=key_123"}) {
		t.Fatalf("stored key: %q %v", env, err)
	}
	if fi, _ := os.Stat(c.WS.HarnessKey("cursor")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 { // Windows has no Unix modes
		t.Fatalf("key file mode %v", fi.Mode())
	}
	if env, err := tok.AgentEnv(Login{Harness: "opencode"}, ""); err != nil || env != nil {
		t.Fatalf("opencode needs no key: %q %v", env, err)
	}
	if service("cursor") != "Cursor" {
		t.Fatal(service("cursor"))
	}
}

// A run whose harness's module was removed waits a few times for it to come
// back, then is blocked saying why, rather than parked forever.
func TestRunOnARemovedHarnessStops(t *testing.T) {
	c, _ := testHive(t)
	installWorkers(t, c.WS)
	route := []Pick{{Harness: "opencode", Model: "opencode/big-pickle"}}
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "opencode", Model: "opencode/big-pickle", Route: route})
	os.RemoveAll(filepath.Join(c.WS.Modules(), "workers"))
	cfg := c.WS.LoadConfig()
	for i := 1; i <= harnessGoneTries+1; i++ {
		r, _ := c.Runs.Get("w1")
		c.Runs.launch(r, cfg)
		got, _ := c.Runs.Get("w1")
		if want := map[bool]string{true: StatusParked, false: StatusBlocked}[i <= harnessGoneTries]; got.Status != want || !strings.HasPrefix(got.Reason, harnessGone) {
			t.Fatalf("launch %d: %s %q, want %s", i, got.Status, got.Reason, want)
		}
	}
}

// A run whose pick can't run here (its harness's module removed, a paid
// model the settings refuse) goes on the next pick of its route that can,
// even one no run has finished on yet, rather than park or block on it.
func TestRunPassesAPickThatCantRunHere(t *testing.T) {
	c, _ := testHive(t)
	installWorkers(t, c.WS)
	route := []Pick{{Harness: "opencode", Model: "opencode/big-pickle"}, {Harness: "claude", Model: "opus"}}
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "opencode", Model: "opencode/big-pickle", Route: route})
	r, _ := c.Runs.Get("w1")
	if p, _ := c.Runs.next(r); !p.Same(route[0]) {
		t.Fatalf("with its harness there it went on %s", p)
	}
	os.RemoveAll(filepath.Join(c.WS.Modules(), "workers"))
	if p, _ := c.Runs.next(r); !p.Same(route[1]) {
		t.Fatalf("with its harness gone it went on %s, want %s", p, route[1])
	}
	if err := c.Runs.cantRun(route[0]); err == nil {
		t.Fatal("a removed harness can run")
	}
}

// A second result in one turn leaves no verdict, not even one read back
// from the last message's text.
func TestTwoResultsLeaveNoVerdict(t *testing.T) {
	c, _ := testHive(t)
	installWorkers(t, c.WS)
	step := `{"type":"text","sessionID":"s","part":{"text":"{\"verdict\": \"land\"}"}}` + "\n" +
		`{"type":"step_finish","sessionID":"s","part":{"reason":"stop","tokens":{"input":5,"output":1}}}` + "\n"
	r := Run{RunSpec: RunSpec{ID: "r4", Harness: "opencode", Schema: `{"type":"object"}`}}
	os.MkdirAll(filepath.Dir(c.WS.TurnOut(r.ID, 1)), 0o755)
	os.WriteFile(c.WS.TurnOut(r.ID, 1), []byte(step), 0o644)
	if tl := c.Runs.ReadTurn(r, 1); tl.Result == nil || tl.Result.Structured == nil {
		t.Fatalf("one result: %+v", tl.Result)
	}
	os.WriteFile(c.WS.TurnOut(r.ID, 1), []byte(step+step), 0o644)
	if tl := c.Runs.ReadTurn(r, 1); tl.Result == nil || tl.Result.Structured != nil {
		t.Fatalf("two results: %+v", tl.Result)
	}
}

// A prompt too long for one argument stops the run with a clear reason
// instead of failing to start with E2BIG every few minutes.
func TestPromptTooLongForAnArg(t *testing.T) {
	c, _ := testHive(t)
	installWorkers(t, c.WS)
	h, _ := harness.Get("cursor")
	r := Run{RunSpec: RunSpec{ID: "r5", Role: "worker", Harness: "cursor", System: strings.Repeat("s", harness.MaxArg), Checkout: t.TempDir()}}
	prompt := filepath.Join(t.TempDir(), "turn-001.prompt")
	os.WriteFile(prompt, []byte("Fix it."), 0o644)
	if _, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, r, 1, false, prompt, nil, ""); !errors.Is(err, harness.ErrArgTooLong) {
		t.Fatalf("a system prompt and brief over 128 KiB: %v", err)
	}
}
