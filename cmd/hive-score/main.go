// Command hive-score is the Hive project's score: one number, higher better,
// which the improve module measures on main after every merge
// (.hive/project.json):
//
//	go run ./cmd/hive-score
//
// It compiles internal/e2e once and runs, one after the other on the fake
// harness, the benchmark the recursive gate judges with (TestBenchmark, all
// four asks on the demo textkit project) six times, three asked one by one and
// three all at once, which is the loop itself (the queue, workers side by side,
// branches landing on a main that moves), and then the rest of the e2e suite,
// a failed test run again once on its own. What it judges is measured outside the Hive:
// each benchmark's score, asks finished and seconds come from its BENCH line,
// which the test works out from textkit's main on fresh inputs and signs with
// a key this command hands it on stdin. How they combine, and the noise, is in
// docs/score.md. The last line printed is the number; the lines before say
// what made it.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	benchAsks    = 4
	benchFuncs   = 6               // textkit's functions: two written, four asked
	benchReps    = 3               // runs of each way of asking: one at-once run alone varies by a tenth
	benchTimeout = 2 * time.Minute // about 10 s now (a minute before the loop woke on events); a stuck ask is cut off, counted as not landed
	rerunTimeout = 6 * time.Minute
	suiteTimeout = 10 * time.Minute // about 4 normally; with a re-run, all stays inside the check's 30
)

func main() {
	out, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hive-score:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// run assumes go, git and python3 are on the PATH. The score command in
// .hive/project.json looks them up in the shell first, because `go run`
// exits 1 whatever its program exits with, and improve reads only the
// shell's own "not found" with exit 127 as a machine that can't run the score.
func run() (string, error) {
	root, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil || strings.TrimSpace(string(root)) == "" {
		return "", fmt.Errorf("not in the Hive's module: %v", err)
	}
	pkg := filepath.Join(filepath.Dir(strings.TrimSpace(string(root))), "internal", "e2e")
	tmp, err := os.MkdirTemp("", "hive-score-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	suite := filepath.Join(tmp, "e2e.test")
	if out, err := exec.Command("go", "test", "-c", "-o", suite, pkg).CombinedOutput(); err != nil {
		return "", fmt.Errorf("the e2e suite does not build: %v\n%s", err, out)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	k := hex.EncodeToString(key)
	var runs []Run
	for i := range 2 * benchReps {
		atOnce := i%2 == 1
		env := []string{"HIVE_BENCH=" + fmt.Sprint(benchAsks)}
		if atOnce {
			env = append(env, "HIVE_BENCH_AT_ONCE=1")
		}
		out, _ := runSuite(suite, pkg, k+"\n", benchTimeout, env,
			"-test.run", "^TestBenchmark$", "-test.v", "-test.timeout", benchTimeout.String())
		b, err := ParseBench(out, k)
		runs = append(runs, Run{AtOnce: atOnce, Bench: b, Err: err})
	}

	listed, err := runSuite(suite, pkg, "", time.Minute, nil, "-test.list", ".")
	var names []string
	for _, l := range strings.Fields(listed) {
		if strings.HasPrefix(l, "Test") && l != "TestBenchmark" {
			names = append(names, l)
		}
	}
	if err != nil || len(names) == 0 {
		// Not a score: 0/0 would read as a 100-point regression.
		return "", fmt.Errorf("could not list the e2e suite's tests: %v\n%s", err, listed)
	}
	// The tests the parent commit declares: one the candidate dropped counts
	// as failed, so deleting a failing test can't raise the pass rate.
	before, _ := exec.Command("git", "-C", pkg, "grep", "-h", "^func Test", "HEAD^", "--", ".").Output()
	first, _ := runSuite(suite, pkg, "", suiteTimeout, nil, "-test.skip", "^TestBenchmark$", "-test.v", "-test.timeout", suiteTimeout.String())
	res := ParseResults(first)
	flaky := 0
	// Each failed test runs again in a process of its own, so one that panics
	// doesn't take its neighbours down with it, all within one bound.
	end := time.Now().Add(rerunTimeout)
	for _, n := range names {
		left := time.Until(end).Truncate(time.Second)
		if r := res[n]; r == "pass" || r == "skip" || left < 10*time.Second {
			continue
		}
		again, _ := runSuite(suite, pkg, "", left, nil, "-test.run", "^"+regexp.QuoteMeta(n)+"$", "-test.v", "-test.timeout", left.String())
		if ParseResults(again)[n] == "pass" {
			res[n] = "pass"
			flaky++
		}
	}
	e, failed := Tally(names, Declared(string(before)), res)
	e.Flaky = flaky
	return Report(runs, e, failed), nil
}

// declared matches a top-level test's declaration in a _test.go file.
var declared = regexp.MustCompile(`^func (Test\w+)\(\w+ \*testing\.T\)`)

// Declared reads the test names from `git grep` lines of declarations,
// TestBenchmark left out (it is scored on its own).
func Declared(src string) []string {
	var names []string
	for _, l := range strings.Split(src, "\n") {
		if m := declared.FindStringSubmatch(l); m != nil && m[1] != "TestBenchmark" {
			names = append(names, m[1])
		}
	}
	return names
}

// Tally counts the suite's results: names are the candidate's tests, before
// those the parent commit declared. A test that failed, never reported (a
// panic, the timeout) or is gone from the candidate counts as failed; a
// skipped one is left out.
func Tally(names, before []string, res map[string]string) (E2E, []string) {
	var e E2E
	var failed []string
	for _, n := range before {
		if !slices.Contains(names, n) {
			e.Run++
			failed = append(failed, n+" (deleted)")
		}
	}
	for _, n := range names {
		switch res[n] {
		case "skip":
		case "pass":
			e.Passed++
			e.Run++
		default:
			e.Run++
			failed = append(failed, n)
		}
	}
	return e, failed
}

// runSuite runs the compiled e2e suite in its package directory, on the fake
// harness and bare whatever the caller's environment says.
func runSuite(suite, dir, stdin string, timeout time.Duration, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, suite, args...)
	cmd.Dir = dir
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "HIVE_E2E_") && !strings.HasPrefix(v, "HIVE_BENCH") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Env, "HIVE_BENCH_KEY=stdin")
	}
	cmd.Stdin = strings.NewReader(stdin)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// Bench is what the benchmark measured, from its signed BENCH line.
type Bench struct {
	Score        int     `json:"score"`
	AsksFinished int     `json:"asks_finished"`
	Asks         int     `json:"asks"`
	Seconds      float64 `json:"seconds"`
}

// ParseBench reads the last BENCH line in out and checks its HMAC under key.
func ParseBench(out, key string) (Bench, error) {
	var b Bench
	line := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); strings.HasPrefix(l, "BENCH ") {
			line = strings.TrimPrefix(l, "BENCH ")
		}
	}
	if line == "" {
		return b, errors.New("the benchmark printed no BENCH line")
	}
	i := strings.LastIndex(line, " ")
	if i < 0 {
		return b, errors.New("the BENCH line has no signature")
	}
	body, sig := line[:i], line[i+1:]
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(body))
	if want := hex.EncodeToString(mac.Sum(nil)); !hmac.Equal([]byte(want), []byte(sig)) {
		return b, errors.New("the BENCH line's signature is not the benchmark's")
	}
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return b, fmt.Errorf("the BENCH line is not JSON: %v", err)
	}
	return b, nil
}

var result = regexp.MustCompile(`(?m)^--- (PASS|FAIL|SKIP): (Test\w*)`)

// ParseResults reads each top-level test's result from `-test.v` output:
// "pass", "fail" or "skip". Subtests are indented, so they are left out.
func ParseResults(out string) map[string]string {
	r := map[string]string{}
	for _, m := range result.FindAllStringSubmatch(out, -1) {
		r[m[2]] = strings.ToLower(m[1])
	}
	return r
}

// E2E is the suite's count: tests that ran (not skipped), those that passed,
// first time or on their second run, and those that passed only the second time.
type E2E struct {
	Run, Passed, Flaky int
}

// Run is one benchmark run: asked one by one or all at once, what its BENCH
// line said, or why there was none.
type Run struct {
	AtOnce bool
	Bench
	Err error
}

// missed says the run didn't land every ask, or printed no valid line.
func (r Run) missed() bool { return r.Err != nil || r.AsksFinished < benchAsks }

// refSeconds is where the loop's term is zero: about three times the six
// runs' seconds when the score began counting the at-once run (2026-10-25:
// 11.5 s one by one, 7.5 s at once). Where it sits shifts every score alike,
// so only differences matter.
const refSeconds = 180

// Score combines them (docs/score.md): the e2e pass rate out of 100, ten for
// each of textkit's functions right on fresh inputs and ten more for each
// asked one, shared out over the runs, and the loop's speed: 15 points for
// every halving of all the runs' seconds together, so 13% faster is about 3
// points (the tolerance) whatever the Hive's speed already is. Each way of
// asking counts its fastest run for every one of its runs: the machine's load
// only ever adds seconds (one slow run under six checks at once cost 5-20
// points, issue #953), so the fastest is the closest to the Hive's own time.
// When any run misses an ask, every run is charged its whole bound, the
// slowest a run can be, so landing the work always outweighs doing it faster.
func Score(runs []Run, e E2E) float64 {
	rate := 0.0
	if e.Run > 0 {
		rate = float64(e.Passed) / float64(e.Run)
	}
	s, missed := 100*rate, len(runs) == 0
	for _, r := range runs {
		if r.Err == nil {
			s += 10 / float64(len(runs)) * float64(r.Score+r.AsksFinished)
		}
		missed = missed || r.missed()
	}
	secs := Seconds(runs)
	if missed {
		secs = benchTimeout.Seconds() * float64(max(len(runs), 1))
	}
	return s + 15*math.Log2(refSeconds/max(secs, 0.1))
}

// Seconds is the loop's time the score counts: for each way of asking, its
// fastest run's seconds once for each of its runs.
func Seconds(runs []Run) float64 {
	secs := 0.0
	for _, atOnce := range []bool{false, true} {
		n, fastest := 0, math.Inf(1)
		for _, r := range runs {
			if r.AtOnce == atOnce {
				n, fastest = n+1, min(fastest, r.Seconds)
			}
		}
		if n > 0 {
			secs += float64(n) * fastest
		}
	}
	return secs
}

// Report is the score's output: what made it, then the number on its own
// last line.
func Report(runs []Run, e E2E, failed []string) string {
	var s strings.Builder
	for _, r := range runs {
		how := "one by one"
		if r.AtOnce {
			how = "all at once"
		}
		if r.Err != nil {
			fmt.Fprintf(&s, "benchmark, %s: %v; counted as nothing landed\n", how, r.Err)
			continue
		}
		fmt.Fprintf(&s, "benchmark, %s: %d/%d functions right, %d/%d asks finished, %.1fs\n", how, r.Score, benchFuncs, r.AsksFinished, benchAsks, r.Seconds)
	}
	if slices.ContainsFunc(runs, Run.missed) {
		fmt.Fprintf(&s, "benchmark: an ask didn't land, so every run is charged its bound (%.0fs)\n", benchTimeout.Seconds())
	} else if len(runs) > 0 {
		fmt.Fprintf(&s, "benchmark: %.1fs counted, each way's fastest run for each of its runs\n", Seconds(runs))
	}
	fmt.Fprintf(&s, "e2e: %d/%d passed (%d only on a second run)", e.Passed, e.Run, e.Flaky)
	if len(failed) > 0 {
		fmt.Fprintf(&s, "; failed: %s", strings.Join(failed, ", "))
	}
	fmt.Fprintf(&s, "\n%.2f\n", Score(runs, e))
	return s.String()
}
