package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// claimSB counts the turns it starts, each as the process agent.
type claimSB struct {
	sandbox.Bare
	n     *atomic.Int32
	agent proc.Handle
}

func (s claimSB) Start(sandbox.Spec) (sandbox.Proc, error) { s.n.Add(1); return s.agent, nil }

// sleeper is a live process that isn't this one, standing in for another
// daemon or a turn's agent.
func sleeper(t *testing.T) proc.Handle {
	t.Helper()
	h, err := proc.StartDetached([]string{"sleep", "60"}, os.Environ(), t.TempDir(), "", filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Kill(h) })
	return h
}

// Two daemons racing on one workspace, each with its own journal handle and
// view of the runs, start a run's turn once: the second backs off on the
// first's claim, and keeps backing off while that daemon lives.
func TestTwoDaemonsStartATurnOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the other daemon")
	}
	a, forge := testHive(t)
	var n atomic.Int32
	a.Runs.sandbox = func() (sandbox.Sandbox, error) { return claimSB{n: &n}, nil }
	var runs []Run
	for i := range 20 {
		r, err := a.Runs.Start(RunSpec{ID: fmt.Sprintf("w%d", i), Role: "worker", Harness: "fake", Checkout: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	b, err := Open(a.WS, forge) // the second daemon replays the same journal
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.Runs.sandbox = a.Runs.sandbox
	b.Runs.self = sleeper(t)
	cfg := a.WS.LoadConfig()
	for _, r := range runs {
		var wg sync.WaitGroup
		for _, rs := range []*Runs{a.Runs, b.Runs} {
			wg.Add(1)
			go func() { defer wg.Done(); rs.launch(r, cfg) }()
		}
		wg.Wait()
	}
	if got := n.Load(); got != int32(len(runs)) {
		t.Fatalf("%d turns started for %d runs' first turns, want each once", got, len(runs))
	}
	// Neither daemon sees the other's journal: the one that lost keeps
	// trying the turn and keeps backing off.
	for _, r := range runs {
		for _, rs := range []*Runs{a.Runs, b.Runs} {
			if got, _ := rs.Get(r.ID); got.Turn == 0 {
				rs.launch(got, cfg)
			}
		}
	}
	if got := n.Load(); got != int32(len(runs)) {
		t.Fatalf("a retry started a claimed turn again: %d starts for %d turns", got, len(runs))
	}
	// Each daemon that backed off says so once in the record, not per turn.
	said := 0
	for _, e := range a.J.Since(0) {
		var al map[string]string
		if e.Kind == "alert.raised" && e.Decode(&al) == nil && al["ref"] == "turn-claim" {
			said++
		}
	}
	if said < 1 || said > 2 {
		t.Errorf("%d back-off alerts for two daemons, want one each that backed off", said)
	}
}

// A dead daemon's claim is taken over, but not while the turn it started
// still runs; this daemon starting its own claimed turn again (after a
// failed start) is no second claimant.
func TestATurnClaimOutlivesItsDaemonOnlyWhileTheTurnRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for the agent")
	}
	c, _ := testHive(t)
	rs := c.Runs
	if _, err := rs.claimTurn("w1", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.claimTurn("w1", 3); err != nil {
		t.Fatalf("this daemon's own claim refused it: %v", err)
	}
	dead := proc.Handle{PID: 1 << 30}
	agent := sleeper(t)
	write := func(cl turnClaim) {
		rs.withClaim("w1", func(turnClaim) (turnClaim, error) { return cl, nil })
	}
	write(turnClaim{Turn: 3, Daemon: dead, Agent: agent})
	if _, err := rs.claimTurn("w1", 3); err == nil {
		t.Fatal("a turn whose process still runs was claimed again")
	}
	if _, err := rs.claimTurn("w1", 4); err != nil {
		t.Fatalf("the next turn: %v", err)
	}
	write(turnClaim{Turn: 3, Daemon: dead, Agent: proc.Handle{PID: 1 << 30}})
	if took, err := rs.claimTurn("w1", 3); err != nil {
		t.Fatalf("a dead daemon's ended turn wasn't taken over: %v", err)
	} else if !strings.Contains(took, "claimed turn 3") {
		t.Errorf("the takeover isn't named: %q", took)
	}
	other := sleeper(t)
	write(turnClaim{Turn: 5, Daemon: other})
	if _, err := rs.claimTurn("w1", 4); err == nil {
		t.Fatal("a live daemon's claim on a later turn was taken")
	}
}

// A claim that won't parse is replaced, not read as held; a turn.lock that
// isn't a plain file is refused rather than followed.
func TestATurnClaimRefusesALinkedLock(t *testing.T) {
	c, _ := testHive(t)
	rs := c.Runs
	os.MkdirAll(rs.ws.RunHost("w1"), 0o755)
	os.WriteFile(rs.claimFile("w1"), []byte("{not json"), 0o600)
	if _, err := rs.claimTurn("w1", 1); err != nil {
		t.Fatalf("a corrupt claim held the turn: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	lock := filepath.Join(rs.ws.RunHost("w2"), "turn.lock")
	os.MkdirAll(filepath.Dir(lock), 0o755)
	target := filepath.Join(t.TempDir(), "planted")
	if err := os.Symlink(target, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.claimTurn("w2", 1); err == nil {
		t.Error("a symlinked turn.lock was followed")
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("the claim created a file through the link")
	}
}
