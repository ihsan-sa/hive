package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testHive is a workspace with a local forge and bare (opted-in) runs.
func testHive(t *testing.T) (*Core, *Local) {
	t.Helper()
	dir := t.TempDir()
	ws := Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax = 0, 0, 0 // a loaded CI runner mustn't hold fake turns
	ws.SaveConfig(cfg)
	forge := &Local{Dir: filepath.Join(dir, "forge")}
	c, err := Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, forge
}

// seedRepo creates repo on the local forge with files on main.
func seedRepo(t *testing.T, f *Local, repo string, files map[string]string) {
	t.Helper()
	if _, err := f.EnsureRepo(repo, ""); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	mustGit(t, "", "clone", "-q", f.RepoPath(repo), tmp)
	for p, c := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(tmp, p)), 0o755)
		os.WriteFile(filepath.Join(tmp, p), []byte(c), 0o644)
	}
	mustGit(t, tmp, "add", "-A")
	mustGit(t, tmp, "commit", "-q", "-m", "seed")
	mustGit(t, tmp, "push", "-q", "origin", "HEAD:main")
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func setupProject(t *testing.T) (*Core, *Local, Project, string) {
	c, f := testHive(t)
	seedRepo(t, f, "me/demo", map[string]string{
		"README.md":          "# demo\n",
		"code.txt.go":        "package demo\n",
		".hive/project.json": `{"test": "test ! -f BROKEN"}`,
		".hive/goals.md":     "# Goals\n",
	})
	p, err := c.Projects.Add(Project{Name: "demo", Repo: "me/demo"})
	if err != nil {
		t.Fatal(err)
	}
	main, err := c.Effects.Sync(p)
	if err != nil || main == "" {
		t.Fatalf("sync: %v %q", err, main)
	}
	return c, f, p, main
}

// commitInCheckout makes a worker-style checkout with one commit.
func commitInCheckout(t *testing.T, c *Core, p Project, id, base, file, content string) (Run, string) {
	t.Helper()
	dir, err := c.Effects.NewCheckout(p, id, base)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644)
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-q", "-m", "work")
	r, err := c.Runs.Start(RunSpec{ID: id, Role: "worker", Project: p.Name, Checkout: dir, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func TestMergeNeedsCheckAndVerdictOnExactCommit(t *testing.T) {
	c, f, p, main := setupProject(t)
	r, _ := commitInCheckout(t, c, p, "w1", main, "feature.go", "package demo\n// feature\n")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Effects.Push(p, "hive/t1", sha); err != nil {
		t.Fatal(err)
	}
	pr, err := f.CreatePR(p.Repo, "hive/t1", "main", "feature", "body")
	if err != nil {
		t.Fatal(err)
	}
	req := MergeRequest{Project: p, PR: pr.Number, SHA: sha, Main: main, Subject: "feature"}
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "no passing test") {
		t.Fatalf("merged without a test run: %v", err)
	}
	if ch := c.Effects.RunCheck(p, sha, "test", c.Effects.Config(p, main).Test); !ch.OK {
		t.Fatalf("tests should pass: %s", ch.Output)
	}
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("merged a code change without a read: %v", err)
	}
	// A verdict from something other than a reviewer bound to sha is refused.
	if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: sha, Run: "w1", Verdict: "land"}); err == nil {
		t.Fatal("accepted a verdict from a worker")
	}
	rev, _ := c.Runs.Start(RunSpec{ID: "r1", Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": sha}})
	if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: sha, Run: rev.ID, Verdict: "land"}); err != nil {
		t.Fatal(err)
	}

	// A commit pushed after the land verdict can't ride on it.
	r2, dir := commitInCheckout(t, c, p, "w1b", sha, "sneaky.go", "package demo\n// after the review\n")
	_ = dir
	sha2, err := c.Effects.Extract(p, r2, main)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Effects.Push(p, "hive/t1", sha2); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "head is") {
		t.Fatalf("merged the reviewed commit while the PR head moved: %v", err)
	}
	c.Effects.RunCheck(p, sha2, "test", "true")
	req2 := req
	req2.SHA = sha2
	if _, err := c.Effects.Merge(req2); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("merged the new head on the old verdict: %v", err)
	}
	// Back to the reviewed commit: now it merges, pinned to that commit.
	c.Effects.Push(p, "hive/t1", sha)
	merged, err := c.Effects.Merge(req)
	if err != nil {
		t.Fatal(err)
	}
	if merged == "" {
		t.Fatal("no merge commit")
	}
}

func TestFailingTestsNeverMerge(t *testing.T) {
	c, f, p, main := setupProject(t)
	r, _ := commitInCheckout(t, c, p, "w2", main, "BROKEN", "x")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	c.Effects.Push(p, "hive/t2", sha)
	pr, _ := f.CreatePR(p.Repo, "hive/t2", "main", "broken", "")
	if ch := c.Effects.RunCheck(p, sha, "test", c.Effects.Config(p, main).Test); ch.OK {
		t.Fatal("tests should fail with BROKEN present")
	}
	if _, err := c.Effects.Merge(MergeRequest{Project: p, PR: pr.Number, SHA: sha, Main: main}); err == nil {
		t.Fatal("merged a commit whose tests failed")
	}
}

func TestExtractRefusesForeignGitMetadata(t *testing.T) {
	c, _, p, main := setupProject(t)
	r, dir := commitInCheckout(t, c, p, "w3", main, "f.go", "package demo\n")
	// The agent points its checkout's git metadata at an unrelated repository.
	other := t.TempDir()
	mustGit(t, other, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(other, "evil"), []byte("x"), 0o644)
	mustGit(t, other, "add", "-A")
	mustGit(t, other, "commit", "-q", "-m", "unrelated")
	os.RemoveAll(filepath.Join(dir, ".git"))
	os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+filepath.ToSlash(filepath.Join(other, ".git"))+"\n"), 0o644)
	if _, err := c.Effects.Extract(p, r, main); err == nil {
		t.Fatal("extracted commits from an unrelated repository")
	}

	// Harder: a repository that has main's commit but whose HEAD doesn't descend from it.
	other2 := t.TempDir()
	mustGit(t, "", "clone", "-q", c.WS.Project(p.Name), other2)
	mustGit(t, other2, "checkout", "-q", "--orphan", "elsewhere")
	os.WriteFile(filepath.Join(other2, "evil"), []byte("x"), 0o644)
	mustGit(t, other2, "add", "-A")
	mustGit(t, other2, "commit", "-q", "-m", "orphan")
	os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+filepath.ToSlash(filepath.Join(other2, ".git"))+"\n"), 0o644)
	_, err := c.Effects.Extract(p, r, main)
	if err == nil || !strings.Contains(err.Error(), "does not descend") {
		t.Fatalf("want a descend-from-main refusal, got %v", err)
	}
}

func TestPushOnlyHiveBranches(t *testing.T) {
	c, _, p, main := setupProject(t)
	if err := c.Effects.Push(p, "main", main); err == nil {
		t.Fatal("pushed to main")
	}
}

func TestClassify(t *testing.T) {
	c, _, p, main := setupProject(t)
	r, _ := commitInCheckout(t, c, p, "w4", main, "README.md", "# demo\n\nA typo fixed.\n")
	sha, _ := c.Effects.Extract(p, r, main)
	ch, err := c.Effects.Classify(p, main, sha)
	if err != nil || !ch.Small {
		t.Fatalf("a short doc change should be small: %+v %v", ch, err)
	}
	r2, _ := commitInCheckout(t, c, p, "w5", main, ".hive/goals.md", "# Goals\n\nNew goal\n")
	sha2, _ := c.Effects.Extract(p, r2, main)
	ch2, _ := c.Effects.Classify(p, main, sha2)
	if ch2.Small || len(ch2.Protected) == 0 {
		t.Fatalf("a goals change must be protected: %+v", ch2)
	}
	c.Effects.RunCheck(p, sha2, "test", "true")
	var np *NeedsPerson
	if _, err := c.Effects.Merge(MergeRequest{Project: p, SHA: sha2, Main: main}); !errors.As(err, &np) {
		t.Fatalf("goals change merged without the person: %v", err)
	}
}

func TestJournalSkipsTornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.jsonl")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	j.MustAppend("a", 1)
	j.MustAppend("b", 2)
	j.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"seq":3,"kind":"c"`) // a crash mid-write
	f.Close()
	j2, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	if j2.Seq() != 2 {
		t.Fatalf("seq %d, want 2", j2.Seq())
	}
}

func TestScoreCheck(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/s", map[string]string{"score.txt": "5\n", ".hive/project.json": `{"test":"true"}`})
	p, _ := c.Projects.Add(Project{Name: "s", Repo: "me/s"})
	main, _ := c.Effects.Sync(p)
	ch := c.Effects.RunCheck(p, main, "score", "cat score.txt")
	if !ch.OK || ch.Score == nil || *ch.Score != 5 {
		t.Fatalf("score check: %+v", ch)
	}
}

// A rename out of a protected path is still a change to it, and once main has
// had a test command, losing it is red, not "no test command configured".
func TestRenameOutOfProtectedPath(t *testing.T) {
	c, _, p, main := setupProject(t)
	dir, err := c.Effects.NewCheckout(p, "w6", main)
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "docs"), 0o755)
	mustGit(t, dir, "mv", ".hive/project.json", "docs/project.md")
	mustGit(t, dir, "commit", "-q", "-m", "move the settings to the docs")
	r, err := c.Runs.Start(RunSpec{ID: "w6", Role: "worker", Project: p.Name, Checkout: dir, Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c.Effects.Classify(p, main, sha)
	if err != nil || ch.Small || len(ch.Protected) == 0 {
		t.Fatalf("a rename out of .hive/project.json must be protected: %+v %v", ch, err)
	}
	if ok := c.Effects.RunCheck(p, main, "test", c.Effects.Config(p, main).Test).OK; !ok {
		t.Fatal("main's tests should pass")
	}
	if ok := c.Effects.RunCheck(p, sha, "test", c.Effects.Config(p, sha).Test).OK; ok {
		t.Fatal("a vanished test command passed")
	}
}

// MainBundle leaves no temp file behind, whether it succeeds or the rename fails.
func TestMainBundleLeavesNoTemp(t *testing.T) {
	c, _, p, _ := setupProject(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bundle")
	if err := c.Effects.MainBundle(p, path); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "blocked", "x"), 0o755) // a non-empty directory can't be renamed over
	if err := c.Effects.MainBundle(p, filepath.Join(dir, "blocked")); err == nil {
		t.Fatal("rename over a directory should fail")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Fatalf("temp left behind: %v", left)
	}
}
