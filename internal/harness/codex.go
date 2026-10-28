package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Codex is OpenAI's Codex CLI in exec mode with JSONL output. Codex picks its
// own session id, which the init event reports.
//
// Inside a Hive sandbox Codex's own sandbox can't start (it is bubblewrap,
// and a namespace can't be made inside ours), so every command it ran would
// fail; there it runs with its sandbox off and the Hive's is the boundary, as
// for Claude Code: the checkout is read-only on a read-only turn. Unlike
// Claude's read-only turn, which has only read tools, a read-only Codex turn
// can run commands, since Codex reads files through its shell. Outside a Hive
// sandbox (bare) Codex keeps its own: read-only for a read-only turn, else
// writes limited to the checkout and the Turn's Writable folders.
type Codex struct{}

func (Codex) Name() string           { return "codex" }
func (Codex) Program() string        { return "codex" }
func (Codex) LoginCommand() []string { return []string{"codex", "login", "--device-auth"} }

// APIHosts are where Codex talks to its model: chatgpt.com on a ChatGPT
// login, api.openai.com on an API key.
func (Codex) APIHosts() []string { return []string{"chatgpt.com", "api.openai.com"} }

func (Codex) Args(t Turn) []string {
	a := []string{"exec"}
	if t.Resume {
		a = append(a, "resume")
	}
	a = append(a, "--json", "--skip-git-repo-check", "-c", `approval_policy="never"`)
	switch {
	case t.Sandboxed:
		a = append(a, "--dangerously-bypass-approvals-and-sandbox")
	case t.ReadOnly:
		a = append(a, "-c", `sandbox_mode="read-only"`)
	default:
		a = append(a, "-c", `sandbox_mode="workspace-write"`, "-c", "sandbox_workspace_write.network_access=true")
		if len(t.Writable) > 0 {
			roots, _ := json.Marshal(t.Writable)
			a = append(a, "-c", "sandbox_workspace_write.writable_roots="+string(roots))
		}
	}
	if t.Model != "" && !claudeModel(t.Model) {
		a = append(a, "-m", t.Model)
	}
	if t.SchemaFile != "" {
		a = append(a, "--output-schema", t.SchemaFile)
	}
	if t.Resume {
		a = append(a, t.SessionID)
	}
	return append(a, "-") // the prompt comes on stdin
}

// Env points Codex at the turn's own CODEX_HOME, which holds only the access
// token and, in its config.toml, the Hive's instructions.
func (Codex) Env(t Turn) []string {
	return []string{"CODEX_HOME=" + t.ConfigDir}
}

type codexLine struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
	Item *struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Command string `json:"command"`
	} `json:"item"`
	Usage *struct {
		Input  int64 `json:"input_tokens"`
		Cached int64 `json:"cached_input_tokens"`
		Output int64 `json:"output_tokens"`
	} `json:"usage"`
}

// Parse maps Codex's JSONL events. turn.completed carries no text; the run
// manager takes the turn's last message as its result.
func (Codex) Parse(line []byte) []Event {
	var l codexLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	switch l.Type {
	case "thread.started":
		return []Event{{Kind: "init", SessionID: l.ThreadID}}
	case "item.completed":
		if l.Item == nil {
			return nil
		}
		switch l.Item.Type {
		case "agent_message":
			if strings.TrimSpace(l.Item.Text) != "" {
				return []Event{{Kind: "text", Text: l.Item.Text}}
			}
		case "command_execution":
			return []Event{{Kind: "tool", Tool: "shell", Text: Clip(l.Item.Command, 160)}}
		}
	case "turn.completed":
		r := &Result{Subtype: "success"}
		if u := l.Usage; u != nil { // Codex's input counts the cached part too
			r.Tokens = &Tokens{Input: max(u.Input-u.Cached, 0), Output: u.Output, CacheRead: u.Cached}
		}
		return []Event{{Kind: "result", Result: r}}
	case "turn.failed":
		msg := ""
		if l.Error != nil {
			msg = l.Error.Message
		}
		return []Event{{Kind: "result", Result: &Result{Text: msg, IsError: true, Subtype: "error", Limited: LooksLimited(msg), APIStatus: codexStatus(msg)}}}
	}
	// A top-level "error" line is not the turn's end: Codex prints one for
	// each retry ("Reconnecting... 2/5"), and a turn that fails ends with
	// turn.failed, carrying the last error's message.
	return nil
}

// codexStatus is the API status in an error message Codex carries as the
// API's JSON body ({"status":400,...}), or 400 when the body names
// a model_not_found; 0 when it says none.
func codexStatus(msg string) int {
	var b struct {
		Status int `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(msg), &b) != nil {
		if strings.Contains(msg, "model_not_found") {
			return 400
		}
		return 0
	}
	if b.Status == 0 && b.Error.Code == "model_not_found" {
		return 400
	}
	return b.Status
}

// Usage reads the plan usage Codex keeps out of its output stream, in the
// session's rollout file under CODEX_HOME/sessions: the last rate-limit
// reading taken at or after since, and what the context held at the last
// request then (0 when it isn't known). A reading at its limit is "rejected".
func (Codex) Usage(rollout []byte, since time.Time) (*RateLimit, int) {
	var rl *RateLimit
	tokens := 0
	for _, line := range bytes.Split(rollout, []byte("\n")) {
		var l codexRollout
		if json.Unmarshal(line, &l) != nil || l.Payload == nil || l.Timestamp.Before(since) {
			continue
		}
		if i := l.Payload.Info; i != nil && i.Last != nil {
			tokens = i.Last.Input
		}
		if r := l.Payload.RateLimits; r != nil {
			if got := r.limit(); got != nil {
				rl = got
			}
		}
	}
	return rl, tokens
}

type codexRollout struct {
	Timestamp time.Time `json:"timestamp"`
	Payload   *struct {
		Info *struct {
			Last *struct {
				Input int `json:"input_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
		RateLimits *codexLimits `json:"rate_limits"`
	} `json:"payload"`
}

type codexLimits struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	Reached   *string      `json:"rate_limit_reached_type"`
}

type codexWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`         // unix seconds
	ResetsIn      int64   `json:"resets_in_seconds"` // older Codex
}

// limit is the reading as a RateLimit: each window under the name Claude's
// windows go by where the length matches, the binding one the fullest.
func (c codexLimits) limit() *RateLimit {
	rl := &RateLimit{Status: "allowed", Windows: map[string]Window{}}
	for i, w := range []*codexWindow{c.Primary, c.Secondary} {
		if w == nil {
			continue
		}
		name := windowName(w.WindowMinutes, i)
		var at time.Time // zero when Codex didn't say, not now or 1970
		switch {
		case w.ResetsAt > 0:
			at = time.Unix(w.ResetsAt, 0).UTC()
		case w.ResetsIn > 0:
			at = time.Now().Add(time.Duration(w.ResetsIn) * time.Second).UTC()
		}
		used := w.UsedPercent / 100
		rl.Windows[name] = Window{Utilization: used, ResetsAt: at}
		if rl.Window == "" || used > rl.Utilization || used >= 1 && at.After(rl.ResetsAt) {
			rl.Window, rl.Utilization, rl.ResetsAt = name, used, at
		}
	}
	if rl.Window == "" {
		return nil
	}
	switch {
	case rl.Utilization >= 1 || c.Reached != nil && *c.Reached != "":
		rl.Status = "rejected"
	case rl.Utilization >= 0.8:
		rl.Status = "allowed_warning"
	}
	return rl
}

func windowName(minutes, i int) string {
	switch {
	case minutes == 300:
		return "five_hour"
	case minutes == 7*24*60:
		return "seven_day"
	case minutes <= 0:
		return []string{"primary", "secondary"}[i]
	case minutes%(24*60) == 0:
		return fmt.Sprintf("%d_day", minutes/(24*60))
	case minutes%60 == 0:
		return fmt.Sprintf("%d_hour", minutes/60)
	}
	return fmt.Sprintf("%d_minute", minutes)
}

// claudeModel reports a Claude model name, which a workspace switched to Codex
// may still carry; Codex then uses its own default model.
func claudeModel(m string) bool {
	m = strings.ToLower(m)
	for _, c := range []string{"claude", "opus", "sonnet", "haiku", "fable"} {
		if strings.Contains(m, c) {
			return true
		}
	}
	return false
}
