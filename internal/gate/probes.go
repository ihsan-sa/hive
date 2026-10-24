package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A ProbeFunc runs one piece of real work on one child and says how it went.
type ProbeFunc func(cfg *Config, c *Child) Result

// DefaultProbes is the work every candidate does, in order: cheapest first.
var DefaultProbes = []string{"daemon", "org", "e2e", "benchmark", "recurse"}

var builtin = map[string]ProbeFunc{
	"daemon":    probeDaemon,
	"org":       probeOrg,
	"e2e":       probeE2E,
	"benchmark": probeBenchmark,
	"recurse":   probeRecurse,
}

// probeDaemon starts the child's daemon on its fresh workspace, with the fake
// harness and a local forge, asks it for status, and shuts it down.
func probeDaemon(cfg *Config, c *Child) Result {
	ws := c.Workspace()
	os.RemoveAll(ws)
	os.MkdirAll(filepath.Join(ws, ".hive"), 0o755)
	conf := `{"harness":"fake","sandbox":"bare","bare_accepted":"` + time.Now().Format(time.RFC3339) + `","auto_update":false}`
	os.WriteFile(filepath.Join(ws, ".hive", "config.json"), []byte(conf), 0o644)
	c.Extra = []string{"HIVE_FORGE=local:" + filepath.Join(c.Root, "forge")}
	defer func() { c.Extra = nil }()
	ver, err := c.Exec(time.Minute, c.Root, c.Bin, "--version")
	if err != nil {
		return Result{Detail: "--version: " + tail(ver, 200)}
	}
	ctx, cancel := contextWith(10 * time.Minute)
	defer cancel()
	d := c.Command(ctx, c.Root, c.Bin, "daemon")
	d.Stdout, d.Stderr = nil, nil
	if err := d.Start(); err != nil {
		return Result{Detail: "daemon: " + err.Error()}
	}
	defer func() { d.Process.Kill(); d.Wait() }()
	var out string
	for i := 0; i < 60; i++ {
		if out, err = c.Exec(30*time.Second, c.Root, c.Bin, "status"); err == nil {
			c.Exec(30*time.Second, c.Root, c.Bin, "shutdown")
			return Result{Pass: true, Detail: strings.TrimSpace(ver)}
		}
		time.Sleep(time.Second)
	}
	return Result{Detail: "status never answered: " + tail(out, 300)}
}

// probeOrg proves the child works on its own org repository with the org's
// token: it clones the empty repository, pushes a commit and reads it back.
func probeOrg(cfg *Config, c *Child) Result {
	if c.RepoURL == "" {
		return Result{Pass: true, Detail: "no org in this run"}
	}
	dir := filepath.Join(c.Root, "org-clone")
	os.RemoveAll(dir)
	git := []string{"git"}
	if strings.HasPrefix(c.RepoURL, "https://") && c.Token != "" {
		git = append(git, "-c", "http.extraHeader=Authorization: Basic "+basic(c.Token))
	}
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

var failed = regexp.MustCompile(`(?m)^--- FAIL: (\S+)`)

// probeE2E is the evaluator's e2e suite (the base's tests, the same for both
// sides) run against the child's own binary.
func probeE2E(cfg *Config, c *Child) Result {
	if c.Eval == "" {
		return Result{Pass: true, Detail: "prebuilt binary: no evaluator to test with"}
	}
	c.Extra = []string{"HIVE_E2E_BIN=" + c.Bin}
	defer func() { c.Extra = nil }()
	out, err := c.Exec(time.Hour, c.Eval, "go", "test", "./internal/e2e/", "-count=1", "-timeout", "50m")
	if err != nil {
		names := failed.FindAllStringSubmatch(out, -1)
		var fs []string
		for _, n := range names {
			fs = append(fs, n[1])
		}
		return Result{Detail: "failed: " + strings.Join(fs, ", ") + " " + tail(out, 300)}
	}
	return Result{Pass: true}
}

var benchLine = regexp.MustCompile(`(?m)^BENCH (\{.*\})$`)

// probeBenchmark runs the fixed asks on the demo textkit project (see
// TestBenchmark in internal/e2e) and measures what the child got done: the
// score, the asks finished, wall time and cost.
func probeBenchmark(cfg *Config, c *Child) Result {
	if c.Eval == "" {
		return Result{Pass: true, Detail: "prebuilt binary: no benchmark to run"}
	}
	env := []string{fmt.Sprintf("HIVE_BENCH=%d", cfg.Bench), "HIVE_E2E_BIN=" + c.Bin}
	for _, k := range []string{"HIVE_E2E_HARNESS", "HIVE_E2E_SANDBOX"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	c.Extra = env
	defer func() { c.Extra = nil }()
	out, err := c.Exec(2*time.Hour, c.Eval, "go", "test", "./internal/e2e/", "-run", "^TestBenchmark$", "-count=1", "-v", "-timeout", "100m")
	m := benchLine.FindStringSubmatch(out)
	if m == nil {
		return Result{Detail: "no BENCH line: " + tail(out, 300)}
	}
	var b struct {
		Score    float64 `json:"score"`
		Finished float64 `json:"asks_finished"`
		Seconds  float64 `json:"seconds"`
		Cost     float64 `json:"cost_usd"`
	}
	if json.Unmarshal([]byte(m[1]), &b) != nil {
		return Result{Detail: "bad BENCH line: " + m[1]}
	}
	r := Result{Pass: err == nil, Detail: m[1], Metrics: []Metric{
		{"score", b.Score, true}, {"asks_finished", b.Finished, true}, {"seconds", b.Seconds, false}, {"cost_usd", b.Cost, false}}}
	return r
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
	out, _ := c.Exec(30*time.Minute, c.Root, c.Bin, "gate", "--self-test", "--out", nested) // a rollback exits non-zero; the report says what happened
	var rep Report
	if raw, err := os.ReadFile(filepath.Join(nested, "report.json")); err == nil {
		json.Unmarshal(raw, &rep)
	}
	switch {
	case rep.ID == "":
		return Result{Detail: "no report from the nested gate: " + tail(out, 300)}
	case rep.Verdict.Keep:
		return Result{Detail: "the nested gate kept a change that was not better"}
	case rep.Error != "" || len(rep.Leaked) > 0 || len(rep.Candidate) == 0:
		return Result{Detail: "the nested gate did not finish clean: " + rep.Error + strings.Join(rep.Leaked, ",")}
	}
	return Result{Pass: true, Detail: "nested gate " + rep.ID + ": " + rep.Verdict.Reason}
}
