package core

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0 // a loaded CI runner mustn't hold fake turns
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
	if _, err := f.EnsureRepo(repo, "", false); err != nil {
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
	// Back to the reviewed commit: it merges once the forge's checks pass there.
	c.Effects.Push(p, "hive/t1", sha)
	f.SetChecks(p.Repo, sha, CheckRun{Name: "test", State: CheckPass}, CheckRun{Name: "sandboxed-e2e", State: CheckPending})
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "sandboxed-e2e") {
		t.Fatalf("merged while a forge check still runs: %v", err)
	}
	f.SetChecks(p.Repo, sha, CheckRun{Name: "test", State: CheckPass}, CheckRun{Name: "sandboxed-e2e", State: CheckPass})
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
	head, err := c.Effects.MainBundle(p, path)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := Git(c.WS.Project(p.Name), "rev-parse", p.Branch); head != strings.TrimSpace(want) {
		t.Fatalf("head %q, want main %q", head, want)
	}
	os.MkdirAll(filepath.Join(dir, "blocked", "x"), 0o755) // a non-empty directory can't be renamed over
	if _, err := c.Effects.MainBundle(p, filepath.Join(dir, "blocked")); err == nil {
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
	// A root path runs with elevated rights: on no security list, it still
	// needs the security seat and a majority of the panel on its own diff.
	if len(ch.SecurityRoot) != 1 || len(ch.Security) != 1 {
		t.Fatalf("a root-class path should need the security read and its panel: %+v", ch)
	}
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "panel") || !strings.Contains(err.Error(), "in the project's root class") {
		t.Fatalf("merged a root path on the ordinary reads alone, or didn't say it's in the root class: %v", err)
	}
	seat := func(run, seat, verdict, diff string) {
		t.Helper()
		c.Runs.Start(RunSpec{ID: run, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": sha, "seat": seat, "diff.patch": diff}})
		if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: sha, Run: run, Verdict: verdict}); err != nil {
			t.Fatal(err)
		}
	}
	// Lands on another diff (the head before a new commit) count for nothing.
	other := ch.Diff + "+echo more\n"
	seat("s7o", SeatSecurity, "land", other)
	for i := 0; i < PanelSeats; i++ {
		seat(fmt.Sprintf("s7o%d", i), PanelSeat(i), "land", other)
	}
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "security read") {
		t.Fatalf("merged on another diff's reads: %v", err)
	}
	seat("s7s", SeatSecurity, "land", ch.Diff)
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "panel") {
		t.Fatalf("merged a root path on the security seat alone: %v", err)
	}
	// A panel majority of fixes declines it, whatever the seat said.
	seat("s7p0", PanelSeat(0), "fix", ch.Diff)
	seat("s7p1", PanelSeat(1), "fix", ch.Diff)
	seat("s7p2", PanelSeat(2), "land", ch.Diff)
	if _, err := c.Effects.Merge(req); err == nil || !strings.Contains(err.Error(), "panel") {
		t.Fatalf("merged on one panel land of three: %v", err)
	}
	seat("s7p1b", PanelSeat(1), "land", ch.Diff) // the seat reads again and lands
	if _, err := c.Effects.Merge(req); err != nil {
		t.Fatalf("the reviewers, the seat and the panel's majority should merge: %v", err)
	}
}

// A read is of a change's own diff: it carries to a head with main merged in
// while the change's own diff is the same, in a self project too, and not to
// one whose merge changed the change's own files. A host-made revert needs no
// read outside a self project and its own read in one.
func TestReadsFollowTheOwnDiff(t *testing.T) {
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
	self := p
	self.Self = true
	read := func(id, sha string, ch Change) {
		t.Helper()
		c.Runs.Start(RunSpec{ID: id, Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": sha, "diff.patch": ch.Diff}})
		if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: sha, Run: id, Verdict: "land"}); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := commitInCheckout(t, c, p, "w8", main, "feature.go", "package demo\n\nfunc A() {}\n")
	sha, _ := c.Effects.Extract(p, r, main)
	ch, _ := c.Effects.Classify(p, main, sha)
	read("r8", sha, ch)
	if v, _ := c.Effects.GetVerdict(p.Name, sha); v.Diff != ch.DiffID || v.Diff == "" {
		t.Fatalf("the verdict isn't bound to the diff its reviewer read: %q, want %q", v.Diff, ch.DiffID)
	}
	tmp := t.TempDir()
	mustGit(t, "", "clone", "-q", f.RepoPath(p.Repo), tmp)
	mustGit(t, tmp, "config", "user.email", "t@t")
	mustGit(t, tmp, "config", "user.name", "t")
	os.WriteFile(filepath.Join(tmp, "other.go"), []byte("package demo\n"), 0o644)
	mustGit(t, tmp, "add", "-A")
	mustGit(t, tmp, "commit", "-q", "-m", "main moves")
	mustGit(t, tmp, "push", "-q", "origin", "HEAD:main")
	main2, _ := c.Effects.Sync(p)
	update, err := c.Effects.UpdateBranch(p, sha, main2, "")
	if err != nil {
		t.Fatal(err)
	}
	ch2, _ := c.Effects.Classify(p, main2, update)
	if ch2.DiffID != ch.DiffID {
		t.Fatal("merging an unrelated main changed the change's own diff")
	}
	for _, pp := range []Project{p, self} {
		if !c.Effects.Reviewed(pp, ch2, update) {
			t.Fatalf("self=%v: a main merge that leaves the own diff alone should keep the read", pp.Self)
		}
	}
	if got := c.Effects.Reads(p.Name, update, ch2.DiffID); len(got) != 1 || got[0] != "r8" {
		t.Fatalf("reads of the caught-up head: %v", got)
	}

	// Main adds its own feature.go; the conflict is resolved by hand into
	// something nobody read.
	os.WriteFile(filepath.Join(tmp, "feature.go"), []byte("package demo\n\nfunc B() {}\n"), 0o644)
	mustGit(t, tmp, "add", "-A")
	mustGit(t, tmp, "commit", "-q", "-m", "main adds feature.go")
	mustGit(t, tmp, "push", "-q", "origin", "HEAD:main")
	main3 := mustGit(t, tmp, "rev-parse", "HEAD")
	mustGit(t, tmp, "fetch", "-q", f.RepoPath(p.Repo), "main")
	if err := c.Effects.Push(p, "hive/t8", update); err != nil {
		t.Fatal(err)
	}
	mustGit(t, tmp, "fetch", "-q", "origin", "hive/t8")
	mustGit(t, tmp, "checkout", "-q", "FETCH_HEAD")
	if _, err := Git(tmp, "merge", "-q", "--no-edit", main3); err == nil {
		t.Fatal("expected a conflict")
	}
	os.WriteFile(filepath.Join(tmp, "feature.go"), []byte("package demo\n\nfunc A() {}\n\nfunc B() {}\n\nfunc Unread() {}\n"), 0o644)
	mustGit(t, tmp, "add", "-A")
	mustGit(t, tmp, "commit", "-q", "--no-edit")
	mustGit(t, tmp, "push", "-q", "origin", "HEAD:refs/heads/hive/t8")
	resolved := mustGit(t, tmp, "rev-parse", "HEAD")
	main3, _ = c.Effects.Sync(p)
	ch3, err := c.Effects.Classify(p, main3, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if ch3.DiffID == ch.DiffID {
		t.Fatal("a conflict resolution that changes the change's files kept its own diff")
	}
	for _, pp := range []Project{p, self} {
		if c.Effects.Reviewed(pp, ch3, resolved) {
			t.Fatalf("self=%v: the read carried to a head whose own diff changed", pp.Self)
		}
	}
	read("r8b", resolved, ch3)
	if !c.Effects.Reviewed(self, ch3, resolved) {
		t.Fatal("a read of the new head should do")
	}

	revert, err := c.Effects.Revert(p, main2, main2, "main moves: it didn't stay")
	if err != nil {
		t.Fatal(err)
	}
	chr, _ := c.Effects.Classify(p, main2, revert)
	if !c.Effects.Reviewed(p, chr, revert) || c.Effects.Reviewed(self, chr, revert) {
		t.Fatal("a host revert needs no read only outside a self project")
	}
	read("r8c", revert, chr)
	if !c.Effects.Reviewed(self, chr, revert) {
		t.Fatal("a read of the revert's own head should do")
	}

	// A fix on the caught-up head is a fix on its diff: the first head, read
	// land with the same diff, no longer counts as read either.
	c.Runs.Start(RunSpec{ID: "r8d", Role: "reviewer", Project: p.Name, ReadOnly: true, Harness: "fake", Inputs: map[string]string{"sha": update, "diff.patch": ch2.Diff}})
	if err := c.Effects.RecordVerdict(Verdict{Project: p.Name, SHA: update, Run: "r8d", Verdict: "fix"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Effects.Reads(p.Name, sha, ch.DiffID); len(got) != 0 {
		t.Fatalf("a land on %s outlived a fix on the same diff at %s: %v", Short(sha), Short(update), got)
	}
	if c.Effects.Reviewed(self, ch, sha) {
		t.Fatal("the first head still reads as reviewed after a fix on its diff")
	}
}

// DiffID ignores where hunks sit and blob ids, and nothing else.
func TestDiffID(t *testing.T) {
	a := "diff --git a/f b/f\nindex 111..222 100644\n--- a/f\n+++ b/f\n@@ -1,2 +1,3 @@ func x\n a\n+b\n c\n"
	moved := "diff --git a/f b/f\nindex 333..444 100644\n--- a/f\n+++ b/f\n@@ -10,2 +10,3 @@ func y\n a\n+b\n c\n"
	changed := "diff --git a/f b/f\nindex 111..222 100644\n--- a/f\n+++ b/f\n@@ -1,2 +1,3 @@ func x\n a\n+B\n c\n"
	if DiffID(a) != DiffID(moved) || DiffID(a) == DiffID(changed) || DiffID("") != "" {
		t.Fatalf("%s %s %s", DiffID(a), DiffID(moved), DiffID(changed))
	}
	// A binary delta is relative to the old blob: the same delta from another
	// blob is another change.
	bin := "diff --git a/b b/b\nindex %s..999 100644\nGIT binary patch\ndelta 5\nzcmV\n\n"
	if DiffID(fmt.Sprintf(bin, "111")) == DiffID(fmt.Sprintf(bin, "222")) {
		t.Fatal("binary deltas from different blobs share an id")
	}
	if DiffID(fmt.Sprintf(bin, "111")+a) != DiffID(fmt.Sprintf(bin, "111")+moved) {
		t.Fatal("a binary file kept a text file's blob ids in the id")
	}
}

// An outside read is by the perms seat or the architect, is bound to a
// diff, and a fix from either outweighs a land from the other.
func TestOutsideRead(t *testing.T) {
	c, _ := testHive(t)
	o := OutsideRead{Project: "p", SHA: "abc", Diff: "d1", By: "perms", Verdict: "land"}
	if err := c.Effects.RecordRead(OutsideRead{Project: "p", SHA: "abc", Diff: "d1", By: "the lead", Verdict: "land"}); err == nil {
		t.Fatal("accepted a read from someone else")
	}
	if err := c.Effects.RecordRead(OutsideRead{Project: "p", SHA: "abc", By: "perms", Verdict: "land"}); err == nil {
		t.Fatal("accepted a read bound to no diff")
	}
	if _, ok := c.Effects.OutsideRead("p", "d1"); ok {
		t.Fatal("a read before any was recorded")
	}
	if err := c.Effects.RecordRead(o); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Effects.OutsideRead("p", "d1"); !ok || got.Verdict != "land" {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := c.Effects.OutsideRead("p", "d2"); ok {
		t.Fatal("a read carried to another diff")
	}
	o.By, o.Verdict = "architect", "fix"
	c.Effects.RecordRead(o)
	if got, _ := c.Effects.OutsideRead("p", "d1"); got.Verdict != "fix" {
		t.Fatalf("a fix should outweigh a land: %+v", got)
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

// Only the shell's own "not found" for a bare tool name, at exit 127, is a
// missing tool; a missing ./script or an ordinary failure is the commit's.
func TestMissingTool(t *testing.T) {
	for _, c := range []struct {
		code int
		out  string
		want string
	}{
		{127, "/bin/sh: 1: go: not found\n", "go"},
		{127, "bash: line 1: cargo: command not found", "cargo"},
		{127, "sh: npm: command not found\n", "npm"},
		{127, "env: 'python3': No such file or directory", "python3"},
		{127, "env: ‘python3’: No such file or directory\n", "python3"},
		{127, "zsh:1: command not found: rustc\n", "rustc"},
		{127, "zsh: command not found: rustc", "rustc"},
		{127, "/bin/sh: 1: ./build.sh: not found", ""},
		{1, "/bin/sh: 1: go: not found", ""},
		{1, "--- FAIL: TestX\nFAIL", ""},
		{127, "some test printed exit 127", ""},
	} {
		if got := MissingTool(c.code, c.out); got != c.want {
			t.Errorf("MissingTool(%d, %q) = %q, want %q", c.code, c.out, got, c.want)
		}
	}
}

// The shells checks really run under word a missing tool as MissingTool
// expects, so a change in their wording shows here rather than as a red.
func TestMissingToolRealShells(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX shell")
	}
	const tool = "hive-no-such-tool-xyz"
	for _, argv := range [][]string{
		{"/bin/sh", "-c", tool + " --version"},
		{"bash", "-c", tool + " --version"},
		{"zsh", "-c", tool + " --version"},
		{"busybox", "sh", "-c", tool + " --version"},
		{"env", tool, "--version"},
	} {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if got := MissingTool(code, string(out)); got != tool {
			t.Errorf("%s words a missing tool as %q, which MissingTool reads as %q", argv[0], out, got)
		}
	}
}

// A check recorded as not starting, or an old red result whose output is the
// shell's "go: not found", is not kept as the commit's answer, so it runs
// again once the tool is there; a real red result is kept.
func TestCantRunIsNotTheCommitsAnswer(t *testing.T) {
	c, _ := testHive(t)
	c.J.MustAppend("effect.checked", Check{Project: "p", SHA: "a", Kind: "test", Output: "/bin/sh: 1: go: not found\n"})
	c.J.MustAppend("effect.checked", Check{Project: "p", SHA: "b", Kind: "test", Output: "x", CantRun: "can't run: go is not installed where checks run"})
	c.J.MustAppend("effect.checked", Check{Project: "p", SHA: "c", Kind: "test", Output: "--- FAIL: TestX\nFAIL\n"})
	// A real red that logs a "not found" line: one recorded since checks carry
	// cant_run, and an old one that named its failing tests.
	c.J.MustAppend("effect.checked", Check{Project: "p", SHA: "d", Kind: "test", Output: "sh: x: not found\n", At: cantRunRecorded.Add(time.Hour)})
	c.J.MustAppend("effect.checked", Check{Project: "p", SHA: "e", Kind: "test", Output: "sh: x: not found\n--- FAIL: TestX\n", Failed: []string{"TestX"}})
	for sha, want := range map[string]bool{"a": false, "b": false, "c": true, "d": true, "e": true} {
		if _, ok := c.Effects.GetCheck("p", sha, "test"); ok != want {
			t.Errorf("check at %s kept %v, want %v", sha, ok, want)
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
		{"go.work", true, false},
		{"go.work.sum", true, false},
		{"vendor/golang.org/x/sys/unix/syscall.go", true, false},
		{"vendor/modules.txt", true, false},
		{"docs/odd.md", true, false}, // the project's own classes still count
		{"internal/tui/views.go", false, false},
	} {
		if runtime.GOOS == "windows" && strings.ContainsAny(tc.file, "\t\"") {
			continue // Windows can't name a file so; git still can, from elsewhere
		}
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

// The repo's own security list parses, and its globs keep `*` in one segment.
func TestSecurityList(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", SecurityList))
	if err != nil {
		t.Fatal(err)
	}
	list := ParseList(string(raw), "paths")
	for _, f := range []string{"internal/core/runs.go", "internal/harness/claude.go", "internal/sandbox/linux/x.go", ".github/workflows/ci.yml", "go.sum", SecurityList} {
		if !matchAny(f, list) {
			t.Errorf("%s should be listed (%v)", f, list)
		}
	}
	for _, f := range []string{"internal/core/journal.go", "internal/mod/work/work.go", "docs/status.md", "internal/harnessx/a.go"} {
		if matchAny(f, list) {
			t.Errorf("%s shouldn't be listed", f)
		}
	}
	if got := ParseList("x = 1\npaths = [\"a/*.go\", \"b/**\"] # c\n", "paths"); len(got) != 2 || !matchAny("a/x.go", got) || matchAny("a/b/x.go", got) || !matchAny("b/c/d", got) {
		t.Fatalf("%v", got)
	}
}

// Two different binary files, or scripts git takes for binary, are two
// different changes: the read of one never carries to the other.
func TestDiffIDTellsBinariesApart(t *testing.T) {
	c, _, p, main := setupProject(t)
	ids := map[string]bool{}
	for i, body := range []string{"\x00\x01\x02 one", "\x00\x01\x02 two", "#!/bin/sh\necho hi\n\x00\nrm -rf /tmp/x\n", "#!/bin/sh\necho hi\n\x00\nrm -rf /tmp/y\n"} {
		r, _ := commitInCheckout(t, c, p, fmt.Sprintf("wb%d", i), main, "blob.bin", body)
		sha, err := c.Effects.Extract(p, r, main)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := c.Effects.Classify(p, main, sha)
		if err != nil {
			t.Fatal(err)
		}
		if ch.DiffID == "" || ids[ch.DiffID] {
			t.Fatalf("change %d has the id of another (%q):\n%s", i, ch.DiffID, ch.Diff)
		}
		ids[ch.DiffID] = true
	}
}

// A required check holds until it has succeeded on the head: missing,
// neutral or skipped is not success.
func TestRequiredChecks(t *testing.T) {
	req := ParseList("paths = []\nrequired_checks = [\n  \"test (ubuntu-latest)\", # Linux\n  \"sandboxed-e2e\",\n]\n", "required_checks")
	if len(req) != 2 {
		t.Fatalf("%v", req)
	}
	pass := CheckRun{Name: "test (ubuntu-latest)", State: CheckPass}
	for _, c := range []struct {
		runs []CheckRun
		want string
	}{
		{nil, CheckPending},
		{[]CheckRun{pass}, CheckPending},
		{[]CheckRun{pass, {Name: "sandboxed-e2e", State: CheckSkipped}}, CheckPending},
		{[]CheckRun{pass, {Name: "sandboxed-e2e", State: CheckPass}, {Name: "lint", State: CheckSkipped}}, CheckPass},
		{[]CheckRun{pass, {Name: "sandboxed-e2e", State: CheckFail}}, CheckFail},
	} {
		if st, which := ChecksState(c.runs, req); st != c.want {
			t.Errorf("%v: %s (%v), want %s", c.runs, st, which, c.want)
		}
	}
	if st, _ := ChecksState(nil, nil); st != CheckPass {
		t.Fatal("with nothing required, no checks should pass")
	}
	raw, _ := os.ReadFile(filepath.Join("..", "..", SecurityList))
	if got := ParseList(string(raw), "required_checks"); !slices.Contains(got, "sandboxed-e2e") || !slices.Contains(got, "test (ubuntu-latest)") {
		t.Fatalf("the repo's list requires %v", got)
	}
}

// Two changes that each add to docs/log.md and docs/status.md merge into main
// one after the other: the repository's own .gitattributes marks them
// merge=union, so bringing the second branch up to date keeps both entries
// instead of sending it back as a conflict. Without those lines the same merge
// conflicts. docs/architecture.md is edited in place and isn't union, which
// the .gitattributes check below holds.
func TestAdditiveDocsMergeWithoutConflict(t *testing.T) {
	attrs, err := os.ReadFile(filepath.Join("..", "..", ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	docs := []string{"docs/log.md", "docs/status.md"}
	if !strings.Contains(string(attrs), "docs/log.md merge=union") || !strings.Contains(string(attrs), "docs/status.md merge=union") || strings.Contains(string(attrs), "docs/architecture.md merge=union") {
		t.Fatalf(".gitattributes should mark log.md and status.md union and leave architecture.md alone:\n%s", attrs)
	}
	for _, union := range []bool{true, false} {
		t.Run(fmt.Sprint("union=", union), func(t *testing.T) {
			c, f := testHive(t)
			files := map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"}
			if union {
				files[".gitattributes"] = string(attrs)
			}
			for _, d := range docs {
				files[d] = "# Doc\n\n## first\n- old\n"
			}
			seedRepo(t, f, "me/docs", files)
			p, err := c.Projects.Add(Project{Name: "docs", Repo: "me/docs"})
			if err != nil {
				t.Fatal(err)
			}
			base, _ := c.Effects.Sync(p)
			branch := func(id string) string {
				dir, err := c.Effects.NewCheckout(p, id, base)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range docs {
					old, _ := os.ReadFile(filepath.Join(dir, d))
					os.WriteFile(filepath.Join(dir, d), append(old, "\n## "+id+"\n- entry from "+id+"\n"...), 0o644)
				}
				mustGit(t, dir, "add", "-A")
				mustGit(t, dir, "commit", "-q", "-m", id)
				r, err := c.Runs.Start(RunSpec{ID: id, Role: "worker", Project: p.Name, Checkout: dir, Harness: "fake"})
				if err != nil {
					t.Fatal(err)
				}
				sha, err := c.Effects.Extract(p, r, base)
				if err != nil {
					t.Fatal(err)
				}
				return sha
			}
			a, b := branch("wa"), branch("wb")
			land := func(name, sha string) {
				if err := c.Effects.Push(p, name, sha); err != nil {
					t.Fatal(err)
				}
				pr, err := f.CreatePR(p.Repo, name, "main", name, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.MergePR(p.Repo, pr.Number, sha, name); err != nil {
					t.Fatal(err)
				}
			}
			land("hive/a", a)
			main, _ := c.Effects.Sync(p)
			for _, from := range []string{"", base} { // a plain merge, and merge-tree from a checkpoint
				_, err := c.Effects.UpdateBranch(p, b, main, from)
				if !union {
					if !errors.Is(err, ErrConflict) {
						t.Fatalf("base %q: without merge=union the docs should conflict: %v", from, err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("base %q: %v", from, err)
				}
			}
			if !union {
				return
			}
			next, _ := c.Effects.UpdateBranch(p, b, main, "")
			land("hive/b", next)
			main, _ = c.Effects.Sync(p)
			for _, d := range docs {
				got := mustGit(t, c.WS.Project(p.Name), "show", main+":"+d)
				if !strings.Contains(got, "entry from wa") || !strings.Contains(got, "entry from wb") || strings.Count(got, "- old") != 1 || strings.Contains(got, "<<<<<<<") {
					t.Fatalf("%s on main should hold both entries once:\n%s", d, got)
				}
			}
		})
	}
}

// Bringing a branch up to date reads attributes from main. A branch cut
// before main marked its docs merge=union still merges them as union; a
// branch that marks its own code merge=union still conflicts on it; and a
// union merge that would leave a heading twice is sent back as a conflict
// naming it.
func TestUpdateBranchTakesAttributesFromMain(t *testing.T) {
	union := "docs/log.md merge=union\n"
	type edit = map[string]string
	for _, tc := range []struct {
		name       string
		seed, a, b edit // a lands first, then b is brought up to date
		want       string
	}{
		{"branch cut before union",
			edit{"docs/log.md": "# Log\n"},
			edit{".gitattributes": union, "docs/log.md": "# Log\n\n## from a, the first entry\n"},
			edit{"docs/log.md": "# Log\n\n## from b, the second entry\n"},
			""},
		{"branch marks its own code union",
			edit{"main.go": "package main\n\nvar x = 1\n"},
			edit{"main.go": "package main\n\nvar x = 2\n"},
			edit{".gitattributes": "*.go merge=union\n", "main.go": "package main\n\nvar x = 3\n"},
			"conflict"},
		{"union doubles a heading",
			edit{".gitattributes": union, "docs/log.md": "# Log\n\n## Old\n- old entry here\n"},
			edit{"docs/log.md": "# Log\n\n## Old\n- old entry, changed by a\n\n## Next\n- from a\n"},
			edit{"docs/log.md": "# Log\n\n## Old\n- old entry, changed by b\n\n## Next\n- from b\n"},
			"docs/log.md: ## Next"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := testHive(t)
			files := edit{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"}
			maps.Copy(files, tc.seed)
			seedRepo(t, f, "me/attrs", files)
			p, err := c.Projects.Add(Project{Name: "attrs", Repo: "me/attrs"})
			if err != nil {
				t.Fatal(err)
			}
			base, _ := c.Effects.Sync(p)
			branch := func(id string, e edit) string {
				dir, err := c.Effects.NewCheckout(p, id, base)
				if err != nil {
					t.Fatal(err)
				}
				for name, body := range e {
					os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
				}
				mustGit(t, dir, "add", "-A")
				mustGit(t, dir, "commit", "-q", "-m", id)
				r, err := c.Runs.Start(RunSpec{ID: id, Role: "worker", Project: p.Name, Checkout: dir, Harness: "fake"})
				if err != nil {
					t.Fatal(err)
				}
				sha, err := c.Effects.Extract(p, r, base)
				if err != nil {
					t.Fatal(err)
				}
				return sha
			}
			a, b := branch("wa", tc.a), branch("wb", tc.b)
			if err := c.Effects.Push(p, "hive/a", a); err != nil {
				t.Fatal(err)
			}
			pr, _ := f.CreatePR(p.Repo, "hive/a", "main", "a", "")
			if _, err := f.MergePR(p.Repo, pr.Number, a, "a"); err != nil {
				t.Fatal(err)
			}
			main, _ := c.Effects.Sync(p)
			for _, from := range []string{"", base} {
				_, err := c.Effects.UpdateBranch(p, b, main, from)
				var d *DoubledError
				switch {
				case tc.want == "" && err != nil:
					t.Fatalf("base %q: %v", from, err)
				case tc.want == "conflict" && !errors.Is(err, ErrConflict):
					t.Fatalf("base %q: want a conflict, got %v", from, err)
				case strings.Contains(tc.want, ": ") && (!errors.As(err, &d) || !errors.Is(err, ErrConflict) || !slices.Contains(d.Lines, tc.want)):
					t.Fatalf("base %q: want %q doubled, got %v", from, tc.want, err)
				}
			}
		})
	}
}

// .gitattributes picks merge drivers, so a change to it, anywhere, is high class.
func TestClassifyGitattributesHigh(t *testing.T) {
	for _, file := range []string{".gitattributes", "docs/.gitattributes"} {
		c, _, p, main := setupProject(t)
		r, _ := commitInCheckout(t, c, p, "w7", main, file, "*.go merge=union\n")
		sha, _ := c.Effects.Extract(p, r, main)
		ch, err := c.Effects.Classify(p, main, sha)
		if err != nil || ch.Small || !slices.Contains(ch.High, file) {
			t.Errorf("%s should be high: %+v %v", file, ch, err)
		}
	}
}

// A failure main shares doesn't count against a head, a required check among
// them included; a new failure or a check still running still does.
func TestChecksAgainstMain(t *testing.T) {
	mainRuns := []CheckRun{{Name: "ubuntu", State: CheckFail}, {Name: "windows", State: CheckFail}, {Name: "macos", State: CheckPass}}
	for _, c := range []struct {
		runs  []CheckRun
		state string
	}{
		{[]CheckRun{{Name: "ubuntu", State: CheckPass}, {Name: "windows", State: CheckFail}, {Name: "macos", State: CheckPass}}, CheckPass},
		{[]CheckRun{{Name: "ubuntu", State: CheckFail}, {Name: "windows", State: CheckFail}, {Name: "macos", State: CheckPass}}, CheckPass},
		{[]CheckRun{{Name: "ubuntu", State: CheckFail}, {Name: "windows", State: CheckPass}, {Name: "macos", State: CheckFail}}, CheckFail},
		{[]CheckRun{{Name: "windows", State: CheckFail}, {Name: "macos", State: CheckPending}}, CheckPending},
	} {
		if st, which, shared, _ := ChecksAgainstMain(c.runs, mainRuns, []string{"windows"}, nil); st != c.state {
			t.Errorf("%v: state %q (%v, shared %v), want %q", c.runs, st, which, shared, c.state)
		}
	}
	if st, _, _, _ := ChecksAgainstMain([]CheckRun{{Name: "macos", State: CheckFail}}, append(mainRuns, CheckRun{Name: "macos", State: CheckFail}), nil, nil); st != CheckFail {
		t.Errorf("a stale failed attempt on main counted as shared: %q", st)
	}
	if st, _, shared, _ := ChecksAgainstMain([]CheckRun{{Name: "windows", State: CheckFail}}, nil, nil, nil); st != CheckFail || len(shared) != 0 {
		t.Errorf("main green: %q, shared %v", st, shared)
	}
}

// With logs that name their failing tests, a shared check counts against a
// head only for a test main's run of it doesn't fail; logs that name none
// leave the check name to decide, and say so.
func TestChecksAgainstMainByTests(t *testing.T) {
	logs := map[string][]string{"h": {"TestA", "TestB"}, "m": {"TestA"}, "h2": {"TestA"}}
	tests := func(r CheckRun) ([]string, bool) { n, ok := logs[r.URL]; return n, ok }
	mainRuns := []CheckRun{{Name: "win", State: CheckFail, URL: "m"}}
	st, which, shared, _ := ChecksAgainstMain([]CheckRun{{Name: "win", State: CheckFail, URL: "h"}}, mainRuns, nil, tests)
	if st != CheckFail || len(shared) != 0 || len(which) != 1 || !strings.Contains(which[0].Detail, "fails TestB,") {
		t.Errorf("a new test in a shared red: %q %v %v", st, which, shared)
	}
	if st, _, shared, byName := ChecksAgainstMain([]CheckRun{{Name: "win", State: CheckFail, URL: "h2"}}, mainRuns, nil, tests); st != CheckPass || len(shared) != 1 || len(byName) != 0 {
		t.Errorf("the same tests: %q %v %v", st, shared, byName)
	}
	if st, _, shared, byName := ChecksAgainstMain([]CheckRun{{Name: "win", State: CheckFail, URL: "none"}}, mainRuns, nil, tests); st != CheckPass || len(shared) != 1 || len(byName) != 1 {
		t.Errorf("no log: %q %v %v", st, shared, byName)
	}
}

// A check is excused only when it shows a failure in quarantined tests
// alone: its log names them and nothing else failing, one step failed, and
// any step it skipped main's run skips too.
func TestExcuse(t *testing.T) {
	flaky := "--- FAIL: TestA (0.1s)\n--- FAIL: TestB (0.2s)\nFAIL\nFAIL\tgithub.com/x/a\t1.0s\nok  \tgithub.com/x/b\t0.3s\n"
	logs := map[string]string{
		"flaky":    flaky,
		"mixed":    "--- FAIL: TestA (0.1s)\n--- FAIL: TestC (0.1s)\nFAIL\nFAIL\tgithub.com/x/a\t1.0s\n",
		"build":    flaky + "# github.com/x/c\nc/c.go:3:1: syntax error\nFAIL\tgithub.com/x/c [build failed]\n",
		"timeout":  flaky + "panic: test timed out after 10m0s\n\trunning tests:\n\t\tTestSlow (10m0s)\nFAIL\tgithub.com/x/d\t600.1s\n",
		"pkgred":   flaky + "FAIL\tgithub.com/x/e\t0.1s\n",
		"twostep":  flaky,
		"skips":    flaky,
		"skips2":   flaky,
		"touched":  flaky,
		"elsepkg":  "--- FAIL: TestA (0.1s)\nFAIL\nFAIL\tgithub.com/x/z\t1.0s\n",
		"unplaced": "--- FAIL: TestA (0.1s)\n--- FAIL: TestB (0.2s)\n",
	}
	read := JobRead{
		Failure: func(r CheckRun) (CheckFailure, bool) {
			log, ok := logs[r.URL]
			keys, placed := FailedTestKeys(log)
			return CheckFailure{Tests: FailedTests(log), Keys: keys, OnlyTests: placed && OnlyTests(log)}, ok
		},
		Steps: func(r CheckRun) ([]JobStep, bool) {
			test := JobStep{Name: "the suite", Conclusion: "failure"}
			switch r.URL {
			case "twostep":
				return []JobStep{test, {Name: "vet", Conclusion: "failure"}}, true
			case "skips", "skips2":
				return []JobStep{test, {Name: "vet darwin", Conclusion: "skipped"}, {Name: "end to end", Conclusion: "skipped"}}, true
			case "main/skips":
				return []JobStep{{Name: "the suite", Conclusion: "success"}, {Name: "vet darwin", Conclusion: "skipped"}, {Name: "end to end", Conclusion: "success"}}, true
			case "main/skips2":
				return []JobStep{{Name: "the suite", Conclusion: "success"}, {Name: "vet darwin", Conclusion: "skipped"}, {Name: "end to end", Conclusion: "skipped"}}, true
			case "none":
				return nil, false
			}
			return []JobStep{{Name: "checkout", Conclusion: "success"}, test}, true
		},
	}
	q := []string{"github.com/x/a::TestA", "github.com/x/a::TestB"}
	for url, want := range map[string]bool{"flaky": true, "mixed": false, "build": false, "timeout": false, "pkgred": false, "twostep": false, "skips": false, "skips2": true, "none": false,
		"touched": false, "elsepkg": false, "unplaced": false} {
		touched := func(key string) bool { return url == "touched" }
		runs := []CheckRun{{Name: url, State: CheckFail, URL: url}, {Name: "linux", State: CheckPass, URL: "flaky"}}
		mainRuns := []CheckRun{{Name: "skips", State: CheckPass, URL: "main/skips"}, {Name: "skips2", State: CheckPass, URL: "main/skips2"}}
		out, excused := Excuse(runs, mainRuns, read, q, touched)
		if got := len(excused) == 1 && excused[0] == url && out[0].State == CheckPass && strings.Contains(out[0].Detail, "quarantined github.com/x/a::TestA, github.com/x/a::TestB"); got != want {
			t.Errorf("%s: excused %v (%+v), want %v", url, excused, out, want)
		}
		if runs[0].State != CheckFail {
			t.Fatal("Excuse changed its input")
		}
	}
	// A skipped step with no finished main run to compare with excuses nothing.
	if _, excused := Excuse([]CheckRun{{Name: "skips2", State: CheckFail, URL: "skips2"}}, []CheckRun{{Name: "skips2", State: CheckPending, URL: "main/skips2"}}, read, q, func(string) bool { return false }); excused != nil {
		t.Errorf("no main answer excused %v", excused)
	}
	runs := []CheckRun{{Name: "win", State: CheckFail, URL: "flaky"}}
	if _, excused := Excuse(runs, nil, read, nil, func(string) bool { return false }); excused != nil {
		t.Errorf("no quarantine excused %v", excused)
	}
	if _, excused := Excuse(runs, nil, JobRead{Failure: read.Failure}, q, func(string) bool { return false }); excused != nil {
		t.Errorf("unread steps excused %v", excused)
	}
}

// A failing test's key carries its package, from the line that closes the
// package's output; a Go failure with no such line can't be placed.
func TestFailedTestKeys(t *testing.T) {
	log := "=== RUN   TestA\n--- FAIL: TestA (0.1s)\n    --- FAIL: TestA/sub (0.0s)\nFAIL\nFAIL\tgithub.com/x/a\t0.1s\nok  \tgithub.com/x/b\t0.2s\n--- FAIL: TestA (0.1s)\nFAIL\nFAIL\tgithub.com/x/c\t0.3s\nFAILED tests/t.py::test_a - assert 1\n"
	keys, ok := FailedTestKeys(log)
	if want := []string{"github.com/x/a::TestA", "github.com/x/a::TestA/sub", "github.com/x/c::TestA", "tests/t.py::test_a"}; !ok || !slices.Equal(keys, want) {
		t.Fatalf("keys %v (%v), want %v", keys, ok, want)
	}
	if _, ok := FailedTestKeys("--- FAIL: TestA (0.1s)\nFAIL\n"); ok {
		t.Fatal("a failure with no package line was placed")
	}
	for key, want := range map[string]string{"github.com/x/internal/e2e::TestA": "internal/e2e", "github.com/x::TestA": ".", "tests/u/t.py::test_a": "tests/u", "other.org/y::TestA": "", "TestA": ""} {
		dir, ok := KeyDir(key, "github.com/x")
		if dir != want || ok != (want != "") {
			t.Errorf("KeyDir(%q) = %q %v, want %q", key, dir, ok, want)
		}
	}
}

func TestOnlyTests(t *testing.T) {
	for log, want := range map[string]bool{
		"--- FAIL: TestA (0.1s)\nFAIL\nFAIL\tx/a\t0.1s\nok  \tx/b\t0.2s\n?   \tx/c\t[no test files]\n": true,
		"FAILED tests/t.py::test_a - assert 1\n1 failed, 3 passed\n":                                   true,
		"--- FAIL: TestA (0.1s)\nFAIL\nFAIL\tx/a\t0.1s\nFAIL\tx/b\t0.1s\n":                             false, // x/b red with no test named
		"--- FAIL: TestA (0.1s)\nFAIL\tx/a\t0.1s\nFAIL\tx/b [setup failed]\n":                          false,
		"--- FAIL: TestA (0.1s)\n    panic: boom [recovered]\n":                                        false,
		"FAILED tests/t.py::test_a\nERROR tests/u.py - ImportError\n1 failed, 1 error\n":               false,
	} {
		if got := OnlyTests(log); got != want {
			t.Errorf("OnlyTests(%q) = %v, want %v", log, got, want)
		}
	}
}

func TestGitHubLogStamp(t *testing.T) {
	log := "2026-10-24T08:00:00.1234567Z --- FAIL: TestX (0.01s)\n2026-10-24T08:00:01.0Z FAIL\n"
	if got := FailedTests(ghLogStamp.ReplaceAllString(log, "")); len(got) != 1 || got[0] != "TestX" {
		t.Errorf("%v", got)
	}
}

// A failed job's excerpt is the end of the step that failed: up to its error
// mark, never before that step's start, and nothing of the steps after it.
func TestLogExcerpt(t *testing.T) {
	log := "##[group]Run setup\nsetup\n##[group]Run go test\na\nb\n--- FAIL: TestX\n##[error]Process completed with exit code 1.\n##[group]Run post\npost\n"
	if got := LogExcerpt(log, 100); got != "##[group]Run go test\na\nb\n--- FAIL: TestX\n##[error]Process completed with exit code 1." {
		t.Fatalf("excerpt %q", got)
	}
	if got := LogExcerpt(log, 2); got != "--- FAIL: TestX\n##[error]Process completed with exit code 1." {
		t.Fatalf("clipped excerpt %q", got)
	}
	if got := LogExcerpt("one\ntwo\nthree\n", 2); got != "two\nthree" {
		t.Fatalf("no error mark: %q", got)
	}
	// A downloaded log: CRLF, a byte-order mark, a timestamp on each line,
	// and a post step that errors after the step that failed.
	ts := "2026-10-24T12:00:00.1234567Z "
	real := "\ufeff" + ts + "##[group]Run actions/checkout@v4\r\n" + ts + "checked out\r\n" + ts + "##[group]Run go test ./...\r\n" + ts + "--- FAIL: TestX\r\n" + ts + "##[error]path_test.go:12: bad\r\n" + ts + "FAIL\r\n" + ts + "##[error]Process completed with exit code 1.\r\n" + ts + "##[group]Run actions/checkout@v4 post\r\n" + ts + "##[error]cleanup failed\r\n"
	want := ts + "##[group]Run go test ./...\n" + ts + "--- FAIL: TestX\n" + ts + "##[error]path_test.go:12: bad\n" + ts + "FAIL\n" + ts + "##[error]Process completed with exit code 1."
	if got := LogExcerpt(real, 100); got != want {
		t.Fatalf("real log excerpt %q", got)
	}
	if got := LogExcerpt("##[group]Run setup\nsetup\n##[group]Run go test\nx\n##[error]boom", 100); got != "##[group]Run go test\nx\n##[error]boom" {
		t.Fatalf("error on the last line: %q", got)
	}
}

// A plain text file or anything under testdata is data a test may read, not
// prose: its red counts as any code change's.
func TestProseOnly(t *testing.T) {
	for files, want := range map[string]bool{
		".hive/goals.md":                 true,
		"docs/a.md,.modules/agents/x.md": true,
		"feature.txt":                    false,
		"internal/x/testdata/golden.md":  false,
		"docs/a.md,main.go":              false,
	} {
		if got := ProseOnly(strings.Split(files, ",")); got != want {
			t.Errorf("ProseOnly(%s) = %v, want %v", files, got, want)
		}
	}
}

// ChecksBesidesFailures leaves a failed check out of the required list too,
// while a required check not yet reported still holds.
func TestChecksBesidesFailures(t *testing.T) {
	runs := []CheckRun{{Name: "win", State: CheckFail}, {Name: "linux", State: CheckPass}}
	if st, _, failed := ChecksBesidesFailures(runs, []string{"win", "linux"}); st != CheckPass || len(failed) != 1 || failed[0] != "win" {
		t.Fatalf("state %q, failed %v", st, failed)
	}
	if st, which, _ := ChecksBesidesFailures(runs, []string{"win", "e2e"}); st != CheckPending || len(which) != 1 || which[0].Name != "e2e" {
		t.Fatalf("state %q, which %v", st, which)
	}
}

// Only the Hive's own project sets a listed check aside, by its exact name,
// and it is then neither counted nor waited on as required, red or pending.
func TestAdvisory(t *testing.T) {
	runs := []CheckRun{{Name: "test (windows-latest)", State: CheckFail}, {Name: "test (ubuntu-latest)", State: CheckPass}, {Name: "lint-windows", State: CheckPass}}
	req := []string{"test (windows-latest)", "test (ubuntu-latest)"}
	adv := []string{"test (windows-latest)"}
	if kept, r, w := Advisory(Project{}, runs, req, adv); len(kept) != 3 || len(r) != 2 || w != nil {
		t.Fatalf("not self: %v %v %v", kept, r, w)
	}
	if kept, r, w := Advisory(Project{Self: true}, runs, req, nil); len(kept) != 3 || len(r) != 2 || w != nil {
		t.Fatalf("no list: %v %v %v", kept, r, w)
	}
	kept, r, w := Advisory(Project{Self: true}, runs, req, adv)
	if len(kept) != 2 || !slices.Equal(r, []string{"test (ubuntu-latest)"}) || !slices.Equal(w, []string{"test (windows-latest)"}) {
		t.Fatalf("self: %v %v %v", kept, r, w)
	}
	if st, _ := ChecksState(kept, r); st != CheckPass {
		t.Fatalf("state %s", st)
	}
	runs[2].State = CheckFail // named like Windows, but not on the list
	if _, _, w := Advisory(Project{Self: true}, runs, req, adv); !slices.Equal(w, []string{"test (windows-latest)"}) {
		t.Fatalf("an unlisted check was set aside: %v", w)
	}
	runs[0].State, runs[2].State = CheckPending, CheckPass
	kept, r, w = Advisory(Project{Self: true}, runs, req, adv)
	if st, _ := ChecksState(kept, r); st != CheckPass || w != nil {
		t.Fatalf("a pending advisory check held the merge: %s %v", st, w)
	}
}

func TestRedKeys(t *testing.T) {
	out := "--- FAIL: TestClock (0.00s)\n    a_test.go:3: late\nFAIL\nFAIL\tx/a\t0.004s\nok  \tx/ok\t0.1s\n" +
		"PASS\nFAIL\tx/exit\t0.002s\n" +
		"--- FAIL: TestClock (0.00s)\npanic: test timed out after 2s\n\trunning tests:\nFAIL\tx/c\t2.011s\n" +
		"# x/d\nd.go:1: syntax error\nFAIL\tx/d [build failed]\nFAIL\n"
	got := strings.Join(RedKeys(out), "|")
	if want := "x/a TestClock|x/exit !|x/c TestClock|x/c !|x/d !"; got != want {
		t.Fatalf("RedKeys = %q, want %q", got, want)
	}
	if got := RedKeys("FAILED tests/a.py::test_x\n"); len(got) != 1 || got[0] != "tests/a.py::test_x" {
		t.Fatalf("pytest: %q", got)
	}
	vet := "# x/e\n# [x/e]\nvet: e/a_test.go:225:42: not enough arguments in call to f\n\thave (string)\n\twant (string, bool)\n"
	if got := RedKeys(vet); got != nil {
		t.Fatalf("vet's errors name no test: %q", got)
	}
	if got := StopKeys(vet); len(got) != 1 || got[0] != "e/a_test.go: not enough arguments in call to f" {
		t.Fatalf("vet: %q", got)
	}
	if moved := StopKeys(strings.Replace(vet, ":225:42:", ":230:9:", 1)); !slices.Equal(moved, StopKeys(vet)) {
		t.Fatalf("the same vet error a few lines down: %q", moved)
	}
	for _, c := range []struct{ name, out, moved, want string }{
		{"javac", "src/A.java:12: error: cannot find symbol\n  symbol: f\n1 error\n", ":12:", "src/A.java: error: cannot find symbol"},
		{"tsc", "src/a.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.\n", "(12,5)", "src/a.ts: error TS2322: Type 'string' is not assignable to type 'number'."},
		{"tsc pretty", "src/a.tsx:12:5 - error TS2304: Cannot find name 'x'.\n\n12 x\n", ":12:5", "src/a.tsx: error TS2304: Cannot find name 'x'."},
		{"rustc", "error[E0308]: mismatched types\n  --> src/main.rs:12:18\n   |\nerror: aborting due to 1 previous error\nerror: could not compile `x`\n", ":12:", "src/main.rs: error[E0308]: mismatched types"},
		{"python", "Traceback (most recent call last):\n  File \"/x/run.py\", line 1, in <module>\n  File \"/x/a.py\", line 12\n    def f(\n         ^\nSyntaxError: '(' was never closed\n", "line 12", "/x/a.py: SyntaxError: '(' was never closed"},
	} {
		got := StopKeys(c.out)
		if len(got) != 1 || got[0] != c.want {
			t.Fatalf("%s: %q", c.name, got)
		}
		if moved := StopKeys(strings.Replace(c.out, c.moved, strings.ReplaceAll(c.moved, "12", "30"), 1)); !slices.Equal(moved, got) {
			t.Fatalf("%s a few lines down: %q", c.name, moved)
		}
	}
	if got := RedKeys("vet: something\n"); got != nil {
		t.Fatalf("nothing named: %q", got)
	}
	if got := RedKeys("--- FAIL: TestX (0.00s)\n    # a log line\nFAIL\nFAIL\tx/a\t0.01s\n"); !slices.Equal(got, []string{"x/a TestX"}) {
		t.Fatalf("a test's log line starting '# ' isn't a build header: %q", got)
	}
}

// go1.27.3's own output, run in each package's directory ("go test", no
// package list), which prints "exit status 1" after every failing package.
func TestRedKeysExitStatus(t *testing.T) {
	// a: TestX fails, so testing prints FAIL and exits 1.
	ordinary := "--- FAIL: TestX (0.00s)\n    a_test.go:3: bad\nFAIL\nexit status 1\nFAIL\tx/m/a\t0.001s\n"
	if got := RedKeys(ordinary); !slices.Equal(got, []string{"x/m/a TestX"}) {
		t.Fatalf("an ordinary failure's exit status counted as unnamed: %q", got)
	}
	// b: TestY fails, then TestZ calls os.Exit(1) before testing prints FAIL.
	exited := "--- FAIL: TestY (0.00s)\n    b_test.go:3: bad\nexit status 1\nFAIL\tx/m/b\t0.001s\n"
	if got := RedKeys(exited); !slices.Equal(got, []string{"x/m/b TestY", "x/m/b !"}) {
		t.Fatalf("an exit beside a named failure went unnamed: %q", got)
	}
}

// SecurityWhere names why a change needs a security read: the list, the
// project's root class, or, for a mixed change, both with the unlisted files.
func TestSecurityWhere(t *testing.T) {
	for _, c := range []struct {
		ch   Change
		want string
	}{
		{Change{Security: []string{"tests/a.go"}}, "on " + SecurityList},
		{Change{Security: []string{".github/x.yml"}, Unlisted: []string{".github/x.yml"}}, "in the project's root class (which runs with elevated rights)"},
		{Change{Security: []string{"tests/a.go", ".github/x.yml"}, Unlisted: []string{".github/x.yml"}}, "on " + SecurityList + " or in the project's root class (.github/x.yml)"},
	} {
		if got := c.ch.SecurityWhere(); got != c.want {
			t.Errorf("%v: %q, want %q", c.ch.Security, got, c.want)
		}
	}
}
