package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	ws := Workspace{Root: t.TempDir()}
	rs := &Runs{ws: ws}
	os.MkdirAll(ws.RunPrivate("r1"), 0o700)
	dir, err := rs.freshCodexHome(Run{RunSpec: RunSpec{ID: "r1"}})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("token"), 0o600)
	outside := t.TempDir()
	os.Chmod(outside, 0o500)
	defer os.Chmod(outside, 0o700)
	os.Symlink(outside, filepath.Join(dir, "out"))
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
	os.MkdirAll(ws.RunPrivate("r1"), 0o700)
	dir, err := rs.freshCodexHome(Run{RunSpec: RunSpec{ID: "r1", System: "a\x7fb"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := os.ReadFile(filepath.Join(dir, "config.toml")); string(cfg) != "developer_instructions = \"a\\u007fb\"\n" {
		t.Fatalf("config.toml: %q", cfg)
	}
}
