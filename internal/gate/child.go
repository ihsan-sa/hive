package gate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Org makes and removes the fresh test repository a child works on. The
// token behind it is the org's only; the gate never hands a child the login
// of the repository being gated.
type Org interface {
	// Create makes an empty repository and returns where a child clones it.
	Create(name string) (url string, err error)
	Delete(name string) error
}

// GitHub is the Hive-RSI-Lab organisation (or whichever Owner names), reached
// with gh and Token.
type GitHub struct{ Owner, Token string }

func (g GitHub) gh(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Env = append(cleanEnv(), "GH_TOKEN="+g.Token, "GH_PROMPT_DISABLED=1")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (g GitHub) Create(name string) (string, error) {
	if out, err := g.gh("repo", "create", g.Owner+"/"+name, "--private", "--description", "Hive gate test repository; deleted when the run ends"); err != nil {
		return "", fmt.Errorf("create %s/%s: %v: %s", g.Owner, name, err, out)
	}
	return "https://github.com/" + g.Owner + "/" + name + ".git", nil
}

func (g GitHub) Delete(name string) error {
	if out, err := g.gh("repo", "delete", g.Owner+"/"+name, "--yes"); err != nil {
		return fmt.Errorf("delete %s/%s: %v: %s", g.Owner, name, err, out)
	}
	return nil
}

// Local keeps test repositories as bare repositories under Dir: the same
// gate with no network, for tests and dry runs.
type Local struct{ Dir string }

func (l Local) Create(name string) (string, error) {
	p := filepath.Join(l.Dir, name+".git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", p).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git init: %v: %s", err, out)
	}
	return p, nil
}

func (l Local) Delete(name string) error { return os.RemoveAll(filepath.Join(l.Dir, name+".git")) }

// passEnv is all a child inherits from the gate: how to find tools and the
// proxy for outbound traffic, nothing that is a login.
var passEnv = []string{"PATH", "LANG", "TZ", "TMPDIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
	"GOMODCACHE", "GOCACHE", "GOPATH", "GOFLAGS", "GOPROXY", "GOTOOLCHAIN", "GOROOT", "SSL_CERT_FILE"}

// cleanEnv is the whitelist of the gate's own environment.
func cleanEnv() []string {
	var env []string
	for _, k := range passEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// Child is one Hive being tried: a binary, the checkout it was built from, a
// root that holds its home, workspace and forge, and the sandbox it runs in.
type Child struct {
	Name    string // "candidate" or "control"
	Src     string // the checkout the binary was built from
	Eval    string // the evaluator: the checkout whose tests and benchmark judge the binary, the same for both sides
	Bin     string
	Root    string // everything the child writes is under here
	Repo    string // the child's org test repository, if the run has an org
	RepoURL string
	Token   string // the org token, the only credential the child holds
	Sandbox string // "bwrap" or "bare"
	Depth   int    // how many more levels the child may gate
	Extra   []string
}

// Home is the child's HOME; Workspace is its HIVE_HOME.
func (c *Child) Home() string      { return filepath.Join(c.Root, "home") }
func (c *Child) Workspace() string { return filepath.Join(c.Root, "hive") }

// Env is the child's whole environment.
func (c *Child) Env() []string {
	env := append(cleanEnv(), "HOME="+c.Home(), "HIVE_HOME="+c.Workspace(), "HIVE_GATE_DEPTH="+fmt.Sprint(c.Depth),
		"GIT_AUTHOR_NAME=Hive gate", "GIT_AUTHOR_EMAIL=gate@localhost", "GIT_COMMITTER_NAME=Hive gate", "GIT_COMMITTER_EMAIL=gate@localhost",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if c.Token != "" {
		env = append(env, "GH_TOKEN="+c.Token, "GITHUB_TOKEN="+c.Token)
	}
	return append(env, c.Extra...)
}

// Prepare makes the child's empty home and workspace.
func (c *Child) Prepare() error {
	for _, d := range []string{c.Home(), c.Workspace()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// bwrapWorks is whether bubblewrap can make the namespaces here.
func bwrapWorks() bool {
	p, err := exec.LookPath("bwrap")
	if err != nil {
		return false
	}
	return exec.Command(p, "--ro-bind", "/", "/", "--unshare-pid", "true").Run() == nil
}

// PickSandbox says how children run on this machine.
func PickSandbox() string {
	if bwrapWorks() {
		return "bwrap"
	}
	return "bare"
}

// Command wraps argv for the child: under bubblewrap the machine is read-only,
// the real home is hidden, and only the child's root, its checkout (to build
// and test) and the Go caches are writable; bare, only the environment is the
// child's.
func (c *Child) Command(ctx context.Context, dir string, argv ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if c.Sandbox == "bwrap" {
		args := []string{"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--tmpfs", "/root",
			"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--die-with-parent", "--new-session"}
		if h := os.Getenv("HOME"); h != "" && h != c.Home() {
			args = append(args, "--tmpfs", h)
		}
		writable := []string{c.Root, c.Src, c.Eval, os.Getenv("GOCACHE"), os.Getenv("GOMODCACHE")}
		seen := map[string]bool{}
		for _, w := range writable {
			if w != "" && !seen[w] {
				if _, err := os.Stat(w); err == nil {
					args = append(args, "--bind", w, w)
					seen[w] = true
				}
			}
		}
		args = append(args, "--chdir", dir, "--")
		cmd = exec.CommandContext(ctx, "bwrap", append(args, argv...)...)
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
	}
	cmd.Env = c.Env()
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

// Exec runs argv in the child and returns its combined output.
func (c *Child) Exec(timeout time.Duration, dir string, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var buf bytes.Buffer
	cmd := c.Command(ctx, dir, argv...)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	return buf.String(), err
}

// tail is the last n runes of s, for a result's detail.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}
