package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// execModule is a dispatch-like module the person put in the workspace: its
// run answers room (unless its HOME holds "full"), and a turn records its
// arguments, environment and spec in HOME and deletes the spec.
const execModule = `#!/bin/sh
case "$1" in
room) [ -f "$HOME/full" ] && exit 1; echo '{"runner": "iiks1"}' ;;
turn) echo "$@" > "$HOME/args"; env > "$HOME/env"; cp "$3" "$HOME/spec"; rm "$3" ;;
list) echo "$@" > "$HOME/args"; exit 3 ;;
*) sleep 60 ;;
esac
`

func execRunner(t *testing.T, manifest string) (*Runner, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the module is a shell script")
	}
	t.Setenv("HIVE_SECRET", "x")
	t.Setenv("GH_TOKEN", "x")
	r, _, _ := newRunner(t, "seed") // a workspace and runner; the module below is the person's own
	dir := filepath.Join(r.C.WS.Modules(), "dispatch")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "module.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(dir, "run"), []byte(execModule), 0o755)
	return r, Home(r.C.WS, "dispatch")
}

// A running executor module with room is handed a turn's spec in a file it
// deletes, on the runner it named, with its own HOME and no HIVE_ settings or
// credentials; one with no room, or not running, is passed over.
func TestExecutorHandsTheSpecToTheModule(t *testing.T) {
	r, home := execRunner(t, `{"name":"dispatch","exec":["./run"],"hooks":["status","executor"]}`)
	if ex, _ := r.Executor(); ex != nil {
		t.Fatal("an executor that isn't running was handed a turn")
	}
	r.status["dispatch"] = "running"
	ex, where := r.Executor()
	if ex == nil || where != "iiks1" || !ex.Isolated() {
		t.Fatalf("Executor = %v, %q", ex, where)
	}
	out := filepath.Join(t.TempDir(), "turn-001.out")
	p, err := ex.Start(sandbox.Spec{ID: "w", Argv: []string{"claude"}, Cwd: "/work", Stdout: out})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; proc.Alive(p); i++ {
		if i == 500 {
			t.Fatal("the turn didn't end")
		}
		time.Sleep(10 * time.Millisecond)
	}
	args, _ := os.ReadFile(filepath.Join(home, "args"))
	f := strings.Fields(string(args))
	if len(f) != 5 || f[0] != "turn" || f[1] != "--spec" || f[3] != "--runner" || f[4] != "iiks1" {
		t.Fatalf("turn's arguments: %q", args)
	}
	if core.Exists(f[2]) {
		t.Error("the spec file is still there")
	}
	if s, err := sandbox.ReadSpec(filepath.Join(home, "spec")); err != nil || s.ID != "w" || s.Stdout != out {
		t.Errorf("spec handed over: %+v, %v", s, err)
	}
	env, _ := os.ReadFile(filepath.Join(home, "env"))
	// Variables are matched by name: CI's ACTIONS_RUNNER_ACTION_ARCHIVE_CACHE
	// has "HIVE_" inside it.
	lines := strings.Split(string(env), "\n")
	if !slices.Contains(lines, "HOME="+home) {
		t.Errorf("turn's HOME isn't the module's %s", home)
	}
	for _, kv := range lines {
		if strings.HasPrefix(kv, "HIVE_") || strings.HasPrefix(kv, "GH_TOKEN=") {
			t.Errorf("turn's environment holds %s", kv)
		}
	}

	os.WriteFile(filepath.Join(home, "full"), nil, 0o644)
	if ex, _ := r.Executor(); ex != nil {
		t.Fatal("a room answer was spent twice: the turn handed over didn't make the next launch ask again")
	}
	os.Remove(filepath.Join(home, "full"))
	r.rooms = nil
	if ex, _ := r.Executor(); ex == nil {
		t.Fatal("an executor with room was passed over")
	}
	os.WriteFile(filepath.Join(home, "full"), nil, 0o644)
	if ex, _ := r.Executor(); ex == nil {
		t.Fatal("a module's room answer wasn't kept a moment, for the launch after the queue's question")
	}
	r.rooms = nil
	if ex, _ := r.Executor(); ex != nil {
		t.Fatal("an executor with no room was handed a turn")
	}
	os.Remove(filepath.Join(home, "full"))
	if ex, _ := r.Executor(); ex != nil {
		t.Error("a module's no-room answer wasn't kept a while")
	}
	r.rooms = nil
	if ex, _ := r.Executor(); ex == nil {
		t.Error("an executor with room again was passed over")
	}
}

// A module without the hook is never handed a turn.
func TestExecutorNeedsTheHook(t *testing.T) {
	r, _ := execRunner(t, `{"name":"dispatch","exec":["./run"],"hooks":["status"]}`)
	r.status["dispatch"] = "running"
	if ex, _ := r.Executor(); ex != nil {
		t.Fatal("a module without the executor hook was handed a turn")
	}
}

// hive <module> runs a module's program with the arguments, its exit code
// the command's, only for a module that asks for it and whose code was read.
func TestModuleCommand(t *testing.T) {
	r, home := execRunner(t, `{"name":"dispatch","exec":["./run"],"command":true}`)
	code, err := Command(r.C.WS, "dispatch", []string{"list", "--all"})
	if err != nil || code != 3 {
		t.Fatalf("Command = %d, %v", code, err)
	}
	if args, _ := os.ReadFile(filepath.Join(home, "args")); string(args) != "list --all\n" {
		t.Errorf("args %q", args)
	}
	if _, err := Command(r.C.WS, "seed", nil); err != ErrNoCommand {
		t.Errorf("a module that asks for no command: %v", err)
	}
	if _, err := Command(r.C.WS, "nothing", nil); err != ErrNoCommand {
		t.Errorf("no module: %v", err)
	}

	// From git, it runs only at the commit its review approved.
	dir := filepath.Join(r.C.WS.Modules(), "dispatch")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "v1"}} {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Command(r.C.WS, "dispatch", []string{"list"}); err == nil || !strings.Contains(err.Error(), "hasn't been read") {
		t.Errorf("an unread module ran: %v", err)
	}
	commit, _ := core.Git(dir, "rev-parse", "HEAD")
	if err := r.Mods.SetReview("dispatch", modules.Review{Commit: commit, Vote: "approve"}); err != nil {
		t.Fatal(err)
	}
	if code, err := Command(r.C.WS, "dispatch", []string{"list"}); err != nil || code != 3 {
		t.Errorf("an approved module: %d, %v", code, err)
	}
}
