package agents

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The workspace lead gets lead_model; a project's own model wins for its lead
// as for its workers, as the README says.
func TestLeadModelYieldsToProject(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	cfg := core.DefaultConfig()
	cfg.Projects = map[string]core.ProjectSettings{"cheap": {Model: "haiku"}}
	ws.SaveConfig(cfg)
	a := &Agents{C: &core.Core{WS: ws}}
	for _, c := range []struct{ role, project, want string }{
		{"lead", "", "opus"},
		{"lead", "cheap", "haiku"},
		{"worker", "", "sonnet"},
	} {
		if _, got := a.Model(c.role, c.project); got != c.want {
			t.Errorf("%s in %q: model %q, want %q", c.role, c.project, got, c.want)
		}
	}
}
