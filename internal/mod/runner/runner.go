// Package runner starts and supervises modules that carry a program: it gives
// each one a token for the slice of the control API its hooks need, restarts
// one that dies, and starts a module installed from git only once a read-only
// review run has read its code at the commit checked out.
package runner

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// Scopes is the slice of the control API each hook may call. The daemon never
// calls a module: a module calls the API, and reads its hooks off the journal.
var Scopes = map[string][]string{
	"source":  {"/api/ask"},                                                    // file an ask, with where it came from
	"channel": {"/api/chat", "/api/events", "/api/run/stop", "/api/task/stop"}, // the lead's conversation out, steer and stop in
	"after":   {"/api/events"},                                                 // run.turn_ended, effect.merged
	"status":  {"/api/events"},
	"idle":    {"/api/events"},                    // hive.idle
	"kb":      {"/api/kb/notes", "/api/kb/dream"}, // read the shared notes, apply a consolidation pass
	// executor: the core hands it turns (Executor); it needs no API path
	"executor": {},
}

// every module may ping and report.
var always = []string{"/api/ping", "/api/module/report"}

// Runner supervises module programs.
type Runner struct {
	C      *core.Core
	Mods   *modules.Registry
	Agents *agents.Agents
	API    string // the control API's base URL; nothing starts until it is set

	mu     sync.Mutex
	procs  map[string]*child
	status map[string]string
	// missing holds the modules whose program wasn't on the PATH, with an
	// alert standing until it starts.
	missing map[string]bool
	tries   map[string]int // reviews started this life, by module and commit
	busy    bool
	rooms   map[string]roomAnswer // executors' last answers to `room`, by module
}

type child struct {
	commit string
	token  string
	paths  map[string]bool
	cmd    *exec.Cmd
	done   chan struct{} // closed once cmd has exited
	wait   time.Duration
	next   time.Time
}

// Tick journals hive.idle when the last run stops, then brings each module's
// program to the state its review allows. busy and r.busy are read and written
// outside r.mu: only the daemon's single tick loop calls Tick.
func (r *Runner) Tick() {
	busy := len(r.C.Runs.List(func(x *core.Run) bool {
		return x.Status == core.StatusRunning || x.Status == core.StatusQueued
	})) > 0
	if r.busy && !busy {
		r.C.J.MustAppend("hive.idle", map[string]string{})
	}
	r.busy = busy
	if r.API == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.procs == nil {
		r.procs, r.status, r.tries, r.missing = map[string]*child{}, map[string]string{}, map[string]int{}, map[string]bool{}
	}
	seen := map[string]bool{}
	for _, m := range r.Mods.List() {
		if len(m.Exec) == 0 {
			continue
		}
		name := filepath.Base(m.Dir)
		seen[name] = true
		commit, why := r.cleared(name, m)
		c := r.procs[name]
		if c != nil && (why != "" || c.commit != commit) {
			r.kill(c)
			delete(r.procs, name)
			c = nil
		}
		if why != "" {
			r.status[name] = why
			continue
		}
		if c == nil {
			c = &child{commit: commit, wait: time.Second}
			r.procs[name] = c
		}
		if c.cmd != nil || time.Now().Before(c.next) {
			continue
		}
		r.start(name, c, m)
	}
	for name, c := range r.procs {
		if !seen[name] {
			r.kill(c)
			delete(r.procs, name)
		}
	}
}

// cleared returns the commit a module may run at, or why it may not run yet.
// A module with neither a git source nor a .git folder was put there by the
// person and runs as is; any other runs at the commit it holds.
func (r *Runner) cleared(name string, m modules.Module) (string, string) {
	return recorded(m)
}

// start launches the program with a fresh token, its own HOME and no
// credentials from the daemon's environment.
func (r *Runner) start(name string, c *child, m modules.Module) {
	argv := Argv(m)
	home := Home(r.C.WS, name)
	os.MkdirAll(home, 0o700)
	var tok [16]byte
	rand.Read(tok[:])
	c.token = hex.EncodeToString(tok[:])
	c.paths = map[string]bool{}
	for _, p := range always {
		c.paths[p] = true
	}
	for _, h := range m.Hooks {
		for _, p := range Scopes[h] {
			c.paths[p] = true
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.Dir
	env := scrub(os.Environ())
	if os.Getenv("GOCACHE") == "" {
		// A program run with `go run` builds into the person's own Go cache,
		// as go would with their HOME, not into a fresh one under its HOME.
		if dir, err := os.UserCacheDir(); err == nil {
			env = append(env, "GOCACHE="+filepath.Join(dir, "go-build"))
		}
	}
	cmd.Env = append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "APPDATA="+filepath.Join(home, ".config"),
		"HIVE_API="+r.API, "HIVE_MODULE="+name, "HIVE_MODULE_TOKEN="+c.token)
	// Telemetry off, as for the prefetch: in its fresh config folder `go run`
	// counts and uploads from a detached child that outlives a kill.
	os.MkdirAll(filepath.Join(home, ".config", "go", "telemetry"), 0o755)
	os.WriteFile(filepath.Join(home, ".config", "go", "telemetry", "mode"), []byte("off\n"), 0o644)
	proc.Group(cmd)
	log, _ := os.OpenFile(filepath.Join(home, "module.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	cmd.Stdout, cmd.Stderr = log, log
	at := time.Now()
	if err := cmd.Start(); err != nil {
		log.Close()
		why := "could not start: " + err.Error()
		if errors.Is(err, exec.ErrNotFound) && r.status[name] != why {
			// It backs off and tries again, but a missing program (go, for a
			// module run with go run) won't appear on its own: say so once.
			r.missing[name] = true
			r.C.J.MustAppend("alert.raised", map[string]string{"kind": "module", "ref": "module:" + name + ":missing", "module": name,
				"text": fmt.Sprintf("module %s can't start: %q is not on the Hive's PATH. Install it, or put it on the PATH the daemon runs with, and the module starts on its next try, within the hour, or at the Hive's next start.", name, argv[0])})
		}
		if r.status[name] != why { // a retry that fails the same way says nothing new
			r.C.J.MustAppend("module.exited", map[string]string{"module": name, "error": err.Error()})
		}
		r.status[name] = why
		c.next, c.wait = at.Add(c.wait), min(c.wait*2, 5*time.Minute)
		if errors.Is(err, exec.ErrNotFound) {
			c.next = at.Add(time.Hour) // it won't appear on its own; the alert says what to do
		}
		return
	}
	done := make(chan struct{})
	c.cmd, c.done = cmd, done
	r.status[name] = "running"
	if r.missing[name] { // the PATH was fixed: the alert's fault is gone
		delete(r.missing, name)
		r.C.J.MustAppend("alert.cleared", map[string]string{"ref": "module:" + name + ":missing", "module": name})
	}
	r.C.J.MustAppend("module.started", map[string]any{"module": name, "commit": c.commit, "pid": cmd.Process.Pid, "hooks": m.Hooks})
	go func() {
		err := cmd.Wait()
		log.Close()
		close(done)
		msg := "exited"
		if err != nil {
			msg = err.Error()
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if c.cmd != cmd {
			return
		}
		if time.Since(at) > time.Minute {
			c.wait = time.Second // it ran a while: this is a fresh failure
		}
		c.cmd, c.next, c.wait = nil, time.Now().Add(c.wait), min(c.wait*2, 5*time.Minute)
		r.status[name] = "restarting: " + msg
		r.C.J.MustAppend("module.exited", map[string]string{"module": name, "error": msg})
	}()
}

// kill stops a module's program and what it started (the binary `go run`
// built, for one); its token dies with it.
func (r *Runner) kill(c *child) {
	if c.cmd != nil {
		proc.KillGroup(c.cmd)
		c.cmd = nil
	}
}

// scrub drops what a module must not hold: forge and model credentials, the
// person's config folders and the Hive's own settings.
func scrub(env []string) []string {
	drop := map[string]bool{"GH_TOKEN": true, "GITHUB_TOKEN": true, "GH_ENTERPRISE_TOKEN": true, "GITHUB_ENTERPRISE_TOKEN": true, "GH_CONFIG_DIR": true,
		"CLAUDE_CODE_OAUTH_TOKEN": true, "ANTHROPIC_API_KEY": true, "CLAUDE_CONFIG_DIR": true, "SSH_AUTH_SOCK": true,
		"HOME": true, "USERPROFILE": true, "XDG_CONFIG_HOME": true, "APPDATA": true}
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[strings.ToUpper(k)] && !strings.HasPrefix(strings.ToUpper(k), "HIVE_") {
			out = append(out, kv)
		}
	}
	return out
}

// Allowed returns the module a token belongs to and whether its hooks reach path.
func (r *Runner) Allowed(token, path string) (string, bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, c := range r.procs {
		if c.cmd != nil && token != "" && c.token == token {
			return name, true, c.paths[path]
		}
	}
	return "", false, false
}

// Retry lets a module whose reviews ended without a vote be read again. name
// is the manifest's name or the folder's; tries are keyed by the folder. It
// clears every commit's count, though only the current commit's is checked.
func (r *Runner) Retry(name string) error {
	m, ok := r.Mods.Get(name)
	if !ok {
		return core.ErrNotFound
	}
	prefix := "mr-" + filepath.Base(m.Dir) + "-"
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.tries {
		// What follows the prefix is the commit, which has no dash, so "echo"
		// leaves "echo-door"'s count alone.
		if rest, ok := strings.CutPrefix(k, prefix); ok && !strings.Contains(rest, "-") {
			delete(r.tries, k)
		}
	}
	return nil
}

// Status says what a module's program is doing, or why it is not running.
func (r *Runner) Status(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status[name]
}

// Close stops every module's program and waits a little for each to go, so
// none still writes in its home once the daemon has stopped.
func (r *Runner) Close() {
	r.mu.Lock()
	var done []chan struct{}
	for _, c := range r.procs {
		if c.cmd != nil && c.done != nil {
			done = append(done, c.done)
		}
		r.kill(c)
	}
	r.mu.Unlock()
	for _, d := range done {
		select {
		case <-d:
		case <-time.After(5 * time.Second):
		}
	}
}
