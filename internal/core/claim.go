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

// claimTurn claims turn of run id for this daemon. A refusal says who holds it.
func (rs *Runs) claimTurn(id string, turn int) error {
	return rs.withClaim(id, func(c turnClaim) (turnClaim, error) {
		if c.Daemon.PID > 0 && c.Turn >= turn && !sameProc(c.Daemon, rs.self) {
			if proc.Alive(c.Daemon) {
				return c, fmt.Errorf("daemon pid %d claimed turn %d", c.Daemon.PID, c.Turn)
			}
			if proc.Alive(c.Agent) {
				return c, fmt.Errorf("turn %d's process %d, started by a daemon that has gone, still runs", c.Turn, c.Agent.PID)
			}
		}
		return turnClaim{Turn: turn, Daemon: rs.self}, nil
	})
}

// claimed records the claimed turn's process, so a daemon taking over a
// dead one's claim doesn't start the turn again while it runs.
func (rs *Runs) claimed(id string, turn int, agent proc.Handle) {
	rs.withClaim(id, func(c turnClaim) (turnClaim, error) {
		if c.Turn != turn || !sameProc(c.Daemon, rs.self) {
			return c, fmt.Errorf("not ours")
		}
		c.Agent = agent
		return c, nil
	})
}

// withClaim reads the run's claim and writes what fn makes of it, all under
// the run's turn lock; an error from fn leaves the claim as it was.
func (rs *Runs) withClaim(id string, fn func(turnClaim) (turnClaim, error)) error {
	if err := os.MkdirAll(rs.ws.RunHost(id), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(rs.ws.RunHost(id), "turn.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockWait(f); err != nil {
		return err
	}
	defer proc.Unlock(f)
	var c turnClaim
	if raw, err := SafeRead(rs.claimFile(id), 4096); err == nil {
		json.Unmarshal(raw, &c)
	}
	c, err = fn(c)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(c)
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
