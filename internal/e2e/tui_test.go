package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// term is a tmux server of the test's own, holding one window that a person
// types into: send-keys types, capture-pane reads the screen.
type term struct {
	t    *testing.T
	sock string
	env  []string
}

func (x *term) tmux(args ...string) string {
	x.t.Helper()
	cmd := exec.Command("tmux", append([]string{"-u", "-S", x.sock}, args...)...)
	cmd.Env = x.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		x.t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// keys presses named keys (Enter, Tab, Down, C-u...); typ types text as is.
func (x *term) keys(k ...string) {
	x.t.Helper()
	for _, key := range k {
		x.tmux("send-keys", "-t", "hive", key)
		time.Sleep(150 * time.Millisecond)
	}
}

func (x *term) typ(text string) {
	x.t.Helper()
	x.tmux("send-keys", "-t", "hive", "-l", text)
	time.Sleep(150 * time.Millisecond)
}

// screen is what the window shows, with lines the terminal wrapped joined back.
func (x *term) screen() string { x.t.Helper(); return x.tmux("capture-pane", "-p", "-J", "-t", "hive") }

// selected is the screen line drawn in reverse video, the control center's cursor.
func (x *term) selected() string {
	x.t.Helper()
	for _, l := range strings.Split(x.tmux("capture-pane", "-p", "-e", "-t", "hive"), "\n") {
		if strings.Contains(l, "\x1b[7m") {
			return strings.TrimSpace(ansi.Strip(l))
		}
	}
	return ""
}

// wait polls the screen until it shows every one of want.
func (x *term) wait(timeout time.Duration, want ...string) string {
	x.t.Helper()
	return x.until(timeout, fmt.Sprintf("%q", want), func(s string) bool {
		for _, w := range want {
			if !strings.Contains(s, w) {
				return false
			}
		}
		return true
	})
}

func (x *term) until(timeout time.Duration, what string, ok func(screen string) bool) string {
	x.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := x.screen()
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			x.t.Fatalf("the screen never showed %s:\n%s", what, s)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// pick moves the cursor down until the selected line starts with label.
func (x *term) pick(label string) {
	x.t.Helper()
	for i := 0; i < 30; i++ {
		if strings.HasPrefix(x.selected(), label) {
			return
		}
		x.keys("Down")
	}
	x.t.Fatalf("no line starting %q to select:\n%s", label, x.screen())
}

// frame checks the control center fills a w×h screen without a broken line:
// the breadcrumb is on the top row (a line wider than the screen would wrap
// and scroll it away) and no row is a wrapped continuation of another.
func (x *term) frame(w, h int, where string) {
	x.t.Helper()
	var s string
	for i := 0; i < 20; i++ {
		s = x.tmux("capture-pane", "-p", "-t", "hive")
		if strings.HasPrefix(s, "Hive ›") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	rows := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	joined := strings.Split(strings.TrimSuffix(x.screen(), "\n"), "\n")
	size := strings.TrimSpace(x.tmux("display-message", "-p", "-t", "hive", "#{window_width}x#{window_height}"))
	if !strings.HasPrefix(s, "Hive ›") || len(rows) != h || len(joined) != h || size != fmt.Sprintf("%dx%d", w, h) {
		x.t.Fatalf("%s at %dx%d (window %s): broken frame, %d rows, %d after joining wraps:\n%s", where, w, h, size, len(rows), len(joined), s)
	}
}

// sections tabs through all five sections from the current one and checks each frame.
func (x *term) sections(w, h int) {
	x.t.Helper()
	names := []string{"Chats", "Dashboard", "Waiting", "Modules", "Settings"}
	top, _, _ := strings.Cut(x.screen(), "\n")
	cur := slices.IndexFunc(names, func(n string) bool { return strings.HasPrefix(top, "Hive › "+n) })
	for i := 1; i <= len(names); i++ {
		name := names[(cur+i)%len(names)]
		x.keys("Tab")
		x.wait(10*time.Second, "Hive › "+name)
		x.frame(w, h, name)
	}
}

func (x *term) shot(name string) { x.t.Helper(); x.t.Logf("screen: %s\n%s", name, x.screen()) }

// TestControlCenterInTmux drives the built hive in a real terminal the way a
// person does: the first run on a clean HOME, a chat that lands work, the
// dashboard, approving and declining from Waiting, module access, settings,
// quitting with the Hive still running, reopening, and resizing. Services are
// stubbed on PATH, so nothing is installed on the account running the test.
func TestControlCenterInTmux(t *testing.T) {
	if os.Getenv("HIVE_E2E_TUI") != "1" {
		t.Skip("set HIVE_E2E_TUI=1 to drive the control center in tmux")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the service stubs cover Linux; elsewhere the first run would register real autostart")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is missing")
	}
	dir := t.TempDir()
	home, stubs, forge, gitOnly := filepath.Join(dir, "home"), filepath.Join(dir, "stubs"), filepath.Join(dir, "forge"), filepath.Join(dir, "git-only")
	for _, d := range []string{home, stubs, forge, gitOnly} {
		os.MkdirAll(d, 0o755)
	}
	stub := func(name, body string) {
		os.WriteFile(filepath.Join(stubs, name), []byte("#!/bin/sh\necho \""+name+" $*\" >> \"$HOME/stubs.log\"\n"+body), 0o755)
	}
	stub("claude", "echo '{\"loggedIn\": true}'\n")
	for _, name := range []string{"gh", "systemctl", "loginctl", "crontab"} {
		stub(name, "")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	os.Symlink(git, filepath.Join(gitOnly, "git"))
	os.Symlink(bin, filepath.Join(stubs, "hive"))
	x := &term{t: t, sock: filepath.Join(dir, "tmux.sock"), env: []string{
		"HOME=" + home, "PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH"), "TERM=xterm-256color",
		"LANG=C.UTF-8", "PS1=$ ", "TMPDIR=" + os.TempDir(), "HIVE_FORGE=local:" + forge}}
	x.tmux("new-session", "-d", "-s", "hive", "-x", "80", "-y", "24", "sh")
	t.Cleanup(func() { exec.Command("tmux", "-S", x.sock, "kill-server").Run() })
	ws := core.Workspace{Root: filepath.Join(home, "hive")}
	t.Cleanup(func() {
		cmd := exec.Command(bin, "shutdown")
		cmd.Env = x.env
		if cmd.Run() == nil {
			return
		}
		var info api.DaemonInfo // shutdown failed: don't leave the daemon running in a removed HOME
		if raw, err := os.ReadFile(filepath.Join(ws.Hive(), "daemon.json")); err == nil && json.Unmarshal(raw, &info) == nil && info.PID > 0 {
			proc.Kill(proc.Handle{PID: info.PID})
		}
	})

	// The checks: with only git on PATH, the first run names what's missing.
	x.typ("env PATH=" + gitOnly + " " + bin)
	x.keys("Enter")
	s := x.wait(20*time.Second, "missing: claude", "missing: gh", "Install what's missing")
	if strings.Contains(s, "missing: git") {
		t.Fatalf("git is on PATH but the check calls it missing:\n%s", s)
	}

	// The first run: Enter for the folder, Enter for the demo project.
	x.typ("hive")
	x.keys("Enter")
	x.wait(20*time.Second, "Where should the Hive live? ["+ws.Root+"]")
	x.keys("Enter")
	s = x.until(time.Minute, "the sandbox or repository prompt", func(s string) bool {
		return strings.Contains(s, "Type `bare`") || strings.Contains(s, "GitHub repository")
	})
	// The folder exists now and the daemon isn't up: point it at the fake
	// harness, as a person's own settings would point it at Claude Code.
	cfg := ws.LoadConfig()
	cfg.Harness = "fake"
	for k := range cfg.Schedules {
		cfg.Schedules[k] = "off"
	}
	ws.SaveConfig(cfg)
	if strings.Contains(s, "Type `bare`") { // no sandbox here: opt in, as the prompt offers
		x.typ("bare")
		x.keys("Enter")
		x.wait(10*time.Second, "for a small demo):")
	}
	x.shot("first run")
	x.keys("Enter")
	x.wait(2*time.Minute, "Hive › Chats", "textkit", "Nothing here yet")
	x.frame(80, 24, "first open")
	x.shot("control center on first open")
	log, _ := os.ReadFile(filepath.Join(home, "stubs.log"))
	for _, want := range []string{"systemctl --user enable hive.service", "loginctl enable-linger"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("autostart didn't run %q: %s", want, log)
		}
	}
	if !core.Exists(filepath.Join(home, ".config", "systemd", "user", "hive.service")) {
		t.Fatal("no unit in the test's HOME")
	}
	c, err := api.Dial(ws)
	if err != nil {
		t.Fatal(err)
	}
	x.sections(80, 24)

	// Chats: a request to the lead gets a reply, starts work, and lands.
	x.typ("implement capitalize_words with a test")
	x.wait(10*time.Second, "› implement capitalize_words")
	x.keys("Enter")
	x.wait(time.Minute, "Understood: implement capitalize_words")
	x.shot("the lead's reply")

	// Dashboard: the project, the running work, then the merge.
	x.keys("Tab")
	x.wait(time.Minute, "Hive › Dashboard", "textkit  local/textkit", "work in flight (1)")
	x.shot("dashboard with work running")
	x.wait(3*time.Minute, "work in flight (0)", "recent work (1)")
	x.pick("▸ recent work")
	x.keys("Right")
	if s := x.wait(10*time.Second, "[done] implement capitalize_words"); !strings.Contains(s, "#") {
		t.Fatalf("the merged work has no PR number:\n%s", s)
	}
	x.shot("dashboard after the merge")
	x.keys("BTab")
	x.wait(time.Minute, "Hive › Chats", "Merged: implement capitalize_words")
	x.shot("chat after the merge")

	// Settings: autonomy to 1, the model, and auto-update, each saved to config.json.
	x.keys("BTab")
	x.wait(10*time.Second, "Hive › Settings")
	x.pick("autonomy")
	x.keys("Enter")
	x.wait(10*time.Second, "3  tell me after")
	x.keys("Enter")
	x.wait(10*time.Second, "1  ask me before anything outside the project")
	x.pick("model")
	x.keys("Enter")
	x.wait(10*time.Second, "› sonnet")
	x.keys("C-u")
	x.typ("opus")
	x.keys("Enter")
	x.wait(10*time.Second, "saved")
	x.pick("auto-update")
	auto := ws.LoadConfig().AutoUpdate
	x.keys("Enter")
	x.wait(10*time.Second, "auto-update changed")
	x.shot("settings")
	if got := ws.LoadConfig(); got.Autonomy != 1 || got.Model != "opus" || got.AutoUpdate == auto {
		t.Fatalf("config.json: autonomy %d model %q auto-update %v (was %v)", got.Autonomy, got.Model, got.AutoUpdate, auto)
	}

	// Waiting: with autonomy 1, two steps outside the project come to the
	// person; one is approved and one declined from the keyboard.
	x.keys("Tab")
	x.wait(10*time.Second, "Hive › Chats")
	for _, ask := range []string{"add left-pad FAKE:needpackage", "add right-pad FAKE:needpackage"} {
		x.typ(ask)
		x.keys("Enter")
		x.wait(time.Minute, "Understood: "+ask[:12])
	}
	x.keys("Tab", "Tab")
	x.wait(3*time.Minute, "Hive › Waiting", "Waiting (2)", "Ask first:", "go ahead    declined")
	x.shot("waiting on you")
	x.keys("Enter")
	x.wait(10*time.Second, "Waiting (1)")
	x.keys("Right", "Enter")
	x.wait(10*time.Second, "Nothing is waiting on you.")
	deadline := time.Now().Add(2 * time.Minute)
	for {
		st, err := c.State()
		if err != nil {
			t.Fatal(err)
		}
		var states []string
		for _, r := range st.Requests {
			states = append(states, r.State)
		}
		slices.Sort(states)
		if len(states) == 2 && states[0] == "declined" && states[1] == "done" { // go ahead: the warden weighed it and the step ran
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("requests after go ahead and declined: %v", states)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Modules: the defaults are listed; kb's access is removed from every
	// project, granted to textkit alone, removed, and given back to all.
	x.keys("Tab")
	s = x.wait(10*time.Second, "Hive › Modules")
	st, _ := c.State()
	for _, m := range st.Modules {
		if !strings.Contains(s, m.Name) {
			t.Fatalf("module %s isn't listed:\n%s", m.Name, s)
		}
	}
	x.shot("modules")
	x.pick("kb")
	x.keys("Enter")
	x.wait(10*time.Second, "Hive › Modules › kb", "[x] every project")
	access := func(want ...string) {
		t.Helper()
		for i := 0; ; i++ {
			st, _ := c.State()
			for _, m := range st.Modules {
				if m.Name == "kb" && slices.Equal(m.Projects, want) {
					return
				}
			}
			if i == 50 {
				t.Fatalf("kb's projects never became %v: %+v", want, st.Modules)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	x.keys("Space")
	x.wait(10*time.Second, "[ ] every project", "[ ] textkit")
	access()
	x.keys("Down", "Space")
	x.wait(10*time.Second, "[ ] every project", "[x] textkit")
	access("textkit")
	x.shot("kb granted to textkit")
	x.keys("Space")
	x.wait(10*time.Second, "[ ] textkit")
	access()
	x.keys("Up", "Space")
	x.wait(10*time.Second, "[x] every project")
	access("*")
	x.keys("Escape")
	x.wait(10*time.Second, "agents ")

	// A wide terminal, then resizes back and forth, then narrow again.
	x.tmux("resize-window", "-t", "hive", "-x", "160", "-y", "48")
	x.frame(160, 48, "wide")
	x.sections(160, 48)
	x.shot("wide chats")
	for _, size := range [][2]string{{"50", "16"}, {"200", "60"}, {"30", "8"}, {"120", "40"}, {"80", "24"}} {
		x.tmux("resize-window", "-t", "hive", "-x", size[0], "-y", size[1])
		time.Sleep(100 * time.Millisecond)
	}
	x.frame(80, 24, "after resizing")
	x.sections(80, 24)

	// Quitting leaves the Hive running, and `hive` opens it again.
	x.keys("q")
	x.wait(10*time.Second, "Opening the control center")
	x.typ("hive status")
	x.keys("Enter")
	x.wait(30*time.Second, "textkit (local/textkit)", "agents:")
	x.shot("hive status after quitting")
	x.typ("hive")
	x.keys("Enter")
	x.wait(30*time.Second, "Hive › Chats", "Merged: implement capitalize_words")
	x.frame(80, 24, "reopened")
	x.keys("q")
}
