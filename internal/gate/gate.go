package gate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config is one gate run.
type Config struct {
	Repo       string   // git repository (path or URL) of the Hive's source
	Head       string   // the candidate's exact commit
	Base       string   // the current Hive's commit: the control
	Eval       string   // the commit whose e2e tests and benchmark judge both sides (default Base); a candidate never grades itself
	CandBin    string   // a prebuilt candidate binary (skips building Head; a self-test)
	CtrlBin    string   // a prebuilt control binary
	Out        string   // where the run's files and report go
	Probes     []string // which probes to run (default all)
	Reps       int      // repeats of every probe on each side, interleaved (default 3)
	Bench      int      // asks in the benchmark (default 4, the whole set)
	Org        Org      // the fresh test repository's host; nil runs without one
	OrgToken   string   // the org token the gate itself uses to make and delete test repositories; children never hold it
	ChildToken string   // the contents-only token the children hold (empty: they hold none and the org probe is skipped)
	ReachOK    bool     // the caller passed CheckReach on these tokens within the hour (the daemon), so the gate doesn't probe again
	Lessons    string   // .hive/lessons.md to append to on a rollback
	Judged     bool     // a judge decides a measured verdict after the run, and writes its lesson itself
	Depth      int      // levels of gating the children may still do (capped at MaxDepth)
	MaxRepos   int      // most org repositories one run may make (default 4)
	Timeout    time.Duration
	Options    Options
	Sandbox    string // "" or "bwrap"; "bare" only with AllowBare
	AllowBare  bool   // run the children without filesystem isolation where bubblewrap cannot; never with a GitHub org, and never installed
	Log        func(string)
	Probe      map[string]ProbeFunc // extra probes, by name
	KeepFiles  bool
	Self       string // the gate's own binary, which runs gate-fwd and gate-check in the sandboxes (default this executable)
}

// MaxDepth is how many levels deep gates may nest; a child gating a
// grandchild is depth 1.
const MaxDepth = 2

// Report is the record of a run.
type Report struct {
	ID          string     `json:"id"`
	Head        string     `json:"head"`
	Merged      string     `json:"merged,omitempty"` // what the candidate was built from: Head with Base merged in (Head itself when it already holds Base)
	Base        string     `json:"base"`
	Sandbox     string     `json:"sandbox"`
	Started     time.Time  `json:"started"`
	Seconds     float64    `json:"seconds"`
	Candidate   []Result   `json:"candidate"`
	Control     []Result   `json:"control"`
	Verdict     Verdict    `json:"verdict"`
	Repos       []string   `json:"repos,omitempty"`   // org repositories made
	Cleaned     []string   `json:"cleaned,omitempty"` // and removed
	Leaked      []string   `json:"leaked,omitempty"`  // made and not removed
	Error       string     `json:"error,omitempty"`
	CandBin     string     `json:"candidate_bin,omitempty"`     // the verified copy of the candidate's binary
	CandSHA256  string     `json:"candidate_sha256,omitempty"`  // its hash, taken right after the build
	CantRun     bool       `json:"cant_run,omitempty"`          // the gate could not run or measure: neither a keep nor the candidate's rollback
	Conflicts   []string   `json:"conflicts,omitempty"`         // the files where Head and Base conflict: cant_run, and the worker merges main
	CandVersion string     `json:"candidate_version,omitempty"` // what its --version must say: the gate stamps it at build time
	Unit        UnitReport `json:"unit"`                        // the vet and short-test step, each side's failures named
	Fixes       FixReport  `json:"fixes"`                       // the candidate's own new tests, run on both sides
	Transient   bool       `json:"transient,omitempty"`         // could not run for a cause that may pass (load, a flake on both sides): try again soon
	Alone       bool       `json:"alone,omitempty"`             // could not run for a cause tied to this candidate alone (preparing its build, the run's clock): another change at this base would not hit it
	Measured    bool       `json:"measured,omitempty"`          // the verdict compares both sides' results (Decide), so a judge may weigh it
	Sig         string     `json:"sig,omitempty"`               // HMAC of the rest with the gate's key, which no child sees: only a signed report installs
}

// versionVar is where the build stamps the version; versionOf is what it
// stamps for a commit.
const versionVar = "github.com/ihsan-sa/hive-standalone-build/internal/core.Version"

func versionOf(sha string) string { return "0.0.0-gate." + short(sha) }

func (c *Config) log(f string, a ...any) {
	if c.Log != nil {
		c.Log(fmt.Sprintf(f, a...))
	}
}

// Run builds both Hives, runs the same work on each in its own sandbox and
// decides. The report is written to Out whatever happens; an error means the
// gate could not decide, which is a rollback.
func Run(cfg Config) (rep Report, err error) {
	t0 := time.Now()
	if cfg.Reps <= 0 {
		cfg.Reps = 3
	}
	if cfg.MaxRepos <= 0 {
		cfg.MaxRepos = 4
	}
	if cfg.Bench <= 0 {
		cfg.Bench = 4
	}
	if cfg.Depth > MaxDepth {
		cfg.Depth = MaxDepth
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Hour
	}
	if len(cfg.Probes) == 0 {
		cfg.Probes = DefaultProbes
	}
	rep = Report{ID: fmt.Sprintf("gate-%d-%s", t0.Unix(), randHex()), Head: cfg.Head, Base: cfg.Base, Sandbox: cfg.Sandbox, Started: t0.UTC()}
	if cfg.Out == "" {
		cfg.Out = filepath.Join(os.TempDir(), rep.ID)
	}
	if cfg.Out, err = filepath.Abs(cfg.Out); err != nil {
		return rep, err
	}
	if err = os.MkdirAll(cfg.Out, 0o755); err != nil {
		return rep, err
	}
	if n := reapOrphans(); n > 0 {
		cfg.log("ended %d processes left by a gate that died", n)
	}
	defer func() {
		// However the run ends: nothing it started outlives it, and its
		// folders go but for the report and the verified binary.
		if n := Reap(cfg.Out); n > 0 {
			cfg.log("ended %d processes the run left", n)
		}
		rep.Seconds = time.Since(t0).Seconds()
		var fault *candidateFault
		var conflict *mergeConflict
		switch {
		case errors.As(err, &conflict):
			// The change can't land as it stands: its worker merges main, and
			// nothing was measured, so it is no rollback and no lesson.
			rep.Error, rep.CantRun, rep.Alone, rep.Conflicts = err.Error(), true, true, conflict.files
			rep.Verdict = Verdict{Reason: err.Error()}
		case errors.As(err, &fault):
			// The candidate itself failed before it could be measured: a rollback.
			rep.Error = err.Error()
			rep.Verdict = Verdict{Reason: "the candidate " + fault.why}
		case err != nil:
			// The gate could not run (no sandbox, no Go, Yama off, an old
			// evaluator, the org, the clock): nothing was measured, so this
			// is not the candidate's failure and no lesson is written.
			rep.Error, rep.CantRun = err.Error(), true
			rep.Transient = errors.As(err, new(transient))
			rep.Alone = errors.As(err, new(alone))
			rep.Verdict = Verdict{Reason: "the gate could not run: " + err.Error()}
		case !rep.Verdict.Keep && len(rep.Verdict.Regressions) == 0 && len(rep.Verdict.Unmeasured) > 0:
			rep.CantRun = true // a probe failed on both sides: that path was never measured
			rep.Transient = true
		}
		rep.sign()
		write(cfg, rep)
		if !rep.Verdict.Keep && !rep.CantRun && !(cfg.Judged && rep.Measured) {
			noteLesson(cfg, rep)
		}
	}()
	if cfg.Sandbox, err = pickSandbox(cfg); err != nil {
		return rep, err
	}
	rep.Sandbox = cfg.Sandbox
	if cfg.Sandbox == "bwrap" && cfg.Self == "" {
		// The sandboxes run the gate's own binary (gate-fwd, gate-check): it
		// must be hive, not a test binary.
		if self, _ := os.Executable(); strings.HasSuffix(strings.TrimSuffix(self, ".exe"), ".test") {
			return rep, fmt.Errorf("the gate runs from a test binary (%s): set Config.Self to a hive binary", filepath.Base(self))
		}
	}
	if g, ok := cfg.Org.(GitHub); ok && cfg.ChildToken != "" && !cfg.ReachOK {
		if err = CheckReach(g, cfg.ChildToken, cfg.Repo); err != nil {
			return rep, err
		}
	}
	// Children get the toolchain and module cache by path, read-only; each has
	// its own build cache. A self-test on prebuilt binaries needs no Go.
	gotool, goErr := FindGo()
	if goErr != nil && (cfg.CandBin == "" || cfg.CtrlBin == "") {
		return rep, goErr
	}

	cand := &Child{Name: "candidate", Root: filepath.Join(cfg.Out, "candidate"), Sandbox: cfg.Sandbox, Depth: cfg.Depth, Bin: cfg.CandBin, Go: gotool, Fwd: cfg.Self}
	ctrl := &Child{Name: "control", Root: filepath.Join(cfg.Out, "control"), Sandbox: cfg.Sandbox, Depth: cfg.Depth, Bin: cfg.CtrlBin, Go: gotool, Fwd: cfg.Self}
	// Each side's way out is its own meter, outside every sandbox: under
	// bubblewrap the only one it has, so the meter's count is its cost.
	sockDir, err := os.MkdirTemp(shortTmp(), "hive-meter-") // short: a socket path has a length limit
	if err != nil {
		return rep, err
	}
	defer os.RemoveAll(sockDir)
	for _, c := range []*Child{cand, ctrl} {
		if c.Meter, err = StartMeter(filepath.Join(sockDir, c.Name+".sock")); err != nil {
			return rep, err
		}
		defer c.Meter.Close()
	}
	// Both sides are built, then both are checked, before anyone's fault is
	// decided: a step that fails on both sides is the gate's (could not run),
	// and only the go build or the side's own checks failing on the
	// candidate alone are the candidate's.
	var built []*Child
	buildErrs := map[*Child]error{}
	for _, c := range []*Child{cand, ctrl} {
		sha, merge := cfg.Head, cfg.Base // the candidate as it would land: its head with the base merged in
		if c == ctrl {
			sha, merge = cfg.Base, ""
		}
		if err = c.Prepare(); err != nil {
			return rep, err
		}
		if c.Bin == "" {
			cfg.log("building %s at %s", c.Name, short(sha))
			e := build(cfg, c, sha, merge)
			if c == cand && c.Commit != "" {
				rep.Merged = c.Commit
				if c.Commit != sha {
					cfg.log("  with %s merged in, at %s", short(merge), short(c.Commit))
				}
			}
			if e != nil {
				var own *ownFailure
				var conflict *mergeConflict
				if errors.As(e, &conflict) {
					return rep, e
				}
				if !errors.As(e, &own) {
					// The gate's own step (the clone, the module download): could not run.
					err = fmt.Errorf("preparing %s's build: %v", c.Name, e)
					if c == cand {
						err = alone{err} // its own go.mod, its own sha: another change may fare otherwise
					}
					return rep, err
				}
				buildErrs[c] = e
				continue
			}
			built = append(built, c)
			if c == cand {
				rep.CandVersion = versionOf(sha)
			}
		}
	}
	if err = whose(cand, ctrl, buildErrs, "does not build"); err != nil {
		return rep, err
	}

	// The candidate's binary is copied out of the child's reach and hashed
	// before any of its code runs; that copy, checked against the hash, is all
	// an install ever uses.
	if err = os.MkdirAll(filepath.Join(cfg.Out, "verified"), 0o755); err != nil {
		return rep, err
	}
	rep.CandBin = filepath.Join(cfg.Out, "verified", "candidate-hive")
	if err = copyFile(cand.Bin, rep.CandBin); err != nil {
		return rep, err
	}
	if rep.CandSHA256, err = FileSHA(rep.CandBin); err != nil {
		return rep, err
	}

	// Only now does each side's own code run: vet and the short tests, on
	// its read-only checkout, in its sandbox.
	// What fails on both sides is the environment's and is only reported;
	// what fails on the candidate alone is run again before it is its own.
	checkErrs := map[*Child]error{}
	for _, c := range built {
		cfg.log("vet and short tests of %s", c.Name)
		if e := unitTest(c); e != nil {
			checkErrs[c] = e
			cfg.log("  %s fails: %s", c.Name, tail(e.Error(), 2000))
		}
	}
	rep.Unit, err = judgeUnit(cand, ctrl, checkErrs, modulePath(cand.Src))
	for _, f := range rep.Unit.Shared {
		cfg.log("  failing on both sides, the environment's: %s", f.Name())
	}
	for _, f := range rep.Unit.Flaky {
		cfg.log("  failed once on the candidate, passed again: %s", f.Name())
	}
	if err != nil {
		return rep, err
	}

	// The candidate's own new and changed tests, on both sides: one that
	// passes on it and fails on the control is a fix the benchmark may not see.
	if len(built) == 2 {
		cfg.log("the candidate's new and changed tests, on both sides")
		rep.Fixes = findFixes(&cfg, cand, ctrl)
		for _, f := range rep.Fixes.Fixes {
			cfg.log("  %s", f)
		}
		for _, n := range rep.Fixes.Notes {
			cfg.log("  %s", n)
		}
	}

	// Both sides are judged by one evaluator, taken from Eval (the base by
	// default), so a candidate cannot change the tests or the benchmark that
	// grade it. Its tests are compiled here, with their own caches, and run
	// in a sandbox of their own; the side's binary runs in the side's sandbox.
	if len(built) > 0 {
		evalSHA := cfg.Eval
		if evalSHA == "" {
			evalSHA = cfg.Base
		}
		cfg.log("building the evaluator at %s", short(evalSHA))
		var ev *Evaluator
		if ev, err = buildEvaluator(cfg, gotool, evalSHA); err != nil {
			return rep, fmt.Errorf("evaluator: %v", err)
		}
		cand.Eval, ctrl.Eval = ev, ev
	}

	// Each side gets its own fresh org repository, made once the builds passed
	// and removed when the run ends, however it ends.
	made := 0
	if cfg.Org != nil {
		defer func() {
			for _, name := range rep.Repos {
				if e := cfg.Org.Delete(name); e != nil {
					rep.Leaked = append(rep.Leaked, name+": "+e.Error())
				} else {
					rep.Cleaned = append(rep.Cleaned, name)
				}
			}
		}()
		for _, c := range []*Child{cand, ctrl} {
			if made >= cfg.MaxRepos {
				return rep, fmt.Errorf("the run would make more than %d org repositories", cfg.MaxRepos)
			}
			made++
			name := fmt.Sprintf("%s-%s", rep.ID, c.Name)
			if c.RepoURL, err = cfg.Org.Create(name); err != nil {
				return rep, err
			}
			// Only a repository this run made is this run's to delete.
			c.Repo = name
			rep.Repos = append(rep.Repos, name)
			c.Token = cfg.ChildToken
			if !strings.Contains(c.RepoURL, "://") {
				// A local org's repository is a folder: the child sees it
				// (writable, to push) and nothing else of the org's.
				c.RW = append(c.RW, c.RepoURL)
			}
		}
	}

	// The same work on both, interleaved, so the day's weather hits both.
	deadline := time.Now().Add(cfg.Timeout)
	for rep0 := 0; rep0 < cfg.Reps; rep0++ {
		for _, name := range cfg.Probes {
			p, ok := cfg.Probe[name]
			if !ok {
				p, ok = builtin[name]
			}
			if !ok {
				return rep, fmt.Errorf("no probe %q", name)
			}
			for _, side := range []struct {
				c   *Child
				out *[]Result
			}{{cand, &rep.Candidate}, {ctrl, &rep.Control}} {
				if time.Now().After(deadline) {
					err = fmt.Errorf("out of time (%s) before %s's %s", cfg.Timeout, side.c.Name, name)
					if slowerAlone(rep.Candidate, rep.Control) {
						err = alone{err} // the candidate took the time; on a slow machine both would
					}
					return rep, err
				}
				cfg.log("rep %d: %s on %s", rep0+1, name, side.c.Name)
				start, spent := time.Now(), side.c.Meter.Bytes()
				r := p(&cfg, side.c)
				reap(cfg.Out, true) // a probe's daemons end with it
				r.Probe, r.Rep, r.Seconds = name, rep0+1, time.Since(start).Seconds()
				if r.Pass && hasMetric(r, "score") { // a benchmark, whatever its name
					// Cost, counted by the meter, lower is better, and judged
					// only between sides that landed the same working code.
					r.Metrics = append(r.Metrics, Metric{Name: "egress_bytes", Value: float64(side.c.Meter.Bytes() - spent), Given: []string{"score", "asks_finished"}})
				}
				*side.out = append(*side.out, r)
				cfg.log("  %s %s: pass=%v %s", side.c.Name, name, r.Pass, r.Detail)
			}
		}
	}
	// The candidate's binary must be the one that was built and hashed.
	if got, e := FileSHA(cand.Bin); e != nil || got != rep.CandSHA256 {
		return rep, &candidateFault{"changed its own binary during the run", fmt.Errorf("the candidate's binary changed during the run")}
	}
	opts := cfg.Options
	for _, f := range rep.Fixes.Fixes {
		opts.Fixed = append(opts.Fixed, f.String())
	}
	rep.Verdict = Decide(rep.Candidate, rep.Control, opts)
	rep.Measured = true
	if len(rep.Unit.Shared) > 0 {
		rep.Verdict.Reason += "; failing on both sides, so the environment's and not counted: " + failureNames(rep.Unit.Shared, modulePath(cand.Src))
	}
	return rep, nil
}

// slowerAlone says the candidate took more time than the control over the
// probes both ran. The candidate goes first, so it may have run one more;
// that one is left out, or a machine slow for both would blame it.
func slowerAlone(cand, ctrl []Result) bool {
	n := min(len(cand), len(ctrl))
	var c, k float64
	for i := 0; i < n; i++ {
		c += cand[i].Seconds
		k += ctrl[i].Seconds
	}
	return c > k
}

func hasMetric(r Result, name string) bool {
	for _, m := range r.Metrics {
		if m.Name == name {
			return true
		}
	}
	return false
}

// shortTmp is /tmp where there is one, whatever TMPDIR says.
func shortTmp() string {
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		return "/tmp"
	}
	return ""
}

func randHex() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// FileSHA is the sha256 of a file, hex.
func FileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func short(s string) string {
	if len(s) > 9 {
		return s[:9]
	}
	return s
}

// pickSandbox is bubblewrap, or bare only when the caller allowed it and no
// GitHub org is in the run.
func pickSandbox(cfg Config) (string, error) {
	_, github := cfg.Org.(GitHub)
	switch {
	case insideSandbox() && cfg.Sandbox == "bwrap":
		return "", fmt.Errorf("inside one of the gate's sandboxes no sandbox is started (bubblewrap nests only one level where user namespaces are restricted)")
	case insideSandbox() && cfg.AllowBare && !github:
		return "bare", nil // contained by the sandbox it runs in
	case cfg.Sandbox == "bwrap" || (cfg.Sandbox == "" && BwrapWorks()):
		return "bwrap", nil
	case cfg.Sandbox != "" && cfg.Sandbox != "bare":
		return "", fmt.Errorf("no sandbox %q", cfg.Sandbox)
	case !cfg.AllowBare:
		return "", fmt.Errorf("bubblewrap cannot make namespaces here, and the children run only isolated (--allow-bare runs them bare, never with --org)")
	case github:
		return "", fmt.Errorf("a run with a GitHub org runs only under bubblewrap")
	}
	return "bare", nil
}

// build clones the repository clean, checks out exactly sha (with merge
// merged in, if given) and builds the binary, stamped with sha's version,
// into a folder the child can write only while it builds. Nothing of the
// side's own code has run yet.
func build(cfg Config, c *Child, sha, merge string) error {
	if sha == "" {
		return fmt.Errorf("no commit given")
	}
	if err := checkout(cfg, c, sha, merge); err != nil {
		return err
	}
	if err := c.Prepare(); err != nil {
		return err
	}
	c.BinDir = filepath.Join(cfg.Out, "built-"+c.Name)
	if err := os.MkdirAll(c.BinDir, 0o755); err != nil {
		return err
	}
	c.Bin = filepath.Join(c.BinDir, "hive")
	if err := prefetch(cfg, c); err != nil {
		return err
	}
	c.Building = true
	defer func() { c.Building = false }()
	defer c.with(offline...)()
	if out, err := c.Exec(30*time.Minute, c.Src, "go", "build", "-ldflags", "-X "+versionVar+"="+versionOf(sha), "-o", c.Bin, "./cmd/hive"); err != nil {
		return &ownFailure{fmt.Errorf("go build: %s", tail(out, 600))}
	}
	return nil
}

// offline is how a side builds and tests: from the modules the gate fetched
// for it, never downloading into the module cache, which it sees read-only.
var offline = []string{"GOPROXY=off", "GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local"}

// with adds env to the child's until the returned func is called.
func (c *Child) with(env ...string) func() {
	saved := c.Extra
	c.Extra = append(append([]string{}, saved...), env...)
	return func() { c.Extra = saved }
}

// prefetch downloads the modules the side's go.mod and go.sum name into the
// module cache, here, before the side is built: a candidate that adds or bumps
// a dependency still builds in its sandbox. It runs no code of the side's:
// the go command reads go.mod and go.sum and fetches what they name, checked
// against go.sum and the checksum database. GOTOOLCHAIN=local, so a go.mod
// that asks for another Go cannot make the go command fetch and run it.
// GOVCS=*:off: modules come only from the module proxy, so a go.mod cannot
// make the go command run git or hg here against a URL the candidate chose
// (a module only reachable that way fails the build: a rollback).
func prefetch(cfg Config, c *Child) error {
	dir := filepath.Join(cfg.Out, "prefetch")
	for _, d := range []string{"home", "gocache"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	cmd := exec.Command("go", "mod", "download")
	cmd.Dir = c.Src
	cmd.Env = append(cleanEnv(), "HOME="+filepath.Join(dir, "home"), "GOCACHE="+goCache("", filepath.Join(dir, "gocache")), "GOROOT="+c.Go.Root, "GOMODCACHE="+c.Go.ModCache,
		"GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local", "GOVCS=*:off", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod download: %v: %s", err, tail(string(out), 600))
	}
	return nil
}

func gitRun(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// Git only, and no HOME: nothing here reads a login.
	cmd.Env = append(cleanEnv(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// checkout clones the repository clean into c.Src at exactly sha, then
// merges merge into it when sha lacks it: the change as it would land, so a
// bug the base has since fixed is not counted against it. A merge that
// conflicts is a *mergeConflict. c.Commit is where the tree ends up.
func checkout(cfg Config, c *Child, sha, merge string) error {
	c.Src = filepath.Join(cfg.Out, "src-"+c.Name)
	run := func(dir, name string, args ...string) (string, error) { return gitRun(dir, name, args...) }
	if out, err := run("", "git", "clone", "-q", "--no-checkout", cfg.Repo, c.Src); err != nil {
		return fmt.Errorf("clone: %v: %s", err, tail(out, 300))
	}
	if out, err := run(c.Src, "git", "checkout", "-q", "--detach", sha); err != nil {
		return fmt.Errorf("checkout %s: %v: %s", short(sha), err, tail(out, 300))
	}
	got, _ := run(c.Src, "git", "rev-parse", "HEAD")
	if got = strings.TrimSpace(got); got == "" || (!strings.HasPrefix(got, sha) && !strings.HasPrefix(sha, got)) {
		return fmt.Errorf("checked out %s, not %s", short(got), short(sha))
	}
	if st, _ := run(c.Src, "git", "status", "--porcelain"); strings.TrimSpace(st) != "" {
		return fmt.Errorf("checkout is not clean: %s", tail(st, 200))
	}
	c.Commit = got
	if merge == "" {
		return nil
	}
	if _, err := run(c.Src, "git", "merge-base", "--is-ancestor", merge, "HEAD"); err == nil {
		return nil // the head already holds the base
	}
	// No hooks, a fixed identity: the merge runs nothing of either side's.
	out, err := run(c.Src, "git", "-c", "core.hooksPath=/dev/null", "-c", "user.name=hive gate", "-c", "user.email=gate@hive.invalid",
		"merge", "-q", "--no-edit", "--no-ff", "-m", "gate: merge "+short(merge)+" into "+short(sha), merge)
	if err != nil {
		files, _ := run(c.Src, "git", "diff", "--name-only", "--diff-filter=U")
		run(c.Src, "git", "merge", "--abort")
		if fs := strings.Fields(files); len(fs) > 0 {
			return &mergeConflict{base: merge, files: fs}
		}
		return fmt.Errorf("merging %s into %s: %v: %s", short(merge), short(sha), err, tail(out, 300))
	}
	got, _ = run(c.Src, "git", "rev-parse", "HEAD")
	c.Commit = strings.TrimSpace(got)
	if st, _ := run(c.Src, "git", "status", "--porcelain"); strings.TrimSpace(st) != "" {
		return fmt.Errorf("the merge is not clean: %s", tail(st, 200))
	}
	return nil
}

// mergeConflict is the candidate's head conflicting with the base it is
// judged against: the gate can't build the change as it would land, and its
// worker merges main. Not a rollback.
type mergeConflict struct {
	base  string
	files []string
}

func (m *mergeConflict) Error() string {
	return fmt.Sprintf("the candidate conflicts with the base %s in %s: its branch needs main merged in", short(m.base), strings.Join(m.files, ", "))
}

// write saves the report as JSON and as a page, and adds a line to the log of
// gate runs beside it.
func write(cfg Config, rep Report) {
	raw, _ := json.MarshalIndent(rep, "", "  ")
	os.WriteFile(filepath.Join(cfg.Out, "report.json"), raw, 0o644)
	os.WriteFile(filepath.Join(cfg.Out, "report.md"), []byte(rep.Markdown()), 0o644)
	line, _ := json.Marshal(map[string]any{"id": rep.ID, "head": rep.Head, "base": rep.Base, "keep": rep.Verdict.Keep, "cant_run": rep.CantRun, "reason": rep.Verdict.Reason, "at": rep.Started})
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(cfg.Out), "gates.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Write(append(line, '\n'))
		f.Close()
	}
}

// Markdown is the report as a page.
func (r Report) Markdown() string {
	var b strings.Builder
	verdict := "ROLLED BACK"
	switch {
	case r.Verdict.Keep:
		verdict = "KEPT"
	case r.CantRun:
		verdict = "COULD NOT RUN"
	}
	cand := short(r.Head)
	if cand == "" {
		cand = "prebuilt"
	}
	if r.Merged != "" && r.Merged != r.Head {
		cand += " merged with " + short(r.Base) + " at " + short(r.Merged)
	}
	if r.CandVersion != "" {
		cand += " (" + r.CandVersion + ")"
	}
	if r.CandSHA256 != "" {
		cand += ", binary sha256 " + r.CandSHA256[:12]
	}
	ctrl := short(r.Base)
	if ctrl == "" {
		ctrl = "prebuilt"
	}
	fmt.Fprintf(&b, "# Gate %s: %s\n\n%s\n\n- candidate %s; control %s; sandbox %s, %.0fs\n", r.ID, verdict, r.Verdict.Reason, cand, ctrl, r.Sandbox, r.Seconds)
	for _, g := range r.Verdict.Gains {
		fmt.Fprintf(&b, "- gain: %s\n", g)
	}
	for _, g := range r.Verdict.Regressions {
		fmt.Fprintf(&b, "- regression: %s\n", g)
	}
	table := func(name string, rs []Result) {
		if len(rs) == 0 {
			fmt.Fprintf(&b, "\n## %s\n\nNo probe ran: the run stopped before them.\n", name)
			return
		}
		fmt.Fprintf(&b, "\n## %s\n\n| probe | rep | pass | seconds | metrics | detail |\n|---|---|---|---|---|---|\n", name)
		for _, x := range rs {
			var ms []string
			for _, m := range x.Metrics {
				ms = append(ms, fmt.Sprintf("%s=%.4g", m.Name, m.Value))
			}
			detail := tail(x.Detail, 120)
			if len(x.Failed) > 0 { // what matters is at its start: the failing tests and what each said
				detail = clip(x.Detail, 400)
			}
			fmt.Fprintf(&b, "| %s | %d | %v | %.1f | %s | %s |\n", x.Probe, x.Rep, x.Pass, x.Seconds, strings.Join(ms, " "), strings.ReplaceAll(strings.ReplaceAll(detail, "\n", " "), "|", "\\|"))
		}
	}
	for _, f := range r.Conflicts {
		fmt.Fprintf(&b, "- conflicts: %s\n", f)
	}
	if r.Error != "" && len(r.Unit.Candidate)+len(r.Unit.Control) == 0 {
		fmt.Fprintf(&b, "\n%s\n", fenced(tail(r.Error, 3000)))
	}
	b.WriteString(r.Unit.markdown())
	b.WriteString(r.Fixes.markdown())
	table("Candidate", r.Candidate)
	table("Control", r.Control)
	fmt.Fprintf(&b, "\nOrg repositories made: %d, removed: %d, left: %d\n", len(r.Repos), len(r.Cleaned), len(r.Leaked))
	for _, l := range r.Leaked {
		fmt.Fprintf(&b, "- LEFT BEHIND: %s\n", l)
	}
	return b.String()
}

// ownFailure is a side's own step failing (its go build, vet or tests), as
// against a step of the gate's around it (the clone, the module download).
type ownFailure struct{ err error }

func (o *ownFailure) Error() string { return o.err.Error() }
func (o *ownFailure) Unwrap() error { return o.err }

// whose decides whose failure a step's errors are. On the candidate alone
// (with the control fine or not built here): the candidate's, a rollback.
// On the control, or on both: the gate's, could not run.
func whose(cand, ctrl *Child, errs map[*Child]error, why string) error {
	ce, ke := errs[cand], errs[ctrl]
	switch {
	case ce != nil && ke == nil:
		return &candidateFault{why, fmt.Errorf("the candidate %s: %v", why, ce)}
	case ce != nil && ke != nil:
		return fmt.Errorf("both sides %s, so the step itself cannot run here: the candidate: %v; the control: %v", why, ce, ke)
	case ke != nil:
		return fmt.Errorf("the control %s: %v", why, ke)
	}
	return nil
}

// alone is a could-not-run tied to this candidate (preparing its own build,
// the run's clock): no verdict on it, but no reason for another change at
// the same base to wait either.
type alone struct{ err error }

func (a alone) Error() string { return a.err.Error() }
func (a alone) Unwrap() error { return a.err }

// candidateFault is a failure of the candidate itself before it was
// measured (it does not build, fails its own checks, rewrote its binary):
// a rollback. Every other error is the gate's: it could not run. why is a
// fixed phrase; the candidate's own output never reaches a lesson.
type candidateFault struct {
	why string
	err error
}

func (f *candidateFault) Error() string { return f.err.Error() }
func (f *candidateFault) Unwrap() error { return f.err }

// noteLesson records a rollback where the next worker reads it. Gates run
// side by side share the file, so the lesson is appended in one write.
func noteLesson(cfg Config, rep Report) {
	if cfg.Lessons == "" {
		return
	}
	os.MkdirAll(filepath.Dir(cfg.Lessons), 0o755)
	raw, _ := os.ReadFile(cfg.Lessons)
	s := ""
	if strings.TrimSpace(string(raw)) == "" {
		s = "# Lessons\n\nWhat was tried and why it didn't stay. Read before working here.\n\n"
	} else if !strings.HasSuffix(string(raw), "\n") {
		s = "\n"
	}
	// Text from the candidate's own output never goes here: a lesson is read by
	// the next worker as instructions.
	reason := rep.Verdict.Reason // Decide's words, or a fixed candidateFault.why
	s += fmt.Sprintf("- The gate rolled back %s (run %s): %s\n", short(rep.Head), rep.ID, reason)
	f, err := os.OpenFile(cfg.Lessons, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	f.WriteString(s)
	f.Close()
}
