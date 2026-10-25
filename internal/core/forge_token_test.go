package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func withOwnerTokens(t *testing.T, m map[string]string) {
	old := OwnerTokens
	OwnerTokens = m
	t.Cleanup(func() { OwnerTokens = old })
}

func TestGhEnvByOwner(t *testing.T) {
	f := filepath.Join(t.TempDir(), "org.token")
	os.WriteFile(f, []byte("org-secret\n"), 0o600)
	withOwnerTokens(t, map[string]string{"hive-rsi-lab": f, "missing": filepath.Join(t.TempDir(), "none")})

	if got := ghEnv("Hive-RSI-Lab/test"); len(got) != 1 || got[0] != "GH_TOKEN=org-secret" {
		t.Fatalf("org repo: %v", got)
	}
	for _, repo := range []string{"ihsan-sa/hive", "", "noslash", "missing/x"} {
		if got := ghEnv(repo); got != nil {
			t.Fatalf("%q should keep gh's login, got %v", repo, got)
		}
	}
}

func TestOwnerTokenReachesGhAndGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "org.token")
	os.WriteFile(f, []byte("org-secret\n"), 0o600)
	withOwnerTokens(t, map[string]string{"hive-rsi-lab": f})
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho \"tok=$GH_TOKEN\"\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "")

	if out, err := gh("Hive-RSI-Lab/x", "anything"); err != nil || out != "tok=org-secret" {
		t.Fatalf("gh org: %q %v", out, err)
	}
	if out, err := gh("ihsan-sa/hive", "anything"); err != nil || out != "tok=" {
		t.Fatalf("gh default: %q %v", out, err)
	}
	out, err := GitEnvT(time.Minute, dir, ghEnv("Hive-RSI-Lab/x"), "-c", `alias.tok=!echo "git=$GH_TOKEN"`, "tok")
	if err != nil || out != "git=org-secret" {
		t.Fatalf("git org: %q %v", out, err)
	}
}

// Under install.sh's system unit the token is systemd's credential
// <owner>.token, which wins over OwnerTokens; an empty one (as the installer
// creates it) falls back, and an owner can't name a path.
func TestGhEnvFromCredentials(t *testing.T) {
	creds, dir := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(creds, "ihsan-sa.token"), []byte("cred-secret\n"), 0o600)
	os.WriteFile(filepath.Join(creds, "hive-rsi-lab.token"), nil, 0o600)
	os.WriteFile(filepath.Join(dir, "org.token"), []byte("org-secret\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "..token"), []byte("dots\n"), 0o600)
	withOwnerTokens(t, map[string]string{"hive-rsi-lab": filepath.Join(dir, "org.token")})
	t.Setenv("CREDENTIALS_DIRECTORY", creds)

	if got := ghEnv("Ihsan-SA/hive"); len(got) != 1 || got[0] != "GH_TOKEN=cred-secret" {
		t.Fatalf("credential: %v", got)
	}
	if got := ghEnv("hive-rsi-lab/x"); len(got) != 1 || got[0] != "GH_TOKEN=org-secret" {
		t.Fatalf("an empty credential should fall back: %v", got)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)
	for _, repo := range []string{"./x", "../x", "a\\b/x"} {
		if got := ghEnv(repo); got != nil {
			t.Fatalf("%q read %v", repo, got)
		}
	}
}

// An unreadable listed token file falls back to gh's login and says so once,
// naming the file and the owner.
func TestGhEnvSaysOnceWhenTokenFileUnreadable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.token")
	withOwnerTokens(t, map[string]string{"hive-rsi-lab": missing})
	t.Setenv("CREDENTIALS_DIRECTORY", t.TempDir()) // no credential: that is never said
	var buf strings.Builder
	old := tokenLog
	tokenLog = &buf
	t.Cleanup(func() { tokenLog = old })

	for range 3 {
		if got := ghEnv("Hive-RSI-Lab/x"); got != nil {
			t.Fatalf("should keep gh's login, got %v", got)
		}
	}
	ghEnv("ihsan-sa/hive")
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, missing) || !strings.Contains(out, "hive-rsi-lab") {
		t.Fatalf("want one line naming %s and hive-rsi-lab, got %q", missing, out)
	}
}
