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
// a failed test run again once on its own. A benchmark run during which the
// machine had no CPU free doesn't count; another runs in its place. What it
// judges is measured outside the Hive:
// each benchmark's score, asks finished and seconds come from its BENCH line,
// which the test works out from textkit's main on fresh inputs and signs with
// a key this command hands it on stdin. How they combine, and the noise, is in
// docs/score.md. The last line printed is the number; the lines before say
// what made it.
package main

import (
	"bufio"
	"bytes"
	"cmp"
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
	"strconv"
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
	benchBudget  = 6 * time.Minute  // for runs again after loud ones; with the suite, still inside the check's 30
	sampleEvery  = 250 * time.Millisecond
	// quietCPUs is the CPU that must stay free, in every sample while a
	// benchmark run goes, for its seconds to count: on a machine with none
	// free the runs took two to three times as long (docs/score.md).
	quietCPUs = 1.0
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
	// Runs go on, alternating, until each way of asking has benchReps runs
	// that count (a run the machine's CPU was free for), or benchBudget is spent with at least the six done.
	var all []Run
	for end := time.Now().Add(benchBudget); ; {
		atOnce, more := Next(all, time.Now().Before(end))
		if !more {
			break
		}
		env := []string{"HIVE_BENCH=" + fmt.Sprint(benchAsks)}
		if atOnce {
			env = append(env, "HIVE_BENCH_AT_ONCE=1")
		}
		free := watchCPU()
		out, _ := runSuite(suite, pkg, k+"\n", benchTimeout, env,
			"-test.run", "^TestBenchmark$", "-test.v", "-test.timeout", benchTimeout.String())
		b, err := ParseBench(out, k)
		if err != nil {
			err = fmt.Errorf("%w (it ended: %s)", err, lastLines(out, 3))
		}
		f := free()
		all = append(all, Run{AtOnce: atOnce, Bench: b, Err: err, Free: f, Loud: f >= 0 && f < quietCPUs})
	}
	runs, loud := Counted(all)

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
	return Report(runs, e, failed, loud), nil
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
// line said, or why there was none, and the least CPU the machine had free
// while it ran (negative: not known), which made it loud when under
// quietCPUs.
type Run struct {
	AtOnce bool
	Bench
	Err  error
	Free float64
	Loud bool
}

// missed says the run didn't land every ask, or printed no valid line.
func (r Run) missed() bool { return r.Err != nil || r.AsksFinished < benchAsks }

// counts says the run's seconds are the Hive's and not the machine's load:
// it wasn't loud. A run that missed an ask counts like any other: a hang
// leaves the CPU free, so it is quiet and counts, while one the load set
// off (on 10-28, two runs with no BENCH line under busy loops charged a
// reading the bound, 136.67 among 252-256) is run again.
func (r Run) counts() bool { return !r.Loud }

// Next says which way of asking the next benchmark run uses, and whether
// there is one: until each way has benchReps runs that count, the one with
// fewer first (on a tie, not the way just run); once the budget is out
// (inTime false) only until each way has benchReps runs, loud or not, the
// one with fewer runs first, so neither is scored on fewer than its three.
func Next(all []Run, inTime bool) (atOnce, more bool) {
	n, ran := map[bool]int{}, map[bool]int{}
	for _, r := range all {
		ran[r.AtOnce]++
		if r.counts() {
			n[r.AtOnce]++
		}
	}
	if !inTime {
		n = ran
	}
	if n[false] >= benchReps && n[true] >= benchReps {
		return false, false
	}
	if n[false] != n[true] {
		return n[true] < n[false], true
	}
	return len(all) > 0 && !all[len(all)-1].AtOnce, true
}

// Counted splits the runs into those the score counts, in the order they
// ran, and the loud ones it leaves out: the first benchReps that count of
// each way of asking, a way short of them made up with its fastest loud
// runs (the ones the load moved least).
func Counted(all []Run) (runs, loud []Run) {
	take := make([]bool, len(all))
	for _, atOnce := range []bool{false, true} {
		var quiet, noisy []int
		for i, r := range all {
			if r.AtOnce != atOnce {
				continue
			}
			if r.counts() {
				quiet = append(quiet, i)
			} else {
				noisy = append(noisy, i)
			}
		}
		slices.SortStableFunc(noisy, func(a, b int) int {
			return cmp.Or(cmp.Compare(btoi(all[a].missed()), btoi(all[b].missed())), cmp.Compare(all[a].Seconds, all[b].Seconds))
		})
		for _, i := range append(quiet, noisy...)[:min(benchReps, len(quiet)+len(noisy))] {
			take[i] = true
		}
	}
	for i, r := range all {
		if take[i] {
			runs = append(runs, r)
		} else if !r.counts() {
			loud = append(loud, r)
		}
	}
	return runs, loud
}

// watchCPU samples /proc/stat every sampleEvery until the func it returns
// is called, which gives the least CPU found free in a sample (idle or
// waiting on the disk, which leaves the CPU to whoever wants it), or -1
// where there is no /proc/stat to read.
func watchCPU() func() float64 {
	prev, ok := cpuStat()
	if !ok {
		return func() float64 { return -1 }
	}
	least := math.Inf(1)
	sample := func() {
		cur, ok := cpuStat()
		if ok && cur.total > prev.total {
			least = min(least, float64(cur.idle-prev.idle)/float64(cur.total-prev.total)*float64(cur.cpus))
			prev = cur
		}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(sampleEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				sample()
				return
			case <-t.C:
				sample()
			}
		}
	}()
	return func() float64 {
		close(stop)
		<-done
		if math.IsInf(least, 1) {
			return -1
		}
		return least
	}
}

type cpuTimes struct {
	idle, total uint64
	cpus        int
}

func cpuStat() (cpuTimes, bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, false
	}
	return ParseCPUStat(string(raw))
}

// ParseCPUStat reads /proc/stat's machine-wide line: idle and iowait
// against all the time counted (user to steal), and how many CPUs there are.
func ParseCPUStat(raw string) (cpuTimes, bool) {
	var c cpuTimes
	ok := false
	for _, l := range strings.Split(raw, "\n") {
		f := strings.Fields(l)
		switch {
		case len(f) > 8 && f[0] == "cpu":
			for i, v := range f[1:9] {
				n, err := strconv.ParseUint(v, 10, 64)
				if err != nil {
					return c, false
				}
				c.total += n
				if i == 3 || i == 4 {
					c.idle += n
				}
			}
			ok = true
		case len(f) > 0 && strings.HasPrefix(f[0], "cpu"):
			c.cpus++
		}
	}
	return c, ok && c.cpus > 0
}

// refSeconds is where the loop's term is zero: about three times the six
// runs' seconds when the score began counting the at-once run (2026-10-25:
// 11.5 s one by one, 7.5 s at once). Where it sits shifts every score alike,
// so only differences matter.
const refSeconds = 180

// Score combines them (docs/score.md): the e2e pass rate out of 100, ten for
// each of textkit's functions right on fresh inputs and ten more for each
// asked one, shared out over the runs, and the loop's speed: 15 points for
// every halving of all the runs' seconds together, so 13% faster is about 3
// points (the tolerance) whatever the Hive's speed already is. Each run of a
// way of asking is taken at that way's fastest: load only slows a run, so
// the fastest is the one it moved least (on a quiet machine they are a few
// percent apart). When any run
// misses an ask, every run is charged its whole bound, the slowest a run can
// be, so landing the work always outweighs doing it faster.
func Score(runs []Run, e E2E) float64 {
	rate := 0.0
	if e.Run > 0 {
		rate = float64(e.Passed) / float64(e.Run)
	}
	s, secs, missed := 100*rate, 0.0, len(runs) == 0
	fastest, n := map[bool]float64{}, map[bool]int{}
	for _, r := range runs {
		if r.Err == nil {
			s += 10 / float64(len(runs)) * float64(r.Score+r.AsksFinished)
		}
		if n[r.AtOnce] == 0 || r.Seconds < fastest[r.AtOnce] {
			fastest[r.AtOnce] = r.Seconds
		}
		n[r.AtOnce]++
		missed = missed || r.missed()
	}
	for way := range n {
		secs += benchReps * fastest[way] // three runs of each way, whatever ran
	}
	if missed {
		secs = benchTimeout.Seconds() * float64(max(len(runs), 1))
	}
	return s + 15*math.Log2(refSeconds/max(secs, 0.1))
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// lastLines is the last n non-empty lines of out, joined by " | ".
func lastLines(out string, n int) string {
	var ls []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ls = append(ls, l)
		}
	}
	return strings.Join(ls[max(len(ls)-n, 0):], " | ")
}

func (r Run) freeNote() string {
	switch {
	case r.Loud:
		return fmt.Sprintf(" (%.1f CPU free at least: loud, counted for want of a quiet run)", r.Free)
	case r.Free > 0:
		return fmt.Sprintf(" (%.1f CPU free at least)", r.Free)
	}
	return ""
}

// Report is the score's output: what made it, then the number on its own
// last line.
func Report(runs []Run, e E2E, failed []string, loud []Run) string {
	var s strings.Builder
	for _, r := range runs {
		how := "one by one"
		if r.AtOnce {
			how = "all at once"
		}
		if r.Err != nil {
			fmt.Fprintf(&s, "benchmark, %s: %v; counted as nothing landed%s\n", how, r.Err, r.freeNote())
			continue
		}
		fmt.Fprintf(&s, "benchmark, %s: %d/%d functions right, %d/%d asks finished, %.1fs%s\n", how, r.Score, benchFuncs, r.AsksFinished, benchAsks, r.Seconds, r.freeNote())
	}
	if len(loud) > 0 {
		var secs []string
		for _, r := range loud {
			if r.missed() {
				secs = append(secs, fmt.Sprintf("missed (%.1f)", r.Free))
			} else {
				secs = append(secs, fmt.Sprintf("%.1fs (%.1f)", r.Seconds, r.Free))
			}
		}
		fmt.Fprintf(&s, "benchmark: %d runs not counted, under %.0f CPU free at times while they ran (least free): %s\n", len(loud), quietCPUs, strings.Join(secs, ", "))
	}
	if slices.ContainsFunc(runs, Run.missed) {
		fmt.Fprintf(&s, "benchmark: an ask didn't land, so every run is charged its bound (%.0fs)\n", benchTimeout.Seconds())
	}
	fmt.Fprintf(&s, "e2e: %d/%d passed (%d only on a second run)", e.Passed, e.Run, e.Flaky)
	if len(failed) > 0 {
		fmt.Fprintf(&s, "; failed: %s", strings.Join(failed, ", "))
	}
	fmt.Fprintf(&s, "\n%.2f\n", Score(runs, e))
	return s.String()
}
