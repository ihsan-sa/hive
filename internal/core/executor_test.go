package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
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

// A test check the line here would make wait runs on an executor module's
// runner with room, without a slot and without this machine's shared caches,
// and the record says where; a check waiting in the line leaves it once a
// runner has room. A check with a free slot, or of main's own commit, runs
// here.
func TestCheckGoesToARunnerWhenTheLineIsFull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the checks are shell")
	}
	c, _, p, main := setupProject(t)
	r, _ := commitInCheckout(t, c, p, "w1", main, "feature.go", "package demo\n")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	var here, away []sandbox.Spec
	c.Effects.sandbox = func() (sandbox.Sandbox, error) { return mapped{specs: &here}, nil }
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return isolatedBox{}, nil }
	var mu sync.Mutex
	room, liar := true, false
	c.Runs.CheckExecutor = func() (sandbox.Sandbox, string) {
		mu.Lock()
		defer mu.Unlock()
		if !room {
			return nil, ""
		}
		if liar {
			return liarBox{}, "iiks1"
		}
		return mapped{specs: &away}, "iiks1"
	}
	c.Effects.Slots.Resize(func() int { return 1 })

	if ch := c.Effects.RunCheck(p, sha, "test", "true"); !ch.OK || ch.Where != "" || len(here) != 1 || len(away) != 0 {
		t.Fatalf("with a free slot: where %q, %d here, %d away", ch.Where, len(here), len(away))
	}
	release := c.Effects.Slots.Acquire("other", "x", "test")
	if ch := c.Effects.RunCheck(p, sha, "test", "true"); !ch.OK || ch.Where != "iiks1" || len(away) != 1 || !strings.Contains(ch.Output, "ran on iiks1") {
		t.Fatalf("with the line full: %+v, %d away", ch, len(away))
	}
	if s := away[0]; s.Cwd != "/work" || slices.ContainsFunc(s.Mounts, func(m sandbox.Mount) bool { return m.Inside == GoModInside || m.Inside == GoBuildInside }) {
		t.Errorf("the spec handed over: cwd %q mounts %+v", s.Cwd, s.Mounts)
	}
	// Its verdict is the command's own exit, red elsewhere as here; a module
	// that exits 0 without running it gives no verdict.
	red := "echo '--- FAIL: TestX (0.01s)'; echo 'hive-check-exit 0 0'; exit 3"
	if ch := c.Effects.RunCheck(p, sha, "test", red); ch.OK || ch.CantRun != "" || ch.Where != "iiks1" || len(ch.Failed) != 1 {
		t.Fatalf("a failing command elsewhere: %+v", ch)
	}
	mu.Lock()
	liar = true
	mu.Unlock()
	if ch := c.Effects.RunCheck(p, sha, "test", "exit 3"); ch.OK || !strings.HasPrefix(ch.CantRun, "can't run: iiks1 gave back no exit") {
		t.Fatalf("a module that ran nothing and exited 0: %+v", ch)
	}
	mu.Lock()
	liar = false
	mu.Unlock()
	away = away[:1]
	if running, waiting := c.Effects.Slots.State(); len(running) != 1 || len(waiting) != 0 {
		t.Errorf("a check run elsewhere held or waited for a slot here: %+v %+v", running, waiting)
	}

	// Waiting in the line, it leaves once a runner has room.
	old := checkLeaveEvery
	checkLeaveEvery = 20 * time.Millisecond
	defer func() { checkLeaveEvery = old }()
	mu.Lock()
	room = false
	mu.Unlock()
	done := make(chan Check)
	go func() { done <- c.Effects.RunCheck(p, sha, "retest", "true") }()
	waitFor(t, "the check to wait in the line with no room", func() bool { _, w := c.Effects.Slots.State(); return len(w) == 1 })
	time.Sleep(5 * checkLeaveEvery) // it asks a few times and stays
	if _, waiting := c.Effects.Slots.State(); len(waiting) != 1 {
		t.Fatalf("with no room it doesn't wait in the line: %+v", waiting)
	}
	mu.Lock()
	room = true
	mu.Unlock()
	// Leaving takes a tick; the run after it (a clean checkout and the
	// command) can take far longer on a loaded machine, so it gets its own,
	// looser wait.
	waitFor(t, "a waiting check to leave the line for a runner with room", func() bool { _, w := c.Effects.Slots.State(); return len(w) == 0 })
	select {
	case ch := <-done:
		if ch.Where != "iiks1" || len(away) != 2 {
			t.Fatalf("left the line for %q, %d away", ch.Where, len(away))
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("a check that left the line didn't finish on the runner")
	}

	// Main's own check waits for a slot here: it writes the shared build cache.
	go func() { done <- c.Effects.RunCheck(p, main, "test", "true") }()
	waitFor(t, "main's check to wait in the line", func() bool { _, w := c.Effects.Slots.State(); return len(w) == 1 })
	time.Sleep(5 * checkLeaveEvery) // it would have left by now
	release()
	if ch := <-done; ch.Where != "" || len(away) != 2 {
		t.Fatalf("main's check went elsewhere: %q", ch.Where)
	}
}

// A check a runner refused before it started (dispatch's "not run:", exit
// 125, no marker) is no can't-run: it goes back in the line here at once,
// waits there for no runner, and runs here, its output saying why.
func TestCheckARunnerRefusedGoesBackInTheLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the checks are shell")
	}
	c, _, p, main := setupProject(t)
	r, _ := commitInCheckout(t, c, p, "w1", main, "feature.go", "package demo\n")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	var here []sandbox.Spec
	c.Effects.sandbox = func() (sandbox.Sandbox, error) { return mapped{specs: &here}, nil }
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return isolatedBox{}, nil }
	var mu sync.Mutex
	asked := 0
	c.Runs.CheckExecutor = func() (sandbox.Sandbox, string) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		return refuserBox{}, "iiks1"
	}
	old := checkLeaveEvery
	checkLeaveEvery = 10 * time.Millisecond
	defer func() { checkLeaveEvery = old }()
	c.Effects.Slots.Resize(func() int { return 1 })
	release := c.Effects.Slots.Acquire("other", "x", "test")
	done := make(chan Check)
	go func() { done <- c.Effects.RunCheck(p, sha, "test", "true") }()
	waitFor(t, "the refused check to wait in the line here", func() bool { _, w := c.Effects.Slots.State(); return len(w) == 1 })
	time.Sleep(10 * checkLeaveEvery) // a runner still says it has room; it stays
	mu.Lock()
	if asked != 1 {
		t.Errorf("a refused check asked a runner %d times, want 1", asked)
	}
	mu.Unlock()
	release()
	ch := <-done
	if !ch.OK || ch.CantRun != "" || ch.Where != "" || len(here) != 1 {
		t.Fatalf("a refused check: %+v, %d here", ch, len(here))
	}
	if !strings.Contains(ch.Output, "iiks1 refused it before it started (iiks1 won't take it: full (3 of 3 turns running)), so it went back in the check line here") {
		t.Errorf("its output doesn't say why it ran here: %q", ch.Output)
	}
	if refusedBefore(125, "x\ndispatch: running it here: boom\n") != "" || refusedBefore(1, "dispatch: not run: full\n") != "" {
		t.Error("refusedBefore read a check that ran as refused")
	}
}

// refuserBox is a module whose runner refused the check before it started.
type refuserBox struct{ isolatedBox }

func (refuserBox) Run(s sandbox.Spec, _ time.Duration) (int, error) {
	os.WriteFile(s.Stdout+".err", []byte("dispatch: not run: iiks1 won't take it: full (3 of 3 turns running); a check never runs here, the core puts it back in its line\n"), 0o644)
	return 125, nil
}

// liarBox is a module whose turn exits 0 without running the command.
type liarBox struct{ isolatedBox }

func (liarBox) Run(sandbox.Spec, time.Duration) (int, error) { return 0, nil }

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
	c.Runs.mu.Lock()
	c.Runs.handedAt = time.Time{} // a step after the last turn went (handing)
	c.Runs.mu.Unlock()
	r, err = c.Runs.Start(RunSpec{ID: "w2", Role: "worker", Harness: "fake", Checkout: t.TempDir(), Away: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, cfg)
	if got, _ := c.Runs.Get("w2"); got.Status != StatusRunning || got.Where != "" {
		t.Fatalf("an away turn with no room elsewhere: %s %q where %q", got.Status, got.Reason, got.Where)
	}
}

// waitFor polls cond until it holds, or fails after 10 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(30 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("timed out waiting for " + what)
		}
	}
}

// Turns go to an executor one at a time: a strained turn launched within
// handStep of the last one handed over waits, though the module says it has
// room, and the work queue hears no room meanwhile (10-29: ~66 turns handed
// over in six minutes on one cached answer, 61 of them run here after all).
func TestTurnsGoToAnExecutorOneAtATime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the turn")
	}
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB = 1 << 30
	if Strained(cfg.MemFloorMB, 0, 0) == "" {
		t.Skip("no /proc/meminfo here")
	}
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return isolatedBox{}, nil }
	c.Runs.Executor = func() (sandbox.Sandbox, string) {
		return elsewhereSB{agent: sleeper(t), spec: new(sandbox.Spec)}, "iiks1"
	}
	for _, id := range []string{"w1", "w2"} {
		r, err := c.Runs.Start(RunSpec{ID: id, Role: "worker", Harness: "fake", Checkout: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		c.Runs.launch(r, cfg)
	}
	if got, _ := c.Runs.Get("w1"); got.Status != StatusRunning || got.Where != "iiks1" {
		t.Fatalf("the first turn: %s %q where %q", got.Status, got.Reason, got.Where)
	}
	if got, _ := c.Runs.Get("w2"); got.Status == StatusRunning || !strings.HasPrefix(got.Reason, "waiting: one turn at a time") {
		t.Fatalf("the second turn, at once: %s %q", got.Status, got.Reason)
	}
	if c.Runs.Elsewhere() {
		t.Error("Elsewhere while a turn was just handed over")
	}
	c.Runs.mu.Lock()
	c.Runs.handedAt = time.Now().Add(-handStep)
	c.Runs.mu.Unlock()
	r, _ := c.Runs.Get("w2")
	c.Runs.launch(r, cfg)
	if got, _ := c.Runs.Get("w2"); got.Status != StatusRunning || got.Where != "iiks1" {
		t.Fatalf("the second turn, a step later: %s %q where %q", got.Status, got.Reason, got.Where)
	}
}

// A turn the executor gave back before it started (exit 125, "dispatch: not
// run:") is no retry and ran nowhere: it keeps the messages it was handed,
// waits on the pace as a worker sent back does (Returned), and its next turn
// runs here, held while the machine is strained rather than handed over again.
func TestGivenBackTurnWaitsItsTurnHere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the turn")
	}
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB = 1 << 30
	if Strained(cfg.MemFloorMB, 0, 0) == "" {
		t.Skip("no /proc/meminfo here")
	}
	var here sandbox.Spec
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return elsewhereSB{agent: sleeper(t), spec: &here}, nil }
	asked, away := 0, sleeper(t)
	c.Runs.Executor = func() (sandbox.Sandbox, string) {
		asked++
		return elsewhereSB{agent: away, spec: new(sandbox.Spec)}, "iiks1"
	}
	r, err := c.Runs.Start(RunSpec{ID: "w", Role: "worker", Harness: "fake", Checkout: t.TempDir(), Away: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.Send("w", "a message for its first turn")
	r, _ = c.Runs.Get("w")
	c.Runs.launch(r, cfg)
	r, _ = c.Runs.Get("w")
	if r.Status != StatusRunning || r.Where != "iiks1" {
		t.Fatalf("handed over: %s %q where %q", r.Status, r.Reason, r.Where)
	}
	out := c.WS.TurnOut(r.ID, r.Turn)
	os.WriteFile(out+".err", []byte("dispatch: not run: iiks1 won't take it: full (3 of 3 turns running); the core runs it here\n"), 0o644)
	proc.Kill(away)
	c.Runs.endTurn(r)
	got, _ := c.Runs.Get("w")
	if got.Status != StatusParked || got.Retries != 0 || got.Where != "" || got.GaveBack != "iiks1" || !got.Returned ||
		!strings.Contains(got.Reason, "iiks1 gave its turn back before it started (iiks1 won't take it: full (3 of 3 turns running)") {
		t.Fatalf("given back: %+v", got)
	}
	if !slices.Contains(got.Pending, "a message for its first turn") {
		t.Errorf("the messages it was handed are lost: %q", got.Pending)
	}

	c.Runs.mu.Lock()
	c.Runs.handedAt = time.Time{}
	c.Runs.mu.Unlock()
	c.Runs.launch(got, cfg)
	if got, _ := c.Runs.Get("w"); got.Status == StatusRunning || !strings.HasPrefix(got.Reason, "waiting: machine low on memory") || asked != 1 {
		t.Fatalf("strained, a given-back turn: %s %q, executor asked %d times", got.Status, got.Reason, asked)
	}
	cfg.MemFloorMB = 0
	got, _ = c.Runs.Get("w")
	c.Runs.launch(got, cfg)
	got, _ = c.Runs.Get("w")
	if got.Status != StatusRunning || got.Where != "" || asked != 1 || got.GaveBack != "" {
		t.Fatalf("unstrained, a given-back turn: %s %q where %q, asked %d", got.Status, got.Reason, got.Where, asked)
	}
	if raw, _ := os.ReadFile(filepath.Join(c.WS.RunHost("w"), fmt.Sprintf("turn-%03d.prompt", got.Turn))); !strings.Contains(string(raw), "a message for its first turn") {
		t.Errorf("its turn here wasn't handed the message: %q", raw)
	}
}
