package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A login's name is one plain path element, and a login that fails or leaves
// no credential leaves no folder to join the pool.
func TestAddLoginKeepsOnlyRealLogins(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	for _, name := range []string{"", ".", "..", "a/b", "../x"} {
		if err := AddLogin(ws, "claude", name); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	dir := filepath.Join(ws.LoginsDir("claude"), "work")
	if err := AddLogin(ws, "claude", "work"); err == nil {
		t.Fatal("a login with no claude to run succeeded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a failed login left its folder: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if err := AddLogin(ws, "claude", "work"); err == nil {
		t.Fatal("a login that left no credential succeeded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a login with no credential left its folder: %v", err)
	}
}

// doctor runs codex where agents run when the Hive uses it, and says so
// plainly when it is missing or fails there.
func TestCodexReport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Codex is checked inside WSL there")
	}
	ws := core.Workspace{Root: t.TempDir()}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if msg, err := CodexReport(ws, ""); msg != "" || err != nil {
		t.Fatalf("a Hive with no Codex checked it: %q %v", msg, err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness, cfg.Sandbox, cfg.BareAccepted = "codex", "bare", "now"
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexReport(ws, ""); err == nil {
		t.Fatal("a Codex Hive with no codex passed")
	}
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if _, err := CodexReport(ws, ""); err == nil {
		t.Fatal("a codex that fails passed")
	}
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if msg, err := CodexReport(ws, ""); err != nil || msg == "" {
		t.Fatalf("a working codex: %q %v", msg, err)
	}
}
