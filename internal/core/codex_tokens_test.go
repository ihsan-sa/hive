package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A Codex run's own CODEX_HOME holds the access token and never the refresh token.
func TestCodexRunGetsOnlyTheAccessToken(t *testing.T) {
	login, home := t.TempDir(), t.TempDir()
	exp := time.Now().Add(24 * time.Hour).Unix()
	access := "h." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp))) + ".s"
	src, _ := json.Marshal(map[string]any{"tokens": map[string]string{"access_token": access, "refresh_token": "secret-refresh", "account_id": "a"}})
	if err := os.WriteFile(filepath.Join(login, "auth.json"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	tk := NewTokens(Workspace{Root: t.TempDir()})
	if _, err := tk.AgentEnv(Login{Harness: "codex", Name: "work", Dir: login}, home); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tokens map[string]string `json:"tokens"`
	}
	json.Unmarshal(raw, &got)
	if got.Tokens["access_token"] != access || got.Tokens["refresh_token"] != "" {
		t.Fatalf("run's auth: %s", raw)
	}
	if _, err := tk.AgentEnv(Login{Harness: "codex", Name: "none", Dir: t.TempDir()}, home); err == nil {
		t.Fatal("a missing login gave no error")
	}
}
