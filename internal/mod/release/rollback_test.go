package release

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A new binary that doesn't run is put back before the restart, and the
// update says so.
func TestUpdateThatDoesntRunIsPutBack(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	tryBinary = func(string, string) error { return errors.New("exec format error") }
	restarted := false
	m.Restart = func() { restarted = true }
	_, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), "v9.9.9 doesn't run here (exec format error); kept v0.1.2") || string(raw) != "old" {
		t.Fatalf("update: %v, binary %q", err, raw)
	}
	if _, ok := readPending(m.C.WS); ok || restarted || journaled(m, "hive.updated") {
		t.Fatal("an update that was put back still counts as one")
	}
}

// The new daemon counts its starts; the third (two failed) puts the old
// binary back and names it for the old daemon, which says so and doesn't
// install that release again unless asked for it by name.
func TestFailedStartsRollBack(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	if _, err := m.Update(""); err != nil {
		t.Fatal(err)
	}
	core.Version = "v9.9.9" // the new daemon starts, and fails, twice
	for i := 0; i < failedStarts; i++ {
		if back := Boot(m.C.WS); back != "" {
			t.Fatalf("start %d rolled back", i+1)
		}
	}
	if back := Boot(m.C.WS); back != exe {
		t.Fatalf("the third start returned %q, want %q", back, exe)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" {
		t.Fatalf("binary after the rollback: %q", raw)
	}
	if back := Boot(m.C.WS); back != "" {
		t.Fatal("a second rollback")
	}
	core.Version = "v0.1.2" // the old daemon starts again
	m.Report()
	if !journaled(m, "hive.rolledback") {
		t.Fatal("the rollback wasn't journaled")
	}
	if _, ok := readPending(m.C.WS); ok {
		t.Fatal("update.json outlived the report")
	}
	var alert string
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "alert.raised" {
			alert = string(e.Data)
		}
	})
	if !strings.Contains(alert, "rolled back to v0.1.2") {
		t.Fatalf("no alert says so: %s", alert)
	}
	msg, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.Contains(msg, "was rolled back") || string(raw) != "old" {
		t.Fatalf("the next update: %q, %v, binary %q", msg, err, raw)
	}
	if _, err := m.Update("v9.9.9"); err != nil {
		t.Fatalf("asked for by name: %v", err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary after asking by name: %q", raw)
	}
}

// A new daemon that stays up keeps its update: later starts never roll back.
func TestSettledUpdateStays(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	if _, err := m.Update(""); err != nil {
		t.Fatal(err)
	}
	core.Version = "v9.9.9"
	Boot(m.C.WS)
	Settled(m.C.WS)
	for i := 0; i < 5; i++ {
		if back := Boot(m.C.WS); back != "" {
			t.Fatal("a settled update rolled back")
		}
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary: %q", raw)
	}
}

// An update whose restart never came finds the binary in place; updating
// again keeps the .old a rollback goes back to.
func TestUpdateAgainKeepsTheOld(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	for i := 0; i < 2; i++ {
		if _, err := m.Update(""); err != nil {
			t.Fatal(err)
		}
	}
	if raw, _ := os.ReadFile(exe + ".old"); string(raw) != "old" {
		t.Fatalf(".old after a second update: %q", raw)
	}
}

// An update whose restart never came added a binary beside this one (as
// Windows' Linux one); updating again still lists it as added, so a rollback
// removes it rather than putting the same new binary back.
func TestUpdateAgainKeepsWhatItAdded(t *testing.T) {
	other := AssetName("plan9", "amd64")
	serve(t, "v9.9.9", map[string]string{AssetName(runtime.GOOS, runtime.GOARCH): "new", other: "new"}, false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	beside := filepath.Join(filepath.Dir(exe), other)
	a := alongside
	t.Cleanup(func() { alongside = a })
	alongside = map[string][]string{runtime.GOOS: {other}}
	for i := 0; i < 2; i++ {
		if _, err := m.Update(""); err != nil {
			t.Fatal(err)
		}
		p, _ := readPending(m.C.WS)
		if !slices.Equal(p.Added, []string{beside}) || !slices.Equal(p.Paths, []string{exe}) || core.Exists(beside+".old") {
			t.Fatalf("update %d: paths %v, added %v", i+1, p.Paths, p.Added)
		}
	}
	p, _ := readPending(m.C.WS)
	if err := rollBack(p.Paths, p.Added); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" || core.Exists(beside) {
		t.Fatalf("after rollback: binary %q, added still there: %v", raw, core.Exists(beside))
	}
}

// tryBinary passes a binary that runs and names the tag, and nothing else.
func TestTryBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for the binary")
	}
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good"), filepath.Join(dir, "bad")
	os.WriteFile(good, []byte("#!/bin/sh\necho hive v9.9.9\n"), 0o755)
	os.WriteFile(bad, []byte("not a binary"), 0o755)
	if err := tryBinary(good, "v9.9.9"); err != nil {
		t.Fatalf("a good binary: %v", err)
	}
	if err := tryBinary(good, "v9.9.8"); err == nil {
		t.Fatal("a binary that says another version passed")
	}
	if err := tryBinary(bad, "v9.9.9"); err == nil {
		t.Fatal("a file that doesn't run passed")
	}
}

// A binary the update put where none was (Windows' Linux one, on an install
// without it) doesn't stop a rollback, which removes it.
func TestRollBackRemovesWhatTheUpdateAdded(t *testing.T) {
	dir := t.TempDir()
	exe, added := filepath.Join(dir, "hive"), filepath.Join(dir, "hive-linux-amd64")
	os.WriteFile(exe, []byte("new"), 0o755)
	os.WriteFile(exe+".old", []byte("old"), 0o755)
	os.WriteFile(added, []byte("new"), 0o755)
	if err := rollBack([]string{exe}, []string{added}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" || core.Exists(added) {
		t.Fatalf("binary %q, added still there: %v", raw, core.Exists(added))
	}
}

// hold makes path a binary that can't be removed, as Windows refuses for one
// still running (the daemon an earlier update replaced, running from its
// .old): there it runs this test binary from path; elsewhere removeFile
// refuses it. The returned func lets it go.
func hold(t *testing.T, path string) func() {
	if runtime.GOOS == "windows" {
		self, err := os.Executable()
		raw, _ := os.ReadFile(self)
		if err != nil || os.WriteFile(path, raw, 0o755) != nil {
			t.Fatal("can't copy the test binary")
		}
		cmd := exec.Command(path, "-test.run=^TestHeldBinary$")
		cmd.Env = append(os.Environ(), "HIVE_TEST_HOLD=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		let := func() { cmd.Process.Kill(); cmd.Wait() }
		t.Cleanup(let)
		if os.Remove(path) == nil {
			t.Fatal("Windows removed a running binary")
		}
		return let
	}
	os.WriteFile(path, []byte("held"), 0o755)
	held := true
	r := removeFile
	t.Cleanup(func() { removeFile = r })
	removeFile = func(p string) error {
		if raw, _ := os.ReadFile(p); held && string(raw) == "held" {
			return errors.New("Access is denied.")
		}
		return os.Remove(p)
	}
	return func() { held = false }
}

// TestHeldBinary is what hold runs on Windows: a process that stays up.
func TestHeldBinary(t *testing.T) {
	if os.Getenv("HIVE_TEST_HOLD") == "" {
		t.Skip("run by hold")
	}
	time.Sleep(time.Minute)
}

// The owner's Windows laptop (issue #252): `hive update` failed with "rename
// hive.exe hive.exe.old: Access is denied." because the daemon an earlier
// update replaced still ran from hive.exe.old. The stuck .old (and a stuck
// .failed from an earlier rollback) moves aside, the update and its rollback
// both work, and a later start sweeps what moved aside once it is let go.
func TestUpdateWithAnOldStillRunning(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	letOld := hold(t, exe+".old")
	letFailed := hold(t, exe+".failed")
	if _, err := m.Update(""); err != nil {
		t.Fatalf("update with a running .old: %v", err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary after the update: %q", raw)
	}
	if raw, _ := os.ReadFile(exe + ".old"); string(raw) != "old" {
		t.Fatalf(".old is %q, not the binary the update replaced", raw)
	}
	if !core.Exists(exe + ".old-1") {
		t.Fatal("the running .old wasn't moved aside")
	}
	core.Version = "v9.9.9"
	for i := 0; i <= failedStarts; i++ {
		Boot(m.C.WS)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" {
		t.Fatalf("binary after the rollback: %q", raw)
	}
	if raw, _ := os.ReadFile(exe + ".failed"); string(raw) != "new" || !core.Exists(exe+".failed-1") {
		t.Fatalf(".failed is %q", raw)
	}
	Sweep(exe)
	if !core.Exists(exe+".old-1") || !core.Exists(exe+".failed-1") {
		t.Fatal("the sweep removed binaries still running")
	}
	letOld()
	letFailed()
	Sweep(exe)
	if core.Exists(exe+".old-1") || core.Exists(exe+".failed-1") || !core.Exists(exe) {
		t.Fatal("a later start didn't sweep what moved aside")
	}
}

func TestOnDisk(t *testing.T) {
	for in, want := range map[string]string{"hive.exe.old": "hive.exe", "hive.exe.old-12": "hive.exe", "hive.exe": "hive.exe", "hive.exe.older": "hive.exe.older", "hive.old-x": "hive.old-x"} {
		if got := OnDisk(in); got != want {
			t.Errorf("OnDisk(%q) = %q, want %q", in, got, want)
		}
	}
}
