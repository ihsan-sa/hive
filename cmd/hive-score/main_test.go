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
	out := Report(full(20, 10), E2E{Run: 40, Passed: 39, Flaky: 1}, []string{"TestStalls"}, nil)
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
	slowed := full(10, 5)
	slowed[0].Seconds, slowed[3].Seconds = 30, 15
	if Score(slowed, e) != Score(full(10, 5), e) {
		t.Fatal("a run the load slowed moved the score: each way counts at its fastest")
	}
	missed := full(1, 1)
	missed[1].Score, missed[1].AsksFinished = 5, 3
	if Score(missed, e) >= Score(full(benchTimeout.Seconds(), benchTimeout.Seconds()), e) {
		t.Fatal("a fast run that missed an ask outscored a slow one that landed them all")
	}
	if !strings.Contains(Report(missed, e, nil, nil), "every run is charged its bound (120s)") {
		t.Fatal("a missed ask's charge isn't reported")
	}
	broken := []Run{{Err: errors.New("no BENCH line")}, {AtOnce: true, Err: errors.New("no BENCH line")}, {Err: errors.New("no BENCH line")}, {AtOnce: true, Err: errors.New("no BENCH line")}}
	if out := Report(broken, E2E{}, nil, nil); !strings.Contains(out, "all at once: no BENCH line; counted as nothing landed") || !strings.HasSuffix(out, "\n-21.23\n") {
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

// A test the parent declared and the candidate dropped counts as failed, so
// deleting a failing test can't raise the pass rate; a skipped one is left out.
func TestTally(t *testing.T) {
	before := Declared("func TestMain(m *testing.M) {\nfunc TestA(t *testing.T) {\nfunc TestBenchmark(t *testing.T) {\nfunc TestGone(t *testing.T) {\nfunc TestC(t *testing.T) {\n")
	if !slices.Equal(before, []string{"TestA", "TestGone", "TestC"}) {
		t.Fatalf("declared %v", before)
	}
	e, failed := Tally([]string{"TestA", "TestC", "TestNew", "TestQuiet"}, before, map[string]string{"TestA": "pass", "TestC": "skip", "TestNew": "pass"})
	if e != (E2E{Run: 4, Passed: 2}) || !slices.Equal(failed, []string{"TestGone (deleted)", "TestQuiet"}) {
		t.Fatalf("got %+v, %v", e, failed)
	}
	if e, _ := Tally([]string{"TestA"}, nil, map[string]string{"TestA": "pass"}); e != (E2E{Run: 1, Passed: 1}) {
		t.Fatalf("with no parent: %+v", e)
	}
}

// A run the machine's CPU was fully taken for at times doesn't count, and
// another of its way of asking runs instead, until each way has its three
// or the budget is out; a run that missed an ask counts however loud.
func TestLoudRunsRunAgain(t *testing.T) {
	ok := func(atOnce, loud bool, secs float64) Run {
		return Run{AtOnce: atOnce, Bench: Bench{Score: 6, AsksFinished: 4, Asks: 4, Seconds: secs}, Loud: loud}
	}
	if atOnce, more := Next(nil, true); atOnce || !more {
		t.Fatal("the first run is not one by one")
	}
	all := []Run{ok(false, true, 9)}
	if atOnce, _ := Next(all, true); !atOnce {
		t.Fatal("after a loud run the other way of asking doesn't get its turn")
	}
	all = append(all, ok(true, false, 1), ok(false, false, 4), ok(true, true, 5), ok(false, false, 4), ok(true, false, 1), ok(false, false, 4))
	if atOnce, more := Next(all, true); !atOnce || !more {
		t.Fatal("all at once has two runs that count of three, yet no run for it")
	}
	if _, more := Next(all, false); more {
		t.Fatal("past the budget with the six run, runs went on")
	}
	runs, loud := Counted(all)
	var secs []float64
	for _, r := range runs {
		secs = append(secs, r.Seconds)
	}
	// One by one has its three quiet runs; all at once is made up with its
	// fastest loud one.
	if !slices.Equal(secs, []float64{1, 4, 5, 4, 1, 4}) || len(loud) != 1 || loud[0].Seconds != 9 {
		t.Fatalf("counted %v, left out %v", secs, loud)
	}
	out := Report(runs, E2E{Run: 1, Passed: 1}, nil, loud)
	for _, want := range []string{"5.0s (0.0 CPU free at least: loud, counted for want of a quiet run)", "benchmark: 1 runs not counted, under 1 CPU free at times while they ran (least free): 9.0s (0.0)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report does not say %q:\n%s", want, out)
		}
	}
	// A missed ask on a quiet run counts; on a loud one it runs again, and
	// fills a way short of quiet runs only after its loud runs that landed.
	hung := Run{Bench: Bench{Score: 4, AsksFinished: 2, Asks: 4, Seconds: 120}}
	if runs, _ := Counted([]Run{hung, ok(false, false, 4), ok(false, false, 4), ok(false, false, 4)}); runs[0].Seconds != 120 {
		t.Fatal("a quiet run that missed an ask was dropped")
	}
	hung.Loud = true
	if _, more := Next([]Run{hung}, true); !more {
		t.Fatal("a loud run that missed an ask is not run again")
	}
	none := Run{Err: errors.New("no BENCH line"), Loud: true}
	if runs, _ := Counted([]Run{none, ok(false, false, 4), ok(false, false, 4), ok(false, true, 9)}); slices.ContainsFunc(runs, Run.missed) {
		t.Fatal("a loud miss filled a way that had a loud run that landed")
	}
	// Past the budget a way that stayed loud doesn't take the other's
	// runs: one quiet run one by one and loud ones at once goes on one by
	// one until it has its three, and the score counts three of each way.
	lone := []Run{ok(false, false, 4), ok(true, true, 5), ok(true, true, 6), ok(true, true, 5), ok(true, true, 7), ok(true, true, 5)}
	if atOnce, more := Next(lone, false); atOnce || !more {
		t.Fatalf("past the budget with one run one by one, next is at once %v, more %v", atOnce, more)
	}
	if _, more := Next(append(lone, ok(false, true, 9), ok(false, true, 8)), false); more {
		t.Fatal("past the budget with three runs of each way, runs went on")
	}
	e1 := E2E{Run: 1, Passed: 1}
	if few, _ := Counted(lone); math.Abs(Score(few, e1)-Score([]Run{ok(false, false, 4), ok(false, false, 4), ok(false, false, 4), ok(true, true, 5), ok(true, true, 5), ok(true, true, 5)}, e1)) > 1e-9 {
		t.Fatal("a way with one run counted is scored on fewer than three")
	}
	if atOnce, more := Next([]Run{ok(false, false, 4), ok(true, false, 1), ok(false, false, 4), ok(true, false, 1), ok(false, false, 4), ok(true, false, 1)}, true); more {
		t.Fatalf("six quiet runs, yet another (at once: %v)", atOnce)
	}
}

// /proc/stat's machine-wide line: idle and iowait leave the CPU free.
func TestParseCPUStat(t *testing.T) {
	raw := "cpu  100 5 50 800 40 1 2 2 0 0\ncpu0 50 2 25 400 20 0 1 1 0 0\ncpu1 50 3 25 400 20 1 1 1 0 0\nintr 1 2 3\nctxt 9\n"
	c, ok := ParseCPUStat(raw)
	if !ok || c != (cpuTimes{idle: 840, total: 1000, cpus: 2}) {
		t.Fatalf("got %+v, %v", c, ok)
	}
	if _, ok := ParseCPUStat("intr 1\n"); ok {
		t.Fatal("no cpu line, yet read")
	}
}
