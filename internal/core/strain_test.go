package core

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// fakeProc writes a /proc with this much available memory and pressure; the
// "full" memory line is always high, so a hold on it would show.
func fakeProc(t *testing.T, memKB, avg10, cpu10 string) string {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "pressure"), 0o755)
	os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal:       16000000 kB\nMemFree:          100000 kB\nMemAvailable:   "+memKB+" kB\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pressure", "memory"), []byte("some avg10="+avg10+" avg60=0.00 avg300=0.00 total=0\nfull avg10=99.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pressure", "cpu"), []byte("some avg10="+cpu10+" avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	return dir
}

func TestStrained(t *testing.T) {
	for _, c := range []struct {
		name, memKB, avg10, cpu10 string
		floor                     int
		mem, cpu                  float64
		want                      string
	}{
		{"healthy", "8000000", "1.50", "20.00", 1024, 10, 40, ""},
		{"low memory", "500000", "1.50", "20.00", 1024, 10, 40, "low on memory (488 MB free"},
		{"memory pressure", "8000000", "15.20", "20.00", 1024, 10, 40, "memory pressure (15.20%"},
		{"cpu pressure", "8000000", "1.50", "55.00", 1024, 10, 40, "cpu pressure (55.00%"},
		{"checks off", "500000", "55.20", "90.00", 0, 0, 0, ""},
	} {
		got := strained(fakeProc(t, c.memKB, c.avg10, c.cpu10), c.floor, c.mem, c.cpu)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := strained(t.TempDir(), 1024, 10, 40); got != "" {
		t.Errorf("no /proc files: %q, want no hold", got)
	}
}

// Pressure is read from the daemon's own cgroup where it has one: a sibling
// unit stalling inside its own cap raises the machine's reading, not the
// Hive's (10-28: every run held on 67% the daemon never felt).
func TestStrainedReadsTheHivesOwnCgroup(t *testing.T) {
	defer func(c string) { cgroupRoot = c }(cgroupRoot)
	cgroupRoot = t.TempDir()
	proc := fakeProc(t, "8000000", "67.00", "90.00") // the machine's, a sibling's stall in it
	os.MkdirAll(filepath.Join(proc, "self"), 0o755)
	own := filepath.Join(cgroupRoot, "user.slice", "hive.slice", "hive.service")
	os.MkdirAll(own, 0o755)
	os.WriteFile(filepath.Join(own, "memory.pressure"), []byte("some avg10=0.40 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	os.WriteFile(filepath.Join(own, "cpu.pressure"), []byte("some avg10=3.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	os.WriteFile(filepath.Join(proc, "self", "cgroup"), []byte("0::/user.slice/hive.slice/hive.service\n"), 0o644)
	if got := strained(proc, 1024, 10, 40); got != "" {
		t.Errorf("the Hive's own cgroup is calm: %q, want no hold", got)
	}
	os.WriteFile(filepath.Join(own, "memory.pressure"), []byte("some avg10=22.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	if got := strained(proc, 1024, 10, 40); !strings.Contains(got, "memory pressure (22.00%") {
		t.Errorf("the Hive's own cgroup stalls: %q, want its memory pressure", got)
	}
	// At the root cgroup, or with none mounted, it is the machine's.
	os.WriteFile(filepath.Join(proc, "self", "cgroup"), []byte("0::/\n"), 0o644)
	if got := strained(proc, 1024, 10, 40); !strings.Contains(got, "memory pressure (67.00%") {
		t.Errorf("no cgroup of its own: %q, want the machine's memory pressure", got)
	}
	if !watched(proc, 0, 10, 0, 0) {
		t.Error("the machine's pressure file is there to read")
	}
}

// A strain hold leaves a parked run's backoff and a nudged run's count alone,
// and journals once however the live figures move.
func TestStrainHoldKeepsBackoffAndNudges(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB = 1 << 30
	if Strained(cfg.MemFloorMB, 0, 0) == "" {
		t.Skip("no /proc/meminfo here")
	}
	for _, id := range []string{"parked", "nudged"} {
		c.J.MustAppend("run.created", RunSpec{ID: id, Harness: "fake"})
	}
	c.J.MustAppend("run.turn_ended", turnEnded{ID: "parked", Turn: 1, Status: StatusParked, ParkedUntil: time.Now().Add(-time.Minute)})
	c.J.MustAppend("run.turn_ended", turnEnded{ID: "nudged", Turn: 1, Status: StatusIdle, Reason: "nudged: ended a turn without finishing", Queue: []string{"finish"}})
	if p, _ := c.Runs.Get("parked"); p.Retries != 1 || p.ParkedUntil.IsZero() {
		t.Fatalf("parked run set up as %+v", p)
	}
	if n, _ := c.Runs.Get("nudged"); n.Nudges != 1 {
		t.Fatalf("nudged run set up as %+v", n)
	}
	for _, id := range []string{"parked", "nudged"} {
		before, _ := c.Runs.Get(id)
		c.Runs.launch(before, cfg)
		mid, _ := c.Runs.Get(id)
		c.Runs.launch(mid, cfg)
		after, _ := c.Runs.Get(id)
		if after.Status != before.Status || !after.ParkedUntil.Equal(before.ParkedUntil) || after.Retries != before.Retries || after.Nudges != before.Nudges {
			t.Errorf("%s: held from %+v to %+v", id, before, after)
		}
		if !strings.HasPrefix(after.Reason, "waiting: machine low on memory") {
			t.Errorf("%s: reason %q", id, after.Reason)
		}
	}
	held := 0
	c.J.Replay(func(e Event) {
		if e.Kind == "run.held" {
			held++
		}
	})
	if held != 2 {
		t.Errorf("%d run.held events for two runs held twice each, want 2", held)
	}
}

// New work waits while the 1-minute load per core is over load_max.
func TestLoaded(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loadavg"), []byte("181.20 104.00 90.00 30/6000 1234\n"), 0o644)
	if got := loaded(dir, 20, 1); !strings.Contains(got, "load 181 on 20 cores") || !strings.HasPrefix(got, LoadedReason) {
		t.Errorf("load 181 on 20 cores: %q", got)
	}
	if got := loaded(dir, 200, 1); got != "" {
		t.Errorf("load 181 on 200 cores: %q", got)
	}
	if got := loaded(dir, 20, 0); got != "" {
		t.Errorf("off: %q", got)
	}
	if got := loaded(t.TempDir(), 20, 1); got != "" {
		t.Errorf("no /proc/loadavg: %q", got)
	}
}

// A lead's turn starts under the pressure and the memory floor that hold a
// worker's: a person's message is answered however busy the machine is. Only
// under the absolute floor (LeadMemFloorMB) does a lead wait, saying why once.
func TestLeadStartsUnderStrainThatHoldsWorker(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax = 1024, 10, 80
	defer func(p string) { procRoot = p }(procRoot)
	procRoot = fakeProc(t, "600000", "1.50", "97.02") // 586 MB free and cpu at 97%: twice over a worker's limits
	t.Cleanup(func() { endTurns(t, c, cfg) })
	for _, s := range []RunSpec{
		{ID: "worker", Role: "worker", Harness: "fake"},
		{ID: "hive-lead", Role: "lead", Harness: "fake", Persistent: true},
		{ID: "project-lead", Role: "lead", Project: "demo", Harness: "fake", Persistent: true},
	} {
		if _, err := c.Runs.Start(s); err != nil {
			t.Fatal(err)
		}
		r, _ := c.Runs.Get(s.ID)
		c.Runs.launch(r, cfg)
	}
	if w, _ := c.Runs.Get("worker"); w.Status != StatusQueued || !strings.HasPrefix(w.Reason, "waiting: machine low on memory") {
		t.Errorf("worker under strain: %s %q, want queued and waiting", w.Status, w.Reason)
	}
	for _, id := range []string{"hive-lead", "project-lead"} {
		if l, _ := c.Runs.Get(id); l.Status != StatusRunning || l.Turn != 1 {
			t.Errorf("%s under strain: %s turn %d %q, want its turn running", id, l.Status, l.Turn, l.Reason)
		}
	}
	// Under the absolute floor the lead waits, keeping its status, and the
	// journal says why once however the figure moves.
	for _, free := range []string{"100000", "120000"} {
		procRoot = fakeProc(t, free, "0.00", "0.00")
		if _, err := c.Runs.Start(RunSpec{ID: "late-lead-" + free, Role: "lead", Harness: "fake", Persistent: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, free := range []string{"100000", "120000"} {
		procRoot = fakeProc(t, free, "0.00", "0.00")
		r, _ := c.Runs.Get("late-lead-100000")
		c.Runs.launch(r, cfg)
	}
	l, _ := c.Runs.Get("late-lead-100000")
	if l.Status != StatusQueued || !strings.HasPrefix(l.Reason, "waiting: machine low on memory (98 MB free, floor 256 MB)") {
		t.Errorf("lead under the absolute floor: %s %q, want queued and waiting on the 256 MB floor", l.Status, l.Reason)
	}
	held := 0
	c.J.Replay(func(e Event) {
		if e.Kind == "run.held" && strings.Contains(string(e.Data), "late-lead-100000") {
			held++
		}
	})
	if held != 1 {
		t.Errorf("%d run.held events for a lead held twice, want 1", held)
	}
}

// Each role's processes run at their place: the Hive-wide lead highest, a
// project's lead under it, every other run under both, checks under all.
func TestNiceByRole(t *testing.T) {
	hive := Run{RunSpec: RunSpec{Role: "lead"}}
	project := Run{RunSpec: RunSpec{Role: "lead", Project: "demo"}}
	if hive.Nice() != 0 || !hive.Lead() || !project.Lead() {
		t.Errorf("hive-wide lead nice %d, want 0 and both leads", hive.Nice())
	}
	if project.Nice() <= hive.Nice() {
		t.Errorf("project lead nice %d, want under the hive-wide lead's %d", project.Nice(), hive.Nice())
	}
	for _, role := range []string{"worker", "reviewer", "security", "warden", "tribunal", "sweep"} {
		r := Run{RunSpec: RunSpec{Role: role, Project: "demo"}}
		if r.Lead() || r.Nice() <= project.Nice() || r.Nice() >= CheckNice {
			t.Errorf("%s nice %d, want between a lead's %d and a check's %d", role, r.Nice(), project.Nice(), CheckNice)
		}
	}
}

// A tick steps the leads first, so a lead's due turn starts ahead of a
// worker's queued at the same time.
func TestTickStartsLeadsFirst(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	t.Cleanup(func() { endTurns(t, c, cfg) })
	for _, s := range []RunSpec{
		{ID: "worker", Role: "worker", Harness: "fake"},
		{ID: "lead", Role: "lead", Harness: "fake", Persistent: true},
	} {
		if _, err := c.Runs.Start(s); err != nil {
			t.Fatal(err)
		}
	}
	c.Runs.Tick(cfg)
	var lead, worker Run
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		lead, _ = c.Runs.Get("lead")
		worker, _ = c.Runs.Get("worker")
		if lead.Turn == 1 && worker.Turn == 1 {
			break
		}
	}
	if lead.Turn != 1 || worker.Turn != 1 {
		t.Fatalf("after a tick: lead %s turn %d, worker %s turn %d", lead.Status, lead.Turn, worker.Status, worker.Turn)
	}
	if worker.TurnStarted.Before(lead.TurnStarted) {
		t.Errorf("worker started %s before the lead %s", worker.TurnStarted, lead.TurnStarted)
	}
}

// endTurns ends the fake turns a test started, ticking so each is recorded,
// before the hive's temp dir goes.
func endTurns(t *testing.T, c *Core, cfg Config) {
	t.Helper()
	c.Runs.HoldFor("test", "ending the test's turns") // no queued run starts meanwhile
	for _, r := range c.Runs.List(func(r *Run) bool { return r.Status == StatusRunning }) {
		c.Runs.Interrupt(r.ID)
	}
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		c.Runs.Tick(cfg)
		if len(c.Runs.List(func(r *Run) bool { return r.Status == StatusRunning })) == 0 {
			return
		}
	}
	t.Log("fake turns still running at the end")
}

// While a lead's turn is being started, another role's launch waits for the
// next tick: a lead's turn has its process before a worker's starts.
func TestWorkerLaunchWaitsForLeadLaunch(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	t.Cleanup(func() { endTurns(t, c, cfg) })
	if _, err := c.Runs.Start(RunSpec{ID: "worker", Role: "worker", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	c.Runs.mu.Lock()
	c.Runs.leadLaunching = 1
	c.Runs.mu.Unlock()
	w, _ := c.Runs.Get("worker")
	c.Runs.launch(w, cfg)
	if w, _ = c.Runs.Get("worker"); w.Status != StatusQueued || w.Turn != 0 {
		t.Fatalf("worker launched while a lead's turn was starting: %s turn %d", w.Status, w.Turn)
	}
	c.Runs.mu.Lock()
	c.Runs.leadLaunching = 0
	c.Runs.mu.Unlock()
	c.Runs.launch(w, cfg)
	if w, _ = c.Runs.Get("worker"); w.Status != StatusRunning || w.Turn != 1 {
		t.Fatalf("worker after the lead's turn started: %s turn %d, want running", w.Status, w.Turn)
	}
}

// Ticks come every second and a lead's step can take longer (its ops drain
// one at a time, a launch picks a login and starts a sandbox), so ticks
// overlap. Each run is still stepped as it is at the time, never from an
// older tick's view: a queued worker's turn starts once, and ends once.
func TestOverlappingTicksStepEachRunOnce(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	t.Cleanup(func() { endTurns(t, c, cfg) })
	if _, err := c.Runs.Start(RunSpec{ID: "lead", Role: "lead", Harness: "fake", Persistent: true}); err != nil {
		t.Fatal(err)
	}
	l, _ := c.Runs.Get("lead")
	c.Runs.launch(l, cfg)
	if l, _ = c.Runs.Get("lead"); l.Status != StatusRunning {
		t.Fatalf("lead %s, want running", l.Status)
	}
	// The lead's next steps drain an op held open until three ticks have
	// come and gone behind it.
	// Each op takes its gate from gates, so the op's goroutine is handed the
	// one slowStep made, never reading a variable the test writes.
	gates := make(chan chan struct{}, 1)
	var gate chan struct{}
	c.Runs.ops.Register(Op{Name: "slow", Fn: func(Run, json.RawMessage) (any, error) { <-<-gates; return nil, nil }})
	slowStep := func(n string) {
		gate = make(chan struct{})
		gates <- gate
		os.WriteFile(filepath.Join(c.WS.RunBox("lead"), "outbox", n+".json"), []byte(`{"id":"`+n+`","op":"slow"}`), 0o644)
	}
	count := func(kind, id string) int {
		n := 0
		c.J.Replay(func(e Event) {
			if e.Kind == kind && strings.Contains(string(e.Data), `"id":"`+id+`"`) {
				n++
			}
		})
		return n
	}
	// settled: no pass over the other runs waits, and no step or launch is
	// still going; a fixed sleep for that fails on a loaded machine, where a
	// launch can outlast it.
	settled := func() {
		t.Helper()
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			c.Runs.mu.Lock()
			done := !c.Runs.passing && len(c.Runs.busy) == 0 && c.Runs.launching == 0
			c.Runs.mu.Unlock()
			if done {
				return
			}
		}
		t.Fatal("the ticks' steps are still going after 30s")
	}
	ticks := func() {
		for i := 0; i < 3; i++ {
			c.Runs.Tick(cfg)
			time.Sleep(50 * time.Millisecond)
		}
		close(gate)
		settled()
	}
	if _, err := c.Runs.Start(RunSpec{ID: "worker", Role: "worker", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	slowStep("one")
	ticks()
	if n := count("run.turn_started", "worker"); n != 1 {
		t.Errorf("the worker's turn started %d times across overlapping ticks, want 1", n)
	}
	w, _ := c.Runs.Get("worker")
	if w.Turn != 1 || w.Status != StatusRunning {
		t.Fatalf("worker %s on turn %d, want running on turn 1", w.Status, w.Turn)
	}
	// Its turn, ended while ticks overlap, is recorded once. The fake
	// worker finishes on its own, so by now its process may be gone already
	// (it is on Windows); killing it is only a nudge. Only a tick sees the
	// end, so the overlapping ticks wait for the process to be gone: on a
	// loaded machine a killed process can outlive three ticks, and then no
	// tick would come to end its turn.
	c.Runs.Interrupt("worker")
	for end := time.Now().Add(10 * time.Second); proc.Alive(w.Proc) && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	if proc.Alive(w.Proc) {
		t.Fatal("the worker's process is still alive 10s after it was killed")
	}
	slowStep("two")
	ticks()
	if w, _ := c.Runs.Get("worker"); w.Status == StatusRunning {
		t.Fatalf("the worker's turn never ended: %s %q", w.Status, w.Reason)
	}
	if n := count("run.turn_ended", "worker"); n != 1 {
		t.Errorf("the worker's turn ended %d times across overlapping ticks, want 1", n)
	}
}

// A lead's step that hangs holds the other runs up only for one wait, and
// daemon.log names it once a stall: a later tick can't claim the hung lead,
// so its pass has nothing to wait for and doesn't say it again.
func TestHungLeadStepIsSaidOnce(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	t.Cleanup(func() { endTurns(t, c, cfg) })
	c.Runs.leadWait = 100 * time.Millisecond
	var logged bytes.Buffer
	var logMu sync.Mutex
	log.SetOutput(writerFunc(func(p []byte) (int, error) { logMu.Lock(); defer logMu.Unlock(); return logged.Write(p) }))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	said := func() int {
		logMu.Lock()
		defer logMu.Unlock()
		return strings.Count(logged.String(), "a lead's step is still going after")
	}
	if _, err := c.Runs.Start(RunSpec{ID: "lead", Role: "lead", Harness: "fake", Persistent: true}); err != nil {
		t.Fatal(err)
	}
	l, _ := c.Runs.Get("lead")
	c.Runs.launch(l, cfg)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) }) // before the turns end (cleanups run last first)
	c.Runs.ops.Register(Op{Name: "hang", Fn: func(Run, json.RawMessage) (any, error) { <-gate; return nil, nil }})
	os.WriteFile(filepath.Join(c.WS.RunBox("lead"), "outbox", "hang.json"), []byte(`{"id":"hang","op":"hang"}`), 0o644)
	if _, err := c.Runs.Start(RunSpec{ID: "worker", Role: "worker", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		c.Runs.Tick(cfg)
		if w, _ := c.Runs.Get("worker"); w.Turn == 1 && said() > 0 {
			break
		}
	}
	if w, _ := c.Runs.Get("worker"); w.Turn != 1 {
		t.Fatalf("the worker's turn didn't start behind a hung lead step: %s turn %d", w.Status, w.Turn)
	}
	for i := 0; i < 5; i++ { // five more ticks while the same step hangs
		c.Runs.Tick(cfg)
		time.Sleep(150 * time.Millisecond)
	}
	if n := said(); n != 1 {
		t.Errorf("the hung lead's step was said %d times in daemon.log, want once:\n%s", n, logged.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// A machine with none of the readings (no /proc, as on macOS or Windows) is
// watched by none of them, whatever the limits say; a reading counts only
// when its limit is on and its file is there.
func TestWatched(t *testing.T) {
	if watched(t.TempDir(), 1024, 10, 40, 1) {
		t.Error("no readings to read, yet watched")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "loadavg"), []byte("1.00 1.00 1.00 1/100 1\n"), 0o644)
	if !watched(dir, 1024, 10, 40, 1) {
		t.Error("loadavg is there and load_max on, yet not watched")
	}
	if watched(dir, 1024, 10, 40, 0) {
		t.Error("only loadavg is there and load_max is off, yet watched")
	}
}

// When a strain hold lifts, the turns it held start one a rampStep, not all
// at once: on 10-28 the lift started every held run together, and the Hive's
// slice ran to load near 400 and an OOM kill. A lead isn't ramped.
func TestHoldLiftsOneTurnAtATime(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB = 1024
	defer func(p string) { procRoot = p }(procRoot)
	procRoot = fakeProc(t, "600000", "0.00", "0.00") // under the floor
	t.Cleanup(func() { endTurns(t, c, cfg) })
	ids := []string{"w1", "w2", "w3"}
	for _, id := range ids {
		if _, err := c.Runs.Start(RunSpec{ID: id, Role: "worker", Harness: "fake"}); err != nil {
			t.Fatal(err)
		}
		r, _ := c.Runs.Get(id)
		c.Runs.launch(r, cfg)
	}
	procRoot = fakeProc(t, "8000000", "0.00", "0.00") // the hold lifts
	launch := func(id string) Run {
		r, _ := c.Runs.Get(id)
		c.Runs.launch(r, cfg)
		r, _ = c.Runs.Get(id)
		return r
	}
	running := func() (n int) {
		for _, id := range ids {
			if r, _ := c.Runs.Get(id); r.Status == StatusRunning {
				n++
			}
		}
		return n
	}
	for _, id := range ids {
		launch(id)
	}
	if n := running(); n != 1 {
		t.Fatalf("%d turns started as the hold lifted, want 1", n)
	}
	if r, _ := c.Runs.Get("w3"); r.Status != StatusQueued || !strings.Contains(r.Reason, "one turn at a time") {
		t.Errorf("w3 after the lift: %s %q, want waiting on the ramp", r.Status, r.Reason)
	}
	if _, err := c.Runs.Start(RunSpec{ID: "lead", Role: "lead", Harness: "fake", Persistent: true}); err != nil {
		t.Fatal(err)
	}
	if l := launch("lead"); l.Status != StatusRunning {
		t.Errorf("a lead on the ramp: %s %q, want its turn running", l.Status, l.Reason)
	}
	c.Runs.mu.Lock()
	c.Runs.rampLast = c.Runs.rampLast.Add(-rampStep) // a step on
	c.Runs.mu.Unlock()
	for _, id := range ids {
		launch(id)
	}
	if n := running(); n != 2 {
		t.Errorf("%d turns running a step after the lift, want 2", n)
	}
	// Once none has waited on it for two steps, the ramp is over.
	c.Runs.mu.Lock()
	c.Runs.heldAt, c.Runs.rampedAt = time.Now().Add(-3*rampStep), time.Now().Add(-3*rampStep)
	c.Runs.mu.Unlock()
	if r := launch("w3"); r.Status != StatusRunning {
		t.Errorf("w3 after the ramp: %s %q, want its turn running", r.Status, r.Reason)
	}
}

// A run reopened after its turn ended is Returned until a turn starts, and
// Admit, a module's say, holds it with its reason.
func TestAdmitHoldsAReturnedRun(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	t.Cleanup(func() { endTurns(t, c, cfg) })
	if _, err := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	c.Runs.SetStatus("w1", StatusDone, "finished")
	if err := c.Runs.Reopen("w1", "main moved"); err != nil {
		t.Fatal(err)
	}
	r, _ := c.Runs.Get("w1")
	if !r.Returned {
		t.Fatalf("reopened after it ended: %+v, want Returned", r)
	}
	full := true
	c.Runs.Admit = func(r Run) string {
		if r.Returned && full {
			return "waiting for a worker (at the pace)"
		}
		return ""
	}
	c.Runs.launch(r, cfg)
	if r, _ = c.Runs.Get("w1"); r.Status != StatusIdle || r.Reason != "waiting for a worker (at the pace)" {
		t.Errorf("held by Admit: %s %q", r.Status, r.Reason)
	}
	full = false
	c.Runs.launch(r, cfg)
	if r, _ = c.Runs.Get("w1"); r.Status != StatusRunning || r.Returned {
		t.Errorf("let through: %s returned %v, want its turn running and no longer Returned", r.Status, r.Returned)
	}
}
