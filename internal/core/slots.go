package core

import (
	"cmp"
	"math"
	"runtime"
	"slices"
	"sync"
	"time"
)

// Slots lets at most a few things run at once and starts the rest in the
// order they asked, so a burst of checks waits its turn instead of piling
// onto the machine together; a higher rank (Prioritize) goes ahead of that
// line, but never past one that has waited slotPatience unless it ranks
// urgent. Its size is read at every hand-off, so a changed setting holds from
// the next start; one already running is never stopped.
type Slots struct {
	size     func() int // <= 0: no limit
	rank     func(project, sha, kind string) int
	urgent   int // > 0: a rank this high goes first even past the patience
	patience time.Duration

	mu      sync.Mutex
	running []SlotUse
	waiting []slotWait
}

// SlotUse is one thing holding or waiting for a slot.
type SlotUse struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`
	Kind    string    `json:"kind"`
	Since   time.Time `json:"since"` // when it started, or began waiting
	Rank    int       `json:"rank,omitempty"`
	id      int64
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
// equal ranks in the order they asked. A rank of urgent or more (when > 0)
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

// Acquire waits for a slot, by rank and then in order, and returns its release.
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
// left, with ok false and a release that does nothing.
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
	w := slotWait{SlotUse{Project: project, SHA: sha, Kind: kind, Since: time.Now().UTC(), Rank: r, id: slotSeq}, make(chan bool, 1)}
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
			// It started (or was dropped) as leave was asked: take that.
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
			s.running = slices.DeleteFunc(s.running, func(u SlotUse) bool { return u.id == w.id })
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

// fill starts the highest-ranked, then oldest, waiters while there is room;
// the oldest goes first once it has waited past the patience, unless an
// urgent one waits. The caller holds s.mu.
func (s *Slots) fill() {
	for len(s.waiting) > 0 {
		if n := s.size(); n > 0 && len(s.running) >= n {
			return
		}
		next := 0
		patient := time.Since(s.waiting[0].Since) >= s.patience // waiters keep the order they asked
		for i, w := range s.waiting {
			if w.Rank > s.waiting[next].Rank && (!patient || s.isUrgent(w.Rank)) {
				next = i
			}
		}
		w := s.waiting[next]
		s.waiting = slices.Delete(s.waiting, next, next+1)
		w.Since = time.Now().UTC()
		s.running = append(s.running, w.SlotUse)
		w.ready <- true
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
	for _, w := range s.waiting {
		waiting = append(waiting, w.SlotUse)
	}
	key := func(u SlotUse) int {
		switch {
		case s.isUrgent(u.Rank):
			return math.MaxInt
		case time.Since(u.Since) >= s.patience:
			return math.MaxInt - 1 // waited long enough: in the order they asked
		}
		return u.Rank
	}
	slices.SortStableFunc(waiting, func(a, b SlotUse) int { return cmp.Compare(key(b), key(a)) })
	return running, waiting
}

// DefaultMaxChecks is where the checks' limit starts, sized to the machine: a
// check (vet, build, tests) keeps several cores busy on its own, so 20 cores
// carry 3. The work module's pace moves it from there.
func DefaultMaxChecks() int { return max(2, runtime.NumCPU()/6) }
