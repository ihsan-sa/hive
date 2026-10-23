package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// A Codex login made with an API key has no access token to hand a turn, so
// it is refused with a reason rather than joining the pool.
func TestAddLoginRefusesCodexAPIKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stands in for codex")
	}
	ws := core.Workspace{Root: t.TempDir()}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	dir := filepath.Join(ws.LoginsDir("codex"), "work")
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho '{\"OPENAI_API_KEY\":\"sk-x\",\"tokens\":null}' > \"$CODEX_HOME/auth.json\"\n"), 0o755)
	if err := AddLogin(ws, "codex", "work"); err == nil || !strings.Contains(err.Error(), "ChatGPT") {
		t.Fatalf("an API-key login: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("an API-key login left its folder: %v", err)
	}
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho '{\"tokens\":{\"access_token\":\"a\"}}' > \"$CODEX_HOME/auth.json\"\n"), 0o755)
	if err := AddLogin(ws, "codex", "work"); err != nil {
		t.Fatalf("a ChatGPT login: %v", err)
	}
}

// A codex on PATH that can't run in the sandbox fails doctor only when this
// Hive uses Codex.
func TestCodexReportIgnoresAnUnusedCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Codex is checked inside WSL there")
	}
	ws := core.Workspace{Root: t.TempDir()}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted = "bare", "now"
	ws.SaveConfig(cfg)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if msg, err := CodexReport(ws, ""); msg != "" || err != nil {
		t.Fatalf("an unused, broken codex: %q %v", msg, err)
	}
	cfg.Projects = map[string]core.ProjectSettings{"p": {Harness: "codex"}}
	ws.SaveConfig(cfg)
	if _, err := CodexReport(ws, ""); err == nil {
		t.Fatal("a project on a broken codex passed")
	}
}
