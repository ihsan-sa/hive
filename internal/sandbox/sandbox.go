// Package sandbox runs agent and check commands where they can read the
// system, write only their own checkout and a scratch home, and reach the
// network outbound only, with no way to the machine's local services.
//
// Linux uses user/mount/pid/net namespaces built by the hive binary itself
// (no root, no extra packages), or bubblewrap where Ubuntu restricts
// unprivileged user namespaces; Windows runs that inside WSL; macOS uses a
// Seatbelt profile. Bare runs are a documented, opted-in state, never a
// silent fallback.
package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// Mount exposes a host path inside the sandbox.
type Mount struct {
	Host   string `json:"host"`
	Inside string `json:"inside"`
	RW     bool   `json:"rw"`
}

// Spec is one sandboxed command.
type Spec struct {
	ID       string   `json:"id"`
	Argv     []string `json:"argv"` // program resolved inside (Tools provide extra programs)
	Env      []string `json:"env"`  // complete environment inside
	Cwd      string   `json:"cwd"`  // inside path
	Mounts   []Mount  `json:"mounts"`
	Tools    []string `json:"tools"`     // host programs to expose at /hive/bin (e.g. claude, hive)
	Stdin    string   `json:"stdin"`     // host path
	Stdout   string   `json:"stdout"`    // host path, appended
	StopFile string   `json:"stop_file"` // host path; when present the command is killed
	Network  bool     `json:"network"`   // outbound through the egress proxy
	Allow    []string `json:"allow"`     // hosts the proxy lets through; empty is every public host
	UID      int      `json:"uid"`
	GID      int      `json:"gid"`
}

// Proc identifies a started, detached command.
type Proc = proc.Handle

// Sandbox starts specs.
type Sandbox interface {
	Name() string
	// Isolated reports whether inside paths differ from host paths.
	Isolated() bool
	// Check returns nil when the sandbox can run on this machine.
	Check() error
	// Start launches spec detached from the caller, so it survives a daemon restart.
	Start(spec Spec) (Proc, error)
	// Run runs spec to completion and returns its exit code (for checks).
	Run(spec Spec, timeout time.Duration) (int, error)
}

// InsidePath returns the path a mount has inside sb.
func InsidePath(sb Sandbox, m Mount) string {
	if sb.Isolated() {
		return m.Inside
	}
	return m.Host
}

// ErrNoSandbox means agents may not start until the person opts into bare runs.
var ErrNoSandbox = errors.New("no sandbox available on this machine")

// BareWarning is shown before the person can opt into bare runs.
const BareWarning = `Agents would run without a sandbox. An unsandboxed agent acts on everything
your logins and files reach: it can read your credentials, change any file you
can, and reach every service on this machine and network.`

// Options selects the sandbox.
type Options struct {
	Mode      string // "auto" or "bare"
	WSLDistro string
	HiveLinux string // path to the linux hive binary (Windows only)
}

// Select returns the platform sandbox, or Bare when the person opted in.
func Select(o Options) (Sandbox, error) {
	if o.Mode == "bare" {
		return Bare{}, nil
	}
	sb := platform(o)
	if sb == nil {
		return nil, fmt.Errorf("%w (%s)", ErrNoSandbox, runtime.GOOS)
	}
	if err := sb.Check(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSandbox, err)
	}
	return sb, nil
}

// Bare runs commands directly on the host. Only used after an explicit opt-in.
type Bare struct{}

func (Bare) Name() string   { return "bare" }
func (Bare) Isolated() bool { return false }
func (Bare) Check() error   { return nil }

func (Bare) Start(s Spec) (Proc, error) {
	env := append(os.Environ(), s.Env...)
	env = withPathPrefix(env, toolDirs(s.Tools))
	return proc.StartDetached(s.Argv, env, s.Cwd, s.Stdin, s.Stdout)
}

func toolDirs(tools []string) []string {
	var dirs []string
	for _, t := range tools {
		if filepath.IsAbs(t) {
			dirs = append(dirs, filepath.Dir(t))
		}
	}
	return dirs
}

func withPathPrefix(env []string, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}
	key := "PATH"
	for i, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, key) {
			env[i] = k + "=" + strings.Join(dirs, string(os.PathListSeparator)) + string(os.PathListSeparator) + v
			return env
		}
	}
	return append(env, key+"="+strings.Join(dirs, string(os.PathListSeparator)))
}

// WriteSpec stores a spec as JSON for the sandbox launcher.
func WriteSpec(path string, s Spec) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// ReadSpec loads a spec.
func ReadSpec(path string) (Spec, error) {
	var s Spec
	raw, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(raw, &s)
}

// InsideEnv is the standard environment inside the sandbox.
func InsideEnv(home string, network bool) []string {
	env := []string{
		"HOME=" + home,
		"PATH=/hive/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG=C.UTF-8",
		"TERM=dumb",
		"TMPDIR=/tmp",
		"GIT_CONFIG_NOSYSTEM=1",
	}
	if network {
		p := "http://127.0.0.1:" + ProxyPort
		env = append(env, "HTTPS_PROXY="+p, "HTTP_PROXY="+p, "https_proxy="+p, "http_proxy="+p, "ALL_PROXY="+p, "NO_PROXY=", "no_proxy=")
	}
	return env
}

// ProxyPort is where the egress forwarder listens inside the sandbox.
const ProxyPort = "3128"
