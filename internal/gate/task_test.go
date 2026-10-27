package gate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// taskFixture is an upstream repository with a base commit and a later fix,
// and a task folder on it whose hidden test passes only with the fix.
func taskFixture(t *testing.T) (taskDir, base, fix string) {
	t.Helper()
	dir := t.TempDir()
	up := filepath.Join(dir, "upstream")
	git := func(d string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", d, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	os.MkdirAll(up, 0o755)
	git(up, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(up, "go.mod"), []byte("module example.com/half\n\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(up, "half.go"), []byte("package half\n\nfunc Half(n int) int { return n }\n"), 0o644)
	git(up, "add", "-A")
	git(up, "commit", "-q", "-m", "base")
	base = git(up, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(up, "half.go"), []byte("package half\n\nfunc Half(n int) int { return n / 2 }\n"), 0o644)
	git(up, "commit", "-q", "-am", "the upstream fix")
	fix = git(up, "rev-parse", "HEAD")
	taskDir = filepath.Join(dir, "tasks", "half")
	os.MkdirAll(filepath.Join(taskDir, "hidden"), 0o755)
	os.WriteFile(filepath.Join(taskDir, "hidden", "half_test.go"), []byte("package half\n\nimport \"testing\"\n\nfunc TestHalf(t *testing.T) {\n\tif Half(4) != 2 {\n\t\tt.Fatal(Half(4))\n\t}\n}\n"), 0o644)
	raw, _ := json.Marshal(map[string]any{"id": "half", "repo": up, "base": base, "fix": fix, "title": "Half doesn't halve",
		"issue": "Half(4) returns 4.", "test": "go test ./...", "hidden_test": "go test -count=1 ./...", "timeout": 120})
	os.WriteFile(filepath.Join(taskDir, "task.json"), raw, 0o644)
	return taskDir, base, fix
}

// A task folder is read as hive-eval writes it, and refused when the gate
// couldn't run or grade it; the side is told what every hive-eval arm is told.
func TestLoadTask(t *testing.T) {
	dir, _, _ := taskFixture(t)
	task, err := LoadTask(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Half doesn't halve\n\nHalf(4) returns 4.\n\nThe project's tests run with `go test ./...`. Make the change in this repository, with tests, and commit it."; task.Prompt() != want {
		t.Errorf("prompt:\n%s\nwant:\n%s", task.Prompt(), want)
	}
	if task.Timeout != 120 {
		t.Errorf("timeout %d", task.Timeout)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "task.json"))
	for name, change := range map[string]func(m map[string]any){
		"lacks hidden_test": func(m map[string]any) { delete(m, "hidden_test") },
		"a backlog":         func(m map[string]any) { m["kind"] = "backlog" },
	} {
		var m map[string]any
		json.Unmarshal(raw, &m)
		change(m)
		bad := t.TempDir()
		b, _ := json.Marshal(m)
		os.WriteFile(filepath.Join(bad, "task.json"), b, 0o644)
		os.MkdirAll(filepath.Join(bad, "hidden"), 0o755)
		if _, err := LoadTask(bad); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	os.RemoveAll(filepath.Join(dir, "hidden"))
	if _, err := LoadTask(dir); err == nil {
		t.Error("a task with no hidden tests loaded")
	}
}

// The seed both sides start from is the tree at base and the Hive files, in
// a repository of its own: no remote, and no later commit holding the fix.
func TestTaskSeedHoldsNoUpstreamHistory(t *testing.T) {
	dir, _, fix := taskFixture(t)
	cfg := &Config{Task: dir, Out: t.TempDir()}
	r := prepareTask(cfg)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if prepareTask(cfg) != r {
		t.Error("the task was prepared twice in one run")
	}
	log, _ := gitRun(r.seed, "git", "log", "--format=%s")
	if strings.TrimSpace(log) != "Set up the Hive files\nInitial import" {
		t.Errorf("seed history: %q", log)
	}
	if out, _ := gitRun(r.seed, "git", "remote"); strings.TrimSpace(out) != "" {
		t.Errorf("seed has a remote: %s", out)
	}
	if _, err := gitRun(r.seed, "git", "cat-file", "-e", fix); err == nil {
		t.Error("the upstream fix is in the seed")
	}
	if src, _ := os.ReadFile(filepath.Join(r.seed, "half.go")); !strings.Contains(string(src), "return n }") {
		t.Errorf("seed isn't the tree at base: %s", src)
	}
	if p, _ := os.ReadFile(filepath.Join(r.seed, ".hive", "project.json")); !strings.Contains(string(p), `"test": "go test ./..."`) {
		t.Errorf("project.json: %s", p)
	}
	if _, err := os.Stat(filepath.Join(cfg.Out, "task", "upstream")); err == nil {
		t.Error("the upstream clone, with the fix in it, outlived the seed")
	}
	if _, err := os.Stat(filepath.Join(r.seed, "half_test.go")); err == nil {
		t.Error("a hidden test is in the seed")
	}
}

// Grading lays the hidden tests over what landed on the side's main and runs
// them sealed: the base fails them, the fix passes.
func TestGradeTask(t *testing.T) {
	g, err := FindGo()
	if err != nil {
		t.Skip(err)
	}
	dir, _, _ := taskFixture(t)
	cfg := &Config{Task: dir, Out: t.TempDir()}
	r := prepareTask(cfg)
	if r.err != nil {
		t.Fatal(r.err)
	}
	side := &Child{Name: "candidate", Root: filepath.Join(cfg.Out, "candidate"), Sandbox: "bare", Go: g}
	bare := filepath.Join(cfg.Out, "forge.git")
	for _, a := range [][]string{{"init", "-q", "--bare", "-b", "main", bare}, {"-C", r.seed, "push", "-q", bare, "HEAD:main"}} {
		if out, err := gitRun(cfg.Out, "git", a...); err != nil {
			t.Fatal(out)
		}
	}
	seed, _ := gitRun(r.seed, "git", "rev-parse", "HEAD")
	main, ok, said := gradeTask(cfg, side, r.task, bare)
	if ok || main != strings.TrimSpace(seed) || !strings.Contains(said, "FAIL") {
		t.Fatalf("the base graded %v at %s: %s", ok, main, said)
	}
	os.WriteFile(filepath.Join(r.seed, "half.go"), []byte("package half\n\nfunc Half(n int) int { return n / 2 }\n"), 0o644)
	for _, a := range [][]string{{"-C", r.seed, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", "fix"}, {"-C", r.seed, "push", "-q", bare, "HEAD:main"}} {
		if out, err := gitRun(cfg.Out, "git", a...); err != nil {
			t.Fatal(out)
		}
	}
	if _, ok, said := gradeTask(cfg, side, r.task, bare); !ok {
		t.Fatalf("the fix failed the hidden tests: %s", said)
	}
}

// With no task given, or no Claude login that outlasts one, the probe runs
// nothing and judges nothing: both sides pass it with no metric.
func TestTaskProbeSkips(t *testing.T) {
	side := &Child{Name: "candidate", Root: t.TempDir(), Eval: &Evaluator{}}
	if r := probeTask(&Config{}, side); !r.Pass || len(r.Metrics) > 0 {
		t.Errorf("no task: %+v", r)
	}
	dir, _, _ := taskFixture(t)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := claudeAccess
	t.Cleanup(func() { claudeAccess = old })
	claudeAccess = func() (string, time.Time, error) { return "tok", time.Now().Add(15 * time.Minute), nil }
	r := probeTask(&Config{Task: dir, Out: t.TempDir()}, side)
	if !r.Pass || len(r.Metrics) > 0 || !strings.Contains(r.Detail, "login") {
		t.Errorf("a login that expires before the task could end: %+v", r)
	}
}

// The whole path on the fake harness: the side's Hive is started inside its
// sandbox by the gate's driver, given the task in the lead's chat, and what
// lands on its forge is graded. The fake's work doesn't fix the task, so the
// hidden tests fail; what this shows is that each step runs and is measured.
func TestTaskProbeRunsTheSidesHive(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Hive")
	}
	g, err := FindGo()
	if err != nil {
		t.Skip(err)
	}
	hive := builtHive(t)
	out := t.TempDir()
	runTaskProbe(t, out, &Child{Name: "candidate", Root: filepath.Join(out, "candidate"), Sandbox: "bare", Go: g, Bin: hive, Fwd: hive, Eval: &Evaluator{}})
}

// The same under bubblewrap, where the side's daemon listens on its
// sandbox's own loopback and its only way out is its meter.
func TestBwrapTaskProbe(t *testing.T) {
	needBwrap(t)
	c, _ := meteredChild(t, "candidate")
	c.Bin = c.Fwd
	c.Eval = &Evaluator{}
	runTaskProbe(t, filepath.Dir(c.Root), c)
}

func runTaskProbe(t *testing.T, out string, side *Child) {
	t.Helper()
	dir, _, _ := taskFixture(t)
	old := taskHarness
	taskHarness = "fake"
	t.Cleanup(func() { taskHarness = old })
	cfg := &Config{Task: dir, Out: out, TaskWall: 3 * time.Minute}
	side.Prepare()
	r := probeTask(cfg, side)
	t.Logf("%+v", r)
	if !r.Pass || len(r.Metrics) == 0 || r.Metrics[0].Name != "hidden_tests_passed" || !strings.Contains(r.Detail, "landed") {
		raw, _ := os.ReadFile(filepath.Join(side.Root, "task", "daemon.log"))
		t.Fatalf("the task probe didn't measure the side: %+v\n%s", r, tail(string(raw), 2000))
	}
	if _, err := os.Stat(filepath.Join(side.Root, "task", "login")); err == nil {
		t.Error("the login folder outlived the run")
	}
}
