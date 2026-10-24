package gate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Lessons    string   // .hive/lessons.md to append to on a rollback
	Depth      int      // levels of gating the children may still do (capped at MaxDepth)
	MaxRepos   int      // most org repositories one run may make (default 4)
	Timeout    time.Duration
	Options    Options
	Sandbox    string // "" or "bwrap"; "bare" only with AllowBare
	AllowBare  bool   // run the children without filesystem isolation where bubblewrap cannot; never with a GitHub org, and never installed
	Log        func(string)
	Probe      map[string]ProbeFunc // extra probes, by name
	KeepFiles  bool
}

// MaxDepth is how many levels deep gates may nest; a child gating a
// grandchild is depth 1.
const MaxDepth = 2

// Report is the record of a run.
type Report struct {
	ID          string    `json:"id"`
	Head        string    `json:"head"`
	Base        string    `json:"base"`
	Sandbox     string    `json:"sandbox"`
	Started     time.Time `json:"started"`
	Seconds     float64   `json:"seconds"`
	Candidate   []Result  `json:"candidate"`
	Control     []Result  `json:"control"`
	Verdict     Verdict   `json:"verdict"`
	Repos       []string  `json:"repos,omitempty"`   // org repositories made
	Cleaned     []string  `json:"cleaned,omitempty"` // and removed
	Leaked      []string  `json:"leaked,omitempty"`  // made and not removed
	Error       string    `json:"error,omitempty"`
	CandBin     string    `json:"candidate_bin,omitempty"`     // the verified copy of the candidate's binary
	CandSHA256  string    `json:"candidate_sha256,omitempty"`  // its hash, taken right after the build
	CandVersion string    `json:"candidate_version,omitempty"` // what its --version must say: the gate stamps it at build time
	Sig         string    `json:"sig,omitempty"`               // HMAC of the rest with the gate's key, which no child sees: only a signed report installs
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
	if err = os.MkdirAll(cfg.Out, 0o755); err != nil {
		return rep, err
	}
	defer func() {
		rep.Seconds = time.Since(t0).Seconds()
		if err != nil {
			rep.Error = err.Error()
			rep.Verdict = Verdict{Reason: "the gate could not finish: " + err.Error()}
		}
		rep.sign()
		write(cfg, rep)
		if !rep.Verdict.Keep {
			noteLesson(cfg, rep)
		}
	}()
	if cfg.Sandbox, err = pickSandbox(cfg); err != nil {
		return rep, err
	}
	rep.Sandbox = cfg.Sandbox
	if g, ok := cfg.Org.(GitHub); ok && cfg.ChildToken != "" {
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

	cand := &Child{Name: "candidate", Root: filepath.Join(cfg.Out, "candidate"), Sandbox: cfg.Sandbox, Depth: cfg.Depth, Bin: cfg.CandBin, Go: gotool}
	ctrl := &Child{Name: "control", Root: filepath.Join(cfg.Out, "control"), Sandbox: cfg.Sandbox, Depth: cfg.Depth, Bin: cfg.CtrlBin, Go: gotool}
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
	var built []*Child
	for _, c := range []*Child{cand, ctrl} {
		sha := cfg.Head
		if c == ctrl {
			sha = cfg.Base
		}
		if err = c.Prepare(); err != nil {
			return rep, err
		}
		if c.Bin == "" {
			cfg.log("building %s at %s", c.Name, short(sha))
			if err = build(cfg, c, sha); err != nil {
				return rep, fmt.Errorf("%s does not build: %v", c.Name, err)
			}
			built = append(built, c)
			if c == cand {
				rep.CandVersion = versionOf(sha)
			}
		}
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
	for _, c := range built {
		cfg.log("vet and short tests of %s", c.Name)
		if err = unitTest(c); err != nil {
			return rep, fmt.Errorf("%s fails its own checks: %v", c.Name, err)
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
					return rep, fmt.Errorf("out of time (%s) before %s's %s", cfg.Timeout, side.c.Name, name)
				}
				cfg.log("rep %d: %s on %s", rep0+1, name, side.c.Name)
				start, spent := time.Now(), side.c.Meter.Bytes()
				r := p(&cfg, side.c)
				r.Probe, r.Rep, r.Seconds = name, rep0+1, time.Since(start).Seconds()
				if name == "benchmark" && r.Pass {
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
		return rep, fmt.Errorf("the candidate's binary changed during the run")
	}
	rep.Verdict = Decide(rep.Candidate, rep.Control, cfg.Options)
	return rep, nil
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

// build clones the repository clean, checks out exactly sha and builds the
// binary, stamped with the commit's version, into a folder the child can
// write only while it builds. Nothing of the side's own code has run yet.
func build(cfg Config, c *Child, sha string) error {
	if sha == "" {
		return fmt.Errorf("no commit given")
	}
	if err := checkout(cfg, c, sha); err != nil {
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
		return fmt.Errorf("go build: %s", tail(out, 600))
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
	cmd.Env = append(cleanEnv(), "HOME="+filepath.Join(dir, "home"), "GOCACHE="+filepath.Join(dir, "gocache"), "GOROOT="+c.Go.Root, "GOMODCACHE="+c.Go.ModCache,
		"GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local", "GOVCS=*:off", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod download: %v: %s", err, tail(string(out), 600))
	}
	return nil
}

// unitTest runs vet and the short tests in the side's sandbox, its checkout
// and binary read-only. The side's own code (its tests, its init functions)
// runs only like this, with the child's empty HOME and no host login; never
// through gitRun.
func unitTest(c *Child) error {
	defer c.with(offline...)()
	if out, err := c.Exec(30*time.Minute, c.Src, "go", "vet", "./..."); err != nil {
		return fmt.Errorf("go vet: %s", tail(out, 600))
	}
	if out, err := c.Exec(30*time.Minute, c.Src, "go", "test", "-short", "./..."); err != nil {
		return fmt.Errorf("go test -short: %s", tail(out, 600))
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

// checkout clones the repository clean into c.Src at exactly sha.
func checkout(cfg Config, c *Child, sha string) error {
	c.Src = filepath.Join(cfg.Out, "src-"+c.Name)
	run := func(dir, name string, args ...string) (string, error) { return gitRun(dir, name, args...) }
	if out, err := run("", "git", "clone", "-q", "--no-checkout", cfg.Repo, c.Src); err != nil {
		return fmt.Errorf("clone: %v: %s", err, tail(out, 300))
	}
	if out, err := run(c.Src, "git", "checkout", "-q", "--detach", sha); err != nil {
		return fmt.Errorf("checkout %s: %v: %s", short(sha), err, tail(out, 300))
	}
	got, _ := run(c.Src, "git", "rev-parse", "HEAD")
	if got = strings.TrimSpace(got); !strings.HasPrefix(got, sha) && !strings.HasPrefix(sha, got) {
		return fmt.Errorf("checked out %s, not %s", short(got), short(sha))
	}
	if st, _ := run(c.Src, "git", "status", "--porcelain"); strings.TrimSpace(st) != "" {
		return fmt.Errorf("checkout is not clean: %s", tail(st, 200))
	}
	return nil
}

// write saves the report as JSON and as a page, and adds a line to the log of
// gate runs beside it.
func write(cfg Config, rep Report) {
	raw, _ := json.MarshalIndent(rep, "", "  ")
	os.WriteFile(filepath.Join(cfg.Out, "report.json"), raw, 0o644)
	os.WriteFile(filepath.Join(cfg.Out, "report.md"), []byte(rep.Markdown()), 0o644)
	line, _ := json.Marshal(map[string]any{"id": rep.ID, "head": rep.Head, "base": rep.Base, "keep": rep.Verdict.Keep, "reason": rep.Verdict.Reason, "at": rep.Started})
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(cfg.Out), "gates.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Write(append(line, '\n'))
		f.Close()
	}
}

// Markdown is the report as a page.
func (r Report) Markdown() string {
	var b strings.Builder
	verdict := "ROLLED BACK"
	if r.Verdict.Keep {
		verdict = "KEPT"
	}
	fmt.Fprintf(&b, "# Gate %s: %s\n\n%s\n\n- candidate %s, control %s, sandbox %s, %.0fs\n", r.ID, verdict, r.Verdict.Reason, short(r.Head), short(r.Base), r.Sandbox, r.Seconds)
	for _, g := range r.Verdict.Gains {
		fmt.Fprintf(&b, "- gain: %s\n", g)
	}
	for _, g := range r.Verdict.Regressions {
		fmt.Fprintf(&b, "- regression: %s\n", g)
	}
	table := func(name string, rs []Result) {
		fmt.Fprintf(&b, "\n## %s\n\n| probe | rep | pass | seconds | metrics | detail |\n|---|---|---|---|---|---|\n", name)
		for _, x := range rs {
			var ms []string
			for _, m := range x.Metrics {
				ms = append(ms, fmt.Sprintf("%s=%.4g", m.Name, m.Value))
			}
			fmt.Fprintf(&b, "| %s | %d | %v | %.1f | %s | %s |\n", x.Probe, x.Rep, x.Pass, x.Seconds, strings.Join(ms, " "), strings.ReplaceAll(tail(x.Detail, 120), "\n", " "))
		}
	}
	table("Candidate", r.Candidate)
	table("Control", r.Control)
	fmt.Fprintf(&b, "\nOrg repositories made: %d, removed: %d, left: %d\n", len(r.Repos), len(r.Cleaned), len(r.Leaked))
	for _, l := range r.Leaked {
		fmt.Fprintf(&b, "- LEFT BEHIND: %s\n", l)
	}
	return b.String()
}

// noteLesson records a rollback where the next worker reads it.
func noteLesson(cfg Config, rep Report) {
	if cfg.Lessons == "" {
		return
	}
	os.MkdirAll(filepath.Dir(cfg.Lessons), 0o755)
	raw, _ := os.ReadFile(cfg.Lessons)
	s := string(raw)
	if strings.TrimSpace(s) == "" {
		s = "# Lessons\n\nWhat was tried and why it didn't stay. Read before working here.\n\n"
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	// Text from the candidate's own output never goes here: a lesson is read by
	// the next worker as instructions.
	reason := rep.Verdict.Reason
	if rep.Error != "" {
		reason = "the gate could not finish (see report.json of the run)"
	}
	s += fmt.Sprintf("- The gate rolled back %s (run %s): %s\n", short(rep.Head), rep.ID, reason)
	os.WriteFile(cfg.Lessons, []byte(s), 0o644)
}
