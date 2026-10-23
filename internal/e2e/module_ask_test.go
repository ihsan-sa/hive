package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Asked for a module with one project open, the lead builds it as a project
// of its own, not in the project that happens to exist, says so, and once it
// merges asks to install it into .modules/, which a warden approves at
// autonomy 2 and the person hears after.
func TestAskForModule(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.post("/api/project", map[string]any{"demo": true}, nil)
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "make a module called ledger that keeps a cost and token ledger"}, nil)
	h.wait("the lead to say where the module is built", minute, func(api.State) bool {
		for _, m := range h.messages("lead") {
			if m.From != "person" && strings.Contains(m.Text, "project of its own") && strings.Contains(m.Text, "install") {
				return true
			}
		}
		return false
	})
	h.wait("the module's work to merge", 3*minute, taskStage("ledger", "Build module ledger", work.StageDone))
	st := h.wait("a warden to install the module", 2*minute, func(st api.State) bool {
		for _, m := range st.Modules {
			if m.Name == "ledger" {
				return true
			}
		}
		return false
	})
	h.wait("the person to be told after", minute, toldAbout("Install module ledger"))
	if !core.Exists(filepath.Join(h.ws.Modules(), "ledger", "ledger.md")) {
		t.Fatal("the installed module lacks the file the worker built")
	}
	for _, p := range st.Projects {
		if p.Name == "textkit" && len(p.Tasks) != 0 {
			t.Fatalf("the module's work landed in textkit: %+v", p.Tasks)
		}
	}
}
