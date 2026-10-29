package release

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// onMain is a Hive on version that releases the Hive (its self project is
// release_repo), with the self project's clone holding files; goBuild writes
// "built <tag> from <src's VERSION>" instead of compiling.
func onMain(t *testing.T, version string, files map[string]string) (*Module, string, string) {
	m, exe := releaseAt(t, version, "me/hive")
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Branch: "main", Self: true}); err != nil {
		t.Fatal(err)
	}
	sha := selfClone(t, m, files)
	b := goBuild
	t.Cleanup(func() { goBuild = b })
	goBuild = func(_ core.Workspace, src, out, goos, goarch, tag, repo string) error {
		v, _ := os.ReadFile(filepath.Join(src, "VERSION"))
		return os.WriteFile(out, []byte("built "+tag+" from "+strings.TrimSpace(string(v))), 0o755)
	}
	return m, exe, sha
}

// The Hive that releases the Hive follows main: the commit it just released
// is built here from its own clone and installed, with no download, release
// build or canary; the released tag it rolled back from isn't taken again.
func TestMainInstallsTheReleasedCommitBuiltHere(t *testing.T) {
	m, exe, sha := onMain(t, "v0.1.2", map[string]string{"VERSION": "0.1", "cmd/hive/main.go": "package main"})
	if m.Channel() != Main {
		t.Fatalf("the Hive that releases the Hive follows %q, not main", m.Channel())
	}
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "already on v0.1.2") {
		t.Fatalf("nothing released yet: %q, %v", msg, err)
	}
	m.C.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.1.3", "sha": sha, "prerelease": true})
	restarted := make(chan bool, 1)
	m.Restart = func() { restarted <- true }
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.3") || string(raw) != "built v0.1.3 from 0.1" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
	if old, _ := os.ReadFile(exe + ".old"); string(old) != "old" {
		t.Fatalf("the replaced binary isn't kept: %q", old)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no restart")
	}
	m.C.J.MustAppend("hive.rolledback", map[string]string{"from": "v0.1.3", "to": "v0.1.2", "why": "x"})
	if tag := m.mainTag(); tag != "" {
		t.Fatalf("a release rolled back from is taken again: %q", tag)
	}
}

// A build that fails installs nothing and says why.
func TestMainBuildFailureInstallsNothing(t *testing.T) {
	m, exe, sha := onMain(t, "v0.1.2", map[string]string{"VERSION": "0.1"})
	goBuild = func(core.Workspace, string, string, string, string, string, string) error { return os.ErrInvalid }
	m.C.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.1.3", "sha": sha})
	_, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), "doesn't build") || string(raw) != "old" {
		t.Fatalf("failed build: %v, binary %q", err, raw)
	}
}

// An automatic update waits for the checks running when it first looked, and
// installs once they end; one that starts meanwhile doesn't hold it.
func TestUpdateWaitsForRunningChecks(t *testing.T) {
	m, exe, sha := onMain(t, "v0.1.2", map[string]string{"VERSION": "0.1"})
	m.C.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.1.3", "sha": sha})
	done := m.C.Effects.Slots.Acquire("hive", "abc", "test")
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "1 check(s) running") {
		t.Fatalf("didn't wait for the running check: %q", msg)
	}
	done()
	later := m.C.Effects.Slots.Acquire("hive", "def", "test")
	defer later()
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.3") {
		t.Fatalf("after the check ended: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "built v0.1.3 from 0.1" {
		t.Fatalf("binary %q", raw)
	}
}

// Fallback puts the binary the last update replaced back at once, journals
// the release it left as rolled back and restarts; with nothing kept it
// refuses.
func TestFallbackPutsTheReplacedBinaryBack(t *testing.T) {
	m, exe, sha := onMain(t, "v0.1.2", map[string]string{"VERSION": "0.1"})
	if _, err := m.Fallback("broken"); err == nil {
		t.Fatal("fell back with no update behind it")
	}
	m.C.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.1.3", "sha": sha})
	m.Restart = func() {}
	if _, err := m.AutoUpdate(); err != nil {
		t.Fatal(err)
	}
	core.Version = "v0.1.3" // the new daemon
	restarted := make(chan bool, 1)
	m.Restart = func() { restarted <- true }
	msg, err := m.Update("back")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.Contains(msg, "falling back to v0.1.2") || string(raw) != "old" {
		t.Fatalf("fallback: %q, %v, binary %q", msg, err, raw)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no restart")
	}
	if !m.rolledBack("v0.1.3") {
		t.Fatal("the release fallen back from isn't recorded as rolled back")
	}
}

// Following main, each merge copies main's default modules into .modules,
// so a merged prompt shapes the next turn with no release; a module edited
// here by hand stays as it is.
func TestLiveSyncCopiesMainsModules(t *testing.T) {
	m, _, _ := onMain(t, "v0.1.2", map[string]string{"modules/agents/worker.md": "v1 worker", "modules/chat/module.json": `{"name":"chat"}`})
	reg := modules.New(m.C.WS, m.C.J)
	m.Live = reg.InstallFrom
	dir := m.C.WS.Project("hive")
	core.Git(dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if msg := m.LiveSync(false); !strings.HasPrefix(msg, "modules live at") {
		t.Fatalf("first copy: %q", msg)
	}
	read := func(rel string) string {
		raw, _ := os.ReadFile(filepath.Join(m.C.WS.Modules(), rel))
		return string(raw)
	}
	if got := reg.Prompt("worker.md"); got != "v1 worker" {
		t.Fatalf("worker prompt %q", got)
	}
	os.WriteFile(filepath.Join(dir, "modules/agents/worker.md"), []byte("v2 worker"), 0o644)
	os.WriteFile(filepath.Join(m.C.WS.Modules(), "chat", "module.json"), []byte(`{"name":"chat","edited":true}`), 0o644)
	os.WriteFile(filepath.Join(dir, "modules/chat/module.json"), []byte(`{"name":"chat","v":2}`), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "y"}, {"update-ref", "refs/remotes/origin/main", "HEAD"}} {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if msg := m.LiveSync(false); !strings.HasPrefix(msg, "modules live at") {
		t.Fatalf("second copy: %q", msg)
	}
	if got := read("agents/worker.md"); got != "v2 worker" {
		t.Fatalf("the merged prompt isn't live: %q", got)
	}
	if got := read("chat/module.json"); !strings.Contains(got, "edited") {
		t.Fatalf("a module edited here was overwritten: %q", got)
	}
	if msg := m.LiveSync(false); msg != "" {
		t.Fatalf("copied again with main where it was: %q", msg)
	}
}

// A tree's name that would land outside the folder is refused.
func TestUntarRefusesEscapes(t *testing.T) {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	w.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	w.Write([]byte("x"))
	w.Close()
	dst := t.TempDir()
	if err := untar(&b, filepath.Join(dst, "in")); err == nil {
		t.Fatal("wrote outside the folder")
	}
	if _, err := os.Stat(filepath.Join(dst, "evil")); err == nil {
		t.Fatal("the file escaped")
	}
}

// The latest channel looks only on its schedule, hours apart, so a check
// running at the look never holds the update.
func TestScheduledLatestUpdateIgnoresRunningChecks(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	m.Restart = func() {}
	done := m.C.Effects.Slots.Acquire("hive", "abc", "test")
	defer done()
	if m.Channel() != Latest {
		t.Fatalf("channel %q", m.Channel())
	}
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v9.9.9") || string(raw) != "new" {
		t.Fatalf("scheduled update held by a running check: %q, %v, binary %q", msg, err, raw)
	}
}

// Following main, the wait for running checks is bounded from its first look
// at a release, however far apart the looks are: past checksWaitMax it
// installs anyway.
func TestCheckWaitEndsAtItsCap(t *testing.T) {
	m, exe, sha := onMain(t, "v0.1.2", map[string]string{"VERSION": "0.1"})
	m.Restart = func() {}
	m.C.J.MustAppend("hive.released", map[string]any{"project": "hive", "tag": "v0.1.3", "sha": sha})
	done := m.C.Effects.Slots.Acquire("hive", "abc", "test")
	defer done()
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "1 check(s) running") {
		t.Fatalf("didn't wait: %q", msg)
	}
	m.checksSince = time.Now().Add(-checksWaitMax - time.Minute) // the first look, long ago
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.3") {
		t.Fatalf("past the cap: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "built v0.1.3 from 0.1" {
		t.Fatalf("binary %q", raw)
	}
}
