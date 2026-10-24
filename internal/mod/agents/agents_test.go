package agents

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// In the first real run a lead wrote "no python on the machine" into three
// briefs from one exit 127, and it was wrong: the lead's prompt says a claim
// about the machine comes from a check, or is marked unchecked.
func TestLeadChecksMachineClaims(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mods := modules.New(ws, c.J)
	if err := mods.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	s := (&Agents{C: c, Mod: mods}).Spec("l", "lead", "", t.TempDir(), "")
	if !strings.Contains(s.System, "from a check you ran") || !strings.Contains(s.System, "marked unchecked") {
		t.Fatal("the lead's prompt doesn't ask for machine claims to be checked or marked")
	}
}

// A project's own model is more specific than lead_model, so it runs that
// project's lead; a lead with no project of its own gets lead_model.
func TestProjectModelBeatsLeadModel(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cfg := c.WS.LoadConfig()
	cfg.Model, cfg.LeadModel = "sonnet", "opus"
	cfg.Projects = map[string]core.ProjectSettings{"p": {Model: "haiku"}}
	if err := c.WS.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := &Agents{C: c}
	for _, tc := range []struct{ role, project, want string }{{"lead", "p", "haiku"}, {"lead", "", "opus"}, {"worker", "q", "sonnet"}} {
		if _, got := a.Model(tc.role, tc.project); got != tc.want {
			t.Errorf("Model(%q, %q) = %q, want %q", tc.role, tc.project, got, tc.want)
		}
	}
}
