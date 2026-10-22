package improve

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A fresh project's digest carries its goals, so the sweep can see them.
func TestDigestCarriesGoals(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Projects.Add(core.Project{Name: "bees", Repo: "me/bees", Goals: "FACTS.md holds true facts about bees; raise the score with more"}); err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c}
	ch := chat.New(c, a)
	m := New(c, a, work.New(c, a, ch), ch)
	d := m.Digest(0)
	if !strings.Contains(d, "raise the score with more") || !strings.Contains(d, "Score: none defined") {
		t.Fatalf("digest lacks goals or score line:\n%s", d)
	}
}
