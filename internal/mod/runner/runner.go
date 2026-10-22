// Package runner starts and supervises modules that carry a program: it gives
// each one a token for the slice of the control API its hooks need, restarts
// one that dies, and starts a module installed from git only once a read-only
// review run has read its code at the commit checked out.
package runner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// Scopes is the slice of the control API each hook may call. The daemon never
// calls a module: a module calls the API, and reads its hooks off the journal.
var Scopes = map[string][]string{
	"source":  {"/api/ask"},                                                    // file an ask, with where it came from
	"channel": {"/api/chat", "/api/events", "/api/run/stop", "/api/task/stop"}, // the lead's conversation out, steer and stop in
	"after":   {"/api/events"},                                                 // run.turn_ended, effect.merged
	"status":  {"/api/events"},
	"idle":    {"/api/events"}, // hive.idle
}

// every module may ping and report.
var always = []string{"/api/ping", "/api/module/report"}

// VoteSchema is the module review's structured answer.
const VoteSchema = `{"type":"object","properties":{"vote":{"type":"string","enum":["approve","decline"]},"reason":{"type":"string"}},"required":["vote","reason"]}`

// Runner supervises module programs.
type Runner struct {
	C      *core.Core
	Mods   *modules.Registry
	Agents *agents.Agents
	API    string // the control API's base URL; nothing starts until it is set

	mu     sync.Mutex
	procs  map[string]*child
	status map[string]string
	busy   bool
}

type child struct {
	commit string
	token  string
	paths  map[string]bool
	cmd    *exec.Cmd
	wait   time.Duration
	next   time.Time
}

// Tick journals hive.idle when the last run stops, then brings each module's
// program to the state its review allows.
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
		r.procs, r.status = map[string]*child{}, map[string]string{}
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
// A module with no git source was put there by the person and runs as is.
func (r *Runner) cleared(name string, m modules.Module) (string, string) {
	if m.Source.Source == "" || m.Source.Source == "builtin" {
		return "local", ""
	}
	commit := m.Source.Commit
	if commit == "" { // installed before the commit was recorded
		var err error
		if commit, err = core.Git(m.Dir, "rev-parse", "HEAD"); err != nil {
			return "", "its commit is unknown: " + err.Error()
		}
	}
	if v := m.Source.Review; v != nil && v.Commit == commit {
		if v.Vote != "approve" {
			return commit, "declined by its review: " + v.Reason
		}
		return commit, ""
	}
	id := "mr-" + name + "-" + commit[:min(12, len(commit))]
	run, ok := r.C.Runs.Get(id)
	if !ok {
		if err := r.review(id, m); err != nil {
			return commit, "its review could not start: " + err.Error()
		}
		return commit, "waiting for its code review"
	}
	if !run.Terminal() {
		return commit, "waiting for its code review"
	}
	v := modules.Review{Commit: commit, Vote: "decline", Reason: "the review ended without a vote (" + run.Status + ")", Run: id}
	var out struct{ Vote, Reason string }
	if run.Status == core.StatusDone && json.Unmarshal(run.Structured, &out) == nil && (out.Vote == "approve" || out.Vote == "decline") {
		v.Vote, v.Reason = out.Vote, core.Clip(out.Reason, 2000)
	}
	r.Mods.SetReview(name, v)
	if v.Vote != "approve" {
		r.C.J.MustAppend("alert.raised", map[string]string{"kind": "module", "module": name, "text": "module " + name + " was not started: its review declined it: " + v.Reason})
	}
	return commit, "waiting for its code review"
}

// review starts a read-only run over a copy of the module's files.
func (r *Runner) review(id string, m modules.Module) error {
	dir, err := r.Agents.Scratch(id)
	if err != nil {
		return err
	}
	if err := copyTree(m.Dir, dir); err != nil {
		return err
	}
	brief := fmt.Sprintf(`Read the module in your working folder, at commit %s from %s.

The Hive will run %q from that folder on the person's machine, outside the sandbox, as the person, with no forge credential in its environment and its HOME set to a folder of its own. It asks to serve these hooks: %s. Its manifest says: %q.

Approve only if the code does what the manifest says and nothing else. Decline if it reads or sends credentials or logins (gh, git, ssh, cloud, browser), writes outside its own folder and HOME, starts other programs it does not need, fetches and runs code, or hides what it does. Your final answer is the structured vote: vote (approve or decline) and reason.`,
		m.Source.Commit, m.Source.Source, strings.Join(m.Exec, " "), strings.Join(m.Hooks, ", "), m.Description)
	s := r.Agents.Spec(id, "module-reviewer", "", dir, brief)
	s.ReadOnly, s.Schema = true, VoteSchema
	_, err = r.C.Runs.Start(s)
	return err
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if d.Name() == ".git" || d.Name() == ".hive-source.json" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), raw, 0o644)
	})
}

// start launches the program with a fresh token, its own HOME and no
// credentials from the daemon's environment.
func (r *Runner) start(name string, c *child, m modules.Module) {
	argv := append([]string{}, m.Exec...)
	if strings.HasPrefix(argv[0], ".") {
		argv[0] = filepath.Join(m.Dir, argv[0])
	}
	home := filepath.Join(r.C.WS.Hive(), "modules", name)
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
	cmd.Env = append(scrub(os.Environ()), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "APPDATA="+filepath.Join(home, ".config"),
		"HIVE_API="+r.API, "HIVE_MODULE="+name, "HIVE_MODULE_TOKEN="+c.token)
	log, _ := os.OpenFile(filepath.Join(home, "module.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	cmd.Stdout, cmd.Stderr = log, log
	at := time.Now()
	if err := cmd.Start(); err != nil {
		log.Close()
		r.status[name] = "could not start: " + err.Error()
		r.C.J.MustAppend("module.exited", map[string]string{"module": name, "error": err.Error()})
		c.next, c.wait = at.Add(c.wait), min(c.wait*2, 5*time.Minute)
		return
	}
	c.cmd = cmd
	r.status[name] = "running"
	r.C.J.MustAppend("module.started", map[string]any{"module": name, "commit": c.commit, "pid": cmd.Process.Pid, "hooks": m.Hooks})
	go func() {
		err := cmd.Wait()
		log.Close()
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

// kill stops a module's program; its token dies with it.
func (r *Runner) kill(c *child) {
	if c.cmd != nil {
		c.cmd.Process.Kill()
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

// Status says what a module's program is doing, or why it is not running.
func (r *Runner) Status(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status[name]
}

// Close stops every module's program.
func (r *Runner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.procs {
		r.kill(c)
	}
}
