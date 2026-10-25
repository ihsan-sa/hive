package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixRepo is a tiny module with a bug in Add, and one commit per kind of
// change on top of it: the commit's sha by name. extra are more files of the
// base commit.
func fixRepo(t *testing.T, extra map[string]string) (repo string, sha map[string]string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		p := filepath.Join(repo, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(name string) {
		git("add", "-A")
		git("commit", "-q", "-m", name)
		sha[name] = git("rev-parse", "HEAD")
	}
	sha = map[string]string{}
	git("init", "-q", "-b", "main")
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n")
	write("calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal(\"0+0\")\n\t}\n}\n")
	for name, body := range extra {
		write(name, body)
	}
	commit("base")

	// A real fix, with the test that shows it.
	write("calc/calc.go", "package calc\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n")
	write("calc/add_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(Add(2, 3))\n\t}\n}\n")
	commit("fix")

	// Nothing fixed: a new function, and a test that cannot build on the control.
	git("checkout", "-q", "--detach", sha["base"])
	write("calc/sub.go", "package calc\n\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n")
	write("calc/sub_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {\n\tif Sub(3, 2) != 1 {\n\t\tt.Fatal(Sub(3, 2))\n\t}\n}\n")
	commit("noop")

	// Nothing fixed: a test that fails on the control by reading the source,
	// running none of the changed code.
	git("checkout", "-q", "--detach", sha["base"])
	write("calc/mul.go", "package calc\n\nfunc Mul(a, b int) int {\n\treturn a * b\n}\n")
	write("calc/src_test.go", "package calc\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestHasMul(t *testing.T) {\n\tif _, err := os.Stat(\"mul.go\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n")
	commit("unrelated")

	// The fix beside a test that cannot build on the control: the package's
	// tests don't build there whole, but the fix's own file does.
	git("checkout", "-q", "--detach", sha["fix"])
	write("calc/sub.go", "package calc\n\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n")
	write("calc/sub_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {\n\tif Sub(3, 2) != 1 {\n\t\tt.Fatal(Sub(3, 2))\n\t}\n}\n")
	commit("fix+new")
	return repo, sha
}

func TestFindFixes(t *testing.T) {
	unixOnly(t)
	gotool, err := FindGo()
	if err != nil {
		t.Skip(err)
	}
	checkFindFixes(t, nil, func(name, root string) *Child {
		return &Child{Name: name, Root: root, Sandbox: "bare", Go: gotool}
	})
}

// checkFindFixes runs the step on each of fixRepo's commits, with sides made
// by side.
func checkFindFixes(t *testing.T, extra map[string]string, side func(name, root string) *Child) {
	t.Helper()
	repo, sha := fixRepo(t, extra)
	run := func(head string) FixReport {
		out := filepath.Join(t.TempDir(), "run")
		cfg := &Config{Repo: repo, Base: sha["base"], Head: sha[head], Out: out}
		cand, ctrl := side("candidate", filepath.Join(out, "candidate")), side("control", filepath.Join(out, "control"))
		for c, s := range map[*Child][2]string{cand: {sha[head], sha["base"]}, ctrl: {sha["base"], ""}} {
			if err := checkout(*cfg, c, s[0], s[1]); err != nil {
				t.Fatal(err)
			}
		}
		return findFixes(cfg, cand, ctrl)
	}
	// A tie on every probe: what the benchmark sees of a bug fix.
	tie := []Result{res("benchmark", true, Metric{Name: "score", Value: 6, Higher: true})}
	decide := func(fr FixReport) Verdict {
		var o Options
		for _, f := range fr.Fixes {
			o.Fixed = append(o.Fixed, f.String())
		}
		return Decide(tie, tie, o)
	}

	fr := run("fix")
	if len(fr.Fixes) != 1 || fr.Fixes[0].Test != "TestAdd" || !strings.HasPrefix(fr.Fixes[0].Covers, "calc/calc.go:") {
		t.Fatalf("the fix and its test were not counted: %+v", fr)
	}
	if v := decide(fr); !v.Keep || !strings.Contains(v.Reason, "TestAdd") {
		t.Fatalf("a real fix with its regression test was not kept: %+v", v)
	}

	fr = run("fix+new")
	if len(fr.Fixes) != 1 || fr.Fixes[0].Test != "TestAdd" || !strings.Contains(strings.Join(fr.Notes, "\n"), "calc TestSub: does not build or run on the control") {
		t.Fatalf("a fix beside a test the control can't build was not counted alone: %+v", fr)
	}
	if fr.Seconds <= 0 || !strings.Contains(fr.markdown(), "the step took") {
		t.Fatalf("the step's time is not in the report: %v\n%s", fr.Seconds, fr.markdown())
	}

	fr = run("noop")
	if len(fr.Fixes) != 0 || !strings.Contains(strings.Join(fr.Notes, "\n"), "does not build or run on the control") {
		t.Fatalf("a test that cannot build on the control counted: %+v", fr)
	}
	if v := decide(fr); v.Keep {
		t.Fatalf("a no-op was kept: %+v", v)
	}

	fr = run("unrelated")
	if len(fr.Fixes) != 0 || !strings.Contains(strings.Join(fr.Notes, "\n"), "runs none of the lines") {
		t.Fatalf("a test that runs none of the change counted: %+v", fr)
	}
}

func TestTestFuncsSeeChangedBodies(t *testing.T) {
	a := testFuncs([]byte("package p\nimport \"testing\"\nfunc TestA(t *testing.T) { t.Log(1) }\nfunc TestMain(m *testing.M) {}\nfunc Testable(t *testing.T) {}\nfunc helper(t *testing.T) {}\n"))
	b := testFuncs([]byte("package p\nimport \"testing\"\n\n// moved and commented\nfunc TestA(t *testing.T) {\n\tt.Log(2)\n}\n"))
	if len(a) != 1 || a["TestA"] == "" || a["TestA"] == b["TestA"] {
		t.Fatalf("test funcs: %v / %v", a, b)
	}
}
