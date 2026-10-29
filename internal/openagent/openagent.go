// Package openagent is the Hive's open harness: an agent loop that drives any
// OpenAI-compatible chat endpoint (Ollama and other local model servers,
// OpenRouter and other hosted tiers) with shell and file tools, and writes
// Claude Code's stream-json, so the run manager reads its turns as it reads
// Claude's. Its session (the conversation) is kept in the run's home, so a
// later turn resumes it.
package openagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// Message is one entry of the chat, in the OpenAI shape.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a model's call of one tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Agent is one turn's settings.
type Agent struct {
	Endpoint, Model, Key string
	Context              int // tokens the model holds
	MaxOutput            int
	Cwd                  string
	ReadOnly             bool
	Dirs                 []string // what a read-only turn may read
	Schema               json.RawMessage
	MaxSteps             int
	Out                  io.Writer
	Client               *http.Client
	Sleep                func(time.Duration) // waits out a short rate limit
	Price                [2]float64          // USD per million input and output tokens; zero is free
	Budget               float64             // USD the turn may spend at Price; 0 is no cap

	in, out int64 // the turn's tokens so far
}

// spent is what the turn has cost so far at its price.
func (a *Agent) spent() float64 {
	return (float64(a.in)*a.Price[0] + float64(a.out)*a.Price[1]) / 1e6
}

// done is result line r with the turn's tokens and cost.
func (a *Agent) done(r map[string]any) map[string]any {
	r["usage"] = map[string]any{"input_tokens": a.in, "output_tokens": a.out}
	r["total_cost_usd"] = a.spent()
	return r
}

// KeyMain is `hive open-key FILE`: it prints the model key the run manager
// left in FILE and deletes it. Claude Code, running a model on an
// Anthropic-compatible API, calls it as its apiKeyHelper once, before its
// first request, so the key is gone before any tool runs.
func KeyMain(argv []string) int {
	if len(argv) != 1 {
		fmt.Fprintln(os.Stderr, "usage: hive open-key FILE")
		return 2
	}
	raw, err := readNoLink(argv[0])
	os.Remove(argv[0]) // a link at its name goes, not what it points to
	key := strings.TrimSpace(string(raw))
	if err != nil || key == "" {
		fmt.Fprintln(os.Stderr, "the model's key was already handed over this turn (a turn is handed it once, so the endpoint refused it mid-turn), or never left: the next turn gets it afresh")
		return 1
	}
	fmt.Println(key)
	return 0
}

// readNoLink reads a regular file at path, never one a link there points to.
func readNoLink(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if after, err := f.Stat(); err != nil || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%s changed while it was read", path)
	}
	return io.ReadAll(f)
}

// Main is `hive open-agent --session S --model NAME [--resume] [--system-file F]
// [--readonly --add-dir D] [--schema-file F] [--max-output N]`, with the
// endpoint and model id in HIVE_OPEN_ENDPOINT and HIVE_OPEN_MODEL, the key in
// the file HIVE_OPEN_KEY_FILE names (deleted once read) or HIVE_OPEN_KEY, and
// its conversations under HIVE_OPEN_HOME (else $HOME).
func Main(argv []string) int {
	fl := flag.NewFlagSet("open-agent", flag.ContinueOnError)
	session := fl.String("session", "", "")
	name := fl.String("model", "", "")
	resume := fl.Bool("resume", false, "")
	systemFile := fl.String("system-file", "", "")
	readonly := fl.Bool("readonly", false, "")
	addDir := fl.String("add-dir", "", "")
	schemaFile := fl.String("schema-file", "", "")
	maxOut := fl.Int("max-output", 0, "")
	budget := fl.Float64("max-budget-usd", 0, "")
	if err := fl.Parse(argv); err != nil || *session == "" {
		fmt.Fprintln(os.Stderr, "usage: hive open-agent --session S --model NAME [--resume] [--system-file F] [--readonly --add-dir D] [--schema-file F]")
		return 2
	}
	a := &Agent{Endpoint: os.Getenv("HIVE_OPEN_ENDPOINT"), Model: os.Getenv("HIVE_OPEN_MODEL"), Key: os.Getenv("HIVE_OPEN_KEY"),
		ReadOnly: *readonly, MaxOutput: *maxOut, MaxSteps: 200, Out: os.Stdout, Sleep: time.Sleep,
		Client: &http.Client{Timeout: 20 * time.Minute}}
	a.Context, _ = strconv.Atoi(os.Getenv("HIVE_OPEN_CONTEXT"))
	a.Budget = *budget
	if in, out, ok := strings.Cut(os.Getenv("HIVE_OPEN_PRICE"), ","); ok {
		a.Price[0], _ = strconv.ParseFloat(in, 64)
		a.Price[1], _ = strconv.ParseFloat(out, 64)
	}
	noDump()
	if f := os.Getenv("HIVE_OPEN_KEY_FILE"); f != "" {
		// The run manager hands the key over in a file, deleted before any
		// tool runs, so no command the model runs can read it.
		raw, err := readNoLink(f)
		os.Remove(f)
		if err != nil {
			a.emit(map[string]any{"type": "result", "subtype": "error", "is_error": true, "result": "the model's key: " + err.Error(), "session_id": *session})
			return 1
		}
		a.Key = strings.TrimSpace(string(raw))
	}
	home := os.Getenv("HIVE_OPEN_HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	for _, k := range []string{"HIVE_OPEN_ENDPOINT", "HIVE_OPEN_MODEL", "HIVE_OPEN_KEY", "HIVE_OPEN_KEY_FILE", "HIVE_OPEN_CONTEXT", "HIVE_OPEN_HOME", "HIVE_OPEN_PRICE"} {
		os.Unsetenv(k)
	}
	if a.Model == "" {
		a.Model = *name
	}
	a.Cwd, _ = os.Getwd()
	a.Dirs = []string{a.Cwd}
	if *addDir != "" {
		a.Dirs = append(a.Dirs, *addDir)
	}
	if *schemaFile != "" {
		raw, err := os.ReadFile(*schemaFile)
		if err != nil {
			a.emit(map[string]any{"type": "result", "subtype": "error", "is_error": true, "result": "schema: " + err.Error(), "session_id": *session})
			return 1
		}
		a.Schema = raw
	}
	prompt, _ := io.ReadAll(os.Stdin)
	system := ""
	if *systemFile != "" {
		raw, _ := os.ReadFile(*systemFile)
		system = string(raw)
	}
	store := filepath.Join(home, ".hive-open", filepath.Base(*session)+".json")
	var msgs []Message
	if *resume {
		if raw, err := os.ReadFile(store); err == nil {
			json.Unmarshal(raw, &msgs)
		}
	}
	if len(msgs) == 0 {
		msgs = []Message{{Role: "system", Content: a.preamble() + system}}
	}
	msgs = append(msgs, Message{Role: "user", Content: string(prompt)})
	a.emit(map[string]any{"type": "system", "subtype": "init", "session_id": *session, "model": *name})
	save := func(m []Message) {
		os.MkdirAll(filepath.Dir(store), 0o700)
		raw, _ := json.Marshal(m)
		os.WriteFile(store, raw, 0o600)
	}
	res := a.Turn(context.Background(), msgs, save)
	res["session_id"] = *session
	a.emit(res)
	if res["is_error"] == true {
		return 1
	}
	return 0
}

// preamble is what the agent is told about its tools before the Hive's prompt.
func (a *Agent) preamble() string {
	var b strings.Builder
	b.WriteString("You are an autonomous coding agent working in " + a.Cwd + ". You act only through your tools; nobody answers questions mid-turn. ")
	if a.ReadOnly {
		b.WriteString("This turn is read-only: read_file, list_dir and grep read the folder you are in and the run's own folder. ")
	} else {
		b.WriteString("Run shell commands with bash (git, tests, `hive agent ...`), read files with read_file, write a whole file with write_file and change part of one with edit_file. ")
	}
	if len(a.Schema) > 0 {
		b.WriteString("End by calling the answer tool once with your final answer. ")
	} else {
		b.WriteString("When the work is done, reply with your final message and no tool call. ")
	}
	b.WriteString("Keep each tool call small and check its result before the next.\n\n")
	return b.String()
}

// Turn runs the loop until the model answers without a tool call, returning
// the result line.
func (a *Agent) Turn(ctx context.Context, msgs []Message, save func([]Message)) map[string]any {
	return a.done(a.turn(ctx, msgs, save))
}

func (a *Agent) turn(ctx context.Context, msgs []Message, save func([]Message)) map[string]any {
	tools := a.tools()
	last := ""
	for step := 0; step < a.MaxSteps; step++ {
		msgs = a.fit(msgs)
		if a.Budget > 0 && a.spent() >= a.Budget {
			save(msgs)
			return errResult(fmt.Sprintf("the turn reached its budget of $%.2f on %s", a.Budget, a.Model), 0)
		}
		reply, tokens, err := a.chat(ctx, msgs, tools)
		if err != nil {
			var le *limitErr
			if errors.As(err, &le) {
				a.emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected", "resetsAt": le.reset.Unix(), "rateLimitType": "model"}})
				return errResult(fmt.Sprintf("usage limit reached on %s; it resets at %s (%s)", a.Model, le.reset.UTC().Format(time.RFC3339), le.msg), 429)
			}
			var he *httpErr
			if errors.As(err, &he) {
				return errResult(he.Error(), he.code)
			}
			return errResult(err.Error(), 0)
		}
		if len(reply.ToolCalls) == 0 {
			if c, ok := textCall(reply.Content, tools); ok {
				reply.ToolCalls, reply.Content = []ToolCall{c}, ""
			}
		}
		msgs = append(msgs, reply)
		content := []map[string]any{}
		if strings.TrimSpace(reply.Content) != "" {
			last = reply.Content
			content = append(content, map[string]any{"type": "text", "text": reply.Content})
		}
		for _, c := range reply.ToolCalls {
			var in any
			if json.Unmarshal([]byte(c.Function.Arguments), &in) != nil {
				in = map[string]any{}
			}
			content = append(content, map[string]any{"type": "tool_use", "name": c.Function.Name, "input": in})
		}
		a.emit(map[string]any{"type": "assistant", "message": map[string]any{"content": content, "usage": map[string]any{"input_tokens": tokens}}})
		if len(reply.ToolCalls) == 0 {
			save(msgs)
			if len(a.Schema) > 0 && !json.Valid([]byte(strings.TrimSpace(last))) {
				msgs = append(msgs, Message{Role: "user", Content: "Call the answer tool with your final answer now."})
				continue
			}
			return map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": last, "total_cost_usd": 0}
		}
		for _, c := range reply.ToolCalls {
			if c.Function.Name == "answer" && len(a.Schema) > 0 {
				save(msgs)
				return map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": c.Function.Arguments,
					"structured_output": json.RawMessage(validJSON(c.Function.Arguments)), "total_cost_usd": 0}
			}
			msgs = append(msgs, Message{Role: "tool", ToolCallID: c.ID, Content: a.run(ctx, c)})
		}
		save(msgs)
	}
	return map[string]any{"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0,
		"result": fmt.Sprintf("%s\n\n(stopped after %d tool steps in one turn)", last, a.MaxSteps)}
}

func errResult(msg string, status int) map[string]any {
	r := map[string]any{"type": "result", "subtype": "error", "is_error": true, "result": msg, "total_cost_usd": 0}
	if status > 0 {
		r["api_error_status"] = status
	}
	return r
}

func validJSON(s string) string {
	if json.Valid([]byte(s)) {
		return s
	}
	return "null"
}

func (a *Agent) emit(v any) {
	raw, _ := json.Marshal(v)
	a.Out.Write(append(raw, '\n'))
}

// tools are the function definitions the model is offered.
func (a *Agent) tools() []map[string]any {
	fn := func(name, desc string, props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": desc,
			"parameters": map[string]any{"type": "object", "properties": props, "required": required}}}
	}
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	num := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	out := []map[string]any{
		fn("read_file", "Read a text file, with line numbers.", map[string]any{"path": str("the file"), "offset": num("first line, from 1"), "limit": num("how many lines (default 400)")}, "path"),
	}
	if a.ReadOnly {
		out = append(out,
			fn("list_dir", "List a folder's entries.", map[string]any{"path": str("the folder")}, "path"),
			fn("grep", "Find lines matching a regular expression in the files under a folder.", map[string]any{"pattern": str("RE2 regular expression"), "path": str("folder or file (default: the working folder)")}, "pattern"))
	} else {
		out = append(out,
			fn("bash", "Run a shell command in the working folder and return its output and exit code.", map[string]any{"command": str("the command"), "timeout": num("seconds (default 600)")}, "command"),
			fn("write_file", "Write a whole file, making folders as needed.", map[string]any{"path": str("the file"), "content": str("its full text")}, "path", "content"),
			fn("edit_file", "Replace one exact, unique piece of a file's text.", map[string]any{"path": str("the file"), "old": str("the exact text now there"), "new": str("what replaces it")}, "path", "old", "new"))
	}
	if len(a.Schema) > 0 {
		var params any
		json.Unmarshal(a.Schema, &params)
		out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": "answer", "description": "Give your final answer; this ends the turn.", "parameters": params}})
	}
	return out
}

// textCall reads a tool call a model wrote as text ({"name": ..., "arguments": ...}),
// as some small local models do.
func textCall(s string, tools []map[string]any) (ToolCall, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```"), "```")
	var c struct {
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(s)), &c) != nil || c.Name == "" {
		return ToolCall{}, false
	}
	known := false
	for _, t := range tools {
		known = known || t["function"].(map[string]any)["name"] == c.Name
	}
	args := c.Arguments
	if len(args) == 0 {
		args = c.Parameters
	}
	var str string
	if json.Unmarshal(args, &str) == nil {
		args = json.RawMessage(str)
	}
	if !known || !json.Valid(args) {
		return ToolCall{}, false
	}
	var tc ToolCall
	tc.ID, tc.Type = "call_text_"+strconv.FormatInt(time.Now().UnixNano(), 36), "function"
	tc.Function.Name, tc.Function.Arguments = c.Name, string(args)
	return tc, true
}

type limitErr struct {
	reset time.Time
	msg   string
}

func (e *limitErr) Error() string { return "rate limited: " + e.msg }

type httpErr struct {
	code int
	msg  string
}

func (e *httpErr) Error() string {
	return fmt.Sprintf("the model server answered %d: %s", e.code, e.msg)
}

// chat sends the conversation and returns the model's reply and the prompt's
// size in tokens. A rate limit that resets within two minutes is waited out.
func (a *Agent) chat(ctx context.Context, msgs []Message, tools []map[string]any) (Message, int, error) {
	if err := harness.OpenRouterPaid(a.Endpoint, a.Model); err != nil && a.Price == [2]float64{} {
		// The run manager allows a paid one under the month's cap, priced so
		// its spend is counted (core.Config.PaidRefused); one with no price
		// would spend what nobody counts.
		return Message{}, 0, fmt.Errorf("%v with no price, so what it spends isn't counted", err)
	}
	body := map[string]any{"model": a.Model, "messages": msgs, "tools": tools}
	if a.MaxOutput > 0 {
		body["max_tokens"] = a.MaxOutput
	}
	raw, _ := json.Marshal(body)
	for try := 0; ; try++ {
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(a.Endpoint, "/")+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return Message{}, 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		if a.Key != "" {
			req.Header.Set("Authorization", "Bearer "+a.Key)
		}
		resp, err := a.Client.Do(req)
		if err != nil {
			return Message{}, 0, err
		}
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		var r struct {
			Choices []struct {
				Message Message `json:"message"`
			} `json:"choices"`
			Usage struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
				Code    any    `json:"code"`
			} `json:"error"`
		}
		json.Unmarshal(out, &r)
		msg := strings.TrimSpace(string(out))
		if r.Error != nil {
			msg = r.Error.Message
		}
		if len(msg) > 600 {
			msg = msg[:600]
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			reset := resetAt(resp.Header, time.Now())
			if time.Until(reset) <= 2*time.Minute && try < 5 {
				a.Sleep(time.Until(reset))
				continue
			}
			return Message{}, 0, &limitErr{reset: reset, msg: msg}
		}
		if resp.StatusCode >= 500 && try < 3 {
			a.Sleep(time.Duration(try+1) * 5 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK || r.Error != nil || len(r.Choices) == 0 {
			code := resp.StatusCode
			if code == http.StatusOK {
				code = 502 // an answer with no reply in it
			}
			return Message{}, 0, &httpErr{code: code, msg: msg}
		}
		m := r.Choices[0].Message
		m.Role = "assistant"
		for i := range m.ToolCalls {
			m.ToolCalls[i].Type = "function"
			if m.ToolCalls[i].ID == "" {
				m.ToolCalls[i].ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
			}
		}
		tokens := r.Usage.Prompt
		if tokens == 0 {
			tokens = size(msgs) / 4
		}
		wrote := r.Usage.Completion
		if wrote == 0 {
			wrote = (len(m.Content) + len(fmt.Sprint(m.ToolCalls))) / 4
		}
		a.in, a.out = a.in+int64(tokens), a.out+int64(wrote)
		return m, tokens, nil
	}
}

// resetAt reads when a rate limit resets: Retry-After (seconds or a date),
// or X-RateLimit-Reset (epoch seconds or milliseconds, as OpenRouter sends);
// a minute when neither says.
func resetAt(h http.Header, now time.Time) time.Time {
	if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return now.Add(time.Duration(n) * time.Second)
		}
		if t, err := http.ParseTime(v); err == nil {
			return t
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			if n > 1e12 {
				return time.UnixMilli(n)
			}
			if n > 1e9 {
				return time.Unix(n, 0)
			}
			return now.Add(time.Duration(n) * time.Second)
		}
	}
	return now.Add(time.Minute)
}

func size(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) + 16
		for _, c := range m.ToolCalls {
			n += len(c.Function.Arguments) + len(c.Function.Name)
		}
	}
	return n
}

const elided = "[elided to fit the model's context]"

// fit trims the conversation to the model's context, at about four
// characters a token: the oldest tool outputs first, then the oldest
// replies' text. The system prompt, the first ask and the last few
// exchanges stay whole while they fit, and the latest message always.
func (a *Agent) fit(msgs []Message) []Message {
	ctxTokens := a.Context
	if ctxTokens <= 0 {
		ctxTokens = 32000
	}
	budget := (ctxTokens - min(a.MaxOutput, ctxTokens/4)) * 4 * 9 / 10
	for _, keep := range []int{len(msgs) - 6, len(msgs) - 1} {
		for _, role := range []string{"tool", "assistant"} {
			for i := 2; i < keep && size(msgs) > budget; i++ {
				if msgs[i].Role == role && len(msgs[i].Content) > len(elided) {
					msgs[i].Content = elided
				}
			}
		}
	}
	return msgs
}

// run carries out one tool call and returns what the model reads back.
func (a *Agent) run(ctx context.Context, c ToolCall) string {
	var in struct {
		Command, Path, Content, Old, New, Pattern string
		Timeout, Offset, Limit                    int
	}
	if err := json.Unmarshal([]byte(c.Function.Arguments), &in); err != nil {
		return "error: the arguments aren't JSON: " + err.Error()
	}
	path := in.Path
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(a.Cwd, path)
	}
	if a.ReadOnly && c.Function.Name != "answer" {
		if path == "" {
			path = a.Cwd
		}
		if !a.readable(path) {
			return "error: a read-only turn reads only " + strings.Join(a.Dirs, " and ")
		}
	}
	switch c.Function.Name {
	case "bash":
		if a.ReadOnly {
			break
		}
		return a.bash(ctx, in.Command, in.Timeout)
	case "read_file":
		return readFile(path, in.Offset, in.Limit)
	case "write_file":
		if a.ReadOnly {
			break
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "error: " + err.Error()
		}
		if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("wrote %s (%d bytes)", in.Path, len(in.Content))
	case "edit_file":
		if a.ReadOnly {
			break
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "error: " + err.Error()
		}
		switch n := strings.Count(string(raw), in.Old); {
		case in.Old == "" || n == 0:
			return "error: the old text isn't in " + in.Path + "; read the file and copy it exactly"
		case n > 1:
			return fmt.Sprintf("error: the old text is in %s %d times; include more around it", in.Path, n)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(raw), in.Old, in.New, 1)), 0o644); err != nil {
			return "error: " + err.Error()
		}
		return "edited " + in.Path
	case "list_dir":
		ents, err := os.ReadDir(path)
		if err != nil {
			return "error: " + err.Error()
		}
		var b strings.Builder
		for _, e := range ents {
			b.WriteString(e.Name())
			if e.IsDir() {
				b.WriteString("/")
			}
			b.WriteString("\n")
		}
		return clip(b.String())
	case "grep":
		return a.grep(in.Pattern, path)
	}
	return "error: no tool " + c.Function.Name + " in this turn"
}

func (a *Agent) readable(path string) bool {
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		p = filepath.Clean(path)
	}
	for _, d := range a.Dirs {
		if real, err := filepath.EvalSymlinks(d); err == nil {
			d = real
		}
		if rel, err := filepath.Rel(d, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (a *Agent) bash(ctx context.Context, command string, timeout int) string {
	if timeout <= 0 {
		timeout = 600
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(min(timeout, 1800))*time.Second)
	defer cancel()
	sh := "bash"
	if _, err := exec.LookPath(sh); err != nil {
		sh = "sh"
	}
	cmd := exec.CommandContext(ctx, sh, "-c", command)
	cmd.Dir = a.Cwd
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	s := clip(string(out))
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		s += fmt.Sprintf("\n[timed out after %ds]", timeout)
	case err != nil:
		s += "\n[" + err.Error() + "]"
	default:
		s += "\n[exit 0]"
	}
	return s
}

func readFile(path string, offset, limit int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "error: " + err.Error()
	}
	lines := strings.Split(string(raw), "\n")
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 {
		limit = 400
	}
	var b strings.Builder
	for i := offset - 1; i < len(lines) && i < offset-1+limit; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
	}
	if offset-1+limit < len(lines) {
		fmt.Fprintf(&b, "[%d more lines; read on with offset %d]\n", len(lines)-(offset-1+limit), offset+limit)
	}
	return clip(b.String())
}

func (a *Agent) grep(pattern, root string) string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "error: " + err.Error()
	}
	var b strings.Builder
	n := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || n >= 200 {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			// A link the change under review planted would read past
			// readable(), which checks only the root.
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(raw[:min(len(raw), 8000)], 0) >= 0 {
			return nil
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if re.MatchString(line) {
				fmt.Fprintf(&b, "%s:%d: %s\n", p, i+1, strings.TrimSpace(line))
				if n++; n >= 200 {
					break
				}
			}
		}
		return nil
	})
	if n == 0 {
		return "no matches"
	}
	return clip(b.String())
}

// clip keeps a tool's output to 30k characters: its head and its tail.
func clip(s string) string {
	const max = 30000
	if len(s) <= max {
		return s
	}
	return s[:max/2] + fmt.Sprintf("\n[... %d characters cut ...]\n", len(s)-max) + s[len(s)-max/2:]
}

// Probe sends a model one real request, as a turn's first would be, and
// says why it can't answer; nil when it answers. A rate limit counts as
// answering: the model is there, only busy. A model on an Anthropic-compatible
// API (anthropic) is asked through its Messages API, as Claude Code would.
// A paid OpenRouter model is asked at its price (USD per million tokens in
// and out), as a turn on it would be; with none it is refused unasked, as
// chat refuses it. The config check lets one in only under
// openrouter_monthly_usd (Config.PaidRefused).
func Probe(ctx context.Context, endpoint, model, key string, price [2]float64, anthropic bool) error {
	client := &http.Client{Timeout: 90 * time.Second, Transport: probeTransport}
	if anthropic {
		body, _ := json.Marshal(map[string]any{"model": model, "max_tokens": 16,
			"messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}})
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(endpoint, "/")+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("anthropic-version", "2023-06-01")
		if key != "" {
			req.Header.Set("X-Api-Key", key)
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusTooManyRequests {
			return nil
		}
		msg := strings.TrimSpace(string(out))
		var e struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out, &e) == nil && e.Error != nil {
			msg = e.Error.Message
		}
		if len(msg) > 600 {
			msg = msg[:600]
		}
		return &httpErr{code: resp.StatusCode, msg: msg}
	}
	a := &Agent{Endpoint: endpoint, Model: model, Key: key, Price: price, MaxOutput: 16, Client: client, Sleep: func(time.Duration) {}}
	_, _, err := a.chat(ctx, []Message{{Role: "user", Content: "Reply with OK."}}, a.tools())
	var le *limitErr
	if errors.As(err, &le) {
		return nil
	}
	return err
}

// probeTransport is what Probe sends through; nil is the default, and a
// test sets its own.
var probeTransport http.RoundTripper

// Status is the API status a Probe's error names, or 0.
func Status(err error) int {
	var he *httpErr
	if errors.As(err, &he) {
		return he.code
	}
	return 0
}
