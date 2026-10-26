package daemon

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// A fresh workspace set to a harness a default module ships starts: the
// check finds it once the default modules are written, not only after the
// daemon has written them.
func TestFreshWorkspaceOnADefaultModulesHarness(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	harness.UseModules(ws.Modules())
	t.Cleanup(func() { harness.UseModules("") })
	c := ws.LoadConfig()
	c.Harness = "opencode"
	if err := ws.SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	if err := checkStartHarness(ws); err != nil {
		t.Fatal(err)
	}
	c.Harness = "nope"
	ws.SaveConfig(c)
	if err := checkStartHarness(ws); err == nil {
		t.Fatal("an unknown harness passed")
	}
}
