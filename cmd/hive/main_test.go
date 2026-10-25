package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

func TestKBArgsTakeLimitAnywhere(t *testing.T) {
	for _, args := range [][]string{
		{"login", "--limit", "all"},
		{"--limit", "all", "login"},
	} {
		if q, l, d := kbArgs(args); q != "login" || l != "all" || d {
			t.Errorf("kbArgs(%q) = %q, %q, %v; want login, all, false", args, q, l, d)
		}
	}
	if q, _, d := kbArgs([]string{"why", "--deep", "reverted"}); q != "why reverted" || !d {
		t.Errorf("kbArgs with --deep = %q, %v", q, d)
	}
}

func TestReleaseArgRefusesWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		args []string
		tag  string
		ok   bool
	}{
		{nil, "", true},
		{[]string{"--release", "v0.1.2"}, "v0.1.2", true},
		{[]string{"--release=v0.1.2"}, "v0.1.2", true},
		{[]string{"--release"}, "", false},
		{[]string{"--release="}, "", false},
		{[]string{"v0.1.2"}, "", false},
		{[]string{"--relase", "v0.1.2"}, "", false},
	} {
		if tag, ok := releaseArg(c.args); tag != c.tag || ok != c.ok {
			t.Errorf("releaseArg(%q) = %q, %v; want %q, %v", c.args, tag, ok, c.tag, c.ok)
		}
	}
}

// approve and refuse never guess on an ask with one option (a "told" seen),
// and say what an answer means where its word doesn't.
func TestApproveRefuseNeverGuess(t *testing.T) {
	if _, err := approveOption("refuse", core.Ask{ID: "t1", Options: []string{"seen"}}); err == nil {
		t.Fatal("refuse answered a one-option ask")
	}
	if _, err := approveOption("approve", core.Ask{ID: "q1"}); err == nil {
		t.Fatal("approve answered an ask with no options")
	}
	if o, _ := approveOption("approve", core.Ask{Options: []string{"done", "declined"}}); o != "done" || meaning(o) == "" {
		t.Fatalf("approve picked %q, meaning %q", o, meaning(o))
	}
	if o, _ := approveOption("refuse", core.Ask{Options: []string{"run", "declined"}}); o != "declined" {
		t.Fatalf("refuse picked %q", o)
	}
}

// The owner read "resets Sat 01:40" at 02:09Z as a reset already past; it was
// 05:40Z printed in EDT with no zone. The line now says how far off it is.
func TestPlanLineSaysWhenTheResetIs(t *testing.T) {
	reset := time.Date(2026, 10, 24, 5, 40, 0, 0, time.UTC)
	now := time.Date(2026, 10, 24, 2, 9, 0, 0, time.UTC)
	p := core.Plan{Harness: "claude", Login: "claude", At: now.Add(-time.Minute), Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour", Utilization: 0.28,
		Windows: map[string]harness.Window{"five_hour": {Utilization: 0.28, ResetsAt: reset}, "seven_day": {Utilization: 0.36}}}}
	got := planLine(p, now)
	for _, want := range []string{"28% of the five_hour window", "resets in 3h31m0s", "seven_day 36%"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if got := planLine(p, reset.Add(time.Minute)); !strings.Contains(got, "window reset at") || !strings.Contains(got, "no reading since") {
		t.Errorf("a reading past its reset should say so: %q", got)
	}
}

// Window names and the status come from the usage feed; hive status spells
// out any control, bidi or zero-width character in them instead of letting it
// rewrite the screen.
func TestPlanLineEscapesFeedNames(t *testing.T) {
	now := time.Date(2026, 10, 24, 2, 9, 0, 0, time.UTC)
	evil := "five\x1b[2J\x1b[H\u009b31m\r\u202e\u200bhour"
	p := core.Plan{Harness: "claude", Login: "me\x1b[1A", At: now, Limit: harness.RateLimit{Status: "allowed\x1b]0;x\x07", ResetsAt: now.Add(time.Hour), Window: evil,
		Windows: map[string]harness.Window{evil: {}, "seven\x1b[Kday\u2066": {Utilization: 0.1}}}}
	for _, got := range []string{planLine(p, now), planLine(p, now.Add(2*time.Hour))} {
		for _, want := range []string{`five\x1b[2J\x1b[H\x9b31m\x0d\u202e\u200bhour`, `me\x1b[1A`} {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
		if strings.ContainsAny(got, "\x1b\x07\r\u009b\u202e\u200b\u2066") {
			t.Errorf("a control or format character reaches the screen: %q", got)
		}
	}
	if got := planLine(p, now); !strings.Contains(got, `allowed\x1b]0;x\x07`) || !strings.Contains(got, `seven\x1b[Kday\u2066 10%`) {
		t.Errorf("the status and the other windows should be escaped too: %q", got)
	}
}

// approve and refuse pick the first and last option; an ask with none is an
// error pointing at hive answer, not a panic.
func TestApproveOption(t *testing.T) {
	a := core.Ask{ID: "a1", Options: []string{"merge", "leave open"}}
	if o, err := approveOption("approve", a); err != nil || o != "merge" {
		t.Errorf("approve: %q, %v", o, err)
	}
	if o, err := approveOption("refuse", a); err != nil || o != "leave open" {
		t.Errorf("refuse: %q, %v", o, err)
	}
	for _, cmd := range []string{"approve", "refuse"} {
		if o, err := approveOption(cmd, core.Ask{ID: "q1"}); err == nil {
			t.Errorf("%s on an ask with no options: %q, want an error", cmd, o)
		}
	}
}

// TestHelperStayAlive is the stand-in daemon process for TestHelpNeverActs.
func TestHelperStayAlive(t *testing.T) {
	if os.Getenv("HIVE_TEST_STAY_ALIVE") == "" {
		t.Skip("a helper process, not a test")
	}
	time.Sleep(5 * time.Minute)
}

// `hive <command> --help` prints that command's usage and does nothing:
// `hive update --help` once installed an update and `hive ask --help` sent
// "--help" to the lead. Every command runs against a fake daemon that counts
// what reaches it.
func TestHelpNeverActs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIVE_HOME", root)
	t.Setenv("HOME", root) // uninstall and login touch files under home
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("APPDATA", root)
	ws := core.Workspace{Root: root}
	// daemon.json names a child process, never this test's own: a regressed
	// `shutdown --help` gets a fresh child in its place, so it's counted and
	// returns at once, and the commands after it still reach the fake daemon.
	var mu sync.Mutex
	var child *exec.Cmd
	var port int
	daemonUp := func() {
		mu.Lock()
		defer mu.Unlock()
		if child != nil {
			child.Process.Kill()
			child.Wait() // reaped, so it no longer looks alive
		}
		child = exec.Command(os.Args[0], "-test.run=^TestHelperStayAlive$")
		child.Env = append(os.Environ(), "HIVE_TEST_STAY_ALIVE=1")
		if err := child.Start(); err != nil {
			t.Error(err)
			return
		}
		info, _ := json.Marshal(api.DaemonInfo{PID: child.Process.Pid, Port: port, Token: "t", Version: core.Version})
		os.MkdirAll(filepath.Dir(ws.DaemonFile()), 0o755)
		if err := os.WriteFile(ws.DaemonFile(), info, 0o600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		child.Process.Kill()
		child.Wait()
	})
	var calls atomic.Int32
	var sent atomic.Value // the text of the last chat message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/api/shutdown" {
			daemonUp()
		}
		if r.URL.Path == "/api/chat" && r.Method == "POST" {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			sent.Store(body["text"])
		}
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	port, _ = strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	daemonUp()
	if t.Failed() {
		t.FailNow()
	}
	// The fake daemon answers: a command without --help reaches it.
	if code := run([]string{"improve"}); code != 0 || calls.Load() == 0 {
		t.Fatalf("improve without --help: exit %d, %d requests; the fake daemon isn't reached", code, calls.Load())
	}
	calls.Store(0)
	cmds := []string{"open", "init", "daemon", "ask", "status", "kb", "improve", "answer", "approve", "refuse", "login", "doctor", "egress", "share", "update", "shutdown", "uninstall"}
	for _, cmd := range cmds {
		for _, args := range [][]string{{"--help"}, {"-h"}, {"x", "--help"}, {"--release", "v0.1.2", "--help"}} {
			if code := run(append([]string{cmd}, args...)); code != 0 {
				t.Errorf("hive %s %q: exit %d, want 0", cmd, args, code)
			}
		}
		if h := commandHelp(cmd); !strings.Contains(h, "hive "+cmd) && !strings.Contains(h, "control center") {
			t.Errorf("help for %s doesn't name it: %q", cmd, commandHelp(cmd))
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("--help made %d requests to the daemon, want none", n)
	}
	if _, err := os.Stat(ws.DaemonFile()); err != nil {
		t.Errorf("--help touched the daemon's record: %v", err)
	}
	// After --, -h and --help are text, sent without the --.
	if code := run([]string{"ask", "--", "what", "does", "--help", "do", "--wait-not"}); code != 0 {
		t.Errorf("ask -- ... --help: exit %d, want 0", code)
	}
	if got, want := sent.Load(), "what does --help do --wait-not"; got != want {
		t.Errorf("ask -- sent %q, want %q", got, want)
	}
	if code := run([]string{"nosuch", "--help"}); code != 2 {
		t.Errorf("an unknown command with --help: exit %d, want 2", code)
	}
}

// hive hold reads the project, the person's words and its flags.
func TestHoldArgs(t *testing.T) {
	b, err := holdArgs([]string{"hive", "only", "the", "fix", "--only", "205,#207", "--until=release"})
	if err != nil || b["project"] != "hive" || b["why"] != "only the fix" || b["until"] != "release" || fmt.Sprint(b["only"]) != "[205 207]" {
		t.Fatalf("%v %v", b, err)
	}
	if b, _ := holdArgs([]string{"hive", "--lift"}); b["lift"] != true {
		t.Fatalf("%v", b)
	}
	if _, err := holdArgs([]string{"--only", "x"}); err == nil {
		t.Fatal("a bad PR number was taken")
	}
}
