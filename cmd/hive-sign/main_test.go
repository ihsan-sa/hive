package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
)

// The workflow's signer signs with the pinned key, refuses another key, and
// refuses to leave a release unsigned once a key is pinned. The seeds are
// test keys.
func TestSign(t *testing.T) {
	seed, other := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	sums := filepath.Join(t.TempDir(), "checksums.txt")
	os.WriteFile(sums, []byte("abc  hive-linux-amd64\n"), 0o644)
	defer func(k string) { release.PublicKey = k }(release.PublicKey)

	release.PublicKey = ""
	t.Setenv("HIVE_RELEASE_KEY", "")
	if err := run([]string{"v1.0.0", sums}); err != nil {
		t.Fatalf("no key anywhere: %v", err)
	}
	if _, err := os.Stat(sums + ".sig"); err == nil {
		t.Fatal("signed with no key")
	}

	release.PublicKey = release.PublicOf(seed)
	if err := run([]string{"v1.0.0", sums}); err == nil || !strings.Contains(err.Error(), "refuse") {
		t.Fatalf("pinned key, no secret: %v", err)
	}
	t.Setenv("HIVE_RELEASE_KEY", encode(other))
	if err := run([]string{"v1.0.0", sums}); err == nil || !strings.Contains(err.Error(), "isn't the key") {
		t.Fatalf("another key: %v", err)
	}
	t.Setenv("HIVE_RELEASE_KEY", encode(seed))
	if err := run([]string{"v1.0.0", sums}); err != nil {
		t.Fatalf("the pinned key: %v", err)
	}
	sig, _ := os.ReadFile(sums + ".sig")
	raw, _ := os.ReadFile(sums)
	want, _ := release.Sign(seed, "v1.0.0", raw)
	if strings.TrimSpace(string(sig)) != want {
		t.Fatalf("signature %q, want %q", sig, want)
	}
}

func encode(seed []byte) string { return base64.StdEncoding.EncodeToString(seed) }
