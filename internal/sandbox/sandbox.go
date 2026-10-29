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
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// Mount exposes a host path inside the sandbox. A Hide mount does the
// opposite: it covers Inside, a path an earlier mount exposes, so that Host (a
// secret) can't be read there. Bare runs can't hide anything.
type Mount struct {
	Host   string `json:"host"`
	Inside string `json:"inside"`
	RW     bool   `json:"rw"`
	Hide   bool   `json:"hide,omitempty"`
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
	// Tmp, when set, is a host folder on disk the sandbox binds at /tmp,
	// emptied before the command and removed after it: a tmpfs /tmp is
	// memory that can't be reclaimed, only swapped, and go build's work dirs
	// and test binaries go there. Unset, /tmp is a tmpfs capped at TmpfsSize.
	Tmp     string   `json:"tmp,omitempty"`
	Network bool     `json:"network"`           // outbound through the egress proxy
	Allow   []string `json:"allow"`             // hosts the proxy lets through; nil is every public host
	Local   []string `json:"local"`             // services on this machine the person granted, loopback ip:port (LocalGrant's)
	Outside []string `json:"outside,omitempty"` // hosts outside it granted to this run's project, host:port (OutsideGrant's), past Allow
	// Guard is host paths no host interpreter's install may contain, as
	// its bind would expose them: the workspace and the folders covered in it
	// (Workspace.Guarded). Every mount's host path is guarded as well. A spec
	// with no guard binds no host interpreter's install at all.
	Guard []string `json:"guard,omitempty"`
	// Cover is host paths no host interpreter's install may sit inside either:
	// the folders covered wherever agents read .hive (Workspace.Covered). Every
	// hidden mount's host path is covered as well.
	Cover []string `json:"cover,omitempty"`
	UID   int      `json:"uid"`
	GID   int      `json:"gid"`
	// Nice is the Unix nice value the command runs at, 0 the daemon's own;
	// a process can only go higher (lower priority), so it is never raised.
	Nice int `json:"nice,omitempty"`
	// Disk, when set, is where Run says how much the command and what it
	// started and waited for read from and wrote to the disk; it stays zero
	// where the platform doesn't count it (only Linux does).
	Disk *Disk `json:"-"`
}

// Disk is what a command read from and wrote to the disk, in bytes: the
// kernel's block I/O counts for it and every child it waited for (a read
// served from the page cache isn't one).
type Disk struct {
	Read, Wrote int64
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
	env = withPathPrefix(env, pathDirs(s))
	return proc.StartDetached(Niced(s.Argv, s.Nice), env, s.Cwd, s.Stdin, s.Stdout)
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

// ToolsInside is where the Hive's tools folder is mounted: the runtimes and
// dev tools agents install for themselves (`hive agent install`).
const ToolsInside = "/hive/tools"

// ToolNames are the tools the Hive installs. Each lives in <folder>/<name>,
// with its programs in <folder>/<name>/bin.
var ToolNames = []string{"python", "uv", "node", "go"}

// ToolBins are the tools folder's bin dirs, for PATH.
func ToolBins(folder string, join func(...string) string) []string {
	var out []string
	for _, n := range ToolNames {
		out = append(out, join(folder, n, "bin"))
	}
	return out
}

// pathDirs are what a command on the host's own paths (bare, Seatbelt) gets
// on PATH: the bin dirs of a mounted tools folder, then the exposed tools'
// dirs, so an installed runtime wins over an older one beside hive on the host.
// An agent can write to those dirs, so no host-side launcher may look its
// argv[0] up on this PATH: it is resolved first, in the daemon's own
// environment (Niced), or a program planted there would run on the host.
func pathDirs(s Spec) []string {
	var dirs []string
	for _, m := range s.Mounts {
		if m.Inside == ToolsInside {
			dirs = append(dirs, ToolBins(m.Host, filepath.Join)...)
		}
	}
	return append(dirs, toolDirs(s.Tools)...)
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

// writeTempSpec writes a spec for sandbox-exec, which deletes it, to the
// temp dir rather than beside the run's output: it carries a token, and every
// agent can read the runs.
func writeTempSpec(s Spec) (string, error) {
	f, err := os.CreateTemp("", "hive-spec-*.json")
	if err != nil {
		return "", err
	}
	f.Close()
	return f.Name(), WriteSpec(f.Name(), s)
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

// ShmSize caps a sandbox's /dev/shm, and TmpfsSize its /tmp where the spec
// gives no folder on disk for it (a runner's turn, a setup probe), so a full
// one fails the command that filled it rather than the machine's memory.
const (
	ShmSize   = 1 << 30
	TmpfsSize = 4 << 30
)

// InsideEnv is the standard environment inside the sandbox.
func InsideEnv(home string, network bool) []string {
	env := []string{
		"HOME=" + home,
		"PATH=/hive/bin:" + strings.Join(ToolBins(ToolsInside, path.Join), ":") + ":" + insideBins,
		"LANG=C.UTF-8",
		"TERM=dumb",
		"TMPDIR=/tmp",
		"GIT_CONFIG_NOSYSTEM=1",
	}
	if network {
		env = append(env, proxyEnv(ProxyPort)...)
	}
	return env
}

func proxyEnv(port string) []string {
	p := "http://127.0.0.1:" + port
	return []string{"HTTPS_PROXY=" + p, "HTTP_PROXY=" + p, "https_proxy=" + p, "http_proxy=" + p, "ALL_PROXY=" + p, "NO_PROXY=", "no_proxy="}
}

// SeatbeltEnv is the environment Seatbelt gives sp when its egress proxy
// listens on port: sp's own, with the proxy moved to port, and the proxy set
// whenever sp has the network, since Seatbelt lets a spec reach only the
// proxy. It lives here so Linux tests can check it.
func SeatbeltEnv(sp Spec, port string) []string {
	var env []string
	for _, kv := range sp.Env {
		env = append(env, strings.ReplaceAll(kv, "127.0.0.1:"+ProxyPort, "127.0.0.1:"+port))
	}
	if sp.Network {
		env = append(env, proxyEnv(port)...)
	}
	return env
}

// insideBins are the system folders on the sandbox's PATH.
const insideBins = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// ProxyPort is where the egress forwarder listens inside the sandbox.
const ProxyPort = "3128"

// lookTool finds tool t on this machine: an absolute path as it is, a bare
// name on PATH or else in ~/.local/bin or /usr/local/bin, where an installer
// puts it without PATH knowing.
func lookTool(t string) (string, error) {
	if filepath.IsAbs(t) {
		return t, nil
	}
	if p, err := exec.LookPath(t); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
	}
	for _, d := range []string{filepath.Join(os.Getenv("HOME"), ".local", "bin"), "/usr/local/bin"} {
		if _, err := os.Stat(filepath.Join(d, t)); err == nil {
			return filepath.Join(d, t), nil
		}
	}
	return "", fmt.Errorf("tool %q not found on this machine", t)
}

// AbsTools names each of tools by the absolute path this machine finds it
// at, for a spec handed to another program (an executor module): that
// program runs with its own PATH and HOME, so a bare name the daemon finds
// (claude in /hive/bin, say) may be missing there. The base name stays the
// tool's own (links aren't followed), as the name inside and a runner's
// mapping go by it. One in the system's bin dirs stays bare: it is on any
// PATH, and found by name it runs where it is inside, beside its package.
// One not found stays as it was, for the sandbox to report.
func AbsTools(tools []string) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t
		if filepath.IsAbs(t) {
			continue
		}
		if p, err := lookTool(t); err == nil && !strings.Contains(":"+insideBins+":", ":"+filepath.Dir(p)+":") {
			out[i] = p
		}
	}
	return out
}
