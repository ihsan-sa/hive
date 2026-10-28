package core

import (
	"runtime"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// elsewhereSB is an executor module's sandbox: it records the spec it was
// handed and runs the turn as the process agent.
type elsewhereSB struct {
	isolatedBox
	agent sandbox.Proc
	spec  *sandbox.Spec
}

func (s elsewhereSB) Start(spec sandbox.Spec) (sandbox.Proc, error) {
	*s.spec = spec
	return s.agent, nil
}

// A turn this machine is too strained for goes to an executor module with
// room, built with inside paths as for the local sandbox, and the record says
// where it ran; with no room it waits as before, and a bare Hive never hands
// a turn over.
func TestStrainedTurnGoesToAnExecutor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the turn")
	}
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB = 1 << 30
	if Strained(cfg.MemFloorMB, 0, 0) == "" {
		t.Skip("no /proc/meminfo here")
	}
	local := sandbox.Sandbox(isolatedBox{})
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return local, nil }
	var spec sandbox.Spec
	room, asked := false, 0
	c.Runs.Executor = func() (sandbox.Sandbox, string) {
		asked++
		if !room {
			return nil, ""
		}
		return elsewhereSB{agent: sleeper(t), spec: &spec}, "iiks1"
	}
	r, err := c.Runs.Start(RunSpec{ID: "w", Role: "worker", Harness: "fake", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, cfg)
	if got, _ := c.Runs.Get("w"); got.Status == StatusRunning || !strings.HasPrefix(got.Reason, "waiting: machine low on memory") || asked != 1 {
		t.Fatalf("with no room elsewhere: %s %q, asked %d", got.Status, got.Reason, asked)
	}
	if c.Runs.Elsewhere() {
		t.Error("Elsewhere with no room")
	}

	local = sandbox.Bare{}
	room = true
	c.Runs.launch(r, cfg)
	if got, _ := c.Runs.Get("w"); got.Status == StatusRunning || asked != 2 {
		t.Fatalf("a bare Hive handed its turn over: %s, asked %d", got.Status, asked)
	}

	local = isolatedBox{}
	if !c.Runs.Elsewhere() {
		t.Error("not Elsewhere with room")
	}
	r, _ = c.Runs.Get("w")
	c.Runs.launch(r, cfg)
	got, _ := c.Runs.Get("w")
	if got.Status != StatusRunning || got.Where != "iiks1" {
		t.Fatalf("with room elsewhere: %s %q where %q", got.Status, got.Reason, got.Where)
	}
	if spec.Cwd != "/work" || !strings.Contains(strings.Join(spec.Env, "\n"), "HIVE_BOX=/hive/box") {
		t.Errorf("the spec handed over isn't built with inside paths: cwd %q env %v", spec.Cwd, spec.Env)
	}
	var where string
	c.J.Replay(func(e Event) {
		if e.Kind == "run.turn_started" {
			var ts turnStarted
			e.Decode(&ts)
			where = ts.Where
		}
	})
	if where != "iiks1" {
		t.Errorf("run.turn_started says where %q", where)
	}
}

// An away run's turn goes to an executor module with room though the machine
// isn't strained, and with no room runs here rather than waiting.
func TestAwayTurnGoesToAnExecutorFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the turn")
	}
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax = 0, 0, 0
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return elsewhereSB{agent: sleeper(t), spec: new(sandbox.Spec)}, nil }
	var spec sandbox.Spec
	room := true
	c.Runs.Executor = func() (sandbox.Sandbox, string) {
		if !room {
			return nil, ""
		}
		return elsewhereSB{agent: sleeper(t), spec: &spec}, "iiks1"
	}
	r, err := c.Runs.Start(RunSpec{ID: "w", Role: "worker", Harness: "fake", Checkout: t.TempDir(), Away: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, cfg)
	if got, _ := c.Runs.Get("w"); got.Status != StatusRunning || got.Where != "iiks1" {
		t.Fatalf("an away turn: %s %q where %q", got.Status, got.Reason, got.Where)
	}
	room = false
	r, err = c.Runs.Start(RunSpec{ID: "w2", Role: "worker", Harness: "fake", Checkout: t.TempDir(), Away: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, cfg)
	if got, _ := c.Runs.Get("w2"); got.Status != StatusRunning || got.Where != "" {
		t.Fatalf("an away turn with no room elsewhere: %s %q where %q", got.Status, got.Reason, got.Where)
	}
}
