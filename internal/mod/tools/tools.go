// Package tools installs the runtimes and dev tools agents need (python, uv,
// node, go) into a folder the Hive owns, with no root. Every agent sandbox
// sees that folder read-only on PATH; installs run in a sandbox of their own
// that may write it, so downloads pass the egress proxy and a Windows Hive
// gets Linux builds for its WSL sandbox. What needs root goes to the person.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Module serves the install op.
type Module struct {
	C  *core.Core
	mu sync.Mutex // one install at a time writes the folder
}

// New registers the install op and the person's answer to a system install.
func New(c *core.Core) *Module {
	m := &Module{C: c}
	c.Runs.Ops().Register(core.Op{Name: "install", Help: `{"tool": "python|uv|node|go", "version": "(optional) e.g. 3.12"} -> installed for every agent, no root, on PATH at once; or {"system": "the exact command, e.g. sudo apt-get install -y libpq-dev", "why": "..."} -> goes to the person`, Fn: m.opInstall})
	c.Person.Handle("install", m.onAnswer)
	return m
}

func (m *Module) opInstall(r core.Run, a json.RawMessage) (any, error) {
	var req struct{ Tool, Version, System, Why string }
	if err := core.Bind(a, &req); err != nil {
		return nil, err
	}
	if cmd := strings.TrimSpace(req.System); cmd != "" {
		m.C.Person.Ask(core.Ask{Kind: "install", Ref: r.ID + " " + cmd, From: r.ID, Project: r.Project, Options: []string{"done", "decline"},
			Title: "An agent needs a system install (root): " + core.Clip(cmd, 100), Detail: cmd + "\n\n" + req.Why})
		return "sent to the person with the exact command; their answer comes back to you as a message", nil
	}
	v, err := m.Install(req.Tool, req.Version, m.C.WS.LoadConfig().Egress.For(r.Role))
	if err != nil {
		return nil, err
	}
	m.C.Person.Ask(core.Ask{Kind: "told", Ref: r.ID + " " + req.Tool + " " + v, From: r.ID, Project: r.Project, Options: []string{"seen"},
		Title: fmt.Sprintf("%s installed %s %s for the Hive's agents", r.Name, req.Tool, v)})
	return fmt.Sprintf("%s %s is installed and on your PATH now", req.Tool, v), nil
}

func (m *Module) onAnswer(a core.Ask) {
	m.C.Runs.Send(a.From, fmt.Sprintf("[install] the person answered %q to: %s %s", a.Answer, a.Detail, a.Note))
}

// Install runs `hive tools-fetch` in a sandbox that may write the tools folder,
// its downloads held to allow (the asking run's egress list; nil is every public host).
func (m *Module) Install(tool, version string, allow []string) (string, error) {
	if err := checkInput(tool, version); err != nil {
		return "", err
	}
	sb, err := m.C.Sandbox()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	home, err := os.MkdirTemp("", "hive-tools-home-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(home)
	logs := filepath.Join(m.C.WS.Hive(), "checks")
	os.MkdirAll(logs, 0o755)
	log := filepath.Join(logs, "tools-"+core.NewID()+".log")
	spec := installSpec(sb, m.C.WS.Tools(), home, m.C.Tools(), log, tool, version, allow)
	spec.Guard = m.C.WS.Guarded()
	code, err := sb.Run(spec, 20*time.Minute)
	var out string
	for _, p := range []string{log, log + ".err", log + ".wsl"} {
		raw, _ := os.ReadFile(p)
		out += string(raw)
		os.Remove(p)
	}
	out = strings.TrimSpace(out)
	if err == nil && code != 0 {
		err = errors.New(core.Clip(out, 2000))
	}
	if err != nil {
		return "", fmt.Errorf("install %s: %w", tool, err)
	}
	return out, nil
}

// installSpec runs `hive tools-fetch` with folder writable and a fresh home.
// Where paths are the host's (Seatbelt, bare) HOME is the host temp dir; the
// sandbox adds the egress proxy for a networked spec.
func installSpec(sb sandbox.Sandbox, folder, home string, bins []string, log, tool, version string, allow []string) sandbox.Spec {
	mounts := []sandbox.Mount{{Host: folder, Inside: sandbox.ToolsInside, RW: true}, {Host: home, Inside: "/hive/home", RW: true}}
	env := []string{"HOME=" + home}
	argv := []string{bins[0], "tools-fetch", folder, tool, version}
	if sb.Isolated() {
		env = sandbox.InsideEnv("/hive/home", true)
		argv[0], argv[2] = "hive", sandbox.ToolsInside
		if src := os.Getenv("HIVE_TOOLS_SOURCE"); src != "" {
			mounts = append(mounts, sandbox.Mount{Host: src, Inside: "/hive/tools-source"})
			env = append(env, "HIVE_TOOLS_SOURCE=/hive/tools-source")
		}
	}
	return sandbox.Spec{ID: "tools-" + tool, Argv: argv, Env: env, Cwd: sandbox.InsidePath(sb, mounts[1]), Mounts: mounts, Tools: bins,
		Stdout: log, Network: true, Allow: allow, UID: os.Getuid(), GID: os.Getgid()}
}

// Installed lists "<tool> <version>" for what the folder holds.
func Installed(folder string) []string {
	var out []string
	for _, n := range sandbox.ToolNames {
		if v, err := os.ReadFile(filepath.Join(folder, n+".version")); err == nil {
			out = append(out, n+" "+strings.TrimSpace(string(v)))
		}
	}
	return out
}
