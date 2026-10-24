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
		{"main.go", "package main\n", self, false},                 // a self project is default-deny: code is read
		{"docs/notes.md", "# notes\n", self, true},                 // a doc under docs/ is not
		{"internal/app/selfgate.go", "package app\n", self, false}, // the recursive gate's verdict, on no list
		{"internal/mod/runner/run.go", "package runner\n", self, false},
		{"internal/proc/proc.go", "package proc\n", self, false},
		{"internal/notes.md", "# notes\n", self, false}, // a doc among its code too
		{"scripts/x.sh", "echo\n", self, false},
		// Docs that act as prompts or rules stay read: only docs/ is allowed.
		{"CLAUDE.md", "# rules\n", self, false},
		{"AGENTS.md", "# rules\n", self, false},
		{"LEARNINGS.md", "# gotchas\n", self, false},
		{".hive/lessons.md", "# lessons\n", self, false},
		{"README.md", "# hive\n", self, false},
		{"docs/spec.md", "# spec\n", self, false}, // the owner's spec keeps a reader
		{"docs/the-hive-essay.md", "# essay\n", self, false},
		{"main.go", strings.Repeat("x\n", DefaultUnreadMax+1), p, false},
		{"cred/token.go", "package cred\n", p, false},
		{"setup.sh", "echo hi\n", p, false},
		{"box/wall.go", "package box\n", p, false},
		{"internal/sandbox/linux.go", "package sandbox\n", self, false},
		{".hive/goals.md", "# Goals\n\nmore\n", p, false},
		{"blob.bin", "a\x00b", p, false},
		// What defines the security bar and the checks is read in any project.
		{"tests/LANDING-security.toml", "paths = []\n", p, false},
		{".github/workflows/ci.yml", "on: push\n", p, false},
		{".hive/lessons.md", "# lessons\n", p, false},
		// In a self project the code and prompts that define the gate are
		// never unread, so a small edit can't move its own bar unread.
		{"internal/core/projects.go", "package core\n", self, false},
		{"internal/mod/work/work.go", "package work\n", self, false},
		{"modules/agents/reviewer.md", "# reviewer\n", self, false},
		{"internal/core/projects.go", "package core\n", p, true}, // only the Hive's own repo
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
	req := MergeRequest{Project: p, PR: pr.Number, SHA: sha, Main: main}
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("an unread merge must name its task: %v", err)
	}
	// A reader's verdict on an earlier head of the task keeps every later
	// head read, however small, whichever way it reaches the merge.
	c.Runs.Start(RunSpec{ID: "r-old", Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Task: "read-before", Inputs: map[string]string{"sha": main}})
	if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: main, Run: "r-old", Verdict: "fix"}); err != nil {
		t.Fatal(err)
	}
	req.Task = "read-before"
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("a task read before merged unread: %v", err)
	}
	req.Task = "tu"
	if _, err := c.Effects.Merge(req); err != nil {
		t.Fatalf("an unread change should merge on its tests: %v", err)
	}
	var merged struct {
		Unread, Sample bool
		Lines          int
	}
	c.J.Replay(func(e Event) {
		if e.Kind == "effect.merged" {
			e.Decode(&merged)
		}
	})
	if !merged.Unread || merged.Lines != 1 || merged.Sample != Sampled("tu", DefaultSampleUnread) {
		t.Fatalf("the merge should say it went unread, and whether it is sampled: %+v", merged)
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

// A high path needs two reads at any size, as the root-path guard says. An
// ordinary read change under the second-read line needs one; an unsure land
// asks for a second, separate one; one over the line needs two.
func TestSecondReadOnlyWhenNeeded(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/h2", map[string]string{".hive/project.json": `{"test": "true", "high": ["cred/"], "second_read_lines": 10, "unread_max_lines": -1}`})
	p, _ := c.Projects.Add(Project{Name: "h2", Repo: "me/h2"})
	main, _ := c.Effects.Sync(p)
	try := func(file, body string, reads ...Verdict) error {
		t.Helper()
		id := strings.NewReplacer("/", "-", ".", "-").Replace(file)
		r, _ := commitInCheckout(t, c, p, "w"+id, main, file, body) // a file each, so merges never conflict
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
	if err := try("cred/a.go", "package cred\n", Verdict{Verdict: "land"}); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("a small high change merged on one read: %v", err)
	}
	if err := try("cred/b.go", "package cred\n", Verdict{Verdict: "land"}, Verdict{Verdict: "land"}); err != nil {
		t.Fatalf("a high change should merge on two reads: %v", err)
	}
	if err := try("app/a.go", "package app\n", Verdict{Verdict: "land"}); err != nil {
		t.Fatalf("a small ordinary change should merge on one read: %v", err)
	}
	if err := try("app/b.go", "package app\n", Verdict{Verdict: "land", Unsure: true}); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("an unsure land should need a second read: %v", err)
	}
	if err := try("app/c.go", "package app\n", Verdict{Verdict: "land", Unsure: true}, Verdict{Verdict: "land"}); err != nil {
		t.Fatalf("a second land after an unsure one should merge: %v", err)
	}
	big := "package app\n" + strings.Repeat("// d\n", 10)
	if err := try("app/d.go", big, Verdict{Verdict: "land"}); err == nil {
		t.Fatal("a change over the second-read line merged on one read")
	}
	if err := try("app/e.go", big, Verdict{Verdict: "land"}, Verdict{Verdict: "land"}); err != nil {
		t.Fatalf("two reads should merge a big change: %v", err)
	}
}
