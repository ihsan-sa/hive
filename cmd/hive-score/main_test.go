package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
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
// reads, and one more function right outweighs a minute saved.
func TestReportFormat(t *testing.T) {
	b := Bench{Score: 6, AsksFinished: 4, Asks: 4, Seconds: 60}
	out := Report(b, nil, E2E{Run: 40, Passed: 39, Flaky: 1}, []string{"TestStalls"})
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	last := lines[len(lines)-1]
	v, err := strconv.ParseFloat(last, 64)
	if err != nil || strings.TrimSpace(last) != last {
		t.Fatalf("last line %q is not a number alone: %v", last, err)
	}
	if want := 100*39.0/40 + 100 - 10; math.Abs(v-want) > 0.005 {
		t.Fatalf("score %g, want %g", v, want)
	}
	if !strings.Contains(out, "39/40 passed (1 only on a second run); failed: TestStalls") {
		t.Fatalf("report does not say what made it:\n%s", out)
	}
	worse := Score(Bench{Score: 5, AsksFinished: 3, Seconds: 0}, E2E{Run: 40, Passed: 40})
	if worse >= Score(Bench{Score: 6, AsksFinished: 4, Seconds: 60}, E2E{Run: 40, Passed: 40}) {
		t.Fatal("a minute saved outweighed a function landed")
	}
	if out := Report(Bench{Seconds: 900}, errors.New("no BENCH line"), E2E{}, nil); !strings.HasSuffix(out, "\n-150.00\n") {
		t.Fatalf("a benchmark that failed must still give a number:\n%s", out)
	}
}
