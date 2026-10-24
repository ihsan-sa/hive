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
	d := m.Digest(0, "")
	if !strings.Contains(d, "raise the score with more") || !strings.Contains(d, "Score: none defined") {
		t.Fatalf("digest lacks goals or score line:\n%s", d)
	}
}

// Agents' open feedback reaches the sweep through the digest, scoped as the
// rest of it is.
func TestDigestCarriesFeedback(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &agents.Agents{C: c}
	ch := chat.New(c, a)
	m := New(c, a, work.New(c, a, ch), ch)
	var asked []bool
	m.Feedback = func(mine func(string) bool) string {
		asked = append(asked, mine(""))
		return "- the brief named no base\n"
	}
	since := c.J.Seq()
	if d := m.Digest(0, ""); !strings.Contains(d, "## Agents' feedback on the Hive, still open\n\n- the brief named no base") {
		t.Fatalf("digest lacks the feedback:\n%s", d)
	}
	if m.Digest(0, "secret"); len(asked) != 2 || !asked[0] || asked[1] {
		t.Fatalf("project-less feedback should reach the workspace digest only: %v", asked)
	}
	c.J.MustAppend("feedback.saved", map[string]string{"id": "f1"})
	if !m.anythingNew(since) {
		t.Fatal("new feedback alone doesn't count as something new for a sweep")
	}
}
