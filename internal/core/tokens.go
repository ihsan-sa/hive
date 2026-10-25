package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tokens hands agents a short-lived credential for their harness, read fresh
// for each turn. Copying a login whole would sign the person out (two holders
// of one refresh token revoke each other), so agents only ever get the access
// token, which expires on its own; the harness's own login on the host keeps
// the refresh token and renews it.
//
// The host's own logins are renewed by the daemon (Watch) shortly before they
// expire, and each renewal is checked and recorded; one that fails goes to the
// person hours ahead where it can (see Watch and done).
type Tokens struct {
	ws     Workspace
	call   sync.Mutex // one renewal call at a time
	mu     sync.Mutex
	state  map[string]*LoginState
	j      *Journal
	person *Person
}

// NewTokens builds the token source.
func NewTokens(ws Workspace) *Tokens { return &Tokens{ws: ws, state: map[string]*LoginState{}} }

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
	case "open":
		return t.ws.openEnv(l.Name)
	case "codex":
		exp, err := writeCodexAccess(l.Dir, home)
		if err == nil && time.Until(exp) < renewWithin["codex"] {
			t.refresh(l) // a failure is recorded and told; an expired token stops the turn below
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
		if err == nil && time.Until(exp) < renewWithin["claude"] {
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
	if h, err := t.ws.Harnesses().Get(l.Harness); err == nil {
		if s, ok := h.(*harness.Spec); ok {
			return t.ws.specEnv(s)
		}
	}
	return nil, errors.New("no token source for harness " + l.Harness)
}

// ErrNoKey means a module's harness needs an API key in a sandbox and the
// person stored none; a bare run uses the harness's own login on the host.
var ErrNoKey = errors.New("no API key stored")

// HarnessKey is where the API key of a module's harness is kept: under
// .hive/logins, which no run sees.
func (w Workspace) HarnessKey(h string) string {
	return filepath.Join(w.LoginsDir(h), "key")
}

// SaveHarnessKey stores a module harness's API key, readable only by the person.
func (w Workspace) SaveHarnessKey(h, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return errors.New("a key is one non-empty line")
	}
	if err := os.MkdirAll(w.LoginsDir(h), 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(w.HarnessKey(h), []byte(key+"\n"), 0o600)
}

// specEnv hands a module's harness its stored key in the variable its spec
// names, as Claude Code gets its token.
func (w Workspace) specEnv(s *harness.Spec) ([]string, error) {
	if s.KeyEnv == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(w.HarnessKey(s.Name()))
	if k := strings.TrimSpace(string(raw)); err == nil && k != "" {
		return []string{s.KeyEnv + "=" + k}, nil
	}
	if s.KeyOptional {
		return nil, nil
	}
	return nil, fmt.Errorf("%w for the %s harness: the person stores one with `%s harness key %s`, or runs bare on its own login", ErrNoKey, s.Name(), Cmd, s.Name())
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
	if l.Harness == "codex" { // an API-key login has no token to hand a turn
		raw, err := os.ReadFile(filepath.Join(l.Dir, "auth.json"))
		var a struct {
			Tokens struct {
				Access string `json:"access_token"`
			} `json:"tokens"`
		}
		return err == nil && json.Unmarshal(raw, &a) == nil && a.Tokens.Access != ""
	}
	_, _, err := readClaudeAccess(l.Dir)
	return err == nil
}

// jwtExpiry reads a JWT's exp claim without checking its signature; a token
// it can't read counts as good for an hour, and the API has the last word.
func jwtExpiry(tok string) time.Time {
	if exp, ok := jwtExp(tok); ok {
		return exp
	}
	return time.Now().Add(time.Hour)
}

// jwtExp is a JWT's exp claim, if it has one the Hive can read.
func jwtExp(tok string) (time.Time, bool) {
	parts := strings.Split(tok, ".")
	var c struct {
		Exp int64 `json:"exp"`
	}
	if len(parts) == 3 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil && json.Unmarshal(raw, &c) == nil && c.Exp > 0 {
			return time.Unix(c.Exp, 0), true
		}
	}
	return time.Time{}, false
}

// renewWithin is how near its expiry a login is renewed. Claude Code renews
// its token only in the last five minutes, and Codex only once the API turns
// the old one away, so a call earlier would leave the expiry where it is
// (see LEARNINGS.md before widening either).
var renewWithin = map[string]time.Duration{"claude": 5 * time.Minute, "codex": 0}

// renewTimeout bounds one renewal call; checkEvery is how often a login's
// harness is tried between renewals, so a renewal that can't run is told
// hours before the login runs out rather than minutes.
var renewTimeout, checkEvery = 2 * time.Minute, time.Hour

// LoginState is how a login's renewal stands, for the control center.
type LoginState struct {
	Login    string    `json:"login"` // Login.Key
	Expires  time.Time `json:"expires,omitempty"`
	Renewed  time.Time `json:"renewed,omitempty"` // the last renewal that moved the expiry
	Failed   time.Time `json:"failed,omitempty"`  // the last failure since then
	Error    string    `json:"error,omitempty"`
	Failures int       `json:"failures,omitempty"` // in a row
	Run      string    `json:"run,omitempty"`      // what the person runs to renew it by hand
	next     time.Time // no renewal before this: the backoff after a failure
	checked  time.Time // when the harness was last tried
	byCheck  bool      // the failure is the harness not running, not a renewal
	stuckAt  time.Time // the expiry when it failed: a later one was renewed by hand
}

// Track records renewals in journal j and puts a failure to the person
// through p, and takes the last renewal of each login from the record, and
// an unresolved failure with its count, so a restart doesn't tell it afresh.
func (t *Tokens) Track(j *Journal, p *Person) {
	j.Replay(func(e Event) {
		if e.Kind != "login.renewed" && e.Kind != "login.renew_failed" {
			return
		}
		var r struct {
			Login    string    `json:"login"`
			Error    string    `json:"error"`
			Expires  time.Time `json:"expires"`
			Failures int       `json:"failures"`
			ByCheck  bool      `json:"by_check"`
		}
		e.Decode(&r)
		s := t.stateLocked(r.Login)
		s.Expires = r.Expires
		if e.Kind == "login.renewed" {
			s.Renewed, s.Failed, s.Error, s.Failures, s.byCheck, s.stuckAt = e.At, time.Time{}, "", 0, false, time.Time{}
		} else {
			s.Failed, s.Error, s.Failures, s.byCheck, s.stuckAt = e.At, r.Error, max(r.Failures, 1), r.ByCheck, r.Expires
		}
	})
	t.mu.Lock()
	t.j, t.person = j, p
	t.mu.Unlock()
}

// stateLocked is login key's state; t.mu is held, or nothing else runs yet.
func (t *Tokens) stateLocked(key string) *LoginState {
	s := t.state[key]
	if s == nil {
		s = &LoginState{Login: key}
		t.state[key] = s
	}
	return s
}

// States is each login's renewal state, by key.
func (t *Tokens) States() []LoginState {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []LoginState
	for _, s := range t.state {
		if !s.Expires.IsZero() || !s.Renewed.IsZero() || s.Error != "" {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Login < out[j].Login })
	return out
}

// Retry is the person saying they renewed login key by hand: the next look
// renews or tries it at once, and a failure then is told afresh.
func (t *Tokens) Retry(key string) {
	t.mu.Lock()
	s := t.stateLocked(key)
	s.next, s.checked, s.Failures = time.Time{}, time.Time{}, 0
	t.mu.Unlock()
}

// WatchLogins keeps the machine's logins renewed until ctx ends, looking
// every poll. A dev Hive on the fake harness leaves the person's logins alone.
func (c *Core) WatchLogins(ctx context.Context, poll time.Duration) {
	for {
		if HarnessName(c.WS.LoadConfig().Harness) != "fake" {
			c.Tokens.Watch()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
}

// Watch renews each Claude and Codex login the Hive can read once it nears
// its expiry, whether or not a turn is starting, and tries the harness of the
// others once an hour. A harness no route sends turns to is left alone: its
// login is the person's business, not the Hive's.
func (t *Tokens) Watch() {
	cfg := t.ws.LoadConfig()
	for _, h := range []string{"claude", "codex"} {
		if !cfg.Uses(h) {
			continue
		}
		for _, l := range t.Logins(h) {
			exp, err := loginExpiry(l)
			if err != nil {
				continue // not logged in: nothing to renew
			}
			now := time.Now()
			t.mu.Lock()
			s := t.stateLocked(l.Key())
			s.Expires = exp
			byHand := s.Error != "" && !s.byCheck && !s.stuckAt.IsZero() && exp.After(s.stuckAt)
			due := now.Sub(s.checked) >= checkEvery || s.byCheck && !now.Before(s.next)
			if due {
				s.checked = now
			}
			t.mu.Unlock()
			switch {
			case byHand: // the person (or the harness in a turn) renewed it since it failed
				t.done(l, exp, nil, renewedByHand)
			case exp.Sub(now) < renewWithin[h]:
				t.refresh(l)
			case due:
				t.done(l, exp, t.tryHarness(h), tried)
			}
		}
	}
}

// loginExpiry is when login l's access token runs out.
func loginExpiry(l Login) (time.Time, error) {
	if l.Harness == "codex" {
		dir := l.Dir
		if dir == "" {
			dir = os.Getenv("CODEX_HOME")
		}
		if dir == "" {
			dir = filepath.Join(HomeDir(), ".codex")
		}
		raw, err := os.ReadFile(filepath.Join(dir, "auth.json"))
		var a struct {
			Tokens struct {
				Access string `json:"access_token"`
			} `json:"tokens"`
		}
		if err != nil || json.Unmarshal(raw, &a) != nil || a.Tokens.Access == "" {
			return time.Time{}, errNoCodex
		}
		// A token whose expiry can't be read can't show a renewal moved it.
		if exp, ok := jwtExp(a.Tokens.Access); ok {
			return exp, nil
		}
		return time.Time{}, errors.New("Codex's access token has no expiry the Hive can read")
	}
	_, exp, err := readClaudeAccess(l.Dir)
	return exp, err
}

// refresh asks the host's own harness to renew login l with the smallest
// possible call, and checks that it did: the call exits 0 and the login's
// expiry moves forward. After a failure the next try waits a minute, doubling
// to fifteen. Every outcome is recorded; see done.
func (t *Tokens) refresh(l Login) error {
	t.call.Lock()
	defer t.call.Unlock()
	t.mu.Lock()
	wait := time.Now().Before(t.stateLocked(l.Key()).next)
	t.mu.Unlock()
	if wait {
		return nil
	}
	before, err := loginExpiry(l)
	if err == nil {
		err = t.renew(l, before)
	}
	after, _ := loginExpiry(l)
	t.done(l, after, err, renewed)
	return err
}

// renew runs the call that renews login l, whose token ran out at before.
func (t *Tokens) renew(l Login, before time.Time) error {
	bin, err := t.harnessBin(l.Harness)
	if err != nil {
		return err
	}
	args := []string{"-p", "Reply with: ok", "--model", "haiku", "--tools", "", "--no-session-persistence", "--strict-mcp-config", "--setting-sources", ""}
	if l.Harness == "codex" {
		args = []string{"exec", "--ephemeral", "--skip-git-repo-check", "-s", "read-only", "Reply with: ok"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), renewTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = binEnv(bin)
	if l.Dir != "" {
		cmd.Env = append(cmd.Env, LoginDirEnv[l.Harness]+"="+l.Dir)
	}
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("`%s` exited with %v: %s", bin, err, harnessSaid(out))
	}
	after, err := loginExpiry(l)
	if err != nil {
		return fmt.Errorf("`%s` ran, but the login can't be read after it: %v", bin, err)
	}
	if !after.After(before) {
		return fmt.Errorf("`%s` exited 0 but the login's expiry didn't move from %s: %s", bin, before.Local().Format("Mon 15:04"), harnessSaid(out))
	}
	return nil
}

// tryHarness runs harness h's program with --version: that it is found and
// runs is most of what a renewal needs, and it costs nothing.
func (t *Tokens) tryHarness(h string) error {
	bin, err := t.harnessBin(h)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), renewTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env, cmd.Dir = binEnv(bin), os.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("`%s --version` exited with %v: %s", bin, err, harnessSaid(out))
	}
	return nil
}

// binEnv is the daemon's environment with bin's folder first on PATH, so a
// launcher script finds what's installed beside it (Node, for Codex).
func binEnv(bin string) []string {
	env := []string{"PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	return env
}

// binDirs are where a harness is commonly installed, for a daemon whose PATH
// leaves them out: a systemd unit's has no ~/.local/bin.
var binDirs = func() []string {
	h := HomeDir()
	return []string{filepath.Join(h, ".local", "bin"), filepath.Join(h, ".claude", "local"), filepath.Join(h, ".npm-global", "bin"),
		filepath.Join(h, ".bun", "bin"), filepath.Join(h, "bin"), "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/snap/bin"}
}

var errNoBin = errors.New("not found")

// harnessBin is the absolute path of harness h's program: on the daemon's
// PATH, else in one of binDirs. It runs as the person, outside any sandbox,
// so one in a folder a run can write (a checkout, a run's home, the Hive's
// tools) is refused rather than run.
func (t *Tokens) harnessBin(h string) (string, error) {
	p, err := exec.LookPath(h)
	if err == nil {
		p, err = filepath.Abs(p)
	}
	for _, d := range binDirs() {
		if err == nil {
			break
		}
		p, err = exec.LookPath(filepath.Join(d, h))
	}
	if err != nil {
		return "", fmt.Errorf("`%s` %w on the Hive's PATH (%s) or in %s", h, errNoBin, os.Getenv("PATH"), strings.Join(binDirs(), ", "))
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		real = p
	}
	for _, d := range []string{t.ws.Hive(), t.ws.Projects()} {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		if within(real, d) {
			return "", fmt.Errorf("`%s` is %s, inside %s where agents can write; the Hive won't run it as you", h, real, d)
		}
	}
	return p, nil
}

// within reports whether path p is dir or inside it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// tokenShaped is what in a harness's output could be a credential: a JWT,
// a bearer value, a known key prefix, or a long run of letters and digits.
var tokenShaped = regexp.MustCompile(`(?i)\bbearer\s+\S+|\b(sk-|ghp_|github_pat_)[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*|[A-Za-z0-9+/_=-]{32,}`)

// harnessSaid is a harness's output fit for the journal every agent reads:
// clipped, with anything token-shaped taken out.
func harnessSaid(out []byte) string {
	s := tokenShaped.ReplaceAllStringFunc(string(out), func(m string) string {
		if len(m) >= 32 && !strings.ContainsAny(m, "0123456789") && !strings.HasPrefix(strings.ToLower(m), "bearer") {
			return m // a long word or path, not a key
		}
		return "[secret]"
	})
	return Clip(s, 300)
}

// What done records: a renewal the Hive ran, a try of the harness, or an
// expiry that moved on its own after a failure.
const (
	renewed = iota
	tried
	renewedByHand
)

// done records how a renewal or a try of the harness (how) of login l went;
// exp is its expiry after. A failure goes to the journal and the daemon's log
// every time, and the first of a run of them raises an alert and asks the
// person to renew the login by hand, with what to run. A success after
// failures says so in the same alert and closes the ask.
func (t *Tokens) done(l Login, exp time.Time, err error, how int) {
	now := time.Now()
	key := l.Key()
	t.mu.Lock()
	s := t.stateLocked(key)
	if !exp.IsZero() {
		s.Expires = exp
	}
	first, recovered := false, false
	switch {
	case err != nil:
		s.Failures++
		first = s.Failures == 1
		s.Failed, s.Error, s.Run, s.byCheck, s.stuckAt = now, err.Error(), renewByHand(l, err), how == tried, s.Expires
		s.next = now.Add(time.Minute << min(s.Failures-1, 4))
		if s.next.Sub(now) > 15*time.Minute {
			s.next = now.Add(15 * time.Minute)
		}
	case how != tried:
		recovered = s.Error != ""
		s.Renewed, s.Failed, s.Error, s.Run, s.Failures, s.byCheck, s.stuckAt = now, time.Time{}, "", "", 0, false, time.Time{}
		s.next = now.Add(time.Minute)
	case s.byCheck: // the harness runs again; nothing was renewed
		recovered = s.Error != ""
		s.Failed, s.Error, s.Run, s.Failures, s.byCheck, s.next = time.Time{}, "", "", 0, false, time.Time{}
	}
	c := *s
	j, p := t.j, t.person
	t.mu.Unlock()

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s login %s: renewal failed (%d in a row): %v\n", now.Format(time.RFC3339), key, c.Failures, err)
	}
	if j == nil {
		return
	}
	switch {
	case err != nil:
		j.MustAppend("login.renew_failed", map[string]any{"login": key, "error": c.Error, "expires": c.Expires, "failures": c.Failures, "by_check": c.byCheck})
	case how == renewed:
		j.MustAppend("login.renewed", map[string]any{"login": key, "expires": c.Expires})
	case how == renewedByHand:
		j.MustAppend("login.renewed", map[string]any{"login": key, "expires": c.Expires, "by": "hand"})
	}
	if first {
		title := fmt.Sprintf("The %s login can't renew itself; it %s", key, runsOut(c.Expires, now))
		j.MustAppend("alert.raised", map[string]string{"kind": "login", "ref": "login:" + key, "text": title + ": " + c.Error})
		if p != nil {
			p.Ask(Ask{Kind: "login", Ref: key, Options: []string{"done"}, Title: title,
				Detail: "What failed: " + c.Error + "\n\nTo renew it by hand: " + c.Run + "\n\nAnswer done once you have, and the Hive tries again at once. It keeps trying on its own meanwhile."})
		}
	}
	if recovered {
		text, why := "The %s login renews itself again; it %s", "renewed on its own"
		if how == renewedByHand {
			text, why = "The %s login was renewed outside the Hive; it %s", "renewed outside the Hive"
		}
		j.MustAppend("alert.raised", map[string]string{"kind": "login", "ref": "login:" + key, "text": fmt.Sprintf(text, key, runsOut(c.Expires, now))})
		if p != nil {
			for _, a := range p.Open() {
				if a.Kind == "login" && a.Ref == key {
					p.Answer(a.ID, "done", why)
				}
			}
		}
	}
}

// runsOut says when a login runs out, or ran out.
func runsOut(exp, now time.Time) string {
	if exp.IsZero() {
		return "may have run out"
	}
	if !exp.After(now) {
		return "ran out " + exp.Local().Format("Mon 15:04")
	}
	return fmt.Sprintf("runs out %s, in %s", exp.Local().Format("Mon 15:04"), exp.Sub(now).Round(time.Minute))
}

// renewByHand is what the person runs to renew login l after err.
func renewByHand(l Login, err error) string {
	name := map[string]string{"claude": "Claude Code", "codex": "Codex"}[l.Harness]
	if errors.Is(err, errNoBin) {
		return fmt.Sprintf("install %s, or link its `%s` into ~/.local/bin, where the Hive looks whatever its PATH", name, l.Harness)
	}
	login := map[string]string{"claude": "claude auth login", "codex": "codex login --device-auth"}[l.Harness]
	if l.Dir != "" {
		return fmt.Sprintf("run `%s=%s %s` in a terminal on this machine", LoginDirEnv[l.Harness], l.Dir, login)
	}
	return fmt.Sprintf("run `%s` once in a terminal on this machine, and `%s` if it says you're logged out", l.Harness, login)
}

// OpenKey is where the API key of the named open model is kept: under
// .hive/logins, which no run sees.
func (w Workspace) OpenKey(name string) string {
	return filepath.Join(w.LoginsDir("open"), filepath.Base(name)+".key")
}

// SaveOpenKey stores a model's API key, readable only by the person.
func (w Workspace) SaveOpenKey(name, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("an empty key")
	}
	if err := os.MkdirAll(w.LoginsDir("open"), 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(w.OpenKey(name), []byte(key+"\n"), 0o600)
}

// openEnv is what the open harness needs for model name: its endpoint, the
// id the endpoint knows, how much it holds, and its key if it has one.
func (w Workspace) openEnv(name string) ([]string, error) {
	m, ok := w.LoadConfig().Models[name]
	if !ok {
		return nil, fmt.Errorf("no model %q in models (.hive/config.json)", name)
	}
	env := []string{"HIVE_OPEN_ENDPOINT=" + m.Endpoint, "HIVE_OPEN_MODEL=" + m.Model}
	if m.Context > 0 {
		env = append(env, fmt.Sprintf("HIVE_OPEN_CONTEXT=%d", m.Context))
	}
	if m.Price != nil {
		env = append(env, fmt.Sprintf("HIVE_OPEN_PRICE=%g,%g", m.Price.Input, m.Price.Output))
	}
	raw, err := os.ReadFile(w.OpenKey(name))
	switch {
	case err == nil && strings.TrimSpace(string(raw)) != "":
		env = append(env, "HIVE_OPEN_KEY="+strings.TrimSpace(string(raw)))
	case m.Key:
		return nil, fmt.Errorf("model %s needs an API key: the person stores it with `%s model key %s`", name, Cmd, name)
	}
	return env, nil
}
