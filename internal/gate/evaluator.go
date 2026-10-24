package gate

import (
	"fmt"
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
	if err := checkout(cfg, c, sha); err != nil {
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
	cmd.Env = append(cleanEnv(), "HOME="+filepath.Join(dir, "home"), "GOCACHE="+filepath.Join(dir, "gocache"), "TMPDIR="+filepath.Join(dir, "tmp"),
		"GOROOT="+g.Root, "GOMODCACHE="+g.ModCache, "GOFLAGS=-mod=readonly", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compiling its e2e tests: %v: %s", err, tail(string(out), 600))
	}
	return ev, nil
}

// judge runs the evaluator's e2e test binary against c's binary. The
// evaluator has a sandbox of its own; c's binary, and any code c's Hive
// landed that the evaluator runs, go through wrappers into c's sandbox, a
// pid namespace apart from the evaluator. They share only c's root, where the
// evaluator keeps the workspaces and forges it hands c.
func (c *Child) judge(cfg *Config, timeout time.Duration, env []string, args ...string) (string, error) {
	wrap := filepath.Join(cfg.Out, "wrap-"+c.Name)
	// Inside the evaluator's sandbox the side shares the evaluator's network
	// (so the suite reaches the side's daemon), which has no way out but the
	// side's meter.
	side := *c
	side.Net = "shared"
	if err := side.Wrapper(filepath.Join(wrap, "hive"), c.Bin); err != nil {
		return "", err
	}
	if err := side.Wrapper(filepath.Join(wrap, "run"), ""); err != nil {
		return "", err
	}
	// Code the side landed is run sealed: the machine's programs, an empty
	// root of its own, no network, nothing of either Hive.
	check := &Child{Name: "check-" + c.Name, Root: filepath.Join(cfg.Out, "check-"+c.Name), Sandbox: c.Sandbox}
	if err := check.Prepare(); err != nil {
		return "", err
	}
	if err := check.Wrapper(filepath.Join(wrap, "check"), ""); err != nil {
		return "", err
	}
	scratch := filepath.Join(c.Root, "evaluator-tmp")
	os.RemoveAll(scratch)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", err
	}
	ev := &Child{Name: "evaluator-" + c.Name, Root: filepath.Join(cfg.Out, "evaluator-"+c.Name), Sandbox: c.Sandbox, Go: c.Go, Meter: c.Meter, Fwd: c.Fwd,
		RO: append([]string{c.Eval.Test, c.Eval.Src, wrap, c.Src, c.Bin, check.Root}, c.RO...), RW: append([]string{c.Root}, c.RW...)}
	if err := ev.Prepare(); err != nil {
		return "", err
	}
	ev.Extra = append([]string{"TMPDIR=" + scratch, "HIVE_E2E_BIN=" + filepath.Join(wrap, "hive"), "HIVE_E2E_RUN=" + filepath.Join(wrap, "run"), "HIVE_E2E_CHECK=" + filepath.Join(wrap, "check"), "HIVE_E2E_WRAPPED=1"}, env...)
	argv := append([]string{c.Eval.Test, "-test.count=1", "-test.timeout", (timeout - time.Minute).String()}, args...)
	return ev.Exec(timeout, filepath.Join(c.Eval.Src, "internal", "e2e"), argv...)
}
