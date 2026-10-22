package harness

import (
	"encoding/json"
	"strings"
)

// Codex is OpenAI's Codex CLI in exec mode with JSONL output. It runs in its
// own sandbox inside the Hive's: read-only for a read-only turn, else writes
// limited to the checkout and the Turn's Writable folders. Codex picks its own
// session id, which the init event reports.
type Codex struct{}

func (Codex) Name() string           { return "codex" }
func (Codex) Program() string        { return "codex" }
func (Codex) LoginCommand() []string { return []string{"codex", "login", "--device-auth"} }

func (Codex) Args(t Turn) []string {
	a := []string{"exec"}
	if t.Resume {
		a = append(a, "resume")
	}
	a = append(a, "--json", "--skip-git-repo-check", "-c", `approval_policy="never"`)
	if t.ReadOnly {
		a = append(a, "-c", `sandbox_mode="read-only"`)
	} else {
		a = append(a, "-c", `sandbox_mode="workspace-write"`, "-c", "sandbox_workspace_write.network_access=true")
		if len(t.Writable) > 0 {
			roots, _ := json.Marshal(t.Writable)
			a = append(a, "-c", "sandbox_workspace_write.writable_roots="+string(roots))
		}
	}
	if t.SystemText != "" {
		// A JSON string is a valid TOML basic string.
		s, _ := json.Marshal(t.SystemText)
		a = append(a, "-c", "developer_instructions="+string(s))
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

// Env points Codex at the run's own CODEX_HOME, which holds only the access token.
func (Codex) Env(t Turn) []string {
	return []string{"CODEX_HOME=" + t.Home + "/.codex"}
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
		return []Event{{Kind: "result", Result: &Result{Subtype: "success"}}}
	case "turn.failed":
		msg := ""
		if l.Error != nil {
			msg = l.Error.Message
		}
		return []Event{{Kind: "result", Result: &Result{Text: msg, IsError: true, Subtype: "error", Limited: LooksLimited(msg)}}}
	}
	return nil
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
