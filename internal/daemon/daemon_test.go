package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/app"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
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
	defer func(f func() (string, error), g func() supervision) { executable, supervisor = f, g }(executable, supervisor)
	executable = func() (string, error) { return filepath.Join(dir, "hive.old"), nil }
	supervisor = func() supervision { return supervision{} } // whatever unit the tests run in
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

// A unit's Restart= says what status a restart exits with, and KillMode=
// whether its stop takes the live turns with it.
func TestSupervisedBy(t *testing.T) {
	for _, c := range []struct {
		restart, kill string
		status        int
		keeps         bool
	}{
		{"on-failure", "control-group", RestartStatus, false},
		{"always", "process", RestartStatus, true},
		{"on-success", "mixed", 0, false},
		{"no", "process", -1, true},
		{"", "", -1, false},
	} {
		if got := supervisedBy("x.service", c.restart, c.kill); got.status != c.status || got.keepsRuns != c.keeps {
			t.Errorf("Restart=%s KillMode=%s: got %+v", c.restart, c.kill, got)
		}
	}
}

// restartUnder runs a restart under sup with one live turn and reports
// whether the daemon stopped before that turn ended, and after.
func restartUnder(t *testing.T, sup supervision) (s *server, early, late bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for a turn")
	}
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "ws")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Runs.Start(core.RunSpec{ID: "live", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	turn, err := proc.StartDetached([]string{"sleep", "60"}, os.Environ(), dir, "", filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Kill(turn) })
	c.J.MustAppend("run.turn_started", map[string]any{"id": "live", "turn": 1, "proc": turn})
	defer func(f func() supervision, d time.Duration) { supervisor, quietPoll = f, d }(supervisor, quietPoll)
	supervisor, quietPoll = func() supervision { return sup }, 20*time.Millisecond
	var stopped atomic.Bool
	s = &server{a: &app.App{C: c}, info: api.DaemonInfo{PID: os.Getpid(), Token: "t"}, cancel: func() { stopped.Store(true) }}
	done := make(chan struct{})
	go func() { s.restart(); close(done) }()
	time.Sleep(200 * time.Millisecond)
	early = stopped.Load()
	proc.Kill(turn)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the restart never finished")
	}
	return s, early, stopped.Load()
}

// Under a system unit of any name that kills its whole cgroup on stop, a
// restart holds new turns, waits for the live one to end, then exits with the
// status Restart= starts the new binary on.
func TestRestartUnderSystemUnitWaitsForLiveTurns(t *testing.T) {
	s, early, late := restartUnder(t, supervisedBy("hive-box.service", "on-failure", "control-group"))
	if early {
		t.Fatal("the daemon stopped while a turn was live, which the unit's stop would kill")
	}
	if !late {
		t.Fatal("the daemon didn't stop once the turn ended")
	}
	if got := s.exit.Load(); got != RestartStatus {
		t.Fatalf("exit status %d, want %d", got, RestartStatus)
	}
	if c, err := api.Dial(s.a.C.WS); err != nil || !c.Info.Restarting {
		t.Fatalf("daemon.json doesn't say a restart is under way: %v", err)
	}
	if !strings.Contains(alerts(s), "once 1 live turn(s) end") {
		t.Fatalf("no alert says the restart waits:\n%s", alerts(s))
	}
}

// Under a user unit with another name and KillMode=process, the live turns
// outlive the daemon, so it exits at once and the new one reattaches to them.
func TestRestartUnderUserUnitKeepsLiveTurns(t *testing.T) {
	s, early, _ := restartUnder(t, supervisedBy("agents.service", "always", "process"))
	if !early {
		t.Fatal("the daemon waited, though the unit's stop leaves the turns running")
	}
	if got := s.exit.Load(); got != RestartStatus {
		t.Fatalf("exit status %d, want %d", got, RestartStatus)
	}
}

// A unit with Restart=no wouldn't start the Hive again, so the daemon keeps
// running on the old code and says the update waits for its next start.
func TestRestartUnderUnitWithoutRestartStays(t *testing.T) {
	s, early, late := restartUnder(t, supervisedBy("hive.service", "no", "control-group"))
	if early || late {
		t.Fatal("the daemon stopped under a unit that won't start it again")
	}
	if !strings.Contains(alerts(s), "takes effect at its next start") {
		t.Fatalf("no alert says the update waits:\n%s", alerts(s))
	}
}

func alerts(s *server) string {
	var out strings.Builder
	s.a.C.J.Replay(func(e core.Event) {
		if e.Kind == "alert.raised" {
			out.Write(e.Data)
			out.WriteByte('\n')
		}
	})
	return out.String()
}

// Only a unit whose main process is this daemon restarts it. A daemon
// started from a shell inside tmux.service or cron.service sits in that
// unit's cgroup, but the unit won't start it again, so it starts its own
// replacement as before.
func TestSupervisorIsTheUnitWhoseMainProcessThisIs(t *testing.T) {
	defer func(f func() (string, bool), g func(string, bool) (string, string, int)) { unitOf, unitSettings = f, g }(unitOf, unitSettings)
	unitOf = func() (string, bool) { return "cron.service", false }
	main := os.Getpid() + 1
	unitSettings = func(string, bool) (string, string, int) { return "on-failure", "control-group", main }
	if sup := supervisor(); sup.name != "" {
		t.Fatalf("an unrelated unit was taken for the supervisor: %+v", sup)
	}
	main = os.Getpid()
	if sup := supervisor(); sup.name != "cron.service" || sup.status != RestartStatus {
		t.Fatalf("the unit whose main process this is wasn't: %+v", sup)
	}
	unitOf = func() (string, bool) { return "hive.service", true }
	if sup := supervisor(); sup.name != "" {
		t.Fatalf("the Hive's own user unit left the systemctl path: %+v", sup)
	}
}
