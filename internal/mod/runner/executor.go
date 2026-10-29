package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// RoomWait is how long the core waits for an executor's `room` answer.
const RoomWait = 5 * time.Second

// noRoomFor is how long a module's "no room" stands before it is asked
// again, so a strained machine with many turns waiting doesn't ask on every
// launch of every one.
const noRoomFor = 15 * time.Second

// roomFor is how long a "room on <runner>" stands, so the work queue's
// question and the launch that follows it ask once. A turn handed over
// spends it, so the next launch asks again; the module still decides at
// `turn`, and runs the turn here when its runner has filled since.
const roomFor = 5 * time.Second

// GiveBackEnv, set to 1 in an executor's environment for `turn`, says the
// core takes back a turn the module gives back before it starts.
const GiveBackEnv = "EXECUTOR_GIVE_BACK"

// roomAnswer is an executor's last answer to `room`: the runner it named
// ("" for no room) and until when it stands.
type roomAnswer struct {
	where string
	until time.Time
}

// Executor returns a running executor module that has room for a turn now,
// as a sandbox that hands the turn's spec to it, and the runner it named.
// A module serves the hook `executor` by answering `<exec> room` (exit 0
// and print {"runner": "<name>"}) and `<exec> turn --spec <file>`.
func (r *Runner) Executor() (sandbox.Sandbox, string) { return r.executor("executor") }

// CheckExecutor is Executor for a check, from a module whose manifest lists
// the hook `checks`: it says its `turn` runs a check's spec to its end and
// brings back its output (the core reads the check's exit from the output,
// never from `turn`'s own code). A module with the `executor` hook alone is
// handed turns, not checks.
func (r *Runner) CheckExecutor() (sandbox.Sandbox, string) { return r.executor("checks") }

func (r *Runner) executor(hook string) (sandbox.Sandbox, string) {
	for _, m := range r.Mods.List() {
		name := filepath.Base(m.Dir)
		if len(m.Exec) == 0 || !slices.Contains(m.Hooks, hook) || r.Status(name) != "running" {
			continue
		}
		argv, env := Argv(m), Env(r.C.WS, name)
		if hook == "executor" {
			// A turn it can't send on after all it may give back unstarted
			// (exit 125, "dispatch: not run: <why>"): the core then runs it
			// here under the pace and the strain hold (core.givenBack).
			env = append(env, GiveBackEnv+"=1")
		}
		r.mu.Lock()
		a := r.rooms[name]
		r.mu.Unlock()
		if time.Now().After(a.until) {
			a.where, _ = room(argv, env, m.Dir)
			a.until = time.Now().Add(noRoomFor)
			if a.where != "" {
				a.until = time.Now().Add(roomFor)
			}
			r.mu.Lock()
			if r.rooms == nil {
				r.rooms = map[string]roomAnswer{}
			}
			r.rooms[name] = a
			r.mu.Unlock()
		}
		if a.where != "" {
			spent := func() { r.mu.Lock(); delete(r.rooms, name); r.mu.Unlock() }
			return &sandbox.Delegate{Module: name, Where: a.where, Argv: argv, Env: env, Dir: m.Dir, Started: spent}, a.where
		}
	}
	return nil, ""
}

// room asks an executor whether it can take a turn now, and on which runner.
func room(argv, env []string, dir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), RoomWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], "room")...)
	cmd.Dir, cmd.Env = dir, env
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return "", false
	}
	var ans struct {
		Runner string `json:"runner"`
	}
	if json.Unmarshal(bytes.TrimSpace(out.Bytes()), &ans) != nil || ans.Runner == "" {
		return "", false
	}
	return ans.Runner, true
}

// Argv is a module's program, a first word starting with "." taken in its folder.
func Argv(m modules.Module) []string {
	argv := append([]string{}, m.Exec...)
	if strings.HasPrefix(argv[0], ".") {
		argv[0] = filepath.Join(m.Dir, argv[0])
	}
	return argv
}

// Home is a module's own HOME.
func Home(ws core.Workspace, name string) string {
	return filepath.Join(ws.Hive(), "modules", name)
}

// Env is the environment a module's program runs with outside its long-lived
// process: its own HOME and none of the daemon's credentials or HIVE_ settings.
func Env(ws core.Workspace, name string) []string {
	home := Home(ws, name)
	os.MkdirAll(home, 0o700)
	return append(scrub(os.Environ()), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "APPDATA="+filepath.Join(home, ".config"))
}

// Cleared says why a module's program may not run now, or "": one the person
// put there runs as is; one from git only at the commit its review approved.
// It reads the recorded review and starts none (the daemon does that).
func Cleared(m modules.Module) string {
	commit, decided, why := recorded(m)
	if !decided {
		return "its code at " + commit[:min(12, len(commit))] + " hasn't been read yet (the running Hive reads it)"
	}
	return why
}

// recorded is the commit a module runs at ("local" for one the person put
// there, with no .git) and whether that commit is decided: with why ""
// when it may run, or why not (its review declined it, or its commit is
// unknown). A git module whose commit no review has read is undecided.
func recorded(m modules.Module) (commit string, decided bool, why string) {
	if (m.Source.Source == "" || m.Source.Source == "builtin") && !core.Exists(filepath.Join(m.Dir, ".git")) {
		return "local", true, ""
	}
	commit = m.Source.Commit
	if commit == "" { // installed before the commit was recorded, or never recorded
		var err error
		if commit, err = core.Git(m.Dir, "rev-parse", "HEAD"); err != nil {
			return "", true, "its commit is unknown: " + err.Error()
		}
	}
	switch v := m.Source.Review; {
	case v == nil || v.Commit != commit:
		return commit, false, ""
	case v.Vote != "approve":
		return commit, true, "declined by its review: " + v.Reason
	}
	return commit, true, ""
}

// ErrNoCommand means no installed module offers a command by that name.
var ErrNoCommand = errors.New("no module offers that command")

// Command runs `hive <module> <args...>` for a module whose manifest asks for
// it ("command": true): its program with args, attached to this terminal,
// from its folder with its own HOME and the scrubbed environment.
func Command(ws core.Workspace, name string, args []string) (int, error) {
	m, ok := modules.New(ws, nil).Get(name)
	if !ok || !m.Command || len(m.Exec) == 0 {
		return 0, ErrNoCommand
	}
	if why := Cleared(m); why != "" {
		return 1, errors.New("module " + name + " can't run: " + why)
	}
	argv := Argv(m)
	cmd := exec.Command(argv[0], append(argv[1:], args...)...)
	cmd.Dir, cmd.Env = m.Dir, Env(ws, filepath.Base(m.Dir))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
