package gate

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	p, err := filepath.Abs(filepath.Join(l.Dir, name+".git")) // bound into a sandbox at this path
	if err != nil {
		return "", err
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", p).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git init: %v: %s", err, out)
	}
	return p, nil
}

func (l Local) Delete(name string) error { return os.RemoveAll(filepath.Join(l.Dir, name+".git")) }

// passEnv is all a child inherits from the gate: the proxy for outbound
// traffic and how Go fetches modules, nothing that is a login. PATH, HOME,
// TMPDIR and the Go caches are the child's own (see Env).
var passEnv = []string{"PATH", "LANG", "TZ", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
	"GOFLAGS", "GOPROXY", "GONOSUMDB", "GOPRIVATE", "GOTOOLCHAIN", "SSL_CERT_FILE", "SSL_CERT_DIR"}

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

// Go is where the gate's Go toolchain and module cache are. Children get both
// by path, read-only (they are often under the person's home, which a child
// never sees).
type Go struct{ Root, ModCache string }

// FindGo asks the go on PATH.
func FindGo() (Go, error) {
	out, err := exec.Command("go", "env", "GOROOT", "GOMODCACHE").Output()
	if err != nil {
		return Go{}, fmt.Errorf("no Go toolchain: %v", err)
	}
	f := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(f) != 2 || f[0] == "" || f[1] == "" {
		return Go{}, fmt.Errorf("go env gave %q", out)
	}
	return Go{Root: strings.TrimSpace(f[0]), ModCache: strings.TrimSpace(f[1])}, nil
}

// Child is one Hive being tried, or the evaluator judging one: a binary, the
// checkout it was built from, a root that holds its home, workspace and
// forge, and the sandbox it runs in.
type Child struct {
	Name     string // "candidate", "control", or "evaluator-<side>"
	Src      string // the checkout the binary was built from (read-only to the child)
	Bin      string // the binary; read-only to the child
	BinDir   string // where the build writes Bin; writable only while Building
	Root     string // everything the child writes is under here
	Repo     string // the child's org test repository, if the run has an org
	RepoURL  string
	Token    string // the contents-only org token, the only credential the child holds (never one that can make or delete repositories)
	Sandbox  string // "bwrap" or "bare"
	Depth    int    // how many more levels the child may gate
	Go       Go
	Eval     *Evaluator // judges this child; nil for prebuilt binaries
	Building bool       // BinDir is writable only while the child builds
	RO, RW   []string   // more paths the child sees, read-only and writable
	Extra    []string   // more environment
	Meter    *Meter     // the side's only way out, under bubblewrap; nil keeps the machine's network
	Fwd      string     // the gate's own binary, which runs the forwarder (default this executable)
}

// Home is the child's HOME; Workspace is its HIVE_HOME; Tmp its TMPDIR.
func (c *Child) Home() string      { return filepath.Join(c.Root, "home") }
func (c *Child) Workspace() string { return filepath.Join(c.Root, "hive") }
func (c *Child) Tmp() string       { return filepath.Join(c.Root, "tmp") }

// Env is the child's whole environment.
func (c *Child) Env() []string {
	env := cleanEnv()
	path := os.Getenv("PATH")
	if c.Go.Root != "" {
		path = filepath.Join(c.Go.Root, "bin") + string(os.PathListSeparator) + path
	}
	root, _ := filepath.Abs(c.Root)
	env = append(env, rootEnv+"="+root, ownerEnv+"="+owner)
	env = append(env, "PATH="+path, "HOME="+c.Home(), "TMPDIR="+c.Tmp(), "HIVE_HOME="+c.Workspace(), "HIVE_GATE_DEPTH="+fmt.Sprint(c.Depth),
		"GOCACHE="+filepath.Join(c.Root, "gocache"), "GOPATH="+filepath.Join(c.Root, "gopath"),
		"GIT_AUTHOR_NAME=Hive gate", "GIT_AUTHOR_EMAIL=gate@localhost", "GIT_COMMITTER_NAME=Hive gate", "GIT_COMMITTER_EMAIL=gate@localhost",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if c.Go.Root != "" {
		env = append(env, "GOROOT="+c.Go.Root, "GOMODCACHE="+c.Go.ModCache)
	}
	if c.Meter != nil {
		env = append(env, c.Meter.Env(c.Sandbox == "bwrap")...)
	}
	if c.Token != "" {
		env = append(env, "GH_TOKEN="+c.Token, "GITHUB_TOKEN="+c.Token)
		// git reads its auth header from the environment, so the token is in no argv.
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+basic(c.Token))
	}
	return append(env, c.Extra...)
}

// Prepare makes the child's empty home, workspace and caches.
func (c *Child) Prepare() error {
	for _, d := range []string{c.Home(), c.Workspace(), c.Tmp(), filepath.Join(c.Root, "gocache"), filepath.Join(c.Root, "gopath")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// BwrapWorks is whether bubblewrap can make the namespaces here. Inside one
// of the gate's sandboxes it is never tried: no sandbox starts another.
func BwrapWorks() bool {
	if insideSandbox() {
		return false
	}
	p, err := exec.LookPath("bwrap")
	if err != nil {
		return false
	}
	return exec.Command(p, "--ro-bind", "/", "/", "--unshare-pid", "true").Run() == nil
}

// systemDirs are what a child sees of the machine, read-only: programs,
// libraries and /etc. Nothing else is there unless named: not the person's
// home, not a token file kept elsewhere, not another workspace (and so not
// its daemon's token), not /run (the user bus, systemd --user) beyond DNS.
var systemDirs = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/etc", "/nix", "/run/systemd/resolve"}

// bwrapArgs is the sandbox for the child: the system dirs, the Go toolchain
// and module cache, its checkout and binary, read-only; its root writable,
// and its build output while it builds; its own pid, ipc and uts namespaces;
// fresh /dev, /proc and /tmp; its own network, with the meter's forwarder as
// the only way out (or none). Marked as inside, so nothing in it starts
// another sandbox.
func (c *Child) bwrapArgs(dir string) []string {
	args := []string{"--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-net", "--die-with-parent", "--new-session", "--setenv", sandboxedEnv, "1"}
	for _, d := range systemDirs {
		fi, err := os.Lstat(d)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(d)
			args = append(args, "--symlink", target, d)
			continue
		}
		args = append(args, "--ro-bind", d, d)
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp")
	if c.Meter != nil {
		// No network of its own: the meter's forwarder is the way out.
		args = append(args, "--ro-bind", c.fwd(), c.fwd(), "--bind", c.Meter.Sock, c.Meter.Sock)
	}
	ro := []string{c.Go.Root, c.Go.ModCache, c.Src, c.Bin}
	for _, k := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR"} {
		ro = append(ro, os.Getenv(k))
	}
	ro = append(ro, c.RO...)
	for _, p := range ro {
		if p != "" && exists(p) {
			args = append(args, "--ro-bind", p, p)
		}
	}
	rw := append([]string{c.Root}, c.RW...)
	if c.Building {
		rw = append(rw, c.BinDir)
	}
	for _, p := range rw {
		if p != "" && exists(p) {
			args = append(args, "--bind", p, p)
		}
	}
	return append(args, "--chdir", dir, "--")
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Command wraps argv for the child: under bubblewrap, the sandbox of
// bwrapArgs; bare, only the environment is the child's.
func (c *Child) Command(ctx context.Context, dir string, argv ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if c.Sandbox == "bwrap" && insideSandbox() {
		// Never a sandbox inside a sandbox (pickSandbox refuses it first).
		cmd = exec.CommandContext(ctx, "sh", "-c", "echo 'the gate starts no sandbox inside its own' >&2; exit 125")
	} else if c.Sandbox == "bwrap" {
		cmd = exec.CommandContext(ctx, "bwrap", append(c.bwrapArgs(dir), c.forwarded(argv)...)...)
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
	}
	cmd.Env = c.Env()
	cmd.WaitDelay = 10 * time.Second
	killTree(cmd)
	return cmd
}

func (c *Child) fwd() string {
	if c.Fwd != "" {
		return c.Fwd
	}
	self, _ := os.Executable()
	return self
}

// forwarded puts the meter's forwarder in front of argv when the sandbox has
// a network of its own and a meter.
func (c *Child) forwarded(argv []string) []string {
	if c.Meter == nil {
		return argv
	}
	return append([]string{c.fwd(), "gate-fwd", c.Meter.Sock, "--"}, argv...)
}

// Exec runs argv in the child and returns its combined output.
func (c *Child) Exec(timeout time.Duration, dir string, argv ...string) (string, error) {
	return c.ExecIn(timeout, dir, nil, argv...)
}

// ExecIn is Exec with stdin.
func (c *Child) ExecIn(timeout time.Duration, dir string, stdin io.Reader, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	buf := &lastBytes{max: 4 << 20} // a child cannot fill the gate's memory
	cmd := c.Command(ctx, dir, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, buf, buf
	err := cmd.Run()
	stopTree(cmd) // what it left running in its group
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	return buf.String(), err
}

// lastBytes keeps the last max bytes written to it.
type lastBytes struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func (l *lastBytes) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	if len(l.b) > 2*l.max {
		l.b = append([]byte(nil), l.b[len(l.b)-l.max:]...)
	}
	return len(p), nil
}

func (l *lastBytes) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.b) > l.max {
		return string(l.b[len(l.b)-l.max:])
	}
	return string(l.b)
}

// tail is the last n runes of s, for a result's detail.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}
