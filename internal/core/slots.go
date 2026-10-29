package core

import (
	"runtime"
	"slices"
	"sync"
	"time"
)

// Slots lets at most a few things run at once and starts the rest in the
// order they asked, so a burst of checks waits its turn instead of piling
// onto the machine together; a higher rank (Prioritize) goes ahead of that
// line, but never past one that has waited slotPatience unless it ranks
// urgent. Within a rank the one expected to be shortest (what its project's
// last runs of its kind took) starts first, and a quick one goes past even a
// waiter out of patience: a check of seconds waited ~50 minutes behind 30
// checks of ten minutes and more (issue #1933), and starting it first costs
// the rest only its seconds. Its size is read at every hand-off, so a changed
// setting holds from the next start; one already running is never stopped.
type Slots struct {
	size     func() int // <= 0: no limit
	rank     func(project, sha, kind string) int
	urgent   int // > 0: a rank this high goes first even past the patience
	patience time.Duration

	mu      sync.Mutex
	running []SlotUse
	waiting []slotWait
	took    map[string]time.Duration // project and kind: what a run of it takes, learnt as each ends
}

// SlotUse is one thing holding or waiting for a slot.
type SlotUse struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`
	Kind    string    `json:"kind"`
	Since   time.Time `json:"since"` // when it started, or began waiting
	Rank    int       `json:"rank,omitempty"`
	// Expect is what a run of it is expected to take, from its project's
	// last runs of its kind; 0 is not known yet.
	Expect time.Duration `json:"expect,omitempty"`
	id     int64
}

type slotWait struct {
	SlotUse
	ready chan bool // true: started; false: dropped from the line (Drop)
}

var slotSeq int64

// slotPatience is how long a waiter can be passed over by higher ranks: a
// steady stream of ranked checks would otherwise keep a rank-0 one waiting
// for good.
const slotPatience = time.Hour

// quickSlot is a run short enough to go past a waiter out of patience: it
// delays that one by no more than this.
const quickSlot = 2 * time.Minute

// Quick says whether u is expected to be short enough to go past the line.
func (u SlotUse) Quick() bool { return u.Expect > 0 && u.Expect <= quickSlot }

func tookKey(project, kind string) string { return project + "\x00" + kind }

// NewSlots makes a queue whose size is read from size.
func NewSlots(size func() int) *Slots { return &Slots{size: size, patience: slotPatience} }

// Resize reads the size from size from now on, and starts what now has room.
func (s *Slots) Resize(size func() int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size = size
	s.fill()
}

// Prioritize ranks each waiter from now on: a higher rank starts first, and
// equal ranks the shortest expected first, then in the order they asked. A rank of urgent or more (when > 0)
// goes first even past a waiter out of patience, so it must be rare enough
// never to starve the line. rank is called without the slots' lock, as a
// check asks and again at every Fill, so it may read what it likes but
// never calls back into the slots.
func (s *Slots) Prioritize(rank func(project, sha, kind string) int, urgent int) {
	s.mu.Lock()
	s.rank, s.urgent = rank, urgent
	s.mu.Unlock()
	s.Fill()
}

// Fill ranks the waiters again and starts what has room, for a size that
// grew or a rank that rose since the last hand-off.
func (s *Slots) Fill() {
	s.mu.Lock()
	rank, waiting := s.rank, append([]slotWait(nil), s.waiting...)
	s.mu.Unlock()
	ranks := map[int64]int{}
	if rank != nil {
		for _, w := range waiting {
			ranks[w.id] = rank(w.Project, w.SHA, w.Kind)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.waiting {
		if r, ok := ranks[s.waiting[i].id]; ok {
			s.waiting[i].Rank = r
		}
	}
	s.fill()
}

// Acquire waits for a slot, by rank, expected length and then in order, and
// returns its release.
func (s *Slots) Acquire(project, sha, kind string) (release func()) {
	release, _ = s.Take(project, sha, kind)
	return release
}

// Take is Acquire, reporting false, with a release that does nothing, when
// the wait was dropped (Drop) before a slot came.
func (s *Slots) Take(project, sha, kind string) (release func(), ok bool) {
	release, ok, _ = s.TakeOr(project, sha, kind, nil, 0)
	return release, ok
}

// TakeOr is Take that, while it waits, asks leave every so often whether to
// go without a slot (it runs elsewhere): if so it leaves the line and reports
// left, with ok false and a release that does nothing. Once leave says yes it
// is asked no more, even if a slot came first.
func (s *Slots) TakeOr(project, sha, kind string, leave func() bool, every time.Duration) (release func(), ok, left bool) {
	s.mu.Lock()
	rank := s.rank
	s.mu.Unlock()
	r := 0
	if rank != nil {
		r = rank(project, sha, kind)
	}
	s.mu.Lock()
	slotSeq++
	w := slotWait{SlotUse{Project: project, SHA: sha, Kind: kind, Since: time.Now().UTC(), Rank: r, Expect: s.took[tookKey(project, kind)], id: slotSeq}, make(chan bool, 1)}
	s.waiting = append(s.waiting, w)
	s.fill()
	s.mu.Unlock()
	var tick <-chan time.Time
	if leave != nil && every > 0 {
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	var started bool
wait:
	for {
		select {
		case started = <-w.ready:
			break wait
		case <-tick:
			if !leave() {
				continue
			}
			s.mu.Lock()
			n := len(s.waiting)
			s.waiting = slices.DeleteFunc(s.waiting, func(o slotWait) bool { return o.id == w.id })
			gone := len(s.waiting) < n
			s.mu.Unlock()
			if gone {
				return func() {}, false, true
			}
			// It started (or was dropped) as leave was asked: take that,
			// and ask leave no more.
			started = <-w.ready
			break wait
		}
	}
	if !started {
		return func() {}, false, false
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if i := slices.IndexFunc(s.running, func(u SlotUse) bool { return u.id == w.id }); i >= 0 {
				s.learn(s.running[i], time.Since(s.running[i].Since))
				s.running = slices.Delete(s.running, i, i+1)
			}
			s.fill()
		})
	}, true, false
}

// Drop takes what waits for a slot on sha in project out of the line, so a
// check nobody wants any more (its pull request closed) doesn't hold a place
// or run once its turn comes; one already running goes on. It returns how
// many it dropped.
func (s *Slots) Drop(project, sha string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	s.waiting = slices.DeleteFunc(s.waiting, func(w slotWait) bool {
		if w.Project != project || w.SHA != sha {
			return false
		}
		w.ready <- false
		n++
		return true
	})
	return n
}

// fill starts the waiters next in line (next) while there is room. The
// caller holds s.mu.
func (s *Slots) fill() {
	for len(s.waiting) > 0 {
		if n := s.size(); n > 0 && len(s.running) >= n {
			return
		}
		i := s.next(s.line(), time.Now())
		w := s.waiting[i]
		s.waiting = slices.Delete(s.waiting, i, i+1)
		w.Since = time.Now().UTC()
		s.running = append(s.running, w.SlotUse)
		w.ready <- true
	}
}

// next is the index of the waiter in ws (in the order they asked) to start
// next at now: the highest urgent rank; else, once the oldest has waited
// past the patience, the quickest quick one or that oldest; else the highest
// rank, the shortest expected within it, and the oldest of those. The caller
// holds s.mu.
func (s *Slots) next(ws []SlotUse, now time.Time) int {
	better := func(a, b SlotUse) bool {
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		return a.Expect < b.Expect // unknown (0) first: one run teaches it
	}
	best, urgent, quick := 0, -1, -1
	for i, u := range ws {
		if s.isUrgent(u.Rank) && (urgent < 0 || u.Rank > ws[urgent].Rank) {
			urgent = i
		}
		if u.Quick() && (quick < 0 || u.Expect < ws[quick].Expect) {
			quick = i
		}
		if better(u, ws[best]) {
			best = i
		}
	}
	switch {
	case urgent >= 0:
		return urgent
	case now.Sub(ws[0].Since) < s.patience: // waiters keep the order they asked
		return best
	case quick >= 0:
		return quick
	}
	return 0
}

// line is what waits, in the order it asked. The caller holds s.mu.
func (s *Slots) line() []SlotUse {
	line := make([]SlotUse, len(s.waiting))
	for i, w := range s.waiting {
		line[i] = w.SlotUse
	}
	return line
}

// learn keeps what u's run took, weighed with what its kind took before,
// so one odd run (a scope of one package, a warm cache) doesn't swing it.
// The caller holds s.mu.
func (s *Slots) learn(u SlotUse, took time.Duration) {
	if s.took == nil {
		s.took = map[string]time.Duration{}
	}
	k := tookKey(u.Project, u.Kind)
	if was, ok := s.took[k]; ok {
		took = (was + took) / 2
	}
	s.took[k] = max(took, time.Millisecond)
	for i := range s.waiting {
		if s.waiting[i].Project == u.Project && s.waiting[i].Kind == u.Kind {
			s.waiting[i].Expect = s.took[k]
		}
	}
}

func (s *Slots) isUrgent(rank int) bool { return s.urgent > 0 && rank >= s.urgent }

// Busy says whether an Acquire now would wait: every slot is taken, or
// something already waits its turn.
func (s *Slots) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.size()
	return len(s.waiting) > 0 || n > 0 && len(s.running) >= n
}

// State returns what holds a slot, oldest first, and what waits, in the
// order it will start.
func (s *Slots) State() (running, waiting []SlotUse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	running = append([]SlotUse(nil), s.running...)
	line, now := s.line(), time.Now()
	for len(line) > 0 {
		i := s.next(line, now)
		waiting = append(waiting, line[i])
		line = slices.Delete(line, i, i+1)
	}
	return running, waiting
}

// DefaultMaxChecks is where the checks' limit starts, sized to the machine: a
// check (vet, build, tests) keeps several cores busy on its own, so 20 cores
// carry 3. The work module's pace moves it from there.
func DefaultMaxChecks() int { return max(2, runtime.NumCPU()/6) }
