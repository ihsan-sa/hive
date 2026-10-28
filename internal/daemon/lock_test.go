package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/app"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// A second daemon on a workspace exits at once, naming the daemon that holds
// it, whatever started that one; once that one ends, a daemon starts there.
func TestSecondDaemonOnAWorkspaceExits(t *testing.T) {
	if root := os.Getenv("HIVE_LOCK_HELPER"); root != "" {
		if _, err := lockWorkspace(core.Workspace{Root: root}, 0, false); err != nil {
			os.Stdout.WriteString("lock: " + err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString("locked\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Hive(), 0o755)
	first := exec.Command(os.Args[0], "-test.run", "^TestSecondDaemonOnAWorkspaceExits$")
	first.Env = append(os.Environ(), "HIVE_LOCK_HELPER="+ws.Root)
	out, _ := first.StdoutPipe()
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer first.Process.Kill()
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "locked\n" {
		t.Fatalf("the first daemon didn't take the workspace: %q", line)
	}
	serveDaemon(t, ws, 0) // the first one answers: it is up, not on its way out
	for !running(ws) {
		time.Sleep(10 * time.Millisecond)
	}
	before, _ := os.ReadFile(ws.DaemonFile())
	start := time.Now()
	err := Run(ws, 0)
	var held *AlreadyRunning
	if !errors.As(err, &held) || held.By.PID != first.Process.Pid {
		t.Fatalf("a second daemon ran on: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the second daemon took %s to exit", d)
	}
	if msg := err.Error(); !strings.Contains(msg, "pid "+strconv.Itoa(first.Process.Pid)) || !strings.Contains(msg, "started by process") {
		t.Errorf("the message doesn't name the running daemon: %s", msg)
	}
	if after, _ := os.ReadFile(ws.DaemonFile()); string(after) != string(before) {
		t.Error("the second daemon wrote daemon.json")
	}
	first.Process.Kill()
	first.Wait()
	l, err := lockWorkspace(ws, 0, false)
	if err != nil {
		t.Fatalf("the lock outlived its daemon: %v", err)
	}
	l.Release()
}

// lockHolder starts a process that holds ws's daemon lock, as a daemon
// starting up does before it writes daemon.json.
func lockHolder(t *testing.T, ws core.Workspace) *exec.Cmd {
	t.Helper()
	os.MkdirAll(ws.Hive(), 0o755)
	cmd := exec.Command(os.Args[0], "-test.run", "^TestSecondDaemonOnAWorkspaceExits$")
	cmd.Env = append(os.Environ(), "HIVE_LOCK_HELPER="+ws.Root)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "locked\n" {
		t.Fatalf("the helper didn't take the workspace: %q", line)
	}
	return cmd
}

// An update's replacement ends the daemon it replaced when that one still
// holds the workspace after the wait, as one hung on shutdown does, and takes
// the workspace; a holder it didn't replace is left alone.
func TestTakeOverEndsAHungDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper's lock outlives a kill until it is reaped")
	}
	ws := core.Workspace{Root: t.TempDir()}
	other := lockHolder(t, ws)
	if _, err := takeOver(ws, other.Process.Pid+100000, 200*time.Millisecond); err == nil {
		t.Fatal("took the workspace from a daemon it didn't replace")
	}
	if !proc.Alive(proc.Handle{PID: other.Process.Pid}) {
		t.Fatal("ended a daemon it didn't replace")
	}
	other.Process.Kill()
	other.Wait()
	hung := lockHolder(t, ws)
	l, err := takeOver(ws, hung.Process.Pid, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("the replacement didn't take the workspace from the hung daemon: %v", err)
	}
	l.Release()
	if proc.Alive(proc.Handle{PID: hung.Process.Pid}) {
		t.Fatal("the hung daemon still runs")
	}
}

// serveDaemon writes daemon.json for a daemon answering at a test server
// after a pause, as one that has finished starting.
func serveDaemon(t *testing.T, ws core.Workspace, after time.Duration) int {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) }))
	t.Cleanup(srv.Close)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(after)
		raw, _ := json.Marshal(api.DaemonInfo{PID: os.Getpid(), Port: port, Token: "t"})
		os.MkdirAll(ws.Private(), 0o700)
		core.WriteFileAtomic(ws.DaemonFile(), raw, 0o600)
	}()
	return port
}

// A command run while a daemon starts up (it holds the lock, daemon.json
// isn't written yet) waits for that daemon instead of starting a second, as
// a login's command did while the unit's daemon started.
func TestACommandWaitsForAStartingDaemon(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	lockHolder(t, ws)
	if !lockHeld(ws) {
		t.Fatal("a held lock reads as free")
	}
	defer func(f func() (string, error)) { executable = f }(executable)
	executable = func() (string, error) { return "", errors.New("started a daemon of its own") }
	port := serveDaemon(t, ws, 700*time.Millisecond)
	c, err := Start(ws)
	if err != nil || c.Info.Port != port {
		t.Fatalf("Start while a daemon starts: %+v, %v; want that daemon", c, err)
	}
}

// With the Hive's unit serving the workspace, a command that finds no daemon
// has systemd start the unit rather than starting one beside it, so a unit
// and a login's command can't make two.
func TestACommandStartsTheUnitNotASecondDaemon(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Hive(), 0o755)
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	unit := filepath.Join(home, ".config", "systemd", "user", "hive.service")
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("[Service]\nEnvironment=HIVE_HOME="+ws.Root+"\nExecStart=/x/hive daemon\n"), 0o644)
	calls := filepath.Join(t.TempDir(), "calls")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> '"+calls+"'\n"), 0o755)
	defer func(f func() (string, error)) { executable = f }(executable)
	spawned := filepath.Join(t.TempDir(), "spawned")
	os.WriteFile(filepath.Join(bin, "fake-hive"), []byte("#!/bin/sh\n: > '"+spawned+"'\n"), 0o755)
	executable = func() (string, error) { return filepath.Join(bin, "fake-hive"), nil }
	port := serveDaemon(t, ws, 500*time.Millisecond)
	c, err := Start(ws)
	if err != nil || c.Info.Port != port {
		t.Fatalf("Start: %+v, %v", c, err)
	}
	if raw, _ := os.ReadFile(calls); !strings.Contains(string(raw), "--user start --no-block hive.service") {
		t.Fatalf("systemd wasn't asked to start the unit: %q", raw)
	}
	time.Sleep(200 * time.Millisecond)
	if core.Exists(spawned) {
		t.Fatal("a daemon was started beside the unit")
	}
	// A unit for another workspace isn't this one's launcher.
	os.WriteFile(unit, []byte("[Service]\nEnvironment=HIVE_HOME=/elsewhere\nExecStart=/x/hive daemon\n"), 0o644)
	os.Remove(ws.DaemonFile())
	os.Remove(calls)
	serveDaemon(t, ws, 500*time.Millisecond)
	if _, err := Start(ws); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(calls); len(raw) > 0 {
		t.Fatalf("another workspace's unit was started: %q", raw)
	}
	for i := 0; !core.Exists(spawned); i++ {
		if i == 50 {
			t.Fatal("without its unit, no daemon was started")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A daemon started while the one before it is still on its way out (it
// holds the lock but no longer answers, as after /api/shutdown on a slow
// Windows runner) waits for the lock rather than exiting.
func TestADaemonWaitsOutOneStopping(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	old := lockHolder(t, ws)
	go func() { time.Sleep(time.Second); old.Process.Kill() }()
	l, err := lockWorkspace(ws, stopWait, true)
	if err != nil {
		t.Fatalf("the new daemon gave up on one stopping: %v", err)
	}
	l.Release()
}

// Once shutdown begins the daemon no longer answers ping, so a daemon
// started meanwhile reads it as on its way out.
func TestPingFailsOnceStopping(t *testing.T) {
	s := &server{a: &app.App{C: &core.Core{WS: core.Workspace{Root: t.TempDir()}}}, info: api.DaemonInfo{Token: "t"}}
	ping := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
		req.Header.Set("Authorization", "Bearer t")
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		return rec.Code
	}
	if code := ping(); code != http.StatusOK {
		t.Fatalf("ping: %d", code)
	}
	s.stopping.Store(true)
	if code := ping(); code == http.StatusOK {
		t.Fatal("a stopping daemon still answers ping")
	}
}

// A unit that takes the start but brings no daemon up (failed, at its start
// limit, or naming a binary that has moved) doesn't leave the command
// waiting for nothing: it starts a daemon itself.
func TestACommandStartsADaemonWhenTheUnitBringsNone(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux's")
	}
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Hive(), 0o755)
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	unit := filepath.Join(home, ".config", "systemd", "user", "hive.service")
	os.MkdirAll(filepath.Dir(unit), 0o755)
	// Written with a trailing slash: still this workspace's unit.
	os.WriteFile(unit, []byte("[Service]\nEnvironment=HIVE_HOME="+ws.Root+"/\nExecStart=/moved/hive daemon\n"), 0o644)
	calls := filepath.Join(t.TempDir(), "calls")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> '"+calls+"'\n[ \"$2\" = is-active ] && echo failed\nexit 0\n"), 0o755)
	defer func(f func() (string, error)) { executable = f }(executable)
	spawned := filepath.Join(t.TempDir(), "spawned")
	os.WriteFile(filepath.Join(bin, "fake-hive"), []byte("#!/bin/sh\n: > '"+spawned+"'\n"), 0o755)
	executable = func() (string, error) { return filepath.Join(bin, "fake-hive"), nil }
	defer func(w time.Duration) { unitWait = w }(unitWait)
	unitWait = 300 * time.Millisecond
	go func() {
		for !core.Exists(spawned) {
			time.Sleep(20 * time.Millisecond)
		}
		serveDaemon(t, ws, 0)
	}()
	if _, err := Start(ws); err != nil {
		t.Fatalf("Start with a unit that brings no daemon: %v", err)
	}
	if raw, _ := os.ReadFile(calls); !strings.Contains(string(raw), "--user start --no-block hive.service") {
		t.Fatalf("the unit wasn't tried first: %q", raw)
	}
}

// Two plain daemons starting from cold on one workspace: the one that loses
// the lock exits once the winner answers, not after the whole stopWait.
func TestTwoColdStartsLeaveOneDaemon(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	os.MkdirAll(ws.Hive(), 0o755)
	type result struct {
		l   *workspaceLock
		err error
	}
	got := make(chan result, 2)
	for range 2 {
		go func() { l, err := lockWorkspace(ws, stopWait, true); got <- result{l, err} }()
	}
	first := <-got
	if first.err != nil {
		t.Fatalf("neither start took the workspace: %v", first.err)
	}
	defer first.l.Release()
	start := time.Now()
	serveDaemon(t, ws, 300*time.Millisecond) // the winner comes up
	second := <-got
	var held *AlreadyRunning
	if !errors.As(second.err, &held) {
		second.l.Release()
		t.Fatalf("the second start ran on: %v", second.err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the second start took %s to exit after the first answered", d)
	}
}
