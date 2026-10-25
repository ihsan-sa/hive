package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// A turn claim makes sure one process starts a run's turn, even if two
// daemons run on one workspace (the workspace lock should stop the second,
// but a turn run twice commits twice in one checkout). A daemon writes
// runs/<id>/host/turn.claim, naming itself and the turn, under a lock on
// turn.lock before it starts the turn; a claim by another live daemon for
// this turn or a later one makes it back off. A dead daemon's claim is taken
// over, unless the agent it started still runs.
type turnClaim struct {
	Turn   int         `json:"turn"`
	Daemon proc.Handle `json:"daemon"`
	Agent  proc.Handle `json:"agent"` // the turn's process, once started
}

// selfHandle is this process as a claimant.
func selfHandle() proc.Handle {
	return proc.Handle{PID: os.Getpid(), Started: proc.StartTime(os.Getpid())}
}

func sameProc(a, b proc.Handle) bool { return a.PID == b.PID && a.Started.Equal(b.Started) }

func (rs *Runs) claimFile(id string) string { return filepath.Join(rs.ws.RunHost(id), "turn.claim") }

// claimTurn claims turn of run id for this daemon. A refusal says who holds
// it; took names a gone daemon whose claim on this turn it took over, which
// means that daemon died between claiming the turn and journalling its start.
func (rs *Runs) claimTurn(id string, turn int) (took string, err error) {
	err = rs.withClaim(id, func(c turnClaim) (turnClaim, error) {
		if c.Daemon.PID > 0 && c.Turn >= turn && !sameProc(c.Daemon, rs.self) {
			if proc.Alive(c.Daemon) {
				return c, fmt.Errorf("daemon pid %d claimed turn %d", c.Daemon.PID, c.Turn)
			}
			if proc.Alive(c.Agent) {
				return c, fmt.Errorf("turn %d's process %d, started by a daemon that has gone, still runs", c.Turn, c.Agent.PID)
			}
			took = fmt.Sprintf("daemon pid %d, which has gone, claimed turn %d", c.Daemon.PID, c.Turn)
			if c.Agent.PID > 0 {
				took += fmt.Sprintf(" and started process %d, which has ended", c.Agent.PID)
			}
		}
		return turnClaim{Turn: turn, Daemon: rs.self}, nil
	})
	return took, err
}

// claimed records the claimed turn's process, so a daemon taking over a
// dead one's claim doesn't start the turn again while it runs.
// A failure is said in daemon.log: without the handle, a daemon taking over
// could start the turn again while this process runs.
func (rs *Runs) claimed(id string, turn int, agent proc.Handle) {
	err := rs.withClaim(id, func(c turnClaim) (turnClaim, error) {
		if c.Turn != turn || !sameProc(c.Daemon, rs.self) {
			return c, fmt.Errorf("the claim names daemon pid %d's turn %d", c.Daemon.PID, c.Turn)
		}
		c.Agent = agent
		return c, nil
	})
	if err != nil {
		fmt.Printf("hive: %s's turn %d: can't record its process %d in the turn claim: %v\n", id, turn, agent.PID, err)
	}
}

// withClaim reads the run's claim and writes what fn makes of it, all under
// the run's turn lock; an error from fn leaves the claim as it was. host/ is
// the daemon's alone, but a turn.lock that isn't a plain file is refused
// rather than followed, in case a mount ever lets a run write there.
func (rs *Runs) withClaim(id string, fn func(turnClaim) (turnClaim, error)) error {
	if err := os.MkdirAll(rs.ws.RunHost(id), 0o755); err != nil {
		return err
	}
	lock := filepath.Join(rs.ws.RunHost(id), "turn.lock")
	if fi, err := os.Lstat(lock); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a plain file", lock)
	}
	f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockWait(f); err != nil {
		return err
	}
	defer proc.Unlock(f)
	var c turnClaim
	// The claim is written whole (WriteFileAtomic), so one that won't parse
	// was written by something else: it is said, and replaced.
	if raw, err := SafeRead(rs.claimFile(id), 4096); err == nil {
		if err := json.Unmarshal(raw, &c); err != nil {
			fmt.Printf("hive: %s's turn claim can't be read (%v); it is replaced\n", id, err)
			c = turnClaim{}
		}
	}
	c, err = fn(c)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return WriteFileAtomic(rs.claimFile(id), raw, 0o600)
}

// lockWait waits up to a few seconds for f's lock: the claim is held only
// for a read and a write.
func lockWait(f *os.File) error {
	for i := 0; ; i++ {
		ok, err := proc.TryLock(f)
		if ok || err != nil {
			return err
		}
		if i == 100 {
			return fmt.Errorf("%s stays locked", f.Name())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
