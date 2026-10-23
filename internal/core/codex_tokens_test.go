package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A Codex turn's CODEX_HOME is a fresh folder on the host side holding the
// access token and never the refresh token; a link the agent planted in its
// home doesn't steer the write, and the token is gone when the turn ends.
func TestCodexTurnGetsOnlyTheAccessToken(t *testing.T) {
	login := t.TempDir()
	exp := time.Now().Add(24 * time.Hour).Unix()
	access := "h." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp))) + ".s"
	src, _ := json.Marshal(map[string]any{"tokens": map[string]string{"access_token": access, "refresh_token": "secret-refresh", "account_id": "a"}})
	if err := os.WriteFile(filepath.Join(login, "auth.json"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	tk := NewTokens(ws)
	rs := NewRuns(ws, j, nil, tk, func() []string { return nil })
	r := Run{RunSpec: RunSpec{ID: "r1", Harness: "codex", System: "be \"brief\""}}

	// What the agent planted where the run's credential used to go.
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("keep"), 0o600)
	os.MkdirAll(filepath.Join(ws.RunHome("r1"), ".codex"), 0o700)
	os.MkdirAll(ws.RunHost("r1"), 0o700)
	if err := os.Symlink(victim, filepath.Join(ws.RunHome("r1"), ".codex", "auth.json")); err != nil {
		t.Skip("no symlinks here:", err)
	}

	dir, err := rs.freshCodexHome(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tk.AgentEnv(Login{Harness: "codex", Name: "work", Dir: login}, dir); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Fatalf("the planted link's target was written: %s", raw)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tokens map[string]string `json:"tokens"`
	}
	json.Unmarshal(raw, &got)
	if got.Tokens["access_token"] != access || got.Tokens["refresh_token"] != "" {
		t.Fatalf("turn's auth: %s", raw)
	}
	if cfg, _ := os.ReadFile(filepath.Join(dir, "config.toml")); !strings.Contains(string(cfg), `developer_instructions = "be \"brief\""`) {
		t.Fatalf("instructions not in config.toml: %s", cfg)
	}

	// Turn end keeps only the sessions; the next turn gets them back with no token.
	os.MkdirAll(filepath.Join(dir, "sessions", "2026"), 0o700)
	rs.dropCodexHome("r1")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("CODEX_HOME outlived its turn: %v", err)
	}
	if dir, err = rs.freshCodexHome(r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "2026")); err != nil {
		t.Fatalf("sessions lost between turns: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("a token is left from the last turn: %v", err)
	}

	if _, err := tk.AgentEnv(Login{Harness: "codex", Name: "none", Dir: t.TempDir()}, dir); err == nil {
		t.Fatal("a missing login gave no error")
	}
}

// A folder the agent locked under its CODEX_HOME is opened and removed, so the
// token goes with it; a link out of it is not followed.
func TestDropCodexHomeOpensLockedFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes differ on Windows")
	}
	ws := Workspace{Root: t.TempDir()}
	rs := &Runs{ws: ws}
	os.MkdirAll(ws.RunHost("r1"), 0o700)
	dir, err := rs.freshCodexHome(Run{RunSpec: RunSpec{ID: "r1"}})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("token"), 0o600)
	outside := t.TempDir()
	os.Chmod(outside, 0o500)
	defer os.Chmod(outside, 0o700)
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	os.MkdirAll(filepath.Join(dir, "locked", "deep"), 0o700)
	os.Chmod(filepath.Join(dir, "locked", "deep"), 0)
	os.Chmod(filepath.Join(dir, "locked"), 0)
	if err := rs.dropCodexHome("r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("CODEX_HOME outlived a locked folder: %v", err)
	}
	if fi, _ := os.Stat(outside); fi.Mode().Perm() != 0o500 {
		t.Fatalf("a link out was followed: %v", fi.Mode())
	}
}

// A token written for a turn that never started is gone once the daemon
// starts again.
func TestCodexTokenFromACrashIsCleanedAtStart(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, nil, func() []string { return nil })
	r, err := rs.Start(RunSpec{ID: "r1", Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := rs.freshCodexHome(r)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("token"), 0o600)
	NewRuns(ws, j, nil, nil, func() []string { return nil })
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the token outlived a restart: %v", err)
	}
}

// TOML forbids a raw DEL in a basic string, which JSON leaves raw.
func TestCodexConfigEscapesDEL(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	rs := &Runs{ws: ws}
	os.MkdirAll(ws.RunHost("r1"), 0o700)
	dir, err := rs.freshCodexHome(Run{RunSpec: RunSpec{ID: "r1", System: "a\x7fb"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := os.ReadFile(filepath.Join(dir, "config.toml")); string(cfg) != "developer_instructions = \"a\\u007fb\"\n" {
		t.Fatalf("config.toml: %q", cfg)
	}
}

// codexRun starts a Codex run with a token on disk and returns the Runs that
// started it, the journal's path and a func that makes the folder undeletable.
func codexRun(t *testing.T) (*Runs, Workspace, string, Run, func()) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only folder does not stop this user")
	}
	ws := Workspace{Root: t.TempDir()}
	path := filepath.Join(t.TempDir(), "j.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	rs := NewRuns(ws, j, nil, nil, func() []string { return nil })
	r, err := rs.Start(RunSpec{ID: "r1", Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(ws.RunHost("r1"), 0o700)
	if _, err := rs.freshCodexHome(r); err != nil {
		t.Fatal(err)
	}
	lock := func() {
		os.Chmod(ws.RunHost("r1"), 0o500)
		t.Cleanup(func() { os.Chmod(ws.RunHost("r1"), 0o700) })
	}
	return rs, ws, path, r, lock
}

// A token that cannot be deleted at turn end blocks the run with the reason.
func TestUndeletableCodexTokenBlocksTheRun(t *testing.T) {
	rs, _, _, r, lock := codexRun(t)
	lock()
	if got, _ := rs.Get("r1"); got.Status == StatusBlocked {
		t.Fatalf("blocked before the turn ended: %q", got.Reason)
	}
	rs.endTurn(r)
	got, _ := rs.Get("r1")
	if got.Status != StatusBlocked || !strings.Contains(got.Reason, "Codex token could not be deleted") {
		t.Fatalf("run is %q (%q)", got.Status, got.Reason)
	}
}

// The same at daemon start: a token a crash left in a folder that cannot be
// deleted is reported on the run, not dropped. A second start adds nothing, and
// a finished run keeps its status.
func TestUndeletableCodexTokenAtStartBlocksTheRun(t *testing.T) {
	rs, ws, path, _, lock := codexRun(t)
	rs.SetStatus("r1", StatusDone, "finished")
	lock()
	start := func() (*Runs, *Journal) {
		j, err := OpenJournal(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { j.Close() })
		return NewRuns(ws, j, nil, nil, func() []string { return nil }), j
	}
	rs2, _ := start()
	got, _ := rs2.Get("r1")
	if got.Status != StatusDone {
		t.Fatalf("finished run is %q", got.Status)
	}
	rs3, _ := start()
	if got, _ := rs3.Get("r1"); got.Status != StatusDone {
		t.Fatalf("finished run is %q", got.Status)
	}
	// A run that was still going is blocked once, then left alone.
	rs.SetStatus("r1", StatusIdle, "")
	rs4, _ := start()
	got, _ = rs4.Get("r1")
	if got.Status != StatusBlocked || !strings.Contains(got.Reason, "Codex token could not be deleted") {
		t.Fatalf("run is %q (%q)", got.Status, got.Reason)
	}
	before := fileSize(t, path)
	start()
	if after := fileSize(t, path); after != before {
		t.Fatalf("a restart wrote %d bytes to the journal", after-before)
	}
}

func fileSize(t *testing.T, p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
