package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestMain keeps every test's Prune out of the host's shared /tmp.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "core-shared-tmp-")
	if err != nil {
		panic(err)
	}
	sharedTmp = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// A daemon whose /tmp is in memory puts its scratch files on disk, in the
// workspace's private folder; a TMPDIR set elsewhere is left alone.
func TestUseDiskTempLeavesMemory(t *testing.T) {
	if runtime.GOOS != "linux" || !inMemory("/dev/shm") {
		t.Skip("needs a tmpfs at /dev/shm")
	}
	ws := Workspace{Root: t.TempDir()}
	if inMemory(ws.Root) {
		t.Skip("the test's temp folder is in memory itself")
	}
	shm, err := os.MkdirTemp("/dev/shm", "core-tmp-")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { os.RemoveAll(shm) })
	defer func(old string) { sharedTmp = old }(sharedTmp)
	sharedTmp = shm

	t.Setenv("TMPDIR", "/somewhere/else")
	if got, err := UseDiskTemp(ws); err != nil || got != "" || os.Getenv("TMPDIR") != "/somewhere/else" {
		t.Fatalf("a TMPDIR set elsewhere was moved: %q, %v", got, err)
	}
	t.Setenv("TMPDIR", "")
	got, err := UseDiskTemp(ws)
	if err != nil || got != ws.Temp() || os.Getenv("TMPDIR") != ws.Temp() || os.TempDir() != ws.Temp() {
		t.Fatalf("UseDiskTemp = %q, %v; TMPDIR %q, want %q", got, err, os.Getenv("TMPDIR"), ws.Temp())
	}
	if fi, err := os.Stat(ws.Temp()); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("the temp folder: %v, %v", fi, err)
	}
}

// The clean job removes the Hive's leftovers in the shared temp folder and
// its own once nothing in them has changed for a day, and nothing else.
func TestPruneTempRemovesQuietLeftovers(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	shared := t.TempDir()
	defer func(old string) { sharedTmp = old }(sharedTmp)
	sharedTmp = shared
	old := time.Now().Add(-2 * leftoverAge)
	mk := func(p string, aged bool) string {
		os.MkdirAll(filepath.Join(p, "x"), 0o755)
		f := filepath.Join(p, "x", "f")
		os.WriteFile(f, []byte("data"), 0o644)
		if aged {
			for _, q := range []string{f, filepath.Join(p, "x"), p} {
				os.Chtimes(q, old, old)
			}
		}
		return p
	}
	stale := mk(filepath.Join(shared, "hive-check-1"), true)
	test := mk(filepath.Join(shared, "TestSoftLayerarmsstandard1"), true)
	own := mk(filepath.Join(ws.Temp(), "go-build1"), true)
	busy := mk(filepath.Join(shared, "hive-meter-2"), true)
	os.WriteFile(filepath.Join(busy, "x", "new"), nil, 0o644) // written to just now, deep inside
	sbx := mk(filepath.Join(shared, "hive-sbx-3"), true)
	other := mk(filepath.Join(shared, "go-build2"), true)
	fresh := mk(filepath.Join(shared, "hive-gate-4"), false)
	var gone []string
	pruneTemp(ws, func(p string) { gone = append(gone, p); RemoveTree(p) })
	for p, want := range map[string]bool{stale: false, test: false, own: false, busy: true, sbx: true, other: true, fresh: true} {
		if Exists(p) != want {
			t.Errorf("%s exists %v, want %v", p, Exists(p), want)
		}
	}
	if len(gone) != 3 {
		t.Errorf("dropped %v", gone)
	}
}
