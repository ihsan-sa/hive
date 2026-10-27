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

// The digest says who talked to whom since the last sweep, by role, and what
// was said, so a sweep can see messaging as it sees the rest of the record.
func TestDigestCarriesAgentsTalking(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := &agents.Agents{C: c}
	ch := chat.New(c, a)
	m := New(c, a, work.New(c, a, ch), ch)
	if d := m.Digest(0, ""); !strings.Contains(d, "## Messages between agents\n\nNone") {
		t.Fatalf("a quiet digest should say none:\n%s", d)
	}
	for _, s := range []core.RunSpec{{ID: "w1", Role: "worker", Harness: "fake"}, {ID: "lead1", Role: "lead", Harness: "fake"}} {
		if _, err := c.Runs.Start(s); err != nil {
			t.Fatal(err)
		}
	}
	old := c.J.Seq()
	c.J.MustAppend("msg.sent", map[string]string{"from": "w1", "to": "lead1", "text": "blocked on a flake"})
	since := c.J.Seq()
	c.J.MustAppend("msg.sent", map[string]string{"from": "w1", "to": "lead1", "text": "the flake is\nTestMeter"})
	c.J.MustAppend("msg.sent", map[string]string{"from": "lead1", "to": "w1", "text": "thanks"})
	d := m.Digest(since, "")
	for _, want := range []string{"2 since the last sweep (a day at most): lead → worker 1, worker → lead 1.", "w1 → lead1: the flake is TestMeter"} {
		if !strings.Contains(d, want) {
			t.Fatalf("digest lacks %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "blocked on a flake") || !strings.Contains(m.Digest(old, ""), "blocked on a flake") {
		t.Fatal("the digest should hold the messages since the sweep it's for, no earlier")
	}
}
