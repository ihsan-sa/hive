package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Open is the Hive's own open harness: `hive open-agent`, an agent loop that
// drives any OpenAI-compatible chat endpoint (a model server on this machine
// such as Ollama, or a hosted tier such as OpenRouter) with shell and file
// tools, and speaks Claude's stream-json. The endpoint, the model id and any
// key come in the turn's environment from the named model's settings, so
// the model a turn names is that name.
type Open struct{}

func (Open) Name() string           { return "open" }
func (Open) CapsSpend()             {} // a run's budget is weighed between turns, on the cost the endpoint reports
func (Open) Program() string        { return "hive" }
func (Open) LoginCommand() []string { return nil }

func (Open) Args(t Turn) []string {
	a := []string{"open-agent", "--session", t.SessionID, "--model", t.Model}
	if t.Resume {
		a = append(a, "--resume")
	}
	if t.SystemFile != "" {
		a = append(a, "--system-file", t.SystemFile)
	}
	if t.ReadOnly {
		a = append(a, "--readonly", "--add-dir", t.RunDir)
	}
	if t.SchemaFile != "" {
		a = append(a, "--schema-file", t.SchemaFile)
	}
	if t.MaxOutput > 0 {
		a = append(a, "--max-output", fmt.Sprint(t.MaxOutput))
	}
	return a
}

func (Open) Env(Turn) []string { return nil }

// Parse uses Claude's stream-json shape, which the open agent emits. Its
// cost is what the endpoint itself reported (OpenRouter says what each
// answer cost; a local server says nothing, so nothing), never a price the
// Hive guessed.
func (Open) Parse(line []byte) []Event { return Claude{}.Parse(line) }

// OpenClaude is how the open harness runs a named model whose API is
// Anthropic-compatible (Kimi, DeepSeek and GLM serve one): Claude Code
// itself, pointed at the endpoint, on the endpoint's model id and never on a
// Claude login. The key comes from KeyCommand, which Claude Code runs once
// before its first request and which deletes the key file it reads, so no
// tool finds the key in a file or in its environment.
type OpenClaude struct{}

func (OpenClaude) Name() string           { return "open" }
func (OpenClaude) CapsSpend()             {} // as Open: a vendor's prices aren't known here, so spend isn't counted
func (OpenClaude) Program() string        { return "claude" }
func (OpenClaude) LoginCommand() []string { return nil }

// Args are Claude's, but with no settings file loaded: the checkout's
// .claude/settings.json is the agent's or the repo's to write, and a hook in
// it would run before the key helper (and could read the key file), or its
// env could send the key to another host. The flag's own settings pin the
// helper, the endpoint and no hooks.
func (OpenClaude) Args(t Turn) []string {
	t.MaxBudgetUSD = 0 // Claude Code would price the vendor's tokens as Claude's
	a := Claude{}.Args(t)
	for i := range a[:len(a)-1] {
		if a[i] == "--setting-sources" {
			a[i+1] = ""
		}
	}
	set := map[string]any{"disableAllHooks": true, "env": map[string]string{"ANTHROPIC_BASE_URL": strings.TrimSuffix(t.Endpoint, "/")}}
	if t.KeyCommand != "" {
		set["apiKeyHelper"] = t.KeyCommand
	}
	s, _ := json.Marshal(set)
	return append(a, "--settings", string(s))
}

func (OpenClaude) Env(t Turn) []string {
	env := append(Claude{}.Env(t),
		"ANTHROPIC_BASE_URL="+strings.TrimSuffix(t.Endpoint, "/"),
		// No Claude credential the daemon's own environment holds may win
		// over the model's key.
		"ANTHROPIC_AUTH_TOKEN=", "CLAUDE_CODE_OAUTH_TOKEN=",
		// The helper's key lasts the turn; a fresh turn hands over a fresh one.
		"CLAUDE_CODE_API_KEY_HELPER_TTL_MS=86400000",
	)
	if t.KeyCommand == "" {
		env = append(env, "ANTHROPIC_API_KEY=none") // a model with no key, such as a server on this machine
	} else {
		env = append(env, "ANTHROPIC_API_KEY=")
	}
	// Claude Code's own side requests (titles, summaries, subagents) go to
	// the same model, since the endpoint knows no Claude model.
	for _, k := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		env = append(env, k+"="+t.Model)
	}
	if t.Context > 0 {
		env = append(env, fmt.Sprintf("CLAUDE_CODE_AUTO_COMPACT_WINDOW=%d", t.Context))
	}
	return env
}

// Parse counts no cost: Claude Code on another vendor's model prices its
// tokens as Claude's.
func (OpenClaude) Parse(line []byte) []Event {
	evs := Claude{}.Parse(line)
	for _, e := range evs {
		if e.Result != nil {
			e.Result.CostUSD = 0
		}
	}
	return evs
}
