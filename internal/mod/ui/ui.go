// Package ui is the bus between a client steering the control center (the
// deck) and the control centers following it: POST /api/ui sends a command,
// GET /api/ui waits for the next, and each control center's own report of
// where it is comes back on the same GET (docs/ui-api.md). The daemon holds
// one Bus and routes to it; the wire types are api's, which the control
// center reads.
package ui

import (
	"context"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
)

// Bus holds the recent commands and the last view; its zero value is ready.
type Bus struct {
	mu   sync.Mutex
	seq  int64
	cmds []api.UICommand
	view *api.UIView
	wake chan struct{} // closed and replaced when a command comes
}

// keep is how many commands the bus keeps for a follower that fell behind.
const keep = 64

// Send queues a command for every control center following.
func (b *Bus) Send(c api.UICommand) (api.UICommand, error) {
	if err := c.Check(); err != nil {
		return c, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	c.Seq, c.At = b.seq, time.Now().UTC()
	if b.cmds = append(b.cmds, c); len(b.cmds) > keep {
		b.cmds = b.cmds[len(b.cmds)-keep:]
	}
	if b.wake != nil {
		close(b.wake)
		b.wake = nil
	}
	return c, nil
}

// Since returns the commands after seq, waiting up to wait (or until ctx
// ends) for one to come. A negative seq, or one past the newest (a
// daemon that restarted), returns at once with no commands and the newest
// seq, which a follower reads on from.
func (b *Bus) Since(ctx context.Context, after int64, wait time.Duration) api.UIState {
	end := time.NewTimer(wait)
	defer end.Stop()
	for {
		b.mu.Lock()
		st := api.UIState{Seq: b.seq, View: b.view, Commands: []api.UICommand{}}
		if after < 0 || after > b.seq {
			b.mu.Unlock()
			return st
		}
		for _, c := range b.cmds {
			if c.Seq > after {
				st.Commands = append(st.Commands, c)
			}
		}
		if len(st.Commands) > 0 || wait <= 0 {
			b.mu.Unlock()
			return st
		}
		if b.wake == nil {
			b.wake = make(chan struct{})
		}
		wake := b.wake
		b.mu.Unlock()
		select {
		case <-wake:
		case <-end.C:
			wait = 0
		case <-ctx.Done():
			wait = 0
		}
	}
}

// SetView records where a control center is.
func (b *Bus) SetView(v api.UIView) {
	v.At = time.Now().UTC()
	b.mu.Lock()
	b.view = &v
	b.mu.Unlock()
}
