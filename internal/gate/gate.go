package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config is one gate run.
type Config struct {
	Repo      string   // git repository (path or URL) of the Hive's source
	Head      string   // the candidate's exact commit
	Base      string   // the current Hive's commit: the control
	Eval      string   // the commit whose e2e tests and benchmark judge both sides (default Base); a candidate never grades itself
	CandBin   string   // a prebuilt candidate binary (skips building Head; a self-test)
	CtrlBin   string   // a prebuilt control binary
	Out       string   // where the run's files and report go
	Probes    []string // which probes to run (default all)
	Reps      int      // repeats of every probe on each side, interleaved (default 3)
	Bench     int      // asks in the benchmark (default 4, the whole set)
	Org       Org      // the fresh test repository's host; nil runs without one
	OrgToken  string   // the org token the children hold
	Lessons   string   // .hive/lessons.md to append to on a rollback
	Depth     int      // levels of gating the children may still do (capped at MaxDepth)
	MaxRepos  int      // most org repositories one run may make (default 4)
	Timeout   time.Duration
	Options   Options
	Sandbox   string // "", "bwrap" or "bare"; "" picks
	Log       func(string)
	Probe     map[string]ProbeFunc // extra probes, by name
	KeepFiles bool
}

// MaxDepth is how many levels deep gates may nest; a child gating a
// grandchild is depth 1.
const MaxDepth = 2

// Report is the record of a run.
type Report struct {
	ID        string    `json:"id"`
	Head      string    `json:"head"`
	Base      string    `json:"base"`
	Sandbox   string    `json:"sandbox"`
	Started   time.Time `json:"started"`
	Seconds   float64   `json:"seconds"`
	Candidate []Result  `json:"candidate"`
	Control   []Result  `json:"control"`
	Verdict   Verdict   `json:"verdict"`
	Repos     []string  `json:"repos,omitempty"`   // org repositories made
	Cleaned   []string  `json:"cleaned,omitempty"` // and removed
	Leaked    []string  `json:"leaked,omitempty"`  // made and not removed
	Error     string    `json:"error,omitempty"`
}

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
	if cfg.Sandbox == "" {
		cfg.Sandbox = PickSandbox()
	}
	if len(cfg.Probes) == 0 {
		cfg.Probes = DefaultProbes
	}
	// Children get the Go caches by path, since their HOME is empty.
	for _, k := range []string{"GOCACHE", "GOMODCACHE"} {
		if os.Getenv(k) == "" {
			if out, err := exec.Command("go", "env", k).Output(); err == nil {
				os.Setenv(k, strings.TrimSpace(string(out)))
			}
		}
	}
	rep = Report{ID: fmt.Sprintf("gate-%d", t0.Unix()), Head: cfg.Head, Base: cfg.Base, Sandbox: cfg.Sandbox, Started: t0.UTC()}
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
		write(cfg, rep)
		if !rep.Verdict.Keep {
			noteLesson(cfg, rep)
		}
	}()

	cand := &Child{Name: "candidate", Root: filepath.Join(cfg.Out, "candidate"), Sandbox: cfg.Sandbox, Depth: cfg.Depth, Bin: cfg.CandBin}
	ctrl := &Child{Name: "control", Root: filepath.Join(cfg.Out, "control"), Sandbox: cfg.Sandbox, Depth: cfg.Depth, Bin: cfg.CtrlBin}
	for _, c := range []*Child{cand, ctrl} {
		sha := cfg.Head
		if c == ctrl {
			sha = cfg.Base
		}
		if c.Bin == "" {
			cfg.log("building %s at %s", c.Name, short(sha))
			if err = build(cfg, c, sha); err != nil {
				return rep, fmt.Errorf("%s does not build: %v", c.Name, err)
			}
		}
		if err = c.Prepare(); err != nil {
			return rep, err
		}
	}

	// Both sides are judged by one evaluator, taken from Eval (the base by
	// default), so a candidate cannot change the tests or the benchmark that
	// grade it.
	if cand.Src != "" || ctrl.Src != "" {
		evalSHA := cfg.Eval
		if evalSHA == "" {
			evalSHA = cfg.Base
		}
		eval := &Child{Name: "evaluator"}
		cfg.log("fetching the evaluator at %s", short(evalSHA))
		if err = checkout(cfg, eval, evalSHA); err != nil {
			return rep, fmt.Errorf("evaluator: %v", err)
		}
		cand.Eval, ctrl.Eval = eval.Src, eval.Src
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
			c.Repo = fmt.Sprintf("%s-%s", rep.ID, c.Name)
			rep.Repos = append(rep.Repos, c.Repo)
			if c.RepoURL, err = cfg.Org.Create(c.Repo); err != nil {
				return rep, err
			}
			c.Token = cfg.OrgToken
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
				start := time.Now()
				r := p(&cfg, side.c)
				r.Probe, r.Rep, r.Seconds = name, rep0+1, time.Since(start).Seconds()
				*side.out = append(*side.out, r)
				cfg.log("  %s %s: pass=%v %s", side.c.Name, name, r.Pass, r.Detail)
			}
		}
	}
	rep.Verdict = Decide(rep.Candidate, rep.Control, cfg.Options)
	return rep, nil
}

func short(s string) string {
	if len(s) > 9 {
		return s[:9]
	}
	return s
}

// build clones the repository clean, checks out exactly sha, runs vet and the
// short tests there, and builds the binary the child will install.
func build(cfg Config, c *Child, sha string) error {
	if sha == "" {
		return fmt.Errorf("no commit given")
	}
	if err := checkout(cfg, c, sha); err != nil {
		return err
	}
	// The candidate's own code (its tests, its init functions) runs only in the
	// child's sandbox, with the child's empty HOME and no host login; the Go
	// caches reach it by path. Never run it through gitRun.
	if err := c.Prepare(); err != nil {
		return err
	}
	run := func(dir string, argv ...string) (string, error) { return c.Exec(30*time.Minute, dir, argv...) }
	if out, err := run(c.Src, "go", "vet", "./..."); err != nil {
		return fmt.Errorf("go vet: %s", tail(out, 600))
	}
	if out, err := run(c.Src, "go", "test", "-short", "./..."); err != nil {
		return fmt.Errorf("go test -short: %s", tail(out, 600))
	}
	c.Bin = filepath.Join(c.Root, "bin", "hive")
	os.MkdirAll(filepath.Dir(c.Bin), 0o755)
	if out, err := run(c.Src, "go", "build", "-o", c.Bin, "./cmd/hive"); err != nil {
		return fmt.Errorf("go build: %s", tail(out, 600))
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
	s += fmt.Sprintf("- The gate rolled back %s (run %s): %s\n", short(rep.Head), rep.ID, rep.Verdict.Reason)
	os.WriteFile(cfg.Lessons, []byte(s), 0o644)
}
