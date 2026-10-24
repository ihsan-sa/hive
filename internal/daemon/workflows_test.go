package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The workflows that drive a released binary (update.yml waits for the
// updated daemon in its daemon.json) read the file where this build writes
// it: v0.1.4 moved it under .hive/private and update.yml went red on every
// system looking for it in .hive. Another path is a fallback for an older
// release's daemon, on a line that names this one first.
func TestWorkflowsReadTheDaemonFileWhereItIs(t *testing.T) {
	want := filepath.ToSlash(core.Workspace{}.DaemonFile())
	files, _ := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if len(files) == 0 {
		t.Fatal("no workflows found")
	}
	path := regexp.MustCompile(`\.hive/(?:[\w.-]+/)*daemon\.json`)
	seen := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			for i, got := range path.FindAllString(line, -1) {
				seen++
				if got != want && (i == 0 || !strings.Contains(line, want)) {
					t.Errorf("%s reads %s; the daemon writes %s:\n%s", filepath.Base(f), got, want, strings.TrimSpace(line))
				}
			}
		}
	}
	if seen == 0 {
		t.Error("no workflow reads daemon.json; update.yml should wait for the updated daemon in it")
	}
}
