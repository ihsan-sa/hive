package e2e

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// TestCodexEndToEnd drives the Codex adapter through the daemon with the
// fake agent behind Codex's command line (`hive fake-codex`): a project on
// Codex takes a brief through its worker, a reviewer's structured verdict and
// the merge, Codex's plan usage is read from its session file, and a turn at
// its usage limit parks and resumes. The live run of the same path is
// TestLiveWorker with HIVE_E2E_HARNESS=codex.
func TestCodexEndToEnd(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Getenv("HIVE_E2E_SANDBOX") == "auto" || os.Getenv("HIVE_E2E_HARNESS") != "" {
		t.Skip("a shell script stands in for codex, on the host's PATH")
	}
	bindir, login := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bindir, "codex"), fmt.Appendf(nil, "#!/bin/sh\nexec %q fake-codex \"$@\"\n", bin), 0o755)
	claim := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, time.Now().Add(48*time.Hour).Unix()))
	os.WriteFile(filepath.Join(login, "auth.json"), fmt.Appendf(nil, `{"tokens":{"access_token":"x.%s.y","refresh_token":"r"}}`, claim), 0o600)
	h := start(t, "PATH="+bindir+string(os.PathListSeparator)+os.Getenv("PATH"), "CODEX_HOME="+login)
	h.seed("me/cx", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "cx", "repo": "me/cx"}, nil)
	cfg := h.ws.LoadConfig()
	cfg.Projects = map[string]core.ProjectSettings{"cx": {Harness: "codex"}}
	h.post("/api/config", cfg, nil)

	h.post("/api/ask", map[string]string{"project": "cx", "title": "add a note FAKE:limit"}, nil)
	st := h.wait("the Codex worker's change to merge", 4*minute, taskStage("cx", "add a note FAKE:limit", work.StageDone))
	roles := map[string]bool{}
	for _, r := range st.Runs {
		if r.Project == "cx" && core.HarnessName(r.Harness) == "codex" {
			roles[r.Role] = true
		}
	}
	if !roles["worker"] || !roles["reviewer"] {
		t.Fatalf("the worker and reviewer didn't run on Codex: %v", roles)
	}
	read := false
	for _, p := range st.Plans {
		read = read || core.HarnessName(p.Harness) == "codex" && len(p.Limit.Windows) > 0
	}
	if !read {
		t.Fatalf("no Codex usage was read: %+v", st.Plans)
	}
	if !h.journaled("run.turn_ended", `"status":"parked"`) {
		t.Error("the turn at its usage limit wasn't parked")
	}
}
