package release

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A source build (no release version) never replaces itself with a release.
func TestSourceBuildNeverUpdates(t *testing.T) {
	dir := t.TempDir()
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, forge)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	forge.CreateRelease(c.WS.LoadConfig().ReleaseRepo, "v9.9.9", "", "")
	restarted := false
	m := &Module{C: c, Restart: func() { restarted = true }}
	defer func(v string) { core.Version = v }(core.Version)
	core.Version = "0.0.0-dev"
	msg, err := m.Update()
	if err != nil || !strings.Contains(msg, "source build") || restarted {
		t.Fatalf("source build: %q, %v, restarted=%v", msg, err, restarted)
	}
}
