package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// newRunner is a workspace with one module, name, cloned from git with no
// source record, and a runner that never ticks.
func newRunner(t *testing.T, name string) (*Runner, modules.Module, string) {
	return newRunnerIn(t, name, name)
}

// newRunnerIn is newRunner with the module in folder dir.
func newRunnerIn(t *testing.T, dir, name string) (*Runner, modules.Module, string) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = "fake"
	ws.SaveConfig(cfg)
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	mods := modules.New(ws, c.J)
	if err := mods.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(ws.Modules(), dir)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "module.json"), []byte(`{"name":"`+name+`","exec":["./run"],"hooks":["source"]}`), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "v1"}} {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	commit, _ := core.Git(dir, "rev-parse", "HEAD")
	m, _ := mods.Get(name)
	r := &Runner{C: c, Mods: mods, Agents: &agents.Agents{C: c, Mod: mods}, status: map[string]string{}, tries: map[string]int{}, missing: map[string]bool{}}
	return r, m, commit
}

func (r *Runner) check(name string, m modules.Module) string {
	_, why := r.cleared(name, m)
	r.status[name] = why // as Tick does
	return why
}

func (r *Runner) count(kind string) int {
	n := 0
	for _, e := range r.C.J.Since(0) {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// A clone with no source record runs at the commit it holds, with no code
// review before it (the person, 10-30: no process).
func TestCloneWithNoRecordRunsUnread(t *testing.T) {
	r, m, commit := newRunner(t, "bare")
	if why := r.check("bare", m); why != "" {
		t.Fatalf("a clone with no record: %q, want it to run", why)
	}
	if got, _ := r.cleared("bare", m); got != commit {
		t.Fatalf("runs at %q, want %s", got, commit)
	}
	if _, ok := r.C.Runs.Get("mr-bare-" + commit[:12]); ok {
		t.Fatal("a review run started")
	}
}

// A program that isn't on the PATH raises one alert saying so, not one per
// try, and is tried again only hourly.
func TestMissingProgramAlertsOnce(t *testing.T) {
	r, m, _ := newRunner(t, "door")
	m.Exec = []string{"hive-no-such-program-xyz"}
	for range 3 {
		c := &child{commit: "local", wait: time.Second}
		r.start("door", c, m)
		if time.Until(c.next) < 50*time.Minute {
			t.Fatalf("tried again in %s", time.Until(c.next))
		}
	}
	if n := r.count("alert.raised"); n != 1 {
		t.Fatalf("%d alerts, want 1", n)
	}
	if n := r.count("module.exited"); n != 1 {
		t.Fatalf("%d module.exited for the same failure, want 1", n)
	}
	if !strings.HasPrefix(r.Status("door"), "could not start") {
		t.Fatalf("status %q", r.Status("door"))
	}
	// The PATH is fixed: the program starts and the alert is cleared.
	m.Exec = []string{"go", "version"}
	c := &child{commit: "local", wait: time.Second}
	r.mu.Lock()
	r.start("door", c, m)
	r.mu.Unlock()
	<-c.done
	if n := r.count("alert.cleared"); n != 1 {
		t.Fatalf("%d alerts cleared once it started, want 1", n)
	}
}
