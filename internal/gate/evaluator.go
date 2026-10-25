package gate

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Evaluator is the checkout whose e2e suite and benchmark judge both sides,
// and its tests compiled once, by the gate, with caches of their own: no
// side's code has touched what grades it.
type Evaluator struct {
	SHA  string
	Src  string
	Test string // the compiled internal/e2e test binary
}

// buildEvaluator checks the evaluator out and compiles its e2e tests here.
// It is the current Hive's own code (the base, by default), which the gate
// trusts as it trusts itself; the candidate never runs in this step.
func buildEvaluator(cfg Config, g Go, sha string) (*Evaluator, error) {
	c := &Child{Name: "evaluator"}
	if err := checkout(cfg, c, sha, ""); err != nil {
		return nil, err
	}
	// The suite must know to run the binary in a sandbox of its own (the
	// daemon there sees another pid). An older one cannot judge a sandboxed side.
	raw, _ := os.ReadFile(filepath.Join(c.Src, "internal", "e2e", "e2e_test.go"))
	if !strings.Contains(string(raw), "HIVE_E2E_WRAPPED") {
		return nil, fmt.Errorf("the e2e suite at %s predates the sandboxed binary (HIVE_E2E_WRAPPED); pass --eval with a commit that has it", short(sha))
	}
	dir := filepath.Join(cfg.Out, "evaluator")
	for _, d := range []string{"home", "gocache", "tmp", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	ev := &Evaluator{SHA: sha, Src: c.Src, Test: filepath.Join(dir, "bin", "e2e.test")}
	cmd := exec.Command("go", "test", "-c", "-o", ev.Test, "./internal/e2e/")
	cmd.Dir = c.Src
	cmd.Env = append(cleanEnv(), "HOME="+filepath.Join(dir, "home"), "GOCACHE="+goCache("", filepath.Join(dir, "gocache")), "TMPDIR="+filepath.Join(dir, "tmp"),
		"GOROOT="+g.Root, "GOMODCACHE="+g.ModCache, "GOFLAGS=-mod=readonly", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compiling its e2e tests: %v: %s", err, tail(string(out), 600))
	}
	return ev, nil
}

// judge runs the evaluator's e2e test binary against c's binary, inside c's
// own sandbox: no sandbox is started from inside another, and the suite has
// to reach c's daemon on c's own network. What keeps c from bending the
// verdict there:
//   - The suite was compiled by the gate before c's code ran, and is
//     read-only to c, as is the evaluator's checkout.
//   - Every process of c's descends from the suite, and Yama (ptrace_scope
//     1 or more, required) lets no process trace or read the memory of an
//     ancestor; so c cannot reach into the suite.
//   - The suite's exit code goes to the gate through processes c cannot
//     touch, and the benchmark's line is signed with a key the suite reads
//     from its stdin before any of c's processes exist, so a line c writes
//     into the suite's output is refused.
//   - Code c landed runs in a sealed sandbox the gate starts beside c's on
//     request (HIVE_E2E_CHECK, a Launcher): no network, nothing of c.
func (c *Child) judge(cfg *Config, timeout time.Duration, env []string, key string, args ...string) (string, error) {
	if c.Sandbox == "bwrap" {
		if err := yamaGuards(); err != nil {
			return "", err
		}
	}
	scratch := filepath.Join(c.Root, "evaluator-tmp")
	os.RemoveAll(scratch)
	evHome := filepath.Join(c.Root, "evaluator-home")
	for _, d := range []string{scratch, evHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}
	extra := []string{"TMPDIR=" + scratch, "HOME=" + evHome, "HIVE_E2E_BIN=" + c.Bin, "HIVE_E2E_WRAPPED=1"}
	ro := []string{c.Eval.Test, c.Eval.Src}
	var rw []string
	if c.Sandbox == "bwrap" {
		// Code c landed runs sealed, in a sandbox the gate starts beside c's.
		dir, err := os.MkdirTemp(shortTmp(), "hive-check-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		check := &Child{Name: "check-" + c.Name, Root: filepath.Join(cfg.Out, "check-"+c.Name), Sandbox: c.Sandbox}
		if err := check.Prepare(); err != nil {
			return "", err
		}
		l, err := StartLauncher(filepath.Join(dir, "check.sock"), check)
		if err != nil {
			return "", err
		}
		defer l.Close()
		script := filepath.Join(dir, "check")
		if err := checkScript(script, c.fwd(), l.Sock); err != nil {
			return "", err
		}
		extra = append(extra, "HIVE_E2E_CHECK="+script)
		ro = append(ro, script, c.fwd())
		rw = append(rw, l.Sock)
	}
	saved := *c
	defer func() { c.RO, c.RW, c.Extra = saved.RO, saved.RW, saved.Extra }()
	c.RO = append(append([]string{}, c.RO...), ro...)
	c.RW = append(append([]string{}, c.RW...), rw...)
	c.Extra = append(append(append([]string{}, c.Extra...), extra...), env...)
	var stdin io.Reader
	if key != "" {
		c.Extra = append(c.Extra, "HIVE_BENCH_KEY=stdin")
		stdin = strings.NewReader(key + "\n")
	}
	argv := append([]string{c.Eval.Test, "-test.count=1", "-test.timeout", (timeout - time.Minute).String()}, args...)
	return c.ExecIn(timeout, filepath.Join(c.Eval.Src, "internal", "e2e"), stdin, argv...)
}

// yamaGuards refuses to share a sandbox between a side and its evaluator
// unless Yama keeps a process from tracing its ancestors.
func yamaGuards() error {
	raw, err := os.ReadFile("/proc/sys/kernel/yama/ptrace_scope")
	if err != nil || strings.TrimSpace(string(raw)) == "0" {
		return fmt.Errorf("the evaluator shares the side's sandbox only where Yama's ptrace_scope is 1 or more (here: %q)", strings.TrimSpace(string(raw)))
	}
	return nil
}
