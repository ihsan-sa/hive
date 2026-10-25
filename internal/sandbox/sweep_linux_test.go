package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A runtime folder whose sandbox still holds its lock stays; one whose
// sandbox died (the lock free), or an old one with no lock, goes; a folder a
// moment old stays whatever it holds.
func TestSweepRemovesDeadSandboxesRuntime(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	old := time.Now().Add(-2 * runtimeGrace)
	mk := func(name string) string {
		rt := filepath.Join(os.TempDir(), "hive-sbx-"+name)
		os.MkdirAll(filepath.Join(rt, "root"), 0o755)
		os.WriteFile(filepath.Join(rt, "init.json"), []byte("{}"), 0o600)
		return rt
	}
	live, dead, legacy, fresh := mk("live"), mk("dead"), mk("legacy"), mk("fresh")
	held, err := holdRuntime(live)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	gone, err := holdRuntime(dead)
	if err != nil {
		t.Fatal(err)
	}
	gone.Close() // as the kernel does when its sandbox-exec is killed
	holdRuntime(fresh)
	ancient := time.Now().Add(-2 * runtimeStale)
	for _, rt := range []string{live, dead, fresh} {
		os.Chtimes(rt, old, old)
	}
	os.Chtimes(legacy, ancient, ancient)
	now := time.Now()
	os.Chtimes(fresh, now, now)
	spec := filepath.Join(os.TempDir(), "hive-spec-1.json")
	os.WriteFile(spec, []byte("{}"), 0o600)
	stale := time.Now().Add(-2 * specStale)
	os.Chtimes(spec, stale, stale)
	if n := Sweep(); n < 3 {
		t.Errorf("swept %d, want at least 3", n)
	}
	for p, want := range map[string]bool{live: true, dead: false, legacy: false, fresh: true, spec: false} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}
