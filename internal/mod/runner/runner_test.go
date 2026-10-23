package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// newRunner is a workspace with one module, name, cloned from git with no
// source record, and a runner that never ticks.
func newRunner(t *testing.T, name string) (*Runner, modules.Module, string) {
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
	dir := filepath.Join(ws.Modules(), name)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "module.json"), []byte(`{"name":"`+name+`","exec":["./run"],"hooks":["source"]}`), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "v1"}} {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	commit, _ := core.Git(dir, "rev-parse", "HEAD")
	m, _ := mods.Get(name)
	r := &Runner{C: c, Mods: mods, Agents: &agents.Agents{C: c, Mod: mods}, status: map[string]string{}, tries: map[string]int{}}
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

// A clone with no source record is read before it runs, at the commit it
// holds, and that commit is the one the brief names.
func TestCloneWithNoRecordIsReviewed(t *testing.T) {
	r, m, commit := newRunner(t, "bare")
	if why := r.check("bare", m); why != "waiting for its code review" {
		t.Fatalf("a clone with no record: %q, want it waiting for its review", why)
	}
	run, ok := r.C.Runs.Get("mr-bare-" + commit[:12])
	if !ok || !run.ReadOnly || run.Role != "module-reviewer" || !strings.Contains(run.Brief, commit) {
		t.Fatalf("review run %+v, want a read-only module-reviewer whose brief names %s", run, commit)
	}
}

// A review that ends with no vote is run again, three times in all, then the
// module waits with one alert; it is never recorded as declined.
func TestReviewWithNoVoteRunsAgain(t *testing.T) {
	r, m, commit := newRunner(t, "flaky")
	base := "mr-flaky-" + commit[:12]
	for i, id := range []string{base, base + "-2", base + "-3"} {
		if why := r.check("flaky", m); why != "waiting for its code review" {
			t.Fatalf("attempt %d: %q", i+1, why)
		}
		if _, ok := r.C.Runs.Get(id); !ok {
			t.Fatalf("attempt %d: no run %s", i+1, id)
		}
		r.C.Runs.Stop(id, "crashed")
	}
	for range 2 {
		if why := r.check("flaky", m); !strings.Contains(why, "three times") {
			t.Fatalf("after three reviews with no vote: %q", why)
		}
	}
	if n := r.count("alert.raised"); n != 1 {
		t.Fatalf("%d alerts, want 1", n)
	}
	if n := r.count("module.reviewed"); n != 0 {
		t.Fatalf("a review with no vote was recorded (%d)", n)
	}
}

// The person's "review again" lets a module held after three silent reviews be
// read once more, under a fresh run id.
func TestRetryReadsItAgain(t *testing.T) {
	r, m, commit := newRunner(t, "held")
	base := "mr-held-" + commit[:12]
	for _, id := range []string{base, base + "-2", base + "-3"} {
		r.check("held", m)
		r.C.Runs.Stop(id, "crashed")
	}
	if why := r.check("held", m); !strings.Contains(why, "three times") {
		t.Fatalf("not held after three silent reviews: %q", why)
	}
	r.Retry("held")
	if why := r.check("held", m); why != "waiting for its code review" {
		t.Fatalf("after Retry: %q", why)
	}
	if _, ok := r.C.Runs.Get(base + "-4"); !ok {
		t.Fatalf("no fourth review run after Retry")
	}
}

// A vote that cannot be recorded is not journalled again on every tick.
func TestReviewThatCannotBeRecorded(t *testing.T) {
	r, m, commit := newRunner(t, "gone")
	id := "mr-gone-" + commit[:12]
	r.check("gone", m)
	r.C.J.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": 1, "status": core.StatusDone,
		"result": map[string]any{"text": "ok", "structured": map[string]string{"vote": "decline", "reason": "no"}}})
	os.Mkdir(filepath.Join(m.Dir, ".hive-source.json"), 0o755) // the record cannot be written
	for range 3 {
		if why := r.check("gone", m); !strings.Contains(why, "could not be recorded") {
			t.Fatalf("%q, want the record failure", why)
		}
	}
	if n := r.count("alert.raised") + r.count("module.reviewed"); n != 0 {
		t.Fatalf("%d events for a vote that was never recorded", n)
	}
}
