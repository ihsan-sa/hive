package work

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Heavy work inside running turns is paced too. The workers' pace decides
// when a turn starts; what the turn then runs is the agent's. On 10-28
// ~19:20Z about ten hwde pytest suites (3 to 13 minutes each), a
// freerouting at ~200% cpu and a mech build ran at once inside workers'
// turns, beside the checks and the person's own sessions: load 110-137 on
// 20 cores, and kswapd busy. So a turn runs a heavy command (a test suite,
// a router, a build) as `hive agent heavy --what "..." -- cmd args`: it
// asks for a lease here and runs the command once it has one, renewing
// it while the command runs (heavyRenew) and giving it back when it ends.
// A lease is given when no other is held, so one always runs, or when
// fewer than the heavy pace's limit are held and nothing strains the
// machine. The limit is a pace like the checks' (it starts where they do
// and load alone cuts it), and a lease not renewed for heavyStale is gone,
// so a killed turn can't keep one. The limits are defaults the agent can
// weigh: the client runs the command anyway after heavyPatience, saying
// so, and an agent that judges a command light runs it bare.

// heavyStale is how long a lease lasts without a renewal; a client renews
// every heavyRenew.
const (
	heavyStale = 3 * time.Minute
	heavyRenew = time.Minute
)

// lease is one heavy command running in a turn.
type lease struct {
	Run, What string
	Since     time.Time
	last      time.Time
}

// heavyLeases is the leases held and when one was last refused.
type heavyLeases struct {
	mu      sync.Mutex
	held    map[string]*lease // by the client's lease id
	refused time.Time
}

// opHeavy asks for a lease (with what), renews one already running
// (running: it goes on whatever the machine says) or gives one back (done).
func (m *Module) opHeavy(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		ID, What      string
		Running, Done bool
	}
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, errors.New("a lease needs its id")
	}
	cfg := m.C.WS.LoadConfig()
	strain, watching := machine(cfg)
	return m.heavyAsk(time.Now(), r.ID, a.ID, a.What, a.Running, a.Done, strain, watching,
		cmp.Or(cfg.MaxChecks, core.DefaultMaxChecks()), cmp.Or(cfg.MaxChecks, safeChecks())), nil
}

// heavyAsk is opHeavy's answer at now, with strain and watching as the
// machine reads them and the pace's start and ceiling (the checks'):
// {"go": true}, or {"wait": why} with the commands running.
func (m *Module) heavyAsk(now time.Time, run, id, what string, running, done bool, strain string, watching bool, start, ceiling int) map[string]any {
	h := &m.heavyHeld
	h.mu.Lock()
	if h.held == nil {
		h.held = map[string]*lease{}
	}
	h.expire(now)
	key := run + "/" + id // ids are the client's; keep two runs' apart
	if done {
		delete(h.held, key)
		h.mu.Unlock()
		return map[string]any{"done": true}
	}
	if l := h.held[key]; l != nil || running {
		if l == nil { // a daemon restarted under it: it runs, so it counts
			l = &lease{Run: run, What: what, Since: now}
			h.held[key] = l
		}
		l.last = now
		h.mu.Unlock()
		return map[string]any{"go": true}
	}
	n, waiting := len(h.held), now.Sub(h.refused) < heavyStale
	h.mu.Unlock()

	before := m.heavy.get()
	limit := m.heavy.next(now, n, waiting, strain, watching, start, ceiling)
	m.journal("heavy", &m.heavy, before)

	h.mu.Lock()
	defer h.mu.Unlock()
	n = len(h.held)
	if n > 0 && (n >= limit || strain != "") {
		h.refused = now
		why := fmt.Sprintf("%d heavy commands run in turns now, at most %d at once", n, limit)
		if strain != "" {
			why = fmt.Sprintf("the machine is strained (%s) and %d heavy commands already run", strain, n)
		}
		return map[string]any{"wait": why, "running": h.whats()}
	}
	h.held[key] = &lease{Run: run, What: what, Since: now, last: now}
	m.C.J.MustAppend("heavy.started", map[string]any{"run": run, "what": what, "running": n + 1, "limit": limit})
	return map[string]any{"go": true}
}

// expire drops the leases not renewed for heavyStale. The caller holds h.mu.
func (h *heavyLeases) expire(now time.Time) {
	for k, l := range h.held {
		if now.Sub(l.last) > heavyStale {
			delete(h.held, k)
		}
	}
}

// whats lists the heavy commands running, run and what, oldest first. The
// caller holds h.mu.
func (h *heavyLeases) whats() []string {
	ls := make([]*lease, 0, len(h.held))
	for _, l := range h.held {
		ls = append(ls, l)
	}
	slices.SortFunc(ls, func(a, b *lease) int { return a.Since.Compare(b.Since) })
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = fmt.Sprintf("%s: %s (%s)", l.Run, cmp.Or(l.What, "a heavy command"), time.Since(l.Since).Round(time.Second))
	}
	return out
}

// Heavy is how many heavy commands run in turns now, for the status op.
func (m *Module) Heavy() int {
	h := &m.heavyHeld
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expire(time.Now())
	return len(h.held)
}
