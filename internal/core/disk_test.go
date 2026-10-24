package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// isolated is a sandbox with its own paths, for the module cache's path.
type isolated struct{ sandbox.Bare }

func (isolated) Isolated() bool { return true }

// A Go project's check gets the shared module cache read-only, offline, filled
// once per go.mod and go.sum by the effects process.
func TestChecksShareTheModuleCache(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the shared module cache is for Linux sandboxes")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH")
	}
	c, _ := testHive(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.21\n"), 0o644)
	g := c.Effects.goModules(isolated{}, dir)
	g.done()
	mounts, env := g.mounts, g.env
	if g.note != "" {
		t.Fatal(g.note)
	}
	if len(mounts) != 1 || mounts[0].Host != c.WS.GoModCache() || mounts[0].Inside != GoModInside || mounts[0].RW {
		t.Fatalf("mounts %+v", mounts)
	}
	for _, kv := range []string{"GOMODCACHE=" + GoModInside, "GOPROXY=off", "GOFLAGS=-mod=readonly"} {
		if !slices.Contains(env, kv) {
			t.Errorf("env %v lacks %s", env, kv)
		}
	}
	sums, _ := os.ReadDir(c.WS.goSums())
	if len(sums) != 1 {
		t.Fatalf("%d go.sum markers, want 1", len(sums))
	}
	// Filled once: a second check of the same sums doesn't fetch again,
	// even with no go to fetch with.
	t.Setenv("PATH", t.TempDir())
	if g := c.Effects.goModules(isolated{}, dir); g.note != "" {
		t.Fatalf("fetched again: %s", g.note)
	} else {
		g.done()
	}
	// Another go.mod has no go to fetch with, and gets no cache (it fetches its own).
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m2\n"), 0o644)
	if g := c.Effects.goModules(isolated{}, dir); g.mounts != nil || g.env != nil || !strings.Contains(g.note, "not prefetched") {
		t.Fatalf("without go: %+v", g)
	}
	// A bare run keeps the host's own module cache.
	if g := c.Effects.goModules(sandbox.Bare{}, dir); g.mounts != nil {
		t.Fatal("bare run got the shared cache")
	}
}

// A check that fails for want of room is can't-run, not red, and raises one
// alert naming what uses the room; a check that runs again ends it.
func TestDiskFullIsCantRun(t *testing.T) {
	c, _, p, main := setupProject(t)
	alerts := 0
	c.J.Subscribe(func(e Event) {
		if e.Kind == "alert.raised" && strings.Contains(string(e.Data), `"disk"`) {
			alerts++
		}
	})
	full := `echo "go: writing go.mod cache: write /hive/home/go/pkg/mod/cache/download/x: disk quota exceeded" >&2; exit 1`
	for i := 0; i < 2; i++ {
		ch := c.Effects.RunCheck(p, main, "test", full)
		if ch.OK || !strings.Contains(ch.CantRun, "disk") {
			t.Fatalf("check %+v", ch)
		}
		if _, ok := c.Effects.GetCheck(p.Name, main, "test"); ok {
			t.Fatal("a full disk was kept as the commit's answer")
		}
	}
	if alerts != 1 {
		t.Fatalf("%d disk alerts, want 1", alerts)
	}
	if d := c.Effects.Disk(); d.At.IsZero() || d.Total == 0 {
		t.Fatalf("the alert measured nothing: %+v", d)
	}
	if ch := c.Effects.RunCheck(p, main, "test", "echo 'write x: no space left on device'; exit 2"); ch.CantRun == "" {
		t.Fatalf("ENOSPC: %+v", ch)
	}
	if alerts != 1 {
		t.Fatal("a second alert while the first still stands")
	}
	c.Effects.RunCheck(p, main, "test", "true")
	c.Effects.RunCheck(p, main, "test", full)
	if alerts != 2 {
		t.Fatalf("after a check ran, a new shortage raised %d alerts in all, want 2", alerts)
	}
	// A failing test that quotes the error is the commit's red, not the disk's.
	for _, quoted := range []string{
		`printf -- '--- FAIL: TestX (0.00s)\n    x_test.go:9: check {CantRun:can'"'"'t run: disk quota exceeded}\nFAIL\n'; exit 1`,
		`printf '    x_test.go:9: write /x: no space left on device\nFAIL\n'; exit 1`,
	} {
		if ch := c.Effects.RunCheck(p, main, "test", quoted); ch.CantRun != "" || ch.OK {
			t.Fatalf("a test's own words read as a full disk: %+v", ch)
		}
	}
}

// A check's home past its cap ends the check by its stop file.
func TestCheckHomeCap(t *testing.T) {
	saved := homeEvery
	homeEvery = 10 * time.Millisecond
	t.Cleanup(func() { homeEvery = saved })
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "big"), make([]byte, 2<<20), 0o644)
	over := watchHome(home, 1<<20)
	deadline := time.Now().Add(5 * time.Second)
	for !Exists(home+".stop") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !over() || !Exists(home+".stop") {
		t.Fatal("the cap didn't stop the check")
	}
	os.Remove(home + ".stop")
	small := t.TempDir()
	if watchHome(small, 1<<20)() {
		t.Fatal("an empty home was over its cap")
	}
}

// Prune removes stale check homes and clean checkouts, never a live one or
// the self gate's folder, and runs' homes live under the cache, gone after.
func TestPruneRemovesLeftovers(t *testing.T) {
	c, _ := testHive(t)
	e := c.Effects
	old := time.Now().Add(-2 * staleAfter)
	mk := func(p string) string {
		os.MkdirAll(filepath.Join(p, "x"), 0o755)
		os.WriteFile(filepath.Join(p, "x", "f"), []byte("data"), 0o444)
		os.Chmod(filepath.Join(p, "x"), 0o555) // as the module cache leaves its folders
		os.Chtimes(p, old, old)
		return p
	}
	live, done := e.newHome()
	defer done()
	os.Chtimes(live, old, old)
	stale := mk(filepath.Join(e.ws.CheckHomes(), "home-1"))
	gate := mk(filepath.Join(e.ws.Gates(), "p", "abc-test-1"))
	recursive := mk(filepath.Join(e.ws.Gates(), "p", "recursive"))
	fresh := filepath.Join(e.ws.Gates(), "p", "def-test-2")
	os.MkdirAll(fresh, 0o755)
	t.Cleanup(func() { os.Chmod(filepath.Join(recursive, "x"), 0o755) })
	if got := e.Prune(); !strings.Contains(got, "removed 2") {
		t.Fatalf("prune said %q", got)
	}
	for p, want := range map[string]bool{live: true, stale: false, gate: false, recursive: true, fresh: true} {
		if Exists(p) != want {
			t.Errorf("%s exists %v, want %v", p, Exists(p), want)
		}
	}
	done()
	if Exists(live) {
		t.Fatal("a check's home outlived the check")
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 3 << 30: "3.0 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
	if DiskFull("ok\nwrite /x: disk quota exceeded\n") != "write /x: disk quota exceeded" || DiskFull("--- FAIL: TestX\nwrite /x: disk quota exceeded") != "" || DiskFull("\tdisk quota exceeded") != "" {
		t.Fatal("DiskFull")
	}
}
