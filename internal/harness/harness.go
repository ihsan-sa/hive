// Package harness is the adapter seam between the core and a coding harness
// (Claude Code, OpenAI's Codex and the Hive's own open harness). A harness turns a Turn into
// a command line and turns its output stream into Events.
package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Turn is one invocation of an agent session.
type Turn struct {
	Role         string
	SessionID    string
	Resume       bool   // continue SessionID instead of starting it
	SystemFile   string // appended system prompt, as a path the agent process sees
	SystemText   string // the same prompt inline (used inside sandboxes, where argv has room)
	Model        string
	ReadOnly     bool     // no write or exec tools at all (reviewer, tribunal)
	RunDir       string   // the run's inputs; a read-only turn may read them besides its checkout
	Tools        []string // explicit tool list; nil means the harness default
	Schema       string   // JSON schema for a structured final answer
	SchemaFile   string   // the same schema as a path the agent process sees
	ConfigDir    string   // the harness's own config folder the Hive made for this turn, if any
	Writable     []string // folders besides the checkout the agent may write
	Sandboxed    bool     // inside a Hive sandbox (permission prompts are meaningless there)
	MaxOutput    int      // cap on one reply's tokens; 0 is the harness default
	MaxBudgetUSD float64  // cap on the turn's spend; 0 is none
	Endpoint     string   // a named model's API base URL (OpenClaude)
	KeyCommand   string   // a command that prints the model's key once (OpenClaude); "" for a model with none
	Context      int      // tokens the model holds; 0 is the harness's own idea
	Prompt       string   // the turn's prompt, for a harness that takes it as an arg (Prompter)
}

// Event is a normalized line of harness output.
type Event struct {
	Kind      string // init, text, tool, context, usage, result, ratelimit
	SessionID string
	Tokens    int // context: what the session's context held at this reply
	Model     string
	Text      string
	Tool      string
	Result    *Result
	RateLimit *RateLimit
	Usage     *Tokens // usage: one step's tokens, summed into a result that carries none
}

// Result ends a turn.
type Result struct {
	Text       string          `json:"text"`
	IsError    bool            `json:"is_error"`
	Subtype    string          `json:"subtype"`
	CostUSD    float64         `json:"cost_usd"`
	Structured json.RawMessage `json:"structured,omitempty"`
	APIStatus  int             `json:"api_status,omitempty"`
	Limited    bool            `json:"limited,omitempty"`
	Tokens     *Tokens         `json:"tokens,omitempty"` // what this turn read and wrote, when the harness says
}

// Tokens is a turn's token use: fresh input, output, and the prompt cache
// read and written.
type Tokens struct {
	Input      int64 `json:"input,omitempty"`
	Output     int64 `json:"output,omitempty"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
}

// Add sums o into t.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
}

// RateLimit is the plan's usage state as the harness reports it.
type RateLimit struct {
	Status      string            `json:"status"` // allowed, allowed_warning, rejected
	ResetsAt    time.Time         `json:"resets_at"`
	Window      string            `json:"window"`
	Utilization float64           `json:"utilization"`
	Windows     map[string]Window `json:"windows,omitempty"`
}

// Window is one usage window (five_hour, seven_day).
type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

// Harness builds commands and parses output.
type Harness interface {
	Name() string
	// Program is the executable name looked up on PATH (inside the sandbox if sandboxed).
	Program() string
	Args(t Turn) []string
	Env(t Turn) []string
	Parse(line []byte) []Event
	// LoginCommand is the harness's own login, run in the person's terminal.
	LoginCommand() []string
}

// APIHosts are the hosts a harness's own client must reach, added to every
// run's egress list. A harness lists them with an APIHosts method.
func APIHosts(h Harness) []string {
	if a, ok := h.(interface{ APIHosts() []string }); ok {
		return a.APIHosts()
	}
	return nil
}

// Prompter is a harness that rewrites the prompt it is sent (Spec): the
// core sends it Prompt(t) instead of the turn's prompt, on stdin, or in the
// args when PromptArg.
type Prompter interface {
	Prompt(t Turn) string
	PromptArg() bool
}

// ReadOnlyTools says h holds a read-only turn to tools that can't write
// (Claude Code's read tools, the fake's and the open agent's own), so the
// turn may keep a writable mailbox; on any other harness a read-only turn can
// run commands, and gets none.
func ReadOnlyTools(h Harness) bool {
	_, ok := h.(interface{ ReadOnlyTools() })
	return ok
}

// AnswersInText says a structured answer from h comes as its result's text.
func AnswersInText(h Harness) bool {
	_, ok := h.(interface{ AnswersInText() bool })
	return ok
}

// Capped is nil when h holds a turn to budget USD itself (Turn.MaxBudgetUSD).
// A harness says it does with a CapsSpend method; 0 is no budget.
func Capped(h Harness, budget float64) error {
	if _, ok := h.(interface{ CapsSpend() }); budget > 0 && !ok {
		return fmt.Errorf("the %s harness can't hold a run to a budget, so a run with one won't start on it", h.Name())
	}
	return nil
}

// Models are the models a harness knows, the default first. The open
// harness knows only the models the config names, and a module's harness
// those its spec lists. Codex's are those its
// 0.153 release lists; a Claude model on Codex runs Codex's own default.
func Models(harness string) []string {
	if !builtin(harness) {
		if s, ok := lookup(harness); ok {
			return slices.Clone(s.ModelList)
		}
		return nil
	}
	switch harness {
	case "open":
		return nil
	case "codex":
		return []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"}
	}
	return []string{"opus", "sonnet", "fable", "haiku"}
}

// Claude is Claude Code in print mode with stream-json output.
type Claude struct{}

func (Claude) Name() string           { return "claude" }
func (Claude) CapsSpend()             {}
func (Claude) ReadOnlyTools()         {}
func (Claude) Program() string        { return "claude" }
func (Claude) LoginCommand() []string { return []string{"claude", "auth", "login"} }
func (Claude) APIHosts() []string     { return []string{"api.anthropic.com"} }

func (Claude) Args(t Turn) []string {
	a := []string{"-p", "--output-format", "stream-json", "--verbose", "--strict-mcp-config", "--setting-sources", "project"}
	if t.Resume {
		a = append(a, "--resume", t.SessionID)
	} else {
		a = append(a, "--session-id", t.SessionID)
	}
	switch {
	case t.Sandboxed && t.SystemText != "":
		a = append(a, "--append-system-prompt", t.SystemText)
	case t.SystemFile != "":
		// Outside a sandbox (Windows argv is capped at 32k), pass a file.
		a = append(a, "--append-system-prompt-file", t.SystemFile)
	}
	if t.Model != "" {
		a = append(a, "--model", t.Model)
	}
	switch {
	case t.ReadOnly:
		a = append(a, "--tools", "Read,Grep,Glob", "--add-dir", t.RunDir, "--permission-mode", "dontAsk")
	case t.Tools != nil:
		a = append(a, "--tools", strings.Join(t.Tools, ","), "--permission-mode", "bypassPermissions")
	default:
		a = append(a, "--permission-mode", "bypassPermissions")
	}
	if t.Schema != "" {
		a = append(a, "--json-schema", t.Schema)
	}
	if t.MaxBudgetUSD > 0 {
		a = append(a, "--max-budget-usd", fmt.Sprintf("%.2f", t.MaxBudgetUSD))
	}
	return a
}

func (Claude) Env(t Turn) []string {
	env := []string{"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	if t.MaxOutput > 0 {
		env = append(env, fmt.Sprintf("CLAUDE_CODE_MAX_OUTPUT_TOKENS=%d", t.MaxOutput))
	}
	return env
}

type claudeLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Message   *struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage *struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Result    string  `json:"result"`
	IsError   bool    `json:"is_error"`
	TotalCost float64 `json:"total_cost_usd"`
	Usage     *struct {
		Input         int64 `json:"input_tokens"`
		Output        int64 `json:"output_tokens"`
		CacheCreation int64 `json:"cache_creation_input_tokens"`
		CacheRead     int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	APIErrorStatus   *int            `json:"api_error_status"`
	RateLimitInfo    *struct {
		Status         string   `json:"status"`
		ResetsAt       int64    `json:"resetsAt"`
		RateLimitType  string   `json:"rateLimitType"`
		Utilization    *float64 `json:"utilization"`
		UnifiedWindows map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

func (Claude) Parse(line []byte) []Event {
	var l claudeLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	switch l.Type {
	case "system":
		if l.Subtype == "init" {
			return []Event{{Kind: "init", SessionID: l.SessionID, Model: l.Model}}
		}
	case "assistant":
		if l.Message == nil {
			return nil
		}
		var out []Event
		if u := l.Message.Usage; u != nil {
			out = append(out, Event{Kind: "context", Tokens: u.Input + u.CacheCreation + u.CacheRead})
		}
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				if strings.TrimSpace(c.Text) != "" {
					out = append(out, Event{Kind: "text", Text: c.Text})
				}
			case "tool_use":
				out = append(out, Event{Kind: "tool", Tool: c.Name, Text: summarizeInput(c.Input)})
			}
		}
		return out
	case "rate_limit_event":
		if l.RateLimitInfo == nil {
			return nil
		}
		ri := l.RateLimitInfo
		rl := &RateLimit{Status: ri.Status, Window: ri.RateLimitType, Windows: map[string]Window{}}
		if ri.ResetsAt > 0 {
			rl.ResetsAt = time.Unix(ri.ResetsAt, 0).UTC()
		}
		for k, w := range ri.UnifiedWindows {
			wnd := Window{Utilization: w.Utilization}
			if w.ResetsAt > 0 { // 0 is "didn't say", not 1970
				wnd.ResetsAt = time.Unix(w.ResetsAt, 0).UTC()
			}
			rl.Windows[k] = wnd
		}
		// Claude Code sends the binding window's utilization only under
		// unifiedWindows, not at the top level, when the status is allowed.
		if ri.Utilization != nil {
			rl.Utilization = *ri.Utilization
		} else if w, ok := rl.Windows[ri.RateLimitType]; ok {
			rl.Utilization = w.Utilization
		}
		return []Event{{Kind: "ratelimit", RateLimit: rl}}
	case "result":
		r := &Result{Text: l.Result, IsError: l.IsError, Subtype: l.Subtype, CostUSD: l.TotalCost}
		if len(l.StructuredOutput) > 0 && string(l.StructuredOutput) != "null" {
			r.Structured = l.StructuredOutput
		}
		if l.APIErrorStatus != nil {
			r.APIStatus = *l.APIErrorStatus
		}
		if u := l.Usage; u != nil {
			r.Tokens = &Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation}
		}
		r.Limited = r.IsError && LooksLimited(r.Text)
		return []Event{{Kind: "result", SessionID: l.SessionID, Result: r}}
	}
	return nil
}

// LooksLimited recognizes a usage-limit message in an error result.
func LooksLimited(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "usage limit") || strings.Contains(s, "hit your limit") ||
		(strings.Contains(s, "limit") && strings.Contains(s, "reset"))
}

func summarizeInput(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "pattern", "url", "description", "prompt"} {
		if v, ok := m[k].(string); ok {
			return Clip(v, 160)
		}
	}
	return ""
}

// Clip shortens s to n runes on one line.
func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
