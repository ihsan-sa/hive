package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testHive is a workspace with a local forge and bare (opted-in) runs.
func testHive(t *testing.T) (*Core, *Local) {
	t.Helper()
	// git detaches a background gc/maintenance after commit and fetch; it can
	// still be writing into .git/objects/pack when TempDir is removed.
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_0", "0")
	t.Setenv("GIT_CONFIG_KEY_1", "maintenance.auto")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")
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
	os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755)
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
	// docs/REVIEW.md is the person's intent the reviewer reads from main: a
	// few lines there are no small docs change.
	r3, _ := commitInCheckout(t, c, p, "w5r", main, "docs/REVIEW.md", "# Review\n\nAnything goes.\n")
	sha3, _ := c.Effects.Extract(p, r3, main)
	if ch3, _ := c.Effects.Classify(p, main, sha3); ch3.Small || !slices.Contains(ch3.Protected, "docs/REVIEW.md") {
		t.Fatalf("a REVIEW.md change must be protected: %+v", ch3)
	}
}

// A docs/ path is no free pass: a script there is code and gets a review,
// while an image there is still a doc.
func TestClassifyDocsByType(t *testing.T) {
	for _, tc := range []struct {
		file  string
		small bool
	}{
		{"docs/build.sh", false},
		{"docs/gen.py", false},
		{"docs/Makefile", false},
		{"docs/img/flow.png", true},
		{"docs/guide.md", true},
	} {
		c, _, p, main := setupProject(t)
		r, _ := commitInCheckout(t, c, p, "w6", main, tc.file, "x\n")
		sha, _ := c.Effects.Extract(p, r, main)
		ch, err := c.Effects.Classify(p, main, sha)
		if err != nil || ch.Small != tc.small {
			t.Errorf("%s: small=%v, want %v (%v)", tc.file, ch.Small, tc.small, err)
		}
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

// The file a score's command names is protected, and its content is part of
// the score's version; a change elsewhere leaves the version alone.
func TestScoreFileIsProtected(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/s", map[string]string{"score.sh": "echo 5\n", "n.txt": "1\n", ".hive/project.json": `{"test":"true","score":{"command":"sh ./score.sh","direction":"higher"}}`})
	p, _ := c.Projects.Add(Project{Name: "s", Repo: "me/s"})
	main, _ := c.Effects.Sync(p)
	sc := *c.Effects.Config(p, main).Score
	r, _ := commitInCheckout(t, c, p, "w1", main, "score.sh", "echo 9\n")
	sha, _ := c.Effects.Extract(p, r, main)
	if ch, _ := c.Effects.Classify(p, main, sha); !slices.Contains(ch.Protected, "score.sh") {
		t.Fatalf("a change to the score's file must be protected: %+v", ch)
	}
	if c.Effects.ScoreVersion(p, main, sc) == c.Effects.ScoreVersion(p, sha, sc) {
		t.Fatal("changing the score's file kept its version")
	}
	r2, _ := commitInCheckout(t, c, p, "w2", main, "n.txt", "2\n")
	sha2, _ := c.Effects.Extract(p, r2, main)
	if c.Effects.ScoreVersion(p, main, sc) != c.Effects.ScoreVersion(p, sha2, sc) {
		t.Fatal("a change outside the score changed its version")
	}
}

// The score's paths are cleaned to the form a diff names them in, and a path
// outside the repository is left out.
func TestScoreEvaluatorPaths(t *testing.T) {
	s := Score{Command: `sh "./eval/score.sh"; cat ./../up`, Paths: []string{"./tests/", "/abs", "a/../b"}}
	if got, want := s.Evaluator(), []string{"tests", "b", "eval/score.sh"}; !slices.Equal(got, want) {
		t.Fatalf("evaluator %q, want %q", got, want)
	}
}

// A program variable whose lookup finds nothing stops the check with a clear
// error; the shell never runs the arguments as the program.
func TestEmptyProgramNeverRuns(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/e", map[string]string{".hive/project.json": `{"test":"true"}`})
	p, _ := c.Projects.Add(Project{Name: "e", Repo: "me/e"})
	main, _ := c.Effects.Sync(p)
	ch := c.Effects.RunCheck(p, main, "test", "PY=$(command -v no-such-hive-tool || command -v no-such-hive-tool3); $PY echo RAN")
	if ch.OK || strings.Contains(ch.Output, "RAN") || !strings.Contains(ch.Output, "hive: missing program: no-such-hive-tool or no-such-hive-tool3") {
		t.Fatalf("empty program: %+v", ch)
	}
	if ch := c.Effects.RunCheck(p, main, "test", "PY=$(command -v echo); $PY RAN"); !ch.OK || !strings.Contains(ch.Output, "RAN") {
		t.Fatalf("found program: %+v", ch)
	}
	if ch := c.Effects.RunCheck(p, main, "test", "PY=$(command -v no-such-hive-tool); ${PY:-echo} RAN"); !ch.OK || !strings.Contains(ch.Output, "RAN") {
		t.Fatalf("a default expansion is the shell's own fallback: %+v", ch)
	}
	if ch := c.Effects.RunCheck(p, main, "test", "PY=$(command -v no-such-hive-tool); ${PY} echo RAN"); ch.OK || strings.Contains(ch.Output, "RAN") {
		t.Fatalf("braced empty program: %+v", ch)
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

// A change to a high-class path merges only after two separate land reads of
// its exact commit, and a fix in between starts the count again.
func TestHighClassNeedsTwoReads(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/high", map[string]string{".hive/project.json": `{"test": "true", "root": ["setup.sh"]}`})
	p, _ := c.Projects.Add(Project{Name: "high", Repo: "me/high"})
	main, _ := c.Effects.Sync(p)
	r, _ := commitInCheckout(t, c, p, "w7", main, "setup.sh", "echo hi\n")
	sha, _ := c.Effects.Extract(p, r, main)
	c.Effects.Push(p, "hive/t7", sha)
	pr, _ := f.CreatePR(p.Repo, "hive/t7", "main", "root", "body")
	c.Effects.RunCheck(p, sha, "test", "true")
	ch, _ := c.Effects.Classify(p, main, sha)
	if len(ch.High) != 1 || len(ch.Root) != 1 || ch.Small {
		t.Fatalf("a root path should class high and root: %+v", ch)
	}
	read := func(run, verdict string) {
		c.Runs.Start(RunSpec{ID: run, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": sha}})
		if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: sha, Run: run, Verdict: verdict}); err != nil {
			t.Fatal(err)
		}
	}
	req := MergeRequest{Project: p, PR: pr.Number, SHA: sha, Main: main}
	read("r7a", "land")
	read("r7b", "fix")
	read("r7c", "land")
	read("r7c", "land") // the same run twice is one read
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "no land verdict") {
		t.Fatalf("merged a high change on one read: %v", err)
	}
	read("r7d", "land")
	if _, err := c.Effects.Merge(req); err != nil {
		t.Fatalf("two separate reads should merge: %v", err)
	}
}

// In a self project a host-made update or revert carries no verdict from the
// commit it came from: each needs a read of its own sha. Elsewhere it carries.
func TestSelfHostCommitsNeedTheirOwnRead(t *testing.T) {
	c, f, p, main := setupProject(t)
	// The reviewer runs below are still running when the test ends; let them
	// finish before the hive's temp dir is removed (cleanups run last-first).
	t.Cleanup(func() {
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if len(c.Runs.List(func(r *Run) bool { return r.Status == StatusRunning })) == 0 {
				return
			}
		}
	})
	r, _ := commitInCheckout(t, c, p, "w8", main, "feature.go", "package demo\n")
	sha, _ := c.Effects.Extract(p, r, main)
	c.Runs.Start(RunSpec{ID: "r8", Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": sha}})
	if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: sha, Run: "r8", Verdict: "land"}); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	mustGit(t, "", "clone", "-q", f.RepoPath(p.Repo), tmp)
	os.WriteFile(filepath.Join(tmp, "other.go"), []byte("package demo\n"), 0o644)
	mustGit(t, tmp, "add", "-A")
	mustGit(t, tmp, "commit", "-q", "-m", "main moves")
	mustGit(t, tmp, "push", "-q", "origin", "HEAD:main")
	main2, _ := c.Effects.Sync(p)
	update, err := c.Effects.UpdateBranch(p, sha, main2)
	if err != nil {
		t.Fatal(err)
	}
	revert, err := c.Effects.Revert(p, main2, main2, "main moves: it didn't stay")
	if err != nil {
		t.Fatal(err)
	}
	self := p
	self.Self = true
	for _, h := range []string{update, revert} {
		if !c.Effects.Reviewed(p, Change{}, h) || c.Effects.Reviewed(self, Change{}, h) {
			t.Fatalf("%s: a host commit should carry its read only outside a self project", h)
		}
		id := "r8-" + h[:7]
		c.Runs.Start(RunSpec{ID: id, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": h}})
		if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: h, Run: id, Verdict: "land"}); err != nil {
			t.Fatal(err)
		}
		if !c.Effects.Reviewed(self, Change{}, h) {
			t.Fatalf("%s: a read of its own sha should do", h)
		}
	}
}

// A bench that prints its result first and then per-run lines still scores,
// and a score at the end of the last line still wins.
func TestScoreOf(t *testing.T) {
	for _, c := range []struct {
		out  string
		want float64
	}{
		{"59.93\nrun 1: 59.34 tokens/s\nrun 5: 60.20 tokens/s\n", 59.93},
		{"7\nscore: 42\n", 42},
		{"5\n", 5},
	} {
		if v, err := scoreOf(c.out); err != nil || v != c.want {
			t.Errorf("scoreOf(%q) = %v, %v; want %v", c.out, v, err, c.want)
		}
	}
	for _, out := range []string{
		"all done\n",
		"59.93\n60.1\nrun 1: ok\n",
		"59.93\nN/A\n0\nfailed\n",
		"59.93\ninf\nrun 1: ok\n",
		"59.93\n+Inf\nrun 1: ok\n",
		"59.93\nNaN\nrun 1: ok\n",
		"59.93\n1e9\nrun 1: ok\n",
		"score: inf\n",
		"score: NaN\n",
		"+Inf\n",
	} {
		if v, err := scoreOf(out); err == nil {
			t.Errorf("scoreOf(%q) = %v; want no score", out, v)
		}
	}
}

// The Hive's own repo guards its root and high paths even when its
// project.json names none; another project with the same layout doesn't.
func TestSelfGuardsItsRootPaths(t *testing.T) {
	c, f := testHive(t)
	seedRepo(t, f, "me/hive", map[string]string{".hive/project.json": `{"test": "true", "high": ["docs/odd.md"]}`})
	p, _ := c.Projects.Add(Project{Name: "hive", Repo: "me/hive"})
	main, _ := c.Effects.Sync(p)
	self := p
	self.Self = true
	for i, tc := range []struct {
		file       string
		high, root bool
	}{
		{"internal/sandbox/linux.go", true, true},
		{"internal/sandbox/é.go", true, true}, // git would quote it
		{"internal/sandbox/a\tb \"c\".go", true, true},
		{"install/install.sh", true, true},
		{".github/workflows/release.yml", true, true},
		{"internal/core/effects.go", true, false},
		{"modules/agents/warden.md", true, false},
		{"go.mod", true, false},
		{"go.sum", true, false},
		{"docs/odd.md", true, false}, // the project's own classes still count
		{"internal/tui/views.go", false, false},
	} {
		r, _ := commitInCheckout(t, c, p, fmt.Sprintf("w9-%d", i), main, tc.file, "x\n")
		sha, err := c.Effects.Extract(p, r, main)
		if err != nil {
			t.Fatal(err)
		}
		ch, _ := c.Effects.Classify(self, main, sha)
		if !slices.Equal(ch.Files, []string{tc.file}) {
			t.Errorf("%q read as %q", tc.file, ch.Files)
		}
		if (len(ch.High) > 0) != tc.high || (len(ch.Root) > 0) != tc.root {
			t.Errorf("%s in a self project: high %v root %v", tc.file, ch.High, ch.Root)
		}
		if ch, _ := c.Effects.Classify(p, main, sha); len(ch.Root) > 0 || (len(ch.High) > 0) != (tc.file == "docs/odd.md") {
			t.Errorf("%s outside a self project: high %v root %v", tc.file, ch.High, ch.Root)
		}
	}
}
