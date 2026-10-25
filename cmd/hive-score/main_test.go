package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func sign(body, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(body))
	return "BENCH " + body + " " + hex.EncodeToString(mac.Sum(nil))
}

// The BENCH line counts only under the key this command gave the benchmark.
func TestParseBench(t *testing.T) {
	body := `{"asks":4,"asks_done_reported":4,"asks_finished":3,"commits_landed":4,"cost_usd_reported":0,"score":5,"seconds":56.8}`
	out := "=== RUN   TestBenchmark\n" + sign(body, "k") + "\n--- PASS: TestBenchmark (58.52s)\n"
	b, err := ParseBench(out, "k")
	if err != nil || b != (Bench{Score: 5, AsksFinished: 3, Asks: 4, Seconds: 56.8}) {
		t.Fatalf("got %+v, %v", b, err)
	}
	if _, err := ParseBench(out, "other"); err == nil {
		t.Fatal("a line signed with another key was taken")
	}
	if _, err := ParseBench("--- FAIL: TestBenchmark (180s)\n", "k"); err == nil {
		t.Fatal("no BENCH line, yet no error")
	}
}

// Only top-level results count; a subtest's line is indented.
func TestParseResults(t *testing.T) {
	out := "=== RUN   TestA\n--- PASS: TestA (1.0s)\n    --- FAIL: TestA/sub (0.1s)\n--- FAIL: TestB (2s)\n--- SKIP: TestC (0s)\n"
	r := ParseResults(out)
	if len(r) != 3 || r["TestA"] != "pass" || r["TestB"] != "fail" || r["TestC"] != "skip" {
		t.Fatalf("got %v", r)
	}
}

// The output ends with the number alone on its line, which is what improve
// reads; landing the work outweighs doing it faster, and the loop's speed
// counts the same for the same share saved, however fast the Hive already is.
func TestReportFormat(t *testing.T) {
	full := func(seq, par float64) []Run {
		var runs []Run
		for range benchReps {
			runs = append(runs, Run{Bench: Bench{Score: 6, AsksFinished: 4, Asks: 4, Seconds: seq}}, Run{AtOnce: true, Bench: Bench{Score: 6, AsksFinished: 4, Asks: 4, Seconds: par}})
		}
		return runs
	}
	out := Report(full(20, 10), E2E{Run: 40, Passed: 39, Flaky: 1}, []string{"TestStalls"})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	last := lines[len(lines)-1]
	v, err := strconv.ParseFloat(last, 64)
	if err != nil || strings.TrimSpace(last) != last {
		t.Fatalf("last line %q is not a number alone: %v", last, err)
	}
	if want := 100*39.0/40 + 100 + 15; math.Abs(v-want) > 0.005 { // 90s in all is one halving of refSeconds
		t.Fatalf("score %g, want %g", v, want)
	}
	for _, want := range []string{"one by one: 6/6 functions right, 4/4 asks finished, 20.0s", "all at once: 6/6", "39/40 passed (1 only on a second run); failed: TestStalls"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report does not say %q:\n%s", want, out)
		}
	}
	e := E2E{Run: 40, Passed: 40}
	if d := Score(full(8.7, 8.7), e) - Score(full(10, 10), e); d < 3 || d > 3.1 {
		t.Fatalf("13%% faster is %.2f points, want about the tolerance (3)", d)
	}
	if a, b := Score(full(1, 1), e)-Score(full(2, 2), e), Score(full(20, 20), e)-Score(full(40, 40), e); math.Abs(a-b) > 1e-9 {
		t.Fatalf("a halving is worth %g fast and %g slow", a, b)
	}
	missed := full(1, 1)
	missed[1].Score, missed[1].AsksFinished = 5, 3
	if Score(missed, e) >= Score(full(benchTimeout.Seconds(), benchTimeout.Seconds()), e) {
		t.Fatal("a fast run that missed an ask outscored a slow one that landed them all")
	}
	if !strings.Contains(Report(missed, e, nil), "every run is charged its bound (120s)") {
		t.Fatal("a missed ask's charge isn't reported")
	}
	broken := []Run{{Err: errors.New("no BENCH line")}, {AtOnce: true, Err: errors.New("no BENCH line")}, {Err: errors.New("no BENCH line")}, {AtOnce: true, Err: errors.New("no BENCH line")}}
	if out := Report(broken, E2E{}, nil); !strings.Contains(out, "all at once: no BENCH line; counted as nothing landed") || !strings.HasSuffix(out, "\n-21.23\n") {
		t.Fatalf("a benchmark that failed must still give a number:\n%s", out)
	}
}

// The score command in project.json, run where python3 is missing, exits as
// improve reads a machine that can't run it: the shell's own "not found" with
// exit 127, which `go run` alone would turn into exit 1.
func TestMissingToolCantRun(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	raw, err := os.ReadFile("../../.hive/project.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := core.ParseProjectConfig(string(raw))
	if cfg.Score == nil {
		t.Fatal("project.json has no score")
	}
	if got := cfg.Score.Evaluator(); !slices.Contains(got, "cmd/hive-score") {
		t.Fatalf("cmd/hive-score is not an evaluator path: %v", got)
	}
	// A PATH with git but no python3 (nor go, which the lookup reaches first).
	bin := t.TempDir()
	if git, err := exec.LookPath("git"); err == nil {
		os.Symlink(git, filepath.Join(bin, "git"))
	}
	cmd := exec.Command(sh, "-c", cfg.Score.Command)
	cmd.Env = []string{"PATH=" + bin}
	out, _ := cmd.CombinedOutput()
	code := cmd.ProcessState.ExitCode()
	if tool := core.MissingTool(code, string(out)); tool != "python3" {
		t.Fatalf("exit %d, %q: improve would not read python3 as missing", code, out)
	}
}
