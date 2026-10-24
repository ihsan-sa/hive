package core

import (
	"os"
	"path/filepath"
	"runtime"
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
