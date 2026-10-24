package improve

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

const goRed = `ok  	example.com/h/internal/core	1.2s
?   	example.com/h/cmd/h	[no test files]
--- FAIL: TestJoinMidStepHearsTheResult (0.31s)
    risk_test.go:812: the joiner heard nothing
--- FAIL: TestTable (0.00s)
    --- FAIL: TestTable/two (0.00s)
        table_test.go:20: got 1, want 2
FAIL
FAIL	example.com/h/internal/mod/risk	4.1s
ok  	example.com/h/internal/mod/work	2.0s
panic: boom

goroutine 1 [running]:
FAIL	example.com/h/internal/mod/chat	0.2s
FAIL
`

// A red go test run names each failing package, its tests and what they said.
func TestRedNamesTheFailingTests(t *testing.T) {
	fs := failures(goRed)
	if len(fs) != 2 || fs[0].Pkg != "example.com/h/internal/mod/risk" || fs[1].Pkg != "example.com/h/internal/mod/chat" {
		t.Fatalf("failures %+v", fs)
	}
	s := summary(fs, goRed)
	for _, want := range []string{"FAIL example.com/h/internal/mod/risk: TestJoinMidStepHearsTheResult (risk_test.go:812: the joiner heard nothing)",
		"TestTable/two (table_test.go:20: got 1, want 2)", "FAIL example.com/h/internal/mod/chat: panic: boom"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary %q lacks %q", s, want)
		}
	}
	if strings.Contains(s, "internal/core") {
		t.Errorf("summary names a passing package: %q", s)
	}
	// The stored output folds whitespace; it still names the tests.
	folded := strings.Join(strings.Fields(goRed), " ")
	if s := summary(failures(folded), folded); !strings.Contains(s, "FAIL example.com/h/internal/mod/risk: TestJoinMidStepHearsTheResult (risk_test.go:812: the joiner heard nothing)") {
		t.Errorf("folded summary %q", s)
	}
}

// Only what failed runs again, under the project's own go test flags.
func TestRerunRunsOnlyWhatFailed(t *testing.T) {
	fs := failures(goRed)
	got := rerunCommand("go vet ./... && go test -short -timeout 10m -run Foo ./...", fs[:1])
	want := "go test -short -timeout 10m -count=1 -run '^(TestJoinMidStepHearsTheResult|TestTable)$' 'example.com/h/internal/mod/risk'"
	if got != want {
		t.Fatalf("rerun\n %q\nwant\n %q", got, want)
	}
	if got := rerunCommand("go test ./...", fs); got != "go test -count=1 'example.com/h/internal/mod/risk' 'example.com/h/internal/mod/chat'" {
		t.Fatalf("a panic outside a test reruns the whole package: %q", got)
	}
	if got := rerunCommand("make test", fs); got != "make test" {
		t.Fatalf("not go test: %q", got)
	}
	if got := rerunCommand("go test ./...", []failure{{Pkg: "a/b", Build: true}}); got != "go test ./..." {
		t.Fatalf("build failure: %q", got)
	}
}

// A test that fails, then passes when it runs again, is flaky: the merge is
// kept and the test filed to fix. One that fails again reverts the merge
// with the failing test as its lesson.
func TestFlakeIsKeptAndBreakIsReverted(t *testing.T) {
	m, merge := scored(t)
	dir := t.TempDir()
	script := func(name string, once bool) string {
		gate := "exit 1"
		if once {
			ran := "'" + filepath.ToSlash(filepath.Join(dir, "ran")) + "'" // a Windows path, quoted, reads in sh too
			gate = "[ -f " + ran + " ] && exit 0; touch " + ran + "; exit 1"
		}
		return "printf -- '--- FAIL: " + name + " (0.01s)\\n    race_test.go:9: late\\nFAIL\\nFAIL\\texample.com/p/race\\t0.1s\\n'\n" + gate + "\n"
	}
	cfg := `{"test": "sh ./t.sh", "score": {"command": "sh ./score.sh", "direction": "higher"}}`
	ms := merge(map[string]string{".hive/project.json": cfg, "t.sh": script("TestRace", true)})
	if !ms.Kept || !strings.Contains(ms.Note, "flaky") || !strings.Contains(ms.Note, "TestRace (race_test.go:9: late)") {
		t.Fatalf("a flake was not kept as flaky: %+v", ms)
	}
	filed := m.Work.List(func(t work.Task) bool { return strings.Contains(t.Title, "flaky TestRace") })
	if len(filed) != 1 || !strings.Contains(filed[0].Known, "race_test.go:9: late") {
		t.Fatalf("the flake was filed %d times: %+v", len(filed), filed)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); err != nil {
		t.Fatal("the test did not run twice")
	}

	// A test that failed nowhere before (TestRace did, so it'd be a flake).
	ms = merge(map[string]string{"t.sh": script("TestBroke", false)})
	if ms.Kept || !strings.Contains(ms.Note, "FAIL example.com/p/race: TestBroke (race_test.go:9: late)") {
		t.Fatalf("a break was not named or was kept: %+v", ms)
	}
	rev := m.Work.List(func(t work.Task) bool { return t.Kind == "revert" })
	if len(rev) != 1 || !strings.Contains(rev[0].Lesson, "the tests went red after it merged: FAIL example.com/p/race: TestBroke (race_test.go:9: late)") {
		t.Fatalf("revert lesson: %+v", rev)
	}
}

// A failing test name the candidate's code printed never reaches the rerun's
// shell line as code: a printed "--- FAIL: x)$';true;#" would make the rerun
// exit 0 and a real break count as a flake.
func TestRerunNeverRunsAPrintedName(t *testing.T) {
	out := "--- FAIL: x)$';true;#\nFAIL\nFAIL\texample.com/h/p\t0.1s\n"
	fs := failures(out)
	if len(fs) != 1 || len(fs[0].Tests) != 1 {
		t.Fatalf("failures %+v", fs)
	}
	cmd := rerunCommand("go test ./...", fs)
	if strings.Contains(cmd, "true") {
		t.Fatalf("the printed name reached the rerun: %q", cmd)
	}
	// A real break: go fails every time it runs, so the rerun must fail too.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("sh", "-c", cmd)
	sh.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
	if err := sh.Run(); err == nil {
		t.Fatalf("the rerun %q passed on a real break", cmd)
	}
	// A package name is held to the same.
	if cmd := rerunCommand("go test ./...", []failure{{Pkg: "a/b;true", Tests: []string{"TestX"}}}); cmd != "go test ./..." {
		t.Fatalf("an odd package name reached the rerun: %q", cmd)
	}
}

// A printed name never reaches the rerun as a flag: "FAIL -test.run" then
// "FAIL Zz/none" would make go run no tests, exit 0, and a break pass as a
// flake.
func TestRerunNeverTakesAPrintedFlag(t *testing.T) {
	fs := failures("FAIL -test.run\nFAIL Zz/none\n")
	if len(fs) != 2 {
		t.Fatalf("failures %+v", fs)
	}
	cmd := rerunCommand("go test ./...", fs)
	if cmd != "go test ./..." {
		t.Fatalf("a printed flag reached the rerun: %q", cmd)
	}
	// A real break: this go fails every time, except that, like go, it runs
	// nothing and passes when handed -test.run with no tests to match.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nfor a; do case \"$a\" in -test.run*) exit 0;; esac; done\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("sh", "-c", cmd)
	sh.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
	if err := sh.Run(); err == nil {
		t.Fatalf("the rerun %q passed on a real break", cmd)
	}
}
