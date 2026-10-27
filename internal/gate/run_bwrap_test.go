package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A whole gate run under real bubblewrap, on a local org. The keep run is a
// real gate from commits: a trivial commit on a clone of this tree against
// that clone's head, both built by the gate (exact sha, stamped version, vet
// and short tests in the sandbox), judged by the base's own suite compiled by
// the gate, and kept with ties allowed. The rollback run hands the gate a
// broken prebuilt candidate, which is rolled back.
// Both leave the org empty. The probes are the real ones: the daemon, the org
// round trip, the benchmark (inside each side's sandbox, its sealed check
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

	// The keep run builds both sides from commits, as a real gate does: a
	// clone of this tree is the base, and a trivial commit on it the head.
	repo := filepath.Join(dir, "repo")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// The base moves on after the head branched, so the candidate is its
	// head with the base merged in, built and bound read-only as it would land.
	git("clone", "-q", root, repo)
	fork := git("-C", repo, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(repo, "docs", "gate-runs", "TRIVIAL.md"), []byte("A trivial change, for the gate's keep run.\n"), 0o644)
	git("-C", repo, "add", "-A")
	git("-C", repo, "commit", "-qm", "a trivial change")
	head := git("-C", repo, "rev-parse", "HEAD")
	git("-C", repo, "branch", "-f", "candidate", head) // a ref, so the gate's clone of repo holds the head
	git("-C", repo, "checkout", "-q", "--detach", fork)
	os.WriteFile(filepath.Join(repo, "docs", "gate-runs", "BASE.md"), []byte("The base moving on, for the gate's keep run.\n"), 0o644)
	git("-C", repo, "add", "-A")
	git("-C", repo, "commit", "-qm", "the base moves on")
	base := git("-C", repo, "rev-parse", "HEAD")

	run := func(name string, cfg Config, tie bool) Report {
		t.Helper()
		org := filepath.Join(dir, name, "org")
		cfg.Self, cfg.Out, cfg.Sandbox, cfg.Reps, cfg.Depth, cfg.Bench = hive, filepath.Join(dir, name, "run"), "bwrap", 1, 1, 1
		cfg.Org, cfg.Timeout = Local{Dir: org}, 30*time.Minute
		cfg.Log = func(s string) { t.Log(name + ": " + s) }
		// The two sides of the keep run differ by a docs line: their time and
		// bytes vary a few percent run to run, so the floor is wide enough
		// that only a pass or a failure decides here, not the clock.
		cfg.Options = Options{AllowTie: tie, Floor: 1}
		cfg.Probes = []string{"daemon", "org", "benchmark", "recurse"}
		if cfg.Probe != nil { // prebuilt sides: the benchmark with this tree's suite
			cfg.Probes[2] = "bench"
		}
		rep, err := Run(cfg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s report:\n%s", name, rep.Markdown())
		if d := os.Getenv("HIVE_GATE_RECORD"); d != "" { // ci turns these into annotations
			os.MkdirAll(d, 0o755)
			os.WriteFile(filepath.Join(d, name+".md"), []byte(rep.Markdown()), 0o644)
		}
		if rep.Sandbox != "bwrap" || len(rep.Repos) != 2 || len(rep.Cleaned) != 2 || len(rep.Leaked) != 0 {
			t.Errorf("%s: sandbox %s, repos made %v, removed %v, left %v", name, rep.Sandbox, rep.Repos, rep.Cleaned, rep.Leaked)
		}
		if ents, _ := os.ReadDir(org); len(ents) != 0 {
			t.Errorf("%s: repositories left in the org: %v", name, ents)
		}
		return rep
	}

	rep := run("keep", Config{Repo: repo, Head: head, Base: base}, true)
	if !rep.Verdict.Keep {
		t.Errorf("the trivial candidate: %+v", rep.Verdict)
	}
	if rep.Merged == "" || rep.Merged == head {
		t.Errorf("the candidate was built at %s, not its head %s merged with the base", short(rep.Merged), short(head))
	}
	if rep.CandVersion != "0.0.0-gate."+head[:9] || rep.CandSHA256 == "" || rep.Sig == "" {
		t.Errorf("the built candidate: version %q, sha256 %q, signed %v", rep.CandVersion, rep.CandSHA256, rep.Sig != "")
	}
	for _, r := range append(rep.Candidate, rep.Control...) {
		if !r.Pass {
			t.Errorf("keep run, %s: %s", r.Probe, r.Detail)
		}
		if r.Probe == "daemon" && !strings.Contains(r.Detail, "0.0.0-gate.") {
			t.Errorf("a side did not run the binary the gate built and stamped: %s", r.Detail)
		}
		if r.Probe == "benchmark" && !strings.Contains(r.Detail, `"score":3`) {
			t.Errorf("the benchmark did not measure the landed function: %s", r.Detail)
		}
		if r.Probe == "benchmark" && !hasMetric(r, "egress_bytes") {
			t.Errorf("the meter's count is missing from the benchmark: %+v", r.Metrics)
		}
	}
	rep = run("rollback", Config{CandBin: broken, CtrlBin: hive, Probe: map[string]ProbeFunc{"bench": bench}}, false)
	if rep.Verdict.Keep || rep.CantRun || !strings.Contains(strings.Join(rep.Verdict.Regressions, "|"), "daemon passes 0/1") {
		t.Errorf("the broken candidate: %+v", rep.Verdict)
	}

	// Could not run, under bubblewrap: a probe broken on both sides was never
	// measured, so the run is neither a keep nor the candidate's rollback.
	rep, err = Run(Config{CandBin: hive, CtrlBin: hive, Self: hive, Out: filepath.Join(dir, "cantrun", "run"), Sandbox: "bwrap", Reps: 1,
		Probes: []string{"broken"}, Probe: map[string]ProbeFunc{"broken": func(cfg *Config, c *Child) Result {
			out, err := c.Exec(time.Minute, c.Root, "sh", "-c", "exit 7")
			return Result{Pass: err == nil, Detail: out}
		}}})
	if err != nil || !rep.CantRun || rep.Verdict.Keep || rep.Sandbox != "bwrap" {
		t.Errorf("a probe broken on both sides under bubblewrap: %v %+v", err, rep)
	}
}

// When the GitHub org leg can't be set up (here gh fails), the run goes on
// with a local org and the report says why, rather than leave the change
// unjudged; what it made on either org is removed.
func TestBwrapOrgFallsBackToLocal(t *testing.T) {
	needBwrap(t)
	hive := builtHive(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho 'HTTP 403: forbidden' >&2\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	rep, err := Run(Config{CandBin: hive, CtrlBin: hive, Self: hive, Out: filepath.Join(dir, "out", "run"), Sandbox: "bwrap", Reps: 1,
		Probes: []string{"org"}, Org: GitHub{Owner: "Lab", Token: "org-secret"}, Options: Options{AllowTie: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.OrgNote, "local org instead of Lab") || !strings.Contains(rep.OrgNote, "403") {
		t.Errorf("org note %q", rep.OrgNote)
	}
	if !rep.Verdict.Keep || len(rep.Leaked) != 0 || len(rep.Cleaned) != 2 || !strings.HasSuffix(rep.Cleaned[0], "-local") {
		t.Errorf("verdict %+v, repos %v cleaned %v leaked %v", rep.Verdict, rep.Repos, rep.Cleaned, rep.Leaked)
	}
}
