package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
var LoginDirEnv = map[string]string{"claude": "CLAUDE_CONFIG_DIR"}

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

// AgentEnv returns environment entries carrying a short-lived credential for login l.
func (t *Tokens) AgentEnv(l Login) ([]string, error) {
	switch l.Harness {
	case "fake":
		return nil, nil
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
	if l.Dir != "" {
		cmd.Env = append(os.Environ(), LoginDirEnv[l.Harness]+"="+l.Dir)
	}
	cmd.Dir = os.TempDir()
	cmd.Stdin = nil
	_ = cmd.Run()
}
