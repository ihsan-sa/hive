package core

import (
	"errors"
	"fmt"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Version is set at build time.
var Version = "0.0.0-dev"

// Core wires the core's parts together.
type Core struct {
	WS       Workspace
	J        *Journal
	Runs     *Runs
	Effects  *Effects
	Projects *Projects
	Person   *Person
	Tokens   *Tokens

	sbMu   sync.Mutex
	sb     sandbox.Sandbox
	sbErr  error
	sbAt   time.Time
	sbMode string
}

// Open loads the workspace's records and builds the core.
func Open(ws Workspace, forge Forge) (*Core, error) {
	if err := ws.Init(); err != nil {
		return nil, err
	}
	harness.UseModules(ws.Modules())
	j, err := OpenJournal(ws.Events())
	if err != nil {
		return nil, err
	}
	c := &Core{WS: ws, J: j, Tokens: NewTokens(ws)}
	c.Projects = NewProjects(j)
	c.Person = NewPerson(j)
	c.Tokens.Track(j, c.Person)
	c.Person.Handle("login", func(a Ask) { c.Tokens.Retry(a.Ref) })
	c.Runs = NewRuns(ws, j, c.Sandbox, c.Tokens, c.Tools)
	c.Runs.projs = c.Projects
	c.Effects = NewEffects(ws, j, forge, c.Sandbox, c.Tools, c.Runs)
	c.Runs.beforeTurn = c.freshBundle
	c.moveOldChecks()
	c.Person.moveTold()
	for _, fn := range opened {
		fn(c)
	}
	return c, nil
}

var opened []func(*Core)

// OnOpen has fn run on every core as it opens, before any module is built:
// a module the rest build on (the board, whose ops are every agent's
// messaging) registers itself from its package's init.
func OnOpen(fn func(*Core)) { opened = append(opened, fn) }

// Sandbox returns the sandbox agents run in, per the person's settings. With
// no sandbox and no opt-in, agents don't start.
func (c *Core) Sandbox() (sandbox.Sandbox, error) {
	cfg := c.WS.LoadConfig()
	mode := cfg.Sandbox
	if mode == "bare" && cfg.BareAccepted == "" {
		return nil, errors.New("bare runs need the warning read first (`" + Cmd + " doctor --bare`)")
	}
	c.sbMu.Lock()
	defer c.sbMu.Unlock()
	if c.sbMode == mode+cfg.WSLDistro && (c.sbErr == nil && c.sb != nil || time.Since(c.sbAt) < 2*time.Minute) {
		return c.sb, c.sbErr
	}
	c.sb, c.sbErr = sandbox.Select(sandbox.Options{Mode: mode, WSLDistro: cfg.WSLDistro, HiveLinux: c.HiveLinux()})
	c.sbAt, c.sbMode = time.Now(), mode+cfg.WSLDistro
	return c.sb, c.sbErr
}

// ResetSandbox forgets the cached sandbox choice (after settings change), or
// with all false only a failed one, so a sandbox fixed since (bwrap installed)
// is probed again without waiting out the two minutes.
func (c *Core) ResetSandbox(all bool) {
	c.sbMu.Lock()
	if all || c.sbErr != nil {
		c.sbMode = ""
	}
	c.sbMu.Unlock()
}

// HiveLinux is the linux/amd64 hive binary used inside WSL.
func (c *Core) HiveLinux() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	exe, _ := os.Executable()
	for _, p := range []string{filepath.Join(filepath.Dir(exe), "hive-linux-amd64"), filepath.Join(c.WS.Bin(), "hive-linux-amd64")} {
		if Exists(p) {
			return p
		}
	}
	return filepath.Join(filepath.Dir(exe), "hive-linux-amd64")
}

// Tools returns the hive binary to expose to agents, built for the sandbox's OS.
func (c *Core) Tools() []string {
	c.sbMu.Lock()
	sb := c.sb
	c.sbMu.Unlock()
	if sb != nil && runtime.GOOS == "windows" && sb.Isolated() {
		return []string{c.HiveLinux()}
	}
	exe, _ := os.Executable()
	return []string{c.agentBinary(exe)}
}

// agentBinary returns a copy of exe named `hive` in .hive/bin, since agents
// call `hive agent` whatever the person's command is called (e.g. hive-v0).
func (c *Core) agentBinary(exe string) string {
	name := "hive"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if filepath.Base(exe) == name {
		return exe
	}
	dst := filepath.Join(c.WS.Bin(), name)
	src, err1 := os.Stat(exe)
	cur, err2 := os.Stat(dst)
	if err1 == nil && (err2 != nil || cur.Size() != src.Size() || cur.ModTime().Before(src.ModTime())) {
		os.MkdirAll(c.WS.Bin(), 0o755)
		tmp := dst + ".tmp-" + NewID()[14:]
		if linkTestBinary(exe, tmp) {
			if os.Rename(tmp, dst) != nil {
				os.Remove(tmp)
			}
		} else if raw, err := os.ReadFile(exe); err == nil {
			if os.WriteFile(tmp, raw, 0o755) == nil {
				os.Remove(dst + ".old")
				os.Rename(dst, dst+".old") // a running copy can be renamed, not overwritten
				if os.Rename(tmp, dst) != nil {
					os.Remove(tmp)
				}
			}
		}
	}
	if Exists(dst) {
		return dst
	}
	return exe
}

// linkTestBinary hardlinks a test binary in place of the copy: every test's
// workspace took its own (13-50 MB), and internal/mod/work's tests wrote
// 13 MB a test that way, gigabytes a run on a disk the checks share. The
// daemon still copies, so rebuilding hive never changes a running agent's.
func linkTestBinary(exe, tmp string) bool {
	return !durable && os.Link(exe, tmp) == nil
}

// Tick advances runs on one read of the config; modules tick themselves
// from the daemon loop.
func (c *Core) Tick() {
	c.Effects.WatchRoom()
	c.Runs.Tick(c.WS.LoadConfig())
}

// Close closes the journal.
func (c *Core) Close() error { return c.J.Close() }

// Cmd is the name the person runs the Hive by (hive, hive-v0, ...), for messages.
var Cmd = "hive"

// moveOldChecks moves check logs written before logs went in their project's
// folder into that folder, where an isolated project's are covered. A log
// whose project it can't tell stays where it is.
func (c *Core) moveOldChecks() {
	dir := filepath.Join(c.WS.Hive(), "checks")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".log")
		if e.IsDir() || !ok {
			continue
		}
		if p := c.checkProject(id); p != "" {
			os.MkdirAll(filepath.Join(dir, p), 0o755)
			os.Rename(filepath.Join(dir, e.Name()), filepath.Join(dir, p, e.Name()))
		}
	}
}

// checkProject names the project an old check log belongs to: a gate's by the
// clone that holds its commit, an isolated project's first when a fork is
// shared (none when two isolated projects hold it, so neither sees the
// other's), a snapshot's or extract's by its run.
func (c *Core) checkProject(id string) string {
	if rest, ok := strings.CutPrefix(id, "gate-"); ok {
		sha, _, _ := strings.Cut(rest, "-")
		found, iso := "", ""
		for _, p := range c.Projects.List() {
			if _, err := Git(c.WS.Project(p.Name), "cat-file", "-e", sha+"^{commit}"); err == nil {
				if p.Hidden() && iso != "" {
					return ""
				}
				if p.Hidden() {
					iso = p.Name
				} else if found == "" {
					found = p.Name
				}
			}
		}
		if iso != "" {
			return iso
		}
		return found
	}
	for _, suf := range []string{"-snapshot", "-extract"} {
		if run, ok := strings.CutSuffix(id, suf); ok {
			if r, ok := c.Runs.Get(run); ok {
				return r.Project
			}
		}
	}
	return ""
}

// freshBundle brings a run's main.bundle, when it was given one (a worker,
// an isolated project's lead), up to its project's main before a turn, so
// whatever reopened it, the bundle holds main's tip. One that can't be
// written leaves the older one, said in the daemon's log.
func (c *Core) freshBundle(r Run) {
	path := filepath.Join(c.WS.RunRO(r.ID), "main.bundle")
	p, ok := c.Projects.Get(r.Project)
	if !ok || !Exists(path) {
		return
	}
	if _, err := c.Effects.FreshBundle(p, path); err != nil {
		fmt.Printf("hive: %s's main.bundle isn't brought up to main: %v\n", r.ID, err)
	}
}
