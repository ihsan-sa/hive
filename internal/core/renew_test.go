package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// renewKit is a workspace with one added Claude login, a fake `claude` the
// daemon's PATH doesn't hold, and the journal and person a renewal reports to.
type renewKit struct {
	t     *testing.T
	tk    *Tokens
	j     *Journal
	p     *Person
	login string // the added login's folder
	bin   string // the folder the fake claude is in
}

func newRenewKit(t *testing.T, expires time.Time) *renewKit {
	if runtime.GOOS == "windows" {
		t.Skip("the fake harness is a shell script")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // the machine's own login: none
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // like a service's PATH: no claude on it
	ws := Workspace{Root: t.TempDir()}
	k := &renewKit{t: t, login: filepath.Join(ws.LoginsDir("claude"), "work"), bin: t.TempDir()}
	os.MkdirAll(k.login, 0o700)
	k.writeCreds(expires)
	old := binDirs
	binDirs = func() []string { return []string{filepath.Join(k.bin, "nothing-here"), k.bin} }
	t.Cleanup(func() { binDirs = old })
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	k.j, k.p, k.tk = j, NewPerson(j), NewTokens(ws)
	k.tk.Track(j, k.p)
	return k
}

func (k *renewKit) writeCreds(exp time.Time) {
	raw := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok","expiresAt":%d}}`, exp.UnixMilli())
	if err := os.WriteFile(filepath.Join(k.login, ".credentials.json"), []byte(raw), 0o600); err != nil {
		k.t.Fatal(err)
	}
}

// harness installs a fake claude whose renewal call runs body; --version
// always works. Each renewal call is counted in calls.
func (k *renewKit) harness(body string) {
	script := "#!/bin/sh\ncase \"$1\" in --version) echo 2.0.0; exit 0;; esac\necho x >> \"$CLAUDE_CONFIG_DIR/calls\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(k.bin, "claude"), []byte(script), 0o755); err != nil {
		k.t.Fatal(err)
	}
}

// renews is a body that moves the login's expiry to exp.
func renews(exp time.Time) string {
	return fmt.Sprintf(`printf '{"claudeAiOauth":{"accessToken":"new","expiresAt":%d}}' > "$CLAUDE_CONFIG_DIR/.credentials.json"; echo ok`, exp.UnixMilli())
}

// skipBackoff lets the next look try at once, as the backoff's end would.
func (k *renewKit) skipBackoff() {
	k.tk.mu.Lock()
	s := k.tk.stateLocked("claude:work")
	s.next, s.checked = time.Time{}, time.Time{}
	k.tk.mu.Unlock()
}

func (k *renewKit) calls() int {
	raw, _ := os.ReadFile(filepath.Join(k.login, "calls"))
	return strings.Count(string(raw), "x")
}

func (k *renewKit) state() LoginState {
	for _, s := range k.tk.States() {
		if s.Login == "claude:work" {
			return s
		}
	}
	return LoginState{}
}

// events counts the journal's events of kind, and of alerts the login ones.
func (k *renewKit) events(kind string) (n int, last string) {
	k.j.Replay(func(e Event) {
		if e.Kind != kind {
			return
		}
		var a struct{ Kind, Text string }
		e.Decode(&a)
		if kind == "alert.raised" && a.Kind != "login" {
			return
		}
		n, last = n+1, a.Text
	})
	return n, last
}

func (k *renewKit) asks() []Ask {
	var out []Ask
	for _, a := range k.p.Open() {
		if a.Kind == "login" {
			out = append(out, a)
		}
	}
	return out
}

// The daemon renews a login in its last minutes even with the harness off
// its PATH, found where it's commonly installed, and checks the expiry moved.
func TestRenewFindsTheHarnessOffPath(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(2*time.Minute))
	later := time.Now().Add(8 * time.Hour).Truncate(time.Millisecond)
	k.harness(renews(later))
	if p, err := k.tk.harnessBin("claude"); err != nil || p != filepath.Join(k.bin, "claude") {
		t.Fatalf("claude is found by its full path off PATH: %q %v", p, err)
	}
	k.tk.Watch()
	s := k.state()
	if k.calls() != 1 || s.Error != "" || !s.Expires.Equal(later) || time.Since(s.Renewed) > time.Minute {
		t.Fatalf("the login is renewed: calls %d, %+v", k.calls(), s)
	}
	if n, _ := k.events("login.renewed"); n != 1 {
		t.Fatalf("the renewal is on record, got %d", n)
	}
	if n, _ := k.events("alert.raised"); n != 0 || len(k.asks()) != 0 {
		t.Fatal("a renewal that worked raises nothing")
	}
	// Its last renewal survives a restart.
	tk := NewTokens(k.tk.ws)
	tk.Track(k.j, k.p)
	if st := tk.States(); len(st) != 1 || st[0].Renewed.IsZero() {
		t.Fatalf("the last renewal is read back from the record: %+v", st)
	}
}

// A renewal that exits non-zero is recorded every time, raises one alert and
// one ask with what to run, backs off, and clears both once it works.
func TestRenewThatFailsIsToldOnceAndBacksOff(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(3*time.Minute))
	k.harness(`echo "OAuth token has been revoked" >&2; exit 1`)
	k.tk.Watch()
	s := k.state()
	if !strings.Contains(s.Error, "exited") || !strings.Contains(s.Error, "revoked") || s.Failures != 1 {
		t.Fatalf("the failure carries the exit and what it said: %+v", s)
	}
	if n, text := k.events("alert.raised"); n != 1 || !strings.Contains(text, "can't renew") {
		t.Fatalf("one alert, got %d: %s", n, text)
	}
	asks := k.asks()
	if len(asks) != 1 || !strings.Contains(asks[0].Detail, "CLAUDE_CONFIG_DIR="+k.login+" claude auth login") {
		t.Fatalf("the person is asked with what to run: %+v", asks)
	}
	k.tk.Watch()
	if k.calls() != 1 {
		t.Fatalf("a failure backs off before the next try, got %d calls", k.calls())
	}
	k.skipBackoff()
	k.tk.Watch()
	if k.calls() != 2 || k.state().Failures != 2 {
		t.Fatalf("after the backoff it tries again: calls %d, %+v", k.calls(), k.state())
	}
	if n, _ := k.events("login.renew_failed"); n != 2 {
		t.Fatalf("every failure is on record, got %d", n)
	}
	if n, _ := k.events("alert.raised"); n != 1 || len(k.asks()) != 1 {
		t.Fatal("a repeat raises no second alert or ask")
	}

	// The person says they renewed it, but it still fails: they hear again.
	if _, err := k.p.Answer(k.asks()[0].ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	k.tk.Retry("claude:work")
	k.tk.Watch()
	if n, _ := k.events("alert.raised"); n != 2 || len(k.asks()) != 1 || k.calls() != 3 {
		t.Fatalf("a failure after the person's done is told afresh: %d alerts, %d asks, %d calls", n, len(k.asks()), k.calls())
	}

	k.harness(renews(time.Now().Add(8 * time.Hour)))
	k.skipBackoff()
	k.tk.Watch()
	if s := k.state(); s.Error != "" || s.Renewed.IsZero() {
		t.Fatalf("it renews once it can: %+v", s)
	}
	if n, text := k.events("alert.raised"); n != 3 || !strings.Contains(text, "renews itself again") {
		t.Fatalf("the alert says it's fixed: %d %s", n, text)
	}
	if len(k.asks()) != 0 {
		t.Fatal("the ask closes once it renews on its own")
	}
}

// A call that exits 0 but leaves the expiry where it was didn't renew it.
func TestRenewThatDoesntMoveTheExpiryFails(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(time.Minute))
	k.harness("echo ok")
	if err := k.tk.refresh(Login{Harness: "claude", Name: "work", Dir: k.login}); err == nil || !strings.Contains(err.Error(), "didn't move") {
		t.Fatalf("an unmoved expiry is a failure: %v", err)
	}
	if n, _ := k.events("alert.raised"); n != 1 || len(k.asks()) != 1 {
		t.Fatal("it is told like any other failure")
	}
}

// A login hours from its expiry isn't renewed yet, but its harness is tried:
// one that can't be found is told at once, hours ahead, and once.
func TestRenewThatCantRunIsToldHoursAhead(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(6*time.Hour))
	k.harness(renews(time.Now().Add(14 * time.Hour)))
	k.tk.Watch()
	if k.calls() != 0 || k.state().Error != "" {
		t.Fatalf("a login hours from expiry with a working harness is left alone: %d %+v", k.calls(), k.state())
	}

	os.Remove(filepath.Join(k.bin, "claude"))
	k.skipBackoff() // the hourly try, now
	k.tk.Watch()
	n, text := k.events("alert.raised")
	asks := k.asks()
	if n != 1 || len(asks) != 1 || !strings.Contains(text, "in 6h") {
		t.Fatalf("a harness that can't be found is told hours before the expiry: %d %q %+v", n, text, asks)
	}
	if !strings.Contains(asks[0].Detail, "~/.local/bin") || !strings.Contains(k.state().Run, "install Claude Code") {
		t.Fatalf("the ask says what to do: %+v", asks[0])
	}
	k.skipBackoff()
	k.tk.Watch()
	if n, _ := k.events("alert.raised"); n != 1 {
		t.Fatal("it is told once")
	}

	k.harness(renews(time.Now().Add(14 * time.Hour)))
	k.skipBackoff()
	k.tk.Watch()
	if k.state().Error != "" || len(k.asks()) != 0 || k.calls() != 0 {
		t.Fatalf("once the harness is back the alert clears, with nothing renewed early: %+v", k.state())
	}
}

// A restart reads an unresolved failure back with its count, so the same
// broken login isn't told afresh; a harness that's back clears it.
func TestRenewFailureSurvivesARestart(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(6*time.Hour))
	k.skipBackoff()
	k.tk.Watch() // the harness isn't installed: the hourly try fails
	if n, _ := k.events("alert.raised"); n != 1 {
		t.Fatalf("one alert, got %d", n)
	}
	k.tk = NewTokens(k.tk.ws)
	k.tk.Track(k.j, k.p)
	if s := k.state(); s.Failures != 1 || s.Error == "" {
		t.Fatalf("the failure and its count are read back: %+v", s)
	}
	k.tk.Watch()
	if n, _ := k.events("alert.raised"); n != 1 || len(k.asks()) != 1 {
		t.Fatalf("the same failure after a restart raises nothing new: %d alerts, %d asks", n, len(k.asks()))
	}
	k.harness("exit 1")
	k.skipBackoff()
	k.tk.Watch()
	if s := k.state(); s.Error != "" || len(k.asks()) != 0 || k.calls() != 0 {
		t.Fatalf("a harness back after a restart clears the restored failure: %+v", s)
	}
}

// A login renewed by hand after a failed renewal clears the failure at the
// next look, without waiting for the Hive's own next renewal.
func TestRenewedByHandClearsTheFailure(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(3*time.Minute))
	k.harness(`echo "refresh token revoked" >&2; exit 1`)
	k.tk.Watch()
	if len(k.asks()) != 1 {
		t.Fatal("the failure is asked about")
	}
	k.writeCreds(time.Now().Add(8 * time.Hour)) // the person runs claude by hand
	k.tk.Watch()
	if s := k.state(); s.Error != "" || k.calls() != 1 {
		t.Fatalf("a moved expiry clears the failure with no renewal run: calls %d, %+v", k.calls(), s)
	}
	if n, text := k.events("alert.raised"); n != 2 || !strings.Contains(text, "outside the Hive") || len(k.asks()) != 0 {
		t.Fatalf("the person hears it's fixed and the ask closes: %d %q", n, text)
	}
	tk := NewTokens(k.tk.ws)
	tk.Track(k.j, k.p)
	if st := tk.States(); len(st) != 1 || st[0].Error != "" {
		t.Fatalf("a restart doesn't bring the failure back: %+v", st)
	}
}

// A harness no route uses is neither renewed nor tried: a Codex login with no
// codex installed raises nothing on a Claude Hive.
func TestRenewLeavesUnusedHarnessesAlone(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(6*time.Hour))
	k.harness("exit 1")
	codex := filepath.Join(k.tk.ws.LoginsDir("codex"), "spare")
	os.MkdirAll(codex, 0o700)
	os.WriteFile(filepath.Join(codex, "auth.json"), []byte(`{"tokens":{"access_token":"`+jwt(time.Now().Add(-time.Hour))+`"}}`), 0o600)
	k.tk.Watch()
	for _, s := range k.tk.States() {
		if strings.HasPrefix(s.Login, "codex") {
			t.Fatalf("an unused harness's login is left alone: %+v", s)
		}
	}
	if n, _ := k.events("alert.raised"); n != 0 {
		t.Fatal("nothing is raised")
	}
	if !(Config{Harness: "claude"}).Uses("claude") || (Config{Harness: "claude"}).Uses("codex") {
		t.Fatal("Uses follows the harness setting")
	}
	if !(Config{Harness: "claude", Routes: map[string][]string{"fallback": {"codex:gpt-5"}}}).Uses("codex") {
		t.Fatal("Uses follows the fallback list")
	}
}

func jwt(exp time.Time) string {
	body := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	return "eyJhbGciOiJub25lIn0." + body + ".sig"
}

// A Codex token whose expiry can't be read can't show a renewal moved it.
func TestRenewNeedsAReadableCodexExpiry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"tokens":{"access_token":"opaque"}}`), 0o600)
	if _, err := loginExpiry(Login{Harness: "codex", Name: "x", Dir: dir}); err == nil {
		t.Fatal("an unreadable expiry is an error")
	}
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"tokens":{"access_token":"`+jwt(exp)+`"}}`), 0o600)
	if got, err := loginExpiry(Login{Harness: "codex", Name: "x", Dir: dir}); err != nil || !got.Equal(exp) {
		t.Fatalf("a JWT's expiry is read: %v %v", got, err)
	}
}

// What a harness printed goes into the journal without anything token-shaped.
func TestHarnessSaidScrubsTokens(t *testing.T) {
	out := "error: Authorization: Bearer abc.def refresh sk-ant-ort01-AbCdEf123456 " + jwt(time.Now()) + " key 9f8e7d6c5b4a39281706f5e4d3c2b1a0ffeeddcc /usr/local/lib/node_modules/whatever-package-name"
	got := harnessSaid([]byte(out))
	for _, bad := range []string{"abc.def", "sk-ant", "eyJ", "9f8e7d6c"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q is scrubbed: %s", bad, got)
		}
	}
	if !strings.Contains(got, "error:") || !strings.Contains(got, "whatever-package-name") {
		t.Fatalf("the rest is kept: %s", got)
	}
}

// A harness in a folder agents can write is never run as the person.
func TestHarnessBinRefusesAgentWritableFolders(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(6*time.Hour))
	inside := filepath.Join(k.tk.ws.Hive(), "tools", "bin")
	os.MkdirAll(inside, 0o755)
	os.WriteFile(filepath.Join(inside, "claude"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", inside)
	if _, err := k.tk.harnessBin("claude"); err == nil || !strings.Contains(err.Error(), "agents can write") {
		t.Fatalf("a harness under .hive is refused: %v", err)
	}
}

// A harness check killed on a loaded machine is retried before the person
// hears of it: it is told only once it repeats askAfter times, and a check
// that runs again in between clears it without a word.
func TestKilledCheckIsRetriedBeforeTheAsk(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(4*time.Hour))
	killed := "#!/bin/sh\nkill -9 $$\n"
	os.WriteFile(filepath.Join(k.bin, "claude"), []byte(killed), 0o755)
	k.skipBackoff()
	k.tk.Watch()
	if s := k.state(); s.Failures != 1 || !strings.Contains(s.Error, "killed") {
		t.Fatalf("the killed check is a failure on record: %+v", s)
	}
	if n, _ := k.events("alert.raised"); n != 0 || len(k.asks()) != 0 {
		t.Fatal("one killed check, hours from the expiry, isn't put to the person")
	}
	k.harness(renews(time.Now().Add(14 * time.Hour)))
	k.skipBackoff()
	k.tk.Watch()
	if n, _ := k.events("alert.raised"); n != 0 || k.state().Error != "" {
		t.Fatalf("a check that runs again clears it quietly: %d alerts, %+v", n, k.state())
	}

	os.WriteFile(filepath.Join(k.bin, "claude"), []byte(killed), 0o755)
	for i := 1; i < askAfter; i++ {
		k.skipBackoff()
		k.tk.Watch()
	}
	if n, _ := k.events("alert.raised"); n != 0 {
		t.Fatalf("not yet told after %d killed checks", askAfter-1)
	}
	k.skipBackoff()
	k.tk.Watch()
	if n, _ := k.events("alert.raised"); n != 1 || len(k.asks()) != 1 {
		t.Fatalf("told once it repeats %d times: %d alerts, %d asks", askAfter, n, len(k.asks()))
	}
	k.skipBackoff()
	k.tk.Watch()
	if n, _ := k.events("alert.raised"); n != 1 {
		t.Fatal("and told once")
	}
}

// A killed check with the login near its expiry is put to the person at once.
func TestKilledCheckNearExpiryIsToldAtOnce(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(30*time.Minute))
	os.WriteFile(filepath.Join(k.bin, "claude"), []byte("#!/bin/sh\nkill -9 $$\n"), 0o755)
	k.skipBackoff()
	k.tk.Watch()
	if n, _ := k.events("alert.raised"); n != 1 || len(k.asks()) != 1 {
		t.Fatalf("near the expiry there's no time to wait: %d alerts", n)
	}
}

// A renewal turned away by the plan's usage limit isn't a failure: the login
// is parked until the limit resets, nothing is asked of the person (logging
// in again changes nothing), an ask an earlier failure left open closes, a
// restart keeps it parked, and after the reset it renews as before.
func TestRenewAtItsUsageLimitIsParkedWithNoAsk(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(3*time.Minute))
	k.harness(`echo "refresh token revoked" >&2; exit 1`)
	k.tk.Watch()
	if len(k.asks()) != 1 {
		t.Fatal("a real failure is asked about")
	}
	reset := time.Now().Add(50 * time.Hour).In(time.UTC)
	said := "You've hit your weekly limit · resets " + reset.Format("Jan 2, 3pm") + " (UTC)"
	k.harness(fmt.Sprintf(`echo %q; exit 1`, said))
	k.skipBackoff()
	k.tk.Watch()
	s := k.state()
	want := reset.Truncate(time.Hour)
	if s.Error != "" || s.Failures != 0 || !s.Limited.Equal(want) {
		t.Fatalf("the login is parked until its reset %s, not failing: %+v", want, s)
	}
	if len(k.asks()) != 0 {
		t.Fatalf("the open ask closes and none is raised: %+v", k.asks())
	}
	if _, last := k.events("alert.raised"); !strings.Contains(last, "at its usage limit until") {
		t.Fatalf("the failure's alert gives way to the park: %q", last)
	}
	if n, _ := k.events("login.limited"); n != 1 {
		t.Fatalf("the park is on record once, got %d", n)
	}
	calls := k.calls()
	k.tk.Watch()
	k.tk = NewTokens(k.tk.ws)
	k.tk.Track(k.j, k.p)
	k.tk.Watch()
	if k.calls() != calls || len(k.asks()) != 0 || !k.state().Limited.Equal(want) {
		t.Fatalf("parked, even across a restart, it isn't tried before its reset: calls %d→%d, %+v", calls, k.calls(), k.state())
	}

	k.harness(renews(time.Now().Add(8 * time.Hour)))
	k.skipBackoff() // the reset has come
	k.tk.Watch()
	if s := k.state(); s.Error != "" || s.Renewed.IsZero() || !s.Limited.IsZero() {
		t.Fatalf("after the reset it renews and is no longer parked: %+v", s)
	}
}

// A limit that doesn't say when it resets parks the login for limitWait.
func TestRenewAtALimitWithNoResetWaits(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(3*time.Minute))
	k.harness(`echo "Claude AI usage limit reached" >&2; exit 1`)
	k.tk.Watch()
	if s := k.state(); s.Error != "" || time.Until(s.Limited) < limitWait-time.Minute || len(k.asks()) != 0 {
		t.Fatalf("parked for limitWait with no ask: %+v", s)
	}
}

// A limit that names no reset is journaled once, not on each hourly try; a
// reset far off (a misread date) parks the login no longer than limitMost; a
// failed check of the harness while parked leaves it parked, live and after
// a restart alike; and a parked login isn't handed to turns.
func TestRenewParkIsBoundedAndReplaysAsLive(t *testing.T) {
	k := newRenewKit(t, time.Now().Add(3*time.Minute))
	k.harness(`echo "Claude AI usage limit reached" >&2; exit 1`)
	k.tk.Watch()
	k.skipBackoff()
	k.tk.Watch()
	if n, _ := k.events("login.limited"); n != 1 || k.calls() != 2 {
		t.Fatalf("two tries of a limit with no reset, journaled once: %d events, %d calls", n, k.calls())
	}

	far := time.Now().AddDate(0, 5, 0).In(time.UTC)
	k.harness(fmt.Sprintf(`echo %q; exit 1`, "You've hit your weekly limit · resets "+far.Format("Jan 2, 3pm")+" (UTC)"))
	k.skipBackoff()
	k.tk.Watch()
	until := k.state().Limited
	if d := time.Until(until); d > limitMost || d < limitMost-time.Minute {
		t.Fatalf("a reset months away parks it for limitMost, got %v", d)
	}
	if !k.tk.LimitedUntil("claude:work").Equal(until) {
		t.Fatal("LimitedUntil reads the park")
	}

	k.tk.done(Login{Harness: "claude", Name: "work"}, time.Time{}, errors.New("claude --version: exit 1"), tried)
	if s := k.state(); !s.Limited.Equal(until) {
		t.Fatalf("a failed check leaves it parked: %+v", s)
	}
	k.tk = NewTokens(k.tk.ws)
	k.tk.Track(k.j, k.p)
	if s := k.state(); !s.Limited.Equal(until) {
		t.Fatalf("and so does a restart: %+v", s)
	}

	j, _ := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	defer j.Close()
	rs := NewRuns(k.tk.ws, j, nil, parkedTokens{k.tk}, func() []string { return nil })
	if l, _, err := rs.pickLogin("claude", "", t.TempDir(), false); err != nil || l.Key() != "claude" {
		t.Fatalf("the parked login is passed over for the other: %v %v", l.Key(), err)
	}
}

// parkedTokens lists twoLogins' logins with tk's renewal state.
type parkedTokens struct{ *Tokens }

func (parkedTokens) Logins(h string) []Login { return twoLogins{}.Logins(h) }
func (parkedTokens) AgentEnv(l Login, home string) ([]string, error) {
	return twoLogins{}.AgentEnv(l, home)
}
