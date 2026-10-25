package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
)

// alerts are the texts of the release alerts raised so far.
func alerts(m *Module) (out []string) {
	m.C.J.Replay(func(e core.Event) {
		var x struct{ Kind, Text string }
		if e.Kind == "alert.raised" && e.Decode(&x) == nil && x.Kind == "release" {
			out = append(out, x.Text)
		}
	})
	return out
}

// selfClone makes the host clone of the self project with files committed,
// and returns its head.
func selfClone(t *testing.T, m *Module, files map[string]string) string {
	dir := m.C.WS.Project("hive")
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "x"}} {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	sha, _ := core.Git(dir, "rev-parse", "HEAD")
	return sha
}

// A release whose scan finds something is never cut, and the alert says
// where, never what.
func TestScanBlocksTheRelease(t *testing.T) {
	m, n, merged, land := selfHive(t)
	m.Scan = func(core.Project, string) error { return os.ErrPermission }
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	if len(n.got) != 0 || journaled(m, "hive.released") {
		t.Fatalf("released past a scan that blocks: %v", n.got)
	}
	if a := alerts(m); len(a) != 1 || !strings.Contains(a[0], "scan blocks it") {
		t.Fatalf("want one alert that the scan blocked it, got %q", a)
	}
}

// The release's own scan reads the commit's public list and the Hive's
// private lists: no handle list blocks, a clean commit passes, a key or a
// handle in a published file blocks, and one in a file the list leaves out
// doesn't.
func TestReleaseScanAtTheCommit(t *testing.T) {
	m, _, _, _ := selfHive(t)
	m.Scan = nil
	p, _ := m.C.Projects.Get("hive")
	key := "ghp_" + strings.Repeat("a1B2c3D4e5F6", 3)
	sha := selfClone(t, m, map[string]string{
		scan.PublicList:  "**\n!secret/**\n",
		"README.md":      "The Hive.\n",
		"secret/keys.md": key + "\nby alice-k\n",
	})
	t.Setenv(scan.HandlesEnv, "")
	if err := m.scan(p, sha); err == nil || !strings.Contains(err.Error(), "no handle list") {
		t.Fatalf("no handle list: %v", err)
	}
	os.WriteFile(filepath.Join(m.C.WS.Private(), scan.HandlesFile), []byte("alice-k\n"), 0o600)
	if err := m.scan(p, sha); err != nil {
		t.Fatalf("a clean commit: %v", err)
	}
	sha = selfClone(t, m, map[string]string{"docs/a.md": "thanks alice-k\n", "cmd/x.go": "var k = \"" + key + "\" // fake token\n"})
	err := m.scan(p, sha)
	if err == nil || !strings.Contains(err.Error(), "docs/a.md:1: handle 1") || !strings.Contains(err.Error(), "cmd/x.go:1: a GitHub token") {
		t.Fatalf("want the handle and the key, got %v", err)
	}
	if strings.Contains(err.Error(), "alice") || strings.Contains(err.Error(), key) {
		t.Fatalf("the failure prints what it found: %v", err)
	}
}
