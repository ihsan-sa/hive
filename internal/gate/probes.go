package gate

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A ProbeFunc runs one piece of real work on one child and says how it went.
type ProbeFunc func(cfg *Config, c *Child) Result

// DefaultProbes is the work every candidate does, in order: cheapest first.
// The task probe runs only when the gate is given a task.
var DefaultProbes = []string{"daemon", "org", "e2e", "benchmark", "recurse", "task"}

var builtin = map[string]ProbeFunc{
	"daemon":    probeDaemon,
	"org":       probeOrg,
	"e2e":       probeE2E,
	"benchmark": probeBenchmark,
	"recurse":   probeRecurse,
	"task":      probeTask,
}

// probeDaemon starts the child's daemon on its fresh workspace, with the fake
// harness and a local forge, asks it for status, and shuts it down.
func probeDaemon(cfg *Config, c *Child) Result {
	ws := c.Workspace()
	os.RemoveAll(ws)
	os.MkdirAll(filepath.Join(ws, ".hive"), 0o755)
	conf := `{"harness":"fake","sandbox":"bare","bare_accepted":"` + time.Now().Format(time.RFC3339) + `","auto_update":false}`
	os.WriteFile(filepath.Join(ws, ".hive", "config.json"), []byte(conf), 0o644)
	defer c.with("HIVE_FORGE=local:" + filepath.Join(c.Root, "forge"))()
	ver, err := c.Exec(time.Minute, c.Root, c.Bin, "--version")
	if err != nil {
		return Result{Detail: "--version: " + tail(ver, 200)}
	}
	// One sandbox for the daemon and its client: under bubblewrap each has its
	// own network, so they must share one. The Exec deadline kills it all.
	script := `"$0" daemon >"$HIVE_HOME/daemon-probe.log" 2>&1 &
i=0
while [ $i -lt 60 ]; do
	if "$0" status >/dev/null 2>&1; then "$0" shutdown >/dev/null 2>&1; sleep 1; kill $! 2>/dev/null; exit 0; fi
	i=$((i+1)); sleep 1
done
kill $! 2>/dev/null; echo "status never answered"; exit 1`
	if out, err := c.Exec(3*time.Minute, c.Root, "sh", "-c", script, c.Bin); err != nil {
		return Result{Detail: tail(out, 300)}
	}
	return Result{Pass: true, Detail: tail(ver, 100)}
}

// probeOrg proves the child works on its own org repository with the org's
// token: it clones the empty repository, pushes a commit and reads it back.
func probeOrg(cfg *Config, c *Child) Result {
	if c.RepoURL == "" {
		return Result{Pass: true, Detail: "no org in this run"}
	}
	dir := filepath.Join(c.Root, "org-clone")
	os.RemoveAll(dir)
	if strings.HasPrefix(c.RepoURL, "https://") && c.Token == "" {
		return Result{Pass: true, Detail: "no contents-only token for the children: org round trip skipped"}
	}
	git := []string{"git"} // the child's environment carries the token to git; it is in no argv
	steps := [][]string{
		{"clone", "-q", c.RepoURL, dir},
		{"-C", dir, "checkout", "-q", "-B", "main"},
		{"-C", dir, "commit", "-q", "--allow-empty", "-m", "gate " + c.Name},
		{"-C", dir, "push", "-q", "origin", "main"},
	}
	for _, s := range steps {
		if out, err := c.Exec(2*time.Minute, c.Root, append(append([]string{}, git...), s...)...); err != nil {
			return Result{Detail: "git " + strings.Join(s[:2], " ") + ": " + tail(out, 300)}
		}
	}
	return Result{Pass: true, Detail: c.Repo}
}

// probeE2E is the evaluator's e2e suite (the base's tests, the same for both
// sides) run against the child's own binary. A failure names each failing
// test and the first line of what it said, which is where t.Fatalf puts its
// file, line and message: the end of the output is the tail of a status dump.
func probeE2E(cfg *Config, c *Child) Result {
	if c.Eval == nil {
		return Result{Pass: true, Detail: "prebuilt binary: no evaluator to test with"}
	}
	out, err := c.judge(cfg, time.Hour, nil, "")
	if err != nil {
		names, said := goTestFailures(out)
		if len(names) == 0 {
			return Result{Detail: "failed naming no test: " + tail(out, 300)}
		}
		lines := make([]string, len(names))
		for i, n := range names {
			lines[i] = n
			if said[n] != "" {
				lines[i] += ": " + said[n]
			}
		}
		return Result{Detail: "failed: " + strings.Join(lines, "; "), Failed: names}
	}
	return Result{Pass: true}
}

// failLine is go test's line for a failing test. A name goes into the
// verdict a worker reads, so only one shaped like a Go test's counts:
// the output passed through the side's sandbox.
var failLine = regexp.MustCompile(`^\s*--- FAIL: (Test[A-Za-z0-9_]{0,80}(?:/[A-Za-z0-9_#.+=-]{1,80}){0,3}) \(`)

// goTestFailures reads go test's plain output: the tests that failed, in
// order, and the first line each said (clipped). A parent that failed only
// with its subtests is named by them.
func goTestFailures(out string) (names []string, said map[string]string) {
	said = map[string]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		if m := failLine.FindStringSubmatch(line); m != nil {
			if _, seen := said[m[1]]; !seen {
				names = append(names, m[1])
				said[m[1]] = ""
			}
			cur = m[1]
			continue
		}
		t := strings.TrimSpace(line)
		if cur != "" && said[cur] == "" && t != "" && !strings.HasPrefix(t, "=== ") && t != "FAIL" {
			said[cur] = clip(t, 200)
		}
		cur = ""
	}
	names = slices.DeleteFunc(names, func(n string) bool {
		return slices.ContainsFunc(names, func(m string) bool { return strings.HasPrefix(m, n+"/") })
	})
	return names, said
}

var benchLine = regexp.MustCompile(`(?m)^BENCH (\{.*\}) ([0-9a-f]{64})$`)

// probeBenchmark runs the fixed asks on the demo textkit project (see
// TestBenchmark in internal/e2e). What is judged the evaluator measured from
// outside the child: what landed on the child's forge (the commits on main,
// and textkit's functions checked on fresh inputs), and its own clock. What
// the child says of itself (its cost, the asks it calls done) is shown, not
// judged.
func probeBenchmark(cfg *Config, c *Child) Result {
	if c.Eval == nil {
		return Result{Pass: true, Detail: "prebuilt binary: no benchmark to run"}
	}
	env := []string{fmt.Sprintf("HIVE_BENCH=%d", cfg.Bench)}
	for _, k := range []string{"HIVE_E2E_HARNESS", "HIVE_E2E_SANDBOX"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	// The evaluator signs its line with a key only it holds (see judge): a
	// line the side writes into the suite's output does not count.
	kb := make([]byte, 32)
	rand.Read(kb)
	key := hex.EncodeToString(kb)
	out, err := c.judge(cfg, 2*time.Hour, env, key, "-test.run", "^TestBenchmark$", "-test.v")
	var m []string
	for _, x := range benchLine.FindAllStringSubmatch(out, -1) {
		h := hmac.New(sha256.New, []byte(key))
		h.Write([]byte(x[1]))
		if hmac.Equal([]byte(x[2]), []byte(hex.EncodeToString(h.Sum(nil)))) {
			m = x
		}
	}
	if m == nil {
		return Result{Detail: "no BENCH line signed by the evaluator: " + tail(out, 300)}
	}
	var b struct {
		Score    *float64 `json:"score"`
		Finished *float64 `json:"asks_finished"`
		Commits  *float64 `json:"commits_landed"`
		Seconds  *float64 `json:"seconds"`
		Done     float64  `json:"asks_done_reported"`
		Cost     float64  `json:"cost_usd_reported"`
		Messages float64  `json:"worker_messages_reported"`
	}
	if json.Unmarshal([]byte(m[1]), &b) != nil || b.Score == nil || b.Finished == nil || b.Commits == nil || b.Seconds == nil {
		return Result{Detail: "the evaluator's BENCH line lacks what is judged (an evaluator from before the outside measure?): " + m[1]}
	}
	// Judged: whether what landed works (score, asks_finished), and the time,
	// only between sides that landed the same working functions. How many
	// commits landed, and what the side says of itself, are shown.
	same := []string{"score", "asks_finished"}
	return Result{Pass: err == nil, Detail: m[1], Metrics: []Metric{
		{Name: "score", Value: *b.Score, Higher: true}, {Name: "asks_finished", Value: *b.Finished, Higher: true},
		{Name: "seconds", Value: *b.Seconds, Given: same, Time: true},
		{Name: "commits_landed", Value: *b.Commits, Higher: true, Info: true},
		{Name: "asks_done_reported", Value: b.Done, Higher: true, Info: true}, {Name: "cost_usd_reported", Value: b.Cost, Info: true},
		{Name: "worker_messages_reported", Value: b.Messages, Higher: true, Info: true}}}
}

// probeRecurse has the child gate a trivial change of its own through a
// grandchild, which shows the gate runs inside a child. It is a self-test:
// the child's binary is both candidate and control, so the right verdict is
// "not better", and it must clean up its own local org repositories.
func probeRecurse(cfg *Config, c *Child) Result {
	if c.Depth <= 0 {
		return Result{Pass: true, Detail: "depth cap reached: not nested further"}
	}
	nested := filepath.Join(c.Root, "grandchild")
	os.RemoveAll(nested)
	c.Exec(30*time.Minute, c.Root, c.Bin, "gate", "--self-test", "--out", nested) // a rollback exits non-zero; the report says what happened
	// The nested report is the child's own output: read at most 1 MB of it,
	// take only its shape, and none of its text into this run's record.
	var rep Report
	if f, err := os.Open(filepath.Join(nested, "report.json")); err == nil {
		json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&rep)
		f.Close()
	}
	switch {
	case !reportID.MatchString(rep.ID):
		return Result{Detail: "no report from the nested gate"}
	case rep.Verdict.Keep:
		return Result{Detail: "the nested gate kept a change that was not better"}
	case rep.Error != "" || len(rep.Leaked) > 0 || len(rep.Candidate) == 0:
		return Result{Detail: fmt.Sprintf("the nested gate did not finish clean (error: %v, repos left: %d, candidate results: %d)", rep.Error != "", len(rep.Leaked), len(rep.Candidate))}
	}
	return Result{Pass: true, Detail: fmt.Sprintf("nested gate %s rolled back its self-test (%d candidate results, %d repos made and removed)", rep.ID, len(rep.Candidate), len(rep.Cleaned))}
}

var reportID = regexp.MustCompile(`^gate-[0-9]+-[0-9a-f]{8}$`)
