package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Every turn but a lead's takes a slot, whatever its role, and so does every
// turn away on an executor's runner: past the slots here a turn goes away
// only while the turns away are fewer than the slots, and otherwise waits
// without asking. A lead's turn starts with every slot in use.
func TestSlotsCountEveryTurnButLeads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the turn")
	}
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB, cfg.Slots = 0, 2
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return elsewhereSB{agent: sleeper(t), spec: new(sandbox.Spec)}, nil }
	asked := 0
	c.Runs.Executor = func() (sandbox.Sandbox, string) {
		asked++
		return elsewhereSB{agent: sleeper(t), spec: new(sandbox.Spec)}, "iiks1"
	}
	c.J.MustAppend("run.created", RunSpec{ID: "here", Role: "sweep", Harness: "fake"})
	c.J.MustAppend("run.turn_started", turnStarted{ID: "here", Turn: 1})
	c.J.MustAppend("run.created", RunSpec{ID: "away", Role: "worker", Harness: "fake"})
	c.J.MustAppend("run.turn_started", turnStarted{ID: "away", Turn: 1, Where: "iiks1"})
	start := func(id, role string) Run {
		t.Helper()
		r, err := c.Runs.Start(RunSpec{ID: id, Role: role, Harness: "fake", Checkout: t.TempDir(), Persistent: role == "lead"})
		if err != nil {
			t.Fatal(err)
		}
		c.Runs.launch(r, cfg)
		got, _ := c.Runs.Get(id)
		return got
	}
	if r := start("reviewer", "reviewer"); r.Status != StatusRunning || r.Where != "" || asked != 0 {
		t.Fatalf("a turn with a slot free here: %s %q where %q, executor asked %d", r.Status, r.Reason, r.Where, asked)
	}
	if r := start("warden", "warden"); r.Status != StatusRunning || r.Where != "iiks1" || asked != 1 {
		t.Fatalf("a turn past the slots with one away: %s %q where %q, executor asked %d", r.Status, r.Reason, r.Where, asked)
	}
	if r := start("worker", "worker"); r.Status == StatusRunning || r.Reason != "waiting for a slot (all 2 in use; slots in config.json)" || asked != 1 {
		t.Fatalf("a turn past the slots here and away: %s %q, executor asked %d", r.Status, r.Reason, asked)
	}
	if r := start("lead", "lead"); r.Status != StatusRunning || r.Where != "" {
		t.Fatalf("a lead with every slot in use: %s %q where %q", r.Status, r.Reason, r.Where)
	}
	if here, away := c.Runs.InUse(); here != 2 || away != 2 {
		t.Errorf("in use: %d here, %d away; want 2 and 2 (the lead's turn takes none)", here, away)
	}
}

// The default slots are what the machine's memory carries at TurnMB a turn
// past the reserve, never under minSlots, and minSlots where memory can't be
// read.
func TestDefaultSlotsFromMemory(t *testing.T) {
	for _, c := range []struct {
		totalKB string
		want    int
	}{{"31918856", 7}, {"65536000", 18}, {"8000000", minSlots}} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal:       "+c.totalKB+" kB\nMemAvailable:   1000 kB\n"), 0o644)
		if got := defaultSlots(dir); got != c.want {
			t.Errorf("%s kB: %d slots, want %d", c.totalKB, got, c.want)
		}
	}
	if got := defaultSlots(t.TempDir()); got != minSlots {
		t.Errorf("no meminfo: %d, want %d", got, minSlots)
	}
	if got := (Config{Slots: 3}).TurnSlots(); got != 3 {
		t.Errorf("slots set to 3: %d", got)
	}
}

// A freed slot goes to the run first in line, not the one that asks first:
// a worker sent back to its task, then the oldest; one that stops asking
// holds its place only until slotSeen passes.
func TestSlotGoesInLine(t *testing.T) {
	now := time.Now()
	rs := &Runs{}
	old := Run{RunSpec: RunSpec{ID: "w-old"}, Created: now.Add(-3 * time.Hour)}
	back := Run{RunSpec: RunSpec{ID: "w-back"}, Created: now.Add(-2 * time.Hour), Returned: true}
	fresh := Run{RunSpec: RunSpec{ID: "w-new"}, Created: now.Add(-time.Minute)}
	for _, r := range []Run{fresh, old, back} { // all full: each waits
		if rs.inLine(r, 0, now) {
			t.Fatalf("%s took a slot with none free", r.ID)
		}
	}
	if rs.inLine(fresh, 1, now.Add(time.Second)) || rs.inLine(old, 1, now.Add(time.Second)) {
		t.Fatal("one slot went past the worker sent back")
	}
	if !rs.inLine(back, 1, now.Add(time.Second)) {
		t.Fatal("the worker sent back didn't take the free slot")
	}
	if rs.inLine(fresh, 1, now.Add(2*time.Second)) || !rs.inLine(old, 1, now.Add(2*time.Second)) {
		t.Fatal("the next slot didn't go to the oldest")
	}
	rs.inLine(old, 0, now.Add(3*time.Second)) // w-old waits again, then stops asking
	if rs.inLine(fresh, 1, now.Add(4*time.Second)) {
		t.Fatal("w-new went ahead of w-old, which asked lately")
	}
	if !rs.inLine(fresh, 1, now.Add(slotSeen+4*time.Second)) {
		t.Error("w-new still waits on w-old, which stopped asking")
	}
}
