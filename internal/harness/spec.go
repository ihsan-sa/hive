package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Spec is a harness a module declares in a JSON file instead of Go code:
// its program, its login, the args and environment each turn gets, the hosts
// its client must reach, and rules that map its JSON output lines to the
// Hive's events. A module lists such files under "harnesses" in its
// module.json's provides (see docs/harnesses.md); the Hive loads them from
// the installed modules (UseModules), so adding a harness changes no Go code.
//
// Placeholders in args and env values: {session}, {model}, {prompt},
// {system_file}, {system_text}, {run_dir}, {schema_file}, {config_dir}.
type Spec struct {
	ID          string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Bin         string   `json:"program"`
	Login       []string `json:"login,omitempty"`  // the harness's own login, run in the person's terminal
	Status      []string `json:"status,omitempty"` // exits 0 when the host login is good
	// KeyEnv is the variable the harness reads an API key from. A sandboxed
	// turn, whose home is the run's own, gets the key the person stored with
	// `hive harness key <name>`; with none stored, a bare turn uses the host's
	// own login, and a sandboxed one waits unless KeyOptional (a harness with
	// free models needs none).
	KeyEnv      string   `json:"key_env,omitempty"`
	KeyOptional bool     `json:"key_optional,omitempty"`
	Hosts       []string `json:"api_hosts,omitempty"`
	ModelList   []string `json:"models,omitempty"` // the default first
	// PromptVia is how the prompt reaches it: "stdin" (the default) or "arg",
	// where {prompt} in the args carries it.
	PromptVia string `json:"prompt,omitempty"`
	// System is how the Hive's system prompt reaches it: "args" (the default:
	// the args or env name {system_file} or {system_text}) or "prompt", where
	// it goes ahead of a fresh session's first prompt, for a harness that
	// takes none.
	System string   `json:"system,omitempty"`
	Argv   SpecArgs `json:"args"`
	Envs   []string `json:"env,omitempty"`
	Rules  []Rule   `json:"parse"`
	file   string   // where it was read from
}

// SpecArgs are a turn's args, put together in this order: Start; New or
// Resume; Model when the turn names one; ReadOnly or Write; Sandboxed or
// Bare; Schema when the turn has a schema; End.
type SpecArgs struct {
	Start     []string `json:"start,omitempty"`
	New       []string `json:"new,omitempty"`
	Resume    []string `json:"resume,omitempty"`
	Model     []string `json:"model,omitempty"`
	ReadOnly  []string `json:"read_only,omitempty"`
	Write     []string `json:"write,omitempty"`
	Sandboxed []string `json:"sandboxed,omitempty"`
	Bare      []string `json:"bare,omitempty"`
	Schema    []string `json:"schema,omitempty"`
	End       []string `json:"end,omitempty"`
}

// Rule maps output lines that match it to one event. Paths are dotted keys
// into the line's JSON; "*" takes every element of an array (or value of an
// object), "@key" the first key of an object, and "a|b" the first path that
// has a value. Match compares a path's value as a string; a value starting
// with "!" matches anything else (a missing path included).
type Rule struct {
	Match   map[string]string `json:"match"`
	Event   string            `json:"event"` // init, text, tool, context, usage, result
	Session string            `json:"session,omitempty"`
	Model   string            `json:"model,omitempty"`
	Text    string            `json:"text,omitempty"`
	Tool    string            `json:"tool,omitempty"`
	// Context is what the session's context held at this reply.
	Context string `json:"context,omitempty"`
	// Tokens are a turn's (result) or one step's (usage) token counts; the
	// steps' usage is summed into a result that carries none.
	Tokens *TokenPaths `json:"tokens,omitempty"`
	Cost   string      `json:"cost,omitempty"`
	// A result is an error when Error is set, or when the value at
	// ErrorPath is true.
	Error      bool   `json:"error,omitempty"`
	ErrorPath  string `json:"error_path,omitempty"`
	Subtype    string `json:"subtype,omitempty"`
	Structured string `json:"structured,omitempty"`
}

// TokenPaths are where a line carries its token counts.
type TokenPaths struct {
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	CacheRead  string `json:"cache_read,omitempty"`
	CacheWrite string `json:"cache_write,omitempty"`
	// InputHasCache says Input counts the cached part too (as OpenAI's do).
	InputHasCache bool `json:"input_has_cache,omitempty"`
}

var (
	specName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	progName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	envName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	events   = []string{"init", "text", "tool", "context", "usage", "result"}
)

// ParseSpec reads a harness spec and checks it. The program is a plain name,
// looked up on PATH like Claude's: a path could expose any host file to the
// sandbox as a tool.
func ParseSpec(raw []byte) (*Spec, error) {
	var s Spec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	switch {
	case !specName.MatchString(s.ID):
		return nil, fmt.Errorf("harness name %q: lower-case letters, digits and dashes", s.ID)
	case builtin(s.ID):
		return nil, fmt.Errorf("harness name %q is built in", s.ID)
	case !progName.MatchString(s.Bin) || s.Bin == "hive":
		return nil, fmt.Errorf("harness %s: program %q must be a plain program name", s.ID, s.Bin)
	case s.KeyEnv != "" && !envName.MatchString(s.KeyEnv):
		return nil, fmt.Errorf("harness %s: key_env %q is not a variable name", s.ID, s.KeyEnv)
	case !slices.Contains([]string{"", "stdin", "arg"}, s.PromptVia):
		return nil, fmt.Errorf("harness %s: prompt is stdin or arg, not %q", s.ID, s.PromptVia)
	case !slices.Contains([]string{"", "args", "prompt"}, s.System):
		return nil, fmt.Errorf("harness %s: system is args or prompt, not %q", s.ID, s.System)
	case len(s.Rules) == 0:
		return nil, fmt.Errorf("harness %s has no parse rules", s.ID)
	}
	for _, e := range s.Envs {
		if k, _, ok := strings.Cut(e, "="); !ok || !envName.MatchString(k) {
			return nil, fmt.Errorf("harness %s: env %q is not NAME=value", s.ID, e)
		}
	}
	for _, h := range s.Hosts {
		if h == "" || strings.ContainsAny(h, "/:* ") || !strings.Contains(h, ".") {
			return nil, fmt.Errorf("harness %s: api host %q is not a host name", s.ID, h)
		}
	}
	for _, r := range s.Rules {
		if !slices.Contains(events, r.Event) {
			return nil, fmt.Errorf("harness %s: a rule's event %q is not one of %s", s.ID, r.Event, strings.Join(events, ", "))
		}
		if len(r.Match) == 0 {
			return nil, fmt.Errorf("harness %s: a %s rule matches every line", s.ID, r.Event)
		}
	}
	if s.PromptVia == "arg" && !strings.Contains(strings.Join(s.allArgs(), "\x00"), "{prompt}") {
		return nil, fmt.Errorf("harness %s takes its prompt as an arg, but no arg holds {prompt}", s.ID)
	}
	return &s, nil
}

// LoadSpec reads a spec file.
func LoadSpec(path string) (*Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := ParseSpec(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	s.file = path
	return s, nil
}

func (s *Spec) allArgs() []string {
	a := s.Argv
	return slices.Concat(a.Start, a.New, a.Resume, a.Model, a.ReadOnly, a.Write, a.Sandboxed, a.Bare, a.Schema, a.End)
}

func (s *Spec) Name() string           { return s.ID }
func (s *Spec) Program() string        { return s.Bin }
func (s *Spec) LoginCommand() []string { return slices.Clone(s.Login) }
func (s *Spec) APIHosts() []string     { return slices.Clone(s.Hosts) }
func (s *Spec) Models() []string       { return slices.Clone(s.ModelList) }

// File is where the spec was read from ("" for one built in code).
func (s *Spec) File() string { return s.file }

// Args builds a turn's args from the spec's parts.
func (s *Spec) Args(t Turn) []string {
	a := s.Argv
	parts := [][]string{a.Start}
	if t.Resume {
		parts = append(parts, a.Resume)
	} else {
		parts = append(parts, a.New)
	}
	if t.Model != "" && (slices.Contains(s.ModelList, t.Model) || !claudeModel(t.Model)) {
		parts = append(parts, a.Model) // a Claude model left in the config runs the harness's default
	}
	if t.ReadOnly {
		parts = append(parts, a.ReadOnly)
	} else {
		parts = append(parts, a.Write)
	}
	if t.Sandboxed {
		parts = append(parts, a.Sandboxed)
	} else {
		parts = append(parts, a.Bare)
	}
	if t.SchemaFile != "" || t.Schema != "" {
		parts = append(parts, a.Schema)
	}
	parts = append(parts, a.End)
	var out []string
	for _, p := range parts {
		for _, x := range p {
			out = append(out, s.fill(x, t))
		}
	}
	return out
}

// Env is the spec's environment with its placeholders filled.
func (s *Spec) Env(t Turn) []string {
	var out []string
	for _, e := range s.Envs {
		out = append(out, s.fill(e, t))
	}
	return out
}

// Prompt is what the harness is sent: the turn's prompt, after the Hive's
// system prompt on a fresh session of a harness that takes none, and with
// the answer's schema spelled out for a harness that has no flag for it.
func (s *Spec) Prompt(t Turn) string {
	p := t.Prompt
	if s.System == "prompt" && !t.Resume && strings.TrimSpace(t.SystemText) != "" {
		p = t.SystemText + "\n\n---\n\n" + p
	}
	if t.Schema != "" && len(s.Argv.Schema) == 0 {
		p += "\n\nEnd your turn with only a JSON object (no code fence, nothing before or after it) that matches this JSON schema:\n" + t.Schema
	}
	return p
}

// PromptArg says the prompt goes in the args rather than on stdin.
func (s *Spec) PromptArg() bool { return s.PromptVia == "arg" }

// AnswersInText says a structured answer comes as the result's text.
func (s *Spec) AnswersInText() bool { return true }

func (s *Spec) fill(x string, t Turn) string {
	if !strings.Contains(x, "{") {
		return x
	}
	return strings.NewReplacer(
		"{session}", t.SessionID, "{model}", t.Model, "{prompt}", s.Prompt(t),
		"{system_file}", t.SystemFile, "{system_text}", t.SystemText, "{run_dir}", t.RunDir,
		"{schema_file}", t.SchemaFile, "{config_dir}", t.ConfigDir,
	).Replace(x)
}

// Parse applies every rule that matches the line, in order.
func (s *Spec) Parse(line []byte) []Event {
	var v any
	if json.Unmarshal(line, &v) != nil {
		return nil
	}
	if _, ok := v.(map[string]any); !ok {
		return nil
	}
	var out []Event
	for _, r := range s.Rules {
		if r.matches(v) {
			out = append(out, r.events(v)...)
		}
	}
	return out
}

func (r Rule) matches(v any) bool {
	for path, want := range r.Match {
		got, ok := first(v, path)
		if not, neg := strings.CutPrefix(want, "!"); neg {
			if ok && str(got) == not {
				return false
			}
		} else if !ok || str(got) != want {
			return false
		}
	}
	return true
}

func (r Rule) events(v any) []Event {
	switch r.Event {
	case "init":
		return []Event{{Kind: "init", SessionID: text(v, r.Session), Model: text(v, r.Model)}}
	case "text":
		var out []Event
		for _, x := range all(v, r.Text) {
			if t := str(x); strings.TrimSpace(t) != "" {
				out = append(out, Event{Kind: "text", Text: t})
			}
		}
		return out
	case "tool":
		return []Event{{Kind: "tool", Tool: text(v, r.Tool), Text: Clip(text(v, r.Text), 160)}}
	case "context":
		if n := num(v, r.Context); n > 0 {
			return []Event{{Kind: "context", Tokens: int(n)}}
		}
	case "usage":
		var out []Event
		if n := num(v, r.Context); n > 0 {
			out = append(out, Event{Kind: "context", Tokens: int(n)})
		}
		if tk := r.Tokens.read(v); tk != nil {
			out = append(out, Event{Kind: "usage", Usage: tk})
		}
		return out
	case "result":
		res := &Result{Text: text(v, r.Text), Subtype: text(v, r.Subtype), CostUSD: num(v, r.Cost), Tokens: r.Tokens.read(v)}
		if b, ok := first(v, r.ErrorPath); r.Error || ok && b == true {
			res.IsError = true
		}
		if res.Subtype == "" {
			res.Subtype = map[bool]string{false: "success", true: "error"}[res.IsError]
		}
		if x, ok := first(v, r.Structured); ok && x != nil {
			res.Structured, _ = json.Marshal(x)
		}
		res.Limited = res.IsError && LooksLimited(res.Text)
		return []Event{{Kind: "result", SessionID: text(v, r.Session), Result: res}}
	}
	return nil
}

func (p *TokenPaths) read(v any) *Tokens {
	if p == nil {
		return nil
	}
	t := &Tokens{Input: int64(num(v, p.Input)), Output: int64(num(v, p.Output)), CacheRead: int64(num(v, p.CacheRead)), CacheWrite: int64(num(v, p.CacheWrite))}
	if p.InputHasCache {
		t.Input = max(t.Input-t.CacheRead, 0)
	}
	if *t == (Tokens{}) {
		return nil
	}
	return t
}

// first is the value at the first of path's alternatives that has one.
func first(v any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	for _, alt := range strings.Split(path, "|") {
		if got := walk(v, strings.Split(alt, ".")); len(got) > 0 {
			return got[0], true
		}
	}
	return nil, false
}

// all is every value at the first of path's alternatives that has any.
func all(v any, path string) []any {
	if path == "" {
		return nil
	}
	for _, alt := range strings.Split(path, "|") {
		if got := walk(v, strings.Split(alt, ".")); len(got) > 0 {
			return got
		}
	}
	return nil
}

func walk(v any, keys []string) []any {
	if len(keys) == 0 {
		if v == nil {
			return nil
		}
		return []any{v}
	}
	k, rest := keys[0], keys[1:]
	switch x := v.(type) {
	case map[string]any:
		switch k {
		case "*":
			var out []any
			for _, key := range sortedKeys(x) {
				out = append(out, walk(x[key], rest)...)
			}
			return out
		case "@key":
			if ks := sortedKeys(x); len(ks) > 0 && len(rest) == 0 {
				return []any{ks[0]}
			}
			return nil
		}
		if c, ok := x[k]; ok {
			return walk(c, rest)
		}
	case []any:
		if k == "*" {
			var out []any
			for _, c := range x {
				out = append(out, walk(c, rest)...)
			}
			return out
		}
		if i, err := strconv.Atoi(k); err == nil && i >= 0 && i < len(x) {
			return walk(x[i], rest)
		}
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func text(v any, path string) string {
	x, _ := first(v, path)
	return str(x)
}

func num(v any, path string) float64 {
	x, _ := first(v, path)
	switch n := x.(type) {
	case float64:
		return n
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

func str(x any) string {
	switch s := x.(type) {
	case nil:
		return ""
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	}
	raw, _ := json.Marshal(x)
	return string(raw)
}

// Display is the harness's name as the person reads it.
func (s *Spec) Display() string {
	if s.Title != "" {
		return s.Title
	}
	return s.ID
}
