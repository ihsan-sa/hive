package core

import (
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
	if p, err := harnessBin("claude"); err != nil || p != filepath.Join(k.bin, "claude") {
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
