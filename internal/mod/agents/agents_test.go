package agents

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// A read-only seat (reviewer, security read, judge) gets its role alone, not
// the orientation on working in the Hive; a worker still gets both.
func TestReadOnlySeatsGetTheirRoleAlone(t *testing.T) {
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
	a := &Agents{C: c, Mod: mods}
	orientation := mods.Prompt("orientation.md")
	for _, role := range []string{"reviewer", core.JudgeRole} {
		if s := a.System(role, ""); s != mods.Prompt(role+".md") || s == "" {
			t.Errorf("%s's system prompt isn't its role alone:\n%s", role, s)
		}
	}
	if s := a.System("worker", ""); !strings.HasPrefix(s, orientation) || !strings.Contains(s, mods.Prompt("worker.md")) {
		t.Error("a worker's system prompt lost the orientation or its role")
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

// A helper asked for on a plain model name runs on its project's harness;
// a named model on the open harness, harness:model as said.
func TestHelperPickFollowsTheProjectsHarness(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Harness = "claude"
	cfg.Models = map[string]core.OpenModel{"qwen": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"}}
	cfg.Projects = map[string]core.ProjectSettings{"p": {Harness: "codex"}}
	for _, tc := range []struct {
		project, model string
		want           core.Pick
	}{
		{"p", "gpt-5.5", core.Pick{Harness: "codex", Model: "gpt-5.5"}},
		{"q", "sonnet", core.Pick{Harness: "claude", Model: "sonnet"}},
		{"p", "claude:opus", core.Pick{Harness: "claude", Model: "opus"}},
		{"p", "qwen", core.Pick{Harness: "open", Model: "qwen"}},
	} {
		if got, err := helperPick(cfg, tc.project, tc.model); err != nil || got != tc.want {
			t.Errorf("helperPick(%q, %q) = %v, %v; want %v", tc.project, tc.model, got, err, tc.want)
		}
	}
}
