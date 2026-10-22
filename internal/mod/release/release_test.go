package release

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// releaseAt is the release module in a build stamped version, with v9.9.9 out.
func releaseAt(t *testing.T, version string) (*Module, *bool) {
	dir := t.TempDir()
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	forge.CreateRelease(c.WS.LoadConfig().ReleaseRepo, "v9.9.9", "", "")
	restarted := false
	m := &Module{C: c, Restart: func() { restarted = true }}
	v := core.Version
	t.Cleanup(func() { core.Version = v })
	core.Version = version
	return m, &restarted
}

// A source build (no release version) never replaces itself with a release.
func TestSourceBuildNeverUpdates(t *testing.T) {
	m, restarted := releaseAt(t, "0.0.0-dev")
	msg, err := m.Update()
	if err != nil || !strings.Contains(msg, "source build") || *restarted {
		t.Fatalf("source build: %q, %v, restarted=%v", msg, err, *restarted)
	}
}

// A release build, stamped with its tag as release.yml does, is offered the
// newer release and goes on to fetch it (the local forge has no assets to give).
func TestReleaseBuildUpdates(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.0")
	if tag, ok, err := m.Check(); err != nil || !ok || tag != "v9.9.9" {
		t.Fatalf("check: %q %v %v", tag, ok, err)
	}
	if _, err := m.Update(); err == nil || !strings.Contains(err.Error(), "release assets") {
		t.Fatalf("the update didn't try to download v9.9.9: %v", err)
	}
}
