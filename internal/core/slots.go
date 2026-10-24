package core

import (
	"runtime"
	"slices"
	"sync"
	"time"
)

// Slots lets at most a few things run at once and starts the rest in the
// order they asked, so a burst of checks waits its turn instead of piling
// onto the machine together. Its size is read at every hand-off, so a
// changed setting holds from the next start; one already running is never stopped.
type Slots struct {
	size func() int // <= 0: no limit

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
	id      int64
}

type slotWait struct {
	SlotUse
	ready chan struct{}
}

var slotSeq int64

// NewSlots makes a queue whose size is read from size.
func NewSlots(size func() int) *Slots { return &Slots{size: size} }

// Acquire waits for a slot in order and returns its release.
func (s *Slots) Acquire(project, sha, kind string) (release func()) {
	s.mu.Lock()
	slotSeq++
	w := slotWait{SlotUse{Project: project, SHA: sha, Kind: kind, Since: time.Now().UTC(), id: slotSeq}, make(chan struct{})}
	s.waiting = append(s.waiting, w)
	s.fill()
	s.mu.Unlock()
	<-w.ready
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.running = slices.DeleteFunc(s.running, func(u SlotUse) bool { return u.id == w.id })
			s.fill()
		})
	}
}

// fill starts the oldest waiters while there is room. The caller holds s.mu.
func (s *Slots) fill() {
	for len(s.waiting) > 0 {
		if n := s.size(); n > 0 && len(s.running) >= n {
			return
		}
		w := s.waiting[0]
		s.waiting = s.waiting[1:]
		w.Since = time.Now().UTC()
		s.running = append(s.running, w.SlotUse)
		close(w.ready)
	}
}

// State returns what holds a slot and what waits, each oldest first.
func (s *Slots) State() (running, waiting []SlotUse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	running = append([]SlotUse(nil), s.running...)
	for _, w := range s.waiting {
		waiting = append(waiting, w.SlotUse)
	}
	return running, waiting
}

// DefaultMaxChecks is a few checks at a time, sized to the machine: a check
// (vet, build, tests) keeps several cores busy on its own, so 20 cores carry 3.
func DefaultMaxChecks() int { return max(2, runtime.NumCPU()/6) }
