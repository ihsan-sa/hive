package core

import (
	"fmt"
	"strings"
	"testing"
)

// A small change no guard names is unread: it merges on its tests alone, and
// the merge says so. A guarded path, a binary file, a size over the line, or a
// project that turns the line off each need a read.
func TestUnreadChange(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/u", map[string]string{
		".hive/project.json":          `{"test": "true", "high": ["cred/"], "root": ["setup.sh"]}`,
		"tests/LANDING-security.toml": "paths = [\"box/**\"]\n",
	})
	p, _ := c.Projects.Add(Project{Name: "u", Repo: "me/u"})
	main, _ := c.Effects.Sync(p)
	self := p
	self.Self = true
	for i, tc := range []struct {
		file, body string
		proj       Project
		unread     bool
	}{
		{"main.go", "package main\n", p, true},
		{"main.go", "package main\n", self, true}, // a self project's unguarded code too
		{"main.go", strings.Repeat("x\n", DefaultUnreadMax+1), p, false},
		{"cred/token.go", "package cred\n", p, false},
		{"setup.sh", "echo hi\n", p, false},
		{"box/wall.go", "package box\n", p, false},
		{"internal/sandbox/linux.go", "package sandbox\n", self, false},
		{".hive/goals.md", "# Goals\n\nmore\n", p, false},
		{"blob.bin", "a\x00b", p, false},
	} {
		r, _ := commitInCheckout(t, c, p, fmt.Sprintf("wu-%d", i), main, tc.file, tc.body)
		sha, err := c.Effects.Extract(p, r, main)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := c.Effects.Classify(tc.proj, main, sha)
		if err != nil || ch.Unread != tc.unread {
			t.Errorf("%s (self %v): unread %v, want %v (%+v %v)", tc.file, tc.proj.Self, ch.Unread, tc.unread, ch, err)
		}
	}

	r, _ := commitInCheckout(t, c, p, "wu-merge", main, "main.go", "package main\n")
	sha, _ := c.Effects.Extract(p, r, main)
	c.Effects.Push(p, "hive/tu", sha)
	pr, _ := f.CreatePR(p.Repo, "hive/tu", "main", "tiny", "body")
	c.Effects.RunCheck(p, sha, "test", "true")
	if _, err := c.Effects.Merge(MergeRequest{Project: p, PR: pr.Number, SHA: sha, Main: main}); err != nil {
		t.Fatalf("an unread change should merge on its tests: %v", err)
	}
	var merged struct {
		Unread bool
		Lines  int
	}
	c.J.Replay(func(e Event) {
		if e.Kind == "effect.merged" {
			e.Decode(&merged)
		}
	})
	if !merged.Unread || merged.Lines != 1 {
		t.Fatalf("the merge should say it went unread: %+v", merged)
	}
}

// A project can move the unread line or turn it off.
func TestUnreadLineInConfig(t *testing.T) {
	if c := ParseProjectConfig(`{}`); c.UnreadMax != DefaultUnreadMax || c.SampleUnread != DefaultSampleUnread || c.ReadTwoMax != DefaultReadTwoMax {
		t.Fatalf("defaults: %+v", c)
	}
	c, f := testHive(t)
	seedRepo(t, f, "me/off", map[string]string{".hive/project.json": `{"test": "true", "unread_max_lines": -1, "second_read_lines": 5}`})
	p, _ := c.Projects.Add(Project{Name: "off", Repo: "me/off"})
	main, _ := c.Effects.Sync(p)
	r, _ := commitInCheckout(t, c, p, "wo", main, "main.go", "package main\n")
	sha, _ := c.Effects.Extract(p, r, main)
	if ch, _ := c.Effects.Classify(p, main, sha); ch.Unread || ch.TwoReads {
		t.Fatalf("a line of -1 reads everything, and 1 line is under 5: %+v", ch)
	}
	r2, _ := commitInCheckout(t, c, p, "wo2", main, "main.go", strings.Repeat("x\n", 6))
	sha2, _ := c.Effects.Extract(p, r2, main)
	if ch, _ := c.Effects.Classify(p, main, sha2); !ch.TwoReads {
		t.Fatalf("6 lines is over a second-read line of 5: %+v", ch)
	}
}

// A high (not root) change under the second-read line needs one read; an
// unsure land asks for a second, separate one; one over the line needs two.
func TestSecondReadOnlyWhenNeeded(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/h2", map[string]string{".hive/project.json": `{"test": "true", "high": ["cred/"], "second_read_lines": 10}`})
	p, _ := c.Projects.Add(Project{Name: "h2", Repo: "me/h2"})
	main, _ := c.Effects.Sync(p)
	try := func(id, body string, reads ...Verdict) error {
		t.Helper()
		r, _ := commitInCheckout(t, c, p, "w"+id, main, "cred/"+id+".go", body) // a file each, so merges never conflict
		sha, _ := c.Effects.Extract(p, r, main)
		c.Effects.Push(p, "hive/"+id, sha)
		pr, _ := f.CreatePR(p.Repo, "hive/"+id, "main", id, "body")
		c.Effects.RunCheck(p, sha, "test", "true")
		for i, v := range reads {
			run := fmt.Sprintf("r%s-%d", id, i)
			c.Runs.Start(RunSpec{ID: run, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": sha}})
			v.Project, v.SHA, v.Run = p.Name, sha, run
			if err := c.Effects.RecordVerdict(v); err != nil {
				t.Fatal(err)
			}
		}
		_, err := c.Effects.Merge(MergeRequest{Project: p, PR: pr.Number, SHA: sha, Main: main})
		return err
	}
	if err := try("a", "package cred\n", Verdict{Verdict: "land"}); err != nil {
		t.Fatalf("a small high change should merge on one read: %v", err)
	}
	if err := try("b", "package cred\n// b\n", Verdict{Verdict: "land", Unsure: true}); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("an unsure land should need a second read: %v", err)
	}
	if err := try("c", "package cred\n// c\n", Verdict{Verdict: "land", Unsure: true}, Verdict{Verdict: "land"}); err != nil {
		t.Fatalf("a second land after an unsure one should merge: %v", err)
	}
	big := "package cred\n" + strings.Repeat("// d\n", 10)
	if err := try("d", big, Verdict{Verdict: "land"}); err == nil {
		t.Fatal("a change over the second-read line merged on one read")
	}
	if err := try("e", big+"// e\n", Verdict{Verdict: "land"}, Verdict{Verdict: "land"}); err != nil {
		t.Fatalf("two reads should merge a big change: %v", err)
	}
}
