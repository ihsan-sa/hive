// Command hive-score is the Hive project's score: one number, higher better,
// which the improve module measures on main after every merge
// (.hive/project.json):
//
//	go run ./cmd/hive-score
//
// It compiles internal/e2e once and runs, one after the other on the fake
// harness, the benchmark the recursive gate judges with (TestBenchmark, all
// four asks on the demo textkit project) and the rest of the e2e suite, a
// failed test run again once. What it judges is measured outside the Hive:
// the benchmark's score, asks finished and seconds come from its BENCH line,
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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	benchAsks    = 4
	benchFuncs   = 6               // textkit's functions: two written, four asked
	benchTimeout = 7 * time.Minute // about 1 normally; a stuck ask is cut off, counted as not landed
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
	began := time.Now()
	benchOut, _ := runSuite(suite, pkg, k+"\n", benchTimeout, "HIVE_BENCH="+fmt.Sprint(benchAsks),
		"-test.run", "^TestBenchmark$", "-test.v", "-test.timeout", benchTimeout.String())
	b, berr := ParseBench(benchOut, k)
	if berr != nil {
		// The Hive didn't get through the benchmark: nothing landed, and it
		// took as long as it took.
		b = Bench{Seconds: time.Since(began).Seconds()}
	}

	listed, _ := runSuite(suite, pkg, "", time.Minute, "", "-test.list", ".")
	var names []string
	for _, l := range strings.Fields(listed) {
		if strings.HasPrefix(l, "Test") && l != "TestBenchmark" {
			names = append(names, l)
		}
	}
	first, _ := runSuite(suite, pkg, "", suiteTimeout, "", "-test.skip", "^TestBenchmark$", "-test.v", "-test.timeout", suiteTimeout.String())
	res := ParseResults(first)
	var again []string
	for _, n := range names {
		if r := res[n]; r != "pass" && r != "skip" {
			again = append(again, regexp.QuoteMeta(n))
		}
	}
	flaky := 0
	if len(again) > 0 {
		second, _ := runSuite(suite, pkg, "", rerunTimeout, "", "-test.run", "^("+strings.Join(again, "|")+")$", "-test.v", "-test.timeout", rerunTimeout.String())
		for n, r := range ParseResults(second) {
			if r == "pass" && res[n] != "pass" {
				res[n] = "pass"
				flaky++
			}
		}
	}
	e := E2E{Flaky: flaky}
	var failed []string
	for _, n := range names {
		switch res[n] {
		case "skip":
		case "pass":
			e.Passed++
			e.Run++
		default: // failed, or never reported (a panic or the timeout)
			e.Run++
			failed = append(failed, n)
		}
	}
	return Report(b, berr, e, failed), nil
}

// runSuite runs the compiled e2e suite in its package directory, on the fake
// harness and bare whatever the caller's environment says.
func runSuite(suite, dir, stdin string, timeout time.Duration, env string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, suite, args...)
	cmd.Dir = dir
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "HIVE_E2E_") && !strings.HasPrefix(v, "HIVE_BENCH") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	if env != "" {
		cmd.Env = append(cmd.Env, env)
	}
	if strings.HasPrefix(env, "HIVE_BENCH=") {
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

// Score combines them (docs/score.md): the e2e pass rate out of 100, ten for
// each of textkit's functions right on fresh inputs and ten more for each
// asked one, less a point for every six seconds the benchmark took. One
// function more right outweighs a minute saved.
func Score(b Bench, e E2E) float64 {
	rate := 0.0
	if e.Run > 0 {
		rate = float64(e.Passed) / float64(e.Run)
	}
	return 100*rate + 10*float64(b.Score) + 10*float64(b.AsksFinished) - b.Seconds/6
}

// Report is the score's output: what made it, then the number on its own
// last line.
func Report(b Bench, berr error, e E2E, failed []string) string {
	var s strings.Builder
	if berr != nil {
		fmt.Fprintf(&s, "benchmark: %v; counted as nothing landed\n", berr)
	}
	fmt.Fprintf(&s, "benchmark: %d/%d functions right, %d/%d asks finished, %.1fs\n", b.Score, benchFuncs, b.AsksFinished, benchAsks, b.Seconds)
	fmt.Fprintf(&s, "e2e: %d/%d passed (%d only on a second run)", e.Passed, e.Run, e.Flaky)
	if len(failed) > 0 {
		fmt.Fprintf(&s, "; failed: %s", strings.Join(failed, ", "))
	}
	fmt.Fprintf(&s, "\n%.2f\n", Score(b, e))
	return s.String()
}
