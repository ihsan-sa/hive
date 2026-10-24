package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A whole gate run under real bubblewrap, on a local org: a trivial
// candidate (the same Hive, ties allowed) is kept, a broken one is rolled
// back with its lesson written, and the org repositories are removed. It
// runs the real probes: the daemon, the org round trip, the benchmark (this
// tree's suite, compiled here, inside each side's sandbox, its sealed check
// beside it, the meter as the only way out) and recurse. ci's bubblewrap
// step runs it; its log is the end-to-end record.
func TestBwrapGateKeepsAndRollsBack(t *testing.T) {
	needBwrap(t)
	if testing.Short() && os.Getenv("HIVE_GATE_BWRAP") != "1" {
		t.Skip("a whole gate run: ci's bubblewrap step")
	}
	hive := builtHive(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	evTest := filepath.Join(filepath.Dir(hive), "e2e.test")
	if out, err := exec.Command("go", "test", "-c", "-o", evTest, "../e2e/").CombinedOutput(); err != nil {
		t.Fatalf("compiling the evaluator: %v %s", err, out)
	}
	ev := &Evaluator{Src: root, Test: evTest}
	bench := func(cfg *Config, c *Child) Result {
		c.Eval = ev
		defer func() { c.Eval = nil }()
		return probeBenchmark(cfg, c)
	}
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken", "hive")
	os.MkdirAll(filepath.Dir(broken), 0o755)
	// Answers --version, starts no daemon.
	os.WriteFile(broken, []byte("#!/bin/sh\ncase \"$1\" in --version) echo hive 0.0.0-broken ;; *) echo broken >&2; exit 1 ;; esac\n"), 0o755)

	run := func(name, cand string, tie bool) (Report, string) {
		t.Helper()
		lessons := filepath.Join(dir, name, ".hive", "lessons.md")
		org := filepath.Join(dir, name, "org")
		rep, err := Run(Config{CandBin: cand, CtrlBin: hive, Self: hive, Out: filepath.Join(dir, name, "run"), Sandbox: "bwrap", Reps: 1, Depth: 1, Bench: 1,
			Probes: []string{"daemon", "org", "bench", "recurse"}, Probe: map[string]ProbeFunc{"bench": bench}, Org: Local{Dir: org}, Lessons: lessons,
			// Same binary on both sides of the keep run: its time and bytes vary
			// a few percent run to run, so the floor is wide enough that only a
			// pass or a failure decides here, not the clock.
			Options: Options{AllowTie: tie, Floor: 1}, Timeout: 30 * time.Minute, Log: func(s string) { t.Log(name + ": " + s) }})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s report:\n%s", name, rep.Markdown())
		if rep.Sandbox != "bwrap" || len(rep.Repos) != 2 || len(rep.Cleaned) != 2 || len(rep.Leaked) != 0 {
			t.Errorf("%s: sandbox %s, repos made %v, removed %v, left %v", name, rep.Sandbox, rep.Repos, rep.Cleaned, rep.Leaked)
		}
		if ents, _ := os.ReadDir(org); len(ents) != 0 {
			t.Errorf("%s: repositories left in the org: %v", name, ents)
		}
		raw, _ := os.ReadFile(lessons)
		return rep, string(raw)
	}

	rep, lessons := run("keep", hive, true)
	if !rep.Verdict.Keep || lessons != "" {
		t.Errorf("the trivial candidate: %+v, lessons %q", rep.Verdict, lessons)
	}
	for _, r := range append(rep.Candidate, rep.Control...) {
		if !r.Pass {
			t.Errorf("keep run, %s: %s", r.Probe, r.Detail)
		}
		if r.Probe == "bench" && !strings.Contains(r.Detail, `"score":3`) {
			t.Errorf("the benchmark did not measure the landed function: %s", r.Detail)
		}
		if r.Probe == "bench" && !hasMetric(r, "egress_bytes") {
			t.Errorf("the meter's count is missing from the benchmark: %+v", r.Metrics)
		}
	}
	rep, lessons = run("rollback", broken, false)
	if rep.Verdict.Keep || !strings.Contains(lessons, "rolled back") || !strings.Contains(strings.Join(rep.Verdict.Regressions, "|"), "daemon passes 0/1") {
		t.Errorf("the broken candidate: %+v, lessons %q", rep.Verdict, lessons)
	}
}
