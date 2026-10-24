package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/app"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// After an update renames the running binary to hive.old, the restart runs
// the new hive, not the old code under its new name, and daemon.json tells
// the control center to wait for it.
func TestRestartRunsNewBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binaries are shell scripts")
	}
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "ws")}
	if err := os.MkdirAll(ws.Hive(), 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(dir, "ran")
	for name, says := range map[string]string{"hive": `"$*"`, "hive.old": "old"} {
		script := fmt.Sprintf("#!/bin/sh\necho %s > '%s.tmp' && mv '%s.tmp' '%s'\n", says, ran, ran, ran)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	defer func(f func() (string, error)) { executable = f }(executable)
	executable = func() (string, error) { return filepath.Join(dir, "hive.old"), nil }
	stopped := false
	s := &server{a: &app.App{C: &core.Core{WS: ws}}, info: api.DaemonInfo{PID: os.Getpid(), Token: "t"}, cancel: func() { stopped = true }}
	s.restart()
	if !stopped {
		t.Fatal("the old daemon didn't stop")
	}
	if c, err := api.Dial(ws); err != nil || !c.Info.Restarting {
		t.Fatalf("daemon.json doesn't say a restart is under way: %v", err)
	}
	want := fmt.Sprintf("daemon --wait-for %d", os.Getpid())
	got := ""
	for i := 0; i < 50 && got != want; i++ {
		time.Sleep(100 * time.Millisecond)
		raw, _ := os.ReadFile(ran)
		got = strings.TrimSpace(string(raw))
	}
	if got != want {
		t.Fatalf("the restart ran %q, want the new binary with %q", got, want)
	}
}

// A command run while a restart hands over waits for the new daemon instead of
// starting one of its own, which would run two daemons on one workspace.
func TestStartWaitsOutAHandOff(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	gone := exec.Command("go", "version")
	gone.Run() // a pid that has exited, standing in for the old daemon
	write := func(info api.DaemonInfo) {
		raw, _ := json.Marshal(info)
		os.MkdirAll(ws.Private(), 0o700)
		if err := core.WriteFileAtomic(ws.DaemonFile(), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(api.DaemonInfo{PID: gone.Process.Pid, Port: 1, Restarting: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"version":"new"}`) }))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	defer func(f func() (string, error)) { executable = f }(executable)
	executable = func() (string, error) { return "", errors.New("started a daemon of its own") }
	go func() {
		time.Sleep(500 * time.Millisecond)
		write(api.DaemonInfo{PID: os.Getpid(), Port: port, Token: "t"})
	}()
	c, err := Start(ws)
	if err != nil || c.Info.Port != port {
		t.Fatalf("Start during a hand-off: %+v, %v; want the new daemon", c, err)
	}
}

// An update from v0.1.2 runs under a systemd unit without KillMode=process,
// which kills the restart child when the old daemon exits. The child hands
// the restart to systemd instead, with the unit rewritten for the new binary.
func TestRestartInOldUnitGoesThroughSystemd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	old := exec.Command("true")
	old.Run() // a pid that has exited, standing in for the old daemon
	dir, home, bin := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	unit := filepath.Join(home, ".config", "systemd", "user", "hive.service")
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("[Service]\nExecStart=/old/hive-v0 daemon\nRestart=on-failure\n"), 0o644)
	calls := filepath.Join(dir, "calls")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> '"+calls+"'\n"), 0o755)
	ws := core.Workspace{Root: filepath.Join(dir, "ws")}
	os.MkdirAll(ws.Hive(), 0o755)
	defer func(f func() (string, error), g func() bool) { executable, inHiveUnit = f, g }(executable, inHiveUnit)
	executable = func() (string, error) { return filepath.Join(dir, "hive"), nil }
	inHiveUnit = func() bool { return true }
	t.Cleanup(func() { signal.Reset(syscall.SIGTERM) })
	if err := Run(ws, old.Process.Pid); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(unit)
	if !strings.Contains(string(raw), "ExecStart="+filepath.Join(dir, "hive")+" daemon\nKillMode=process\n") {
		t.Fatalf("unit not rewritten:\n%s", raw)
	}
	if got, _ := os.ReadFile(calls); !strings.Contains(string(got), "--user restart --no-block hive.service") {
		t.Fatalf("systemd wasn't asked to restart; calls:\n%s", got)
	}
	if core.Exists(ws.DaemonFile()) {
		t.Fatal("the child ran a daemon itself")
	}
}

// GET /api/config says what config.json got wrong and still returns every
// setting that read, the floor and egress list after a bad seat among them.
func TestConfigAPIReportsALoadError(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(ws.ConfigPath(), []byte(`{"tribunal_models": [{"model": 5}], "floor": ["*.pem"], "egress": {"default": ["github.com"]}}`), 0o600)
	s := &server{a: &app.App{C: &core.Core{WS: ws}}, info: api.DaemonInfo{Token: "t"}}
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var got api.ConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	if !strings.Contains(got.LoadError, `"model": 5`) {
		t.Fatalf("load_error %q, want the bad seat named", got.LoadError)
	}
	if len(got.Floor) != 1 || got.Floor[0] != "*.pem" || len(got.Egress.Default) != 1 || got.Egress.Default[0] != "github.com" {
		t.Fatalf("lost settings after the bad seat: floor %v, egress %v", got.Floor, got.Egress)
	}
}

// State carries the first lines of a diff over 1 MiB, and /api/waiting the
// whole of it.
func TestStateCarriesADiffPreview(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(ws.Root, "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	pad := "+" + strings.Repeat("x", 99) + "\n"
	diff := strings.Repeat(pad, (1<<20)/len(pad)+1) + "+the last line\n"
	person := core.NewPerson(j)
	a := person.Ask(core.Ask{Kind: "merge", Ref: "t1@abc", Title: "Merge?", Options: []string{"merge", "leave open"}, Diff: diff})
	s := &server{a: &app.App{C: &core.Core{WS: ws, Person: person}}, info: api.DaemonInfo{Token: "t"}}
	open := previews(s.a.C.Person.Open())
	if len(open) != 1 || len(open[0].Diff) > diffPreview || !strings.HasPrefix(diff, open[0].Diff) || !strings.HasSuffix(open[0].Diff, "\n") {
		t.Fatalf("state should carry whole lines from the diff's start, at most %d bytes: got %d", diffPreview, len(open[0].Diff))
	}
	req := httptest.NewRequest(http.MethodGet, "/api/waiting?id="+a.ID, nil)
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var got core.Ask
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Diff != diff {
		t.Fatalf("%d: /api/waiting should return the whole %d-byte diff, got %d (%v)", rec.Code, len(diff), len(got.Diff), err)
	}
}
