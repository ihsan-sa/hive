package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Tokens hands agents a short-lived credential for their harness, read fresh
// for each turn. Copying a login whole would sign the person out (two holders
// of one refresh token revoke each other), so agents only ever get the access
// token, which expires on its own; the harness's own login on the host keeps
// the refresh token and renews it.
type Tokens struct {
	ws          Workspace
	mu          sync.Mutex
	lastRefresh map[string]time.Time
}

// NewTokens builds the token source.
func NewTokens(ws Workspace) *Tokens { return &Tokens{ws: ws, lastRefresh: map[string]time.Time{}} }

// ErrNoLogin means the harness is not logged in on this machine.
var ErrNoLogin = errors.New("Claude Code is not logged in on this machine")

// Login is one plan runs can go on: the harness's own login on this machine
// (no name), or one the person added with `hive login add`, which keeps its
// own config folder under .hive/logins/<harness>/<name>.
type Login struct {
	Harness string
	Name    string
	Dir     string
}

// Key names the login in plan records; the machine's own login is the harness name.
func (l Login) Key() string {
	if l.Name == "" {
		return l.Harness
	}
	return l.Harness + ":" + l.Name
}

// LoginDirEnv is the variable that points a harness at a config folder of its own.
var LoginDirEnv = map[string]string{"claude": "CLAUDE_CONFIG_DIR", "codex": "CODEX_HOME"}

// HarnessName folds a harness's aliases into one name.
func HarnessName(h string) string {
	if h == "" || h == "claude-code" {
		return "claude"
	}
	return h
}

// LoginsDir is where added logins for harness h live.
func (w Workspace) LoginsDir(h string) string {
	return filepath.Join(w.Hive(), "logins", HarnessName(h))
}

// Logins lists the logins for harness h, the machine's own first.
func (t *Tokens) Logins(h string) []Login {
	h = HarnessName(h)
	out := []Login{{Harness: h}}
	ents, _ := os.ReadDir(t.ws.LoginsDir(h))
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, Login{Harness: h, Name: e.Name(), Dir: filepath.Join(t.ws.LoginsDir(h), e.Name())})
		}
	}
	return out
}

// AgentEnv returns environment entries carrying a short-lived credential for
// login l. Codex reads its credential from a file instead, so for Codex it
// writes one holding only the access token into home, the turn's fresh
// CODEX_HOME.
func (t *Tokens) AgentEnv(l Login, home string) ([]string, error) {
	switch l.Harness {
	case "fake":
		return nil, nil
	case "codex":
		exp, err := writeCodexAccess(l.Dir, home)
		if err == nil && time.Until(exp) < 15*time.Minute {
			t.refresh(l)
			exp, err = writeCodexAccess(l.Dir, home)
		}
		if err == nil && time.Until(exp) <= 0 {
			err = errors.New("Codex login has expired; open Codex once to renew it")
		}
		return nil, err
	case "claude":
		cfg := t.ws.LoadConfig()
		if cfg.APIKeyOptIn && l.Name == "" {
			if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" {
				return []string{"ANTHROPIC_API_KEY=" + k}, nil
			}
		}
		tok, exp, err := readClaudeAccess(l.Dir)
		if err == nil && time.Until(exp) < 15*time.Minute {
			t.refresh(l)
			tok, exp, err = readClaudeAccess(l.Dir)
		}
		if err != nil {
			return nil, err
		}
		if time.Until(exp) <= 0 {
			return nil, errors.New("Claude login has expired; open Claude Code once to renew it")
		}
		return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + tok}, nil
	}
	return nil, errors.New("no token source for harness " + l.Harness)
}

type claudeCreds struct {
	OAuth *struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"` // ms since epoch
	} `json:"claudeAiOauth"`
}

// readClaudeAccess reads only the access token and its expiry from Claude
// Code's own credential store, or from an added login's folder dir. It is
// never logged, printed or stored.
func readClaudeAccess(dir string) (string, time.Time, error) {
	var raw []byte
	var err error
	if dir == "" {
		dir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if runtime.GOOS == "darwin" && dir == "" {
		raw, err = exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	} else {
		if dir == "" {
			dir = filepath.Join(HomeDir(), ".claude")
		}
		raw, err = os.ReadFile(filepath.Join(dir, ".credentials.json"))
		if err != nil && runtime.GOOS == "darwin" {
			// On macOS a login in its own config folder is in the keychain
			// under a name suffixed with the folder's hash.
			sum := sha256.Sum256([]byte(dir))
			raw, err = exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials-"+hex.EncodeToString(sum[:])[:8], "-w").Output()
		}
	}
	if err != nil {
		return "", time.Time{}, ErrNoLogin
	}
	var c claudeCreds
	if json.Unmarshal(raw, &c) != nil || c.OAuth == nil || c.OAuth.AccessToken == "" {
		return "", time.Time{}, ErrNoLogin
	}
	return c.OAuth.AccessToken, time.UnixMilli(c.OAuth.ExpiresAt), nil
}

// writeCodexAccess copies Codex's login from dir (its own CODEX_HOME when
// empty) into home/auth.json with the refresh token blanked, and returns when
// the access token expires.
func writeCodexAccess(dir, home string) (time.Time, error) {
	if dir == "" {
		dir = os.Getenv("CODEX_HOME")
	}
	if dir == "" {
		dir = filepath.Join(HomeDir(), ".codex")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	var a struct {
		Tokens map[string]any `json:"tokens"`
	}
	if err != nil || json.Unmarshal(raw, &a) != nil {
		return time.Time{}, errNoCodex
	}
	tok, _ := a.Tokens["access_token"].(string)
	if tok == "" {
		return time.Time{}, errNoCodex
	}
	a.Tokens["refresh_token"] = ""
	// A fresh last_refresh keeps Codex from trying to renew the token itself.
	out, _ := json.Marshal(map[string]any{"tokens": a.Tokens, "last_refresh": time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(home, "auth.json"), out, 0o600); err != nil {
		return time.Time{}, err
	}
	return jwtExpiry(tok), nil
}

var errNoCodex = errors.New("Codex is not logged in on this machine")

// LoggedIn reports whether login l holds a credential the Hive can read.
func LoggedIn(l Login) bool {
	if l.Harness == "codex" {
		_, err := os.Stat(filepath.Join(l.Dir, "auth.json"))
		return err == nil
	}
	_, _, err := readClaudeAccess(l.Dir)
	return err == nil
}

// jwtExpiry reads a JWT's exp claim without checking its signature; a token
// it can't read counts as good for an hour, and the API has the last word.
func jwtExpiry(tok string) time.Time {
	parts := strings.Split(tok, ".")
	var c struct {
		Exp int64 `json:"exp"`
	}
	if len(parts) == 3 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil && json.Unmarshal(raw, &c) == nil && c.Exp > 0 {
			return time.Unix(c.Exp, 0)
		}
	}
	return time.Now().Add(time.Hour)
}

// refresh asks the host's own harness to renew login l with the smallest
// possible call. At most once every ten minutes per login.
func (t *Tokens) refresh(l Login) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.lastRefresh[l.Key()]) < 10*time.Minute {
		return
	}
	t.lastRefresh[l.Key()] = time.Now()
	cmd := exec.Command("claude", "-p", "Reply with: ok", "--model", "haiku", "--tools", "", "--no-session-persistence", "--strict-mcp-config", "--setting-sources", "")
	if l.Harness == "codex" {
		cmd = exec.Command("codex", "exec", "--ephemeral", "--skip-git-repo-check", "-s", "read-only", "Reply with: ok")
	}
	if l.Dir != "" {
		cmd.Env = append(os.Environ(), LoginDirEnv[l.Harness]+"="+l.Dir)
	}
	cmd.Dir = os.TempDir()
	cmd.Stdin = nil
	_ = cmd.Run()
}
