package e2e

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// The Hive's own code is a project its lead works on: asked to fix the Hive,
// the lead forks the Hive's repository under the person's account, files the
// brief there, and the change goes through the gate like any other. The kept
// merge is released on the fork (never on a repository the lead named), which is now this Hive's next update, and is
// offered upstream as a pull request once the person says yes.
func TestHiveWorksOnItself(t *testing.T) {
	t.Parallel()
	h := start(t)
	up := h.ws.LoadConfig().ReleaseRepo
	h.seed(up, map[string]string{".hive/project.json": `{"test": "true", "dryrun": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "fix the hive itself: the sweep should say why it skipped"}, nil)
	h.wait("the brief to land in the Hive's own project", 3*minute, taskStage("hive", "the sweep should say why it skipped", work.StageDone))
	h.wait("a release on the fork", 2*minute, func(api.State) bool {
		rel, err := h.forge.LatestRelease("local/hive")
		return err == nil && rel.Tag == "v0.0.1"
	})
	st := h.wait("the person asked before anything goes upstream", minute, func(st api.State) bool {
		for _, a := range st.Asks {
			if a.Kind == "upstream" {
				return len(h.forge.PRs(up)) == 0
			}
		}
		return false
	})
	for _, a := range st.Asks {
		if a.Kind == "upstream" {
			h.post("/api/answer", map[string]string{"id": a.ID, "answer": "yes"}, nil)
		}
	}
	h.wait("the change offered upstream", minute, func(api.State) bool {
		for _, pr := range h.forge.PRs(up) {
			if pr.State == "OPEN" {
				return true
			}
		}
		return false
	})
}
