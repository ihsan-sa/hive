package core

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Checks past the cap wait and start in the order they asked; the waiting
// ones show as waiting; a raised cap lets more in, and a lowered one stops
// nothing that holds a slot.
func TestSlotsCapAndOrder(t *testing.T) {
	size := 2
	var mu sync.Mutex
	s := NewSlots(func() int { mu.Lock(); defer mu.Unlock(); return size })
	var order []string
	releases := map[string]func(){}
	started := make(chan string, 10)
	var wg sync.WaitGroup
	ask := func(name string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel := s.Acquire("p", name, "test")
			mu.Lock()
			order = append(order, name)
			releases[name] = rel
			mu.Unlock()
			started <- name
		}()
		// each asks after the one before is in the queue
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if r, w := s.State(); inSlots(r, w, name) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("%s never queued", name)
			}
		}
	}
	for i := range 5 {
		ask(fmt.Sprint("c", i))
	}
	<-started
	<-started
	running, waiting := s.State()
	if len(running) != 2 || len(waiting) != 3 || waiting[0].SHA != "c2" || waiting[2].SHA != "c4" {
		t.Fatalf("running %+v, waiting %+v", running, waiting)
	}
	select {
	case n := <-started:
		t.Fatalf("%s started past the cap", n)
	case <-time.After(50 * time.Millisecond):
	}

	release := func(name string) {
		mu.Lock()
		rel := releases[name]
		mu.Unlock()
		rel()
		rel() // a second release is a no-op
	}
	release("c0")
	if n := <-started; n != "c2" {
		t.Fatalf("%s started before c2", n)
	}
	mu.Lock()
	size = 1
	mu.Unlock()
	release("c1") // c2 still holds one slot, at the new cap
	select {
	case n := <-started:
		t.Fatalf("%s started past the lowered cap", n)
	case <-time.After(50 * time.Millisecond):
	}
	mu.Lock()
	size = 0 // no limit
	mu.Unlock()
	release("c2")
	if running, waiting := s.State(); len(waiting) != 0 || len(running) != 2 || running[0].SHA != "c3" || running[1].SHA != "c4" {
		t.Fatalf("with no limit: running %+v, waiting %+v", running, waiting)
	}
	wg.Wait()
	if fmt.Sprint(order[:3]) != "[c0 c1 c2]" {
		t.Fatalf("started in order %v", order)
	}
}

func inSlots(r, w []SlotUse, sha string) bool {
	for _, u := range append(r, w...) {
		if u.SHA == sha {
			return true
		}
	}
	return false
}

func TestSlotsBusy(t *testing.T) {
	s := NewSlots(func() int { return 1 })
	if s.Busy() {
		t.Fatal("busy while empty")
	}
	release := s.Acquire("p", "a", "test")
	if !s.Busy() {
		t.Fatal("not busy with every slot taken")
	}
	release()
	if s.Busy() {
		t.Fatal("busy after release")
	}
	if NewSlots(func() int { return 0 }).Busy() {
		t.Fatal("an unlimited queue is never busy while nothing waits")
	}
}

// A waiter ranked higher starts ahead of the line, equal ranks in the order
// they asked, and a rank that rose while waiting counts from the next Fill.
func TestSlotsRankGoesFirst(t *testing.T) {
	var mu sync.Mutex
	ranks := map[string]int{"urgent": 2}
	s := NewSlots(func() int { return 1 })
	s.Prioritize(func(_, sha, _ string) int { mu.Lock(); defer mu.Unlock(); return ranks[sha] }, 0)
	hold := s.Acquire("p", "first", "test")
	started := make(chan string, 10)
	ask := func(sha string) {
		go func() {
			rel := s.Acquire("p", sha, "test")
			started <- sha
			rel()
		}()
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if r, w := s.State(); inSlots(r, w, sha) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("%s never queued", sha)
			}
		}
	}
	for _, sha := range []string{"a", "b", "urgent", "c", "late"} {
		ask(sha)
	}
	mu.Lock()
	ranks["late"] = 1 // it came to hold others while it waited
	mu.Unlock()
	s.Fill()
	if _, w := s.State(); len(w) != 5 || w[0].SHA != "urgent" || w[1].SHA != "late" || w[2].SHA != "a" {
		t.Fatalf("waiting %+v", w)
	}
	hold()
	var order []string
	for range 5 {
		order = append(order, <-started)
	}
	if fmt.Sprint(order) != "[urgent late a b c]" {
		t.Fatalf("started in order %v", order)
	}
}

func TestSlotsPatienceStopsPassingOver(t *testing.T) {
	s := NewSlots(func() int { return 1 })
	s.Prioritize(func(_, sha, _ string) int {
		if sha == "plain" {
			return 0
		}
		return 2
	}, 0)
	s.patience = 50 * time.Millisecond
	hold := s.Acquire("p", "first", "test")
	started := make(chan string, 10)
	ask := func(sha string) {
		go func() {
			rel := s.Acquire("p", sha, "test")
			started <- sha
			rel()
		}()
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if r, w := s.State(); inSlots(r, w, sha) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("%s never queued", sha)
			}
		}
	}
	ask("plain")
	time.Sleep(60 * time.Millisecond)
	ask("ranked")
	if _, w := s.State(); len(w) != 2 || w[0].SHA != "plain" {
		t.Fatalf("waiting %+v", w)
	}
	hold()
	if a, b := <-started, <-started; a != "plain" || b != "ranked" {
		t.Fatalf("started %s then %s", a, b)
	}
}

// A dropped waiter leaves the line at once without a slot, and the next one
// starts in its place.
func TestSlotsDrop(t *testing.T) {
	s := NewSlots(func() int { return 1 })
	hold := s.Acquire("p", "first", "test")
	dropped := make(chan bool, 1)
	go func() { _, ok := s.Take("p", "closed", "test"); dropped <- ok }()
	for {
		if _, w := s.State(); len(w) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if n := s.Drop("p", "closed"); n != 1 {
		t.Fatalf("dropped %d, want 1", n)
	}
	select {
	case ok := <-dropped:
		if ok {
			t.Fatal("a dropped wait got a slot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a dropped wait still waits")
	}
	if run, w := s.State(); len(run) != 1 || len(w) != 0 {
		t.Fatalf("running %v, waiting %v", run, w)
	}
	hold()
	if s.Busy() {
		t.Fatal("busy after the holder released")
	}
}

// An urgent rank goes first even past a waiter out of patience, which still
// goes ahead of a merely ranked one: main's measurement waited behind PR
// checks an hour old in a backlog (issue #1713).
func TestSlotsUrgentPassesPatience(t *testing.T) {
	s := NewSlots(func() int { return 1 })
	s.Prioritize(func(_, sha, _ string) int {
		return map[string]int{"plain": 0, "ranked": 2, "main": 3}[sha]
	}, 3)
	s.patience = 50 * time.Millisecond
	hold := s.Acquire("p", "first", "test")
	started := make(chan string, 10)
	ask := func(sha string) {
		go func() {
			rel := s.Acquire("p", sha, "test")
			started <- sha
			rel()
		}()
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if r, w := s.State(); inSlots(r, w, sha) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("%s never queued", sha)
			}
		}
	}
	ask("plain")
	time.Sleep(60 * time.Millisecond)
	ask("ranked")
	ask("main")
	if _, w := s.State(); len(w) != 3 || w[0].SHA != "main" || w[1].SHA != "plain" || w[2].SHA != "ranked" {
		t.Fatalf("waiting %+v", w)
	}
	hold()
	if a, b, c := <-started, <-started, <-started; a+" "+b+" "+c != "main plain ranked" {
		t.Fatalf("started %s %s %s", a, b, c)
	}
}

// Within a rank the check expected to be shortest starts first, one not
// known yet before both, and a higher rank still goes ahead; a quick one goes
// past even a waiter out of patience, a long one doesn't: a check of seconds
// waited ~50 minutes behind 30 of ten minutes (issue #1933).
func TestSlotsShortFirstWithinRank(t *testing.T) {
	s := NewSlots(func() int { return 1 })
	s.Prioritize(func(_, sha, _ string) int {
		if sha == "fix" {
			return 1
		}
		return 0
	}, 3)
	s.took = map[string]time.Duration{tookKey("heavy", "test"): 15 * time.Minute, tookKey("light", "test"): 5 * time.Second}
	hold := s.Acquire("x", "first", "test")
	started := make(chan string, 10)
	ask := func(project, sha string) {
		go func() {
			rel := s.Acquire(project, sha, "test")
			started <- sha
			rel()
		}()
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if r, w := s.State(); inSlots(r, w, sha) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("%s never queued", sha)
			}
		}
	}
	ask("heavy", "h1")
	ask("heavy", "fix")
	ask("light", "l1")
	ask("new", "n1")
	ask("heavy", "h2")
	_, w := s.State()
	var line []string
	for _, u := range w {
		line = append(line, u.SHA)
	}
	if fmt.Sprint(line) != "[fix n1 l1 h1 h2]" {
		t.Fatalf("waiting in order %v", line)
	}
	if !w[2].Quick() || w[3].Quick() || w[1].Quick() {
		t.Fatalf("quick: %+v", w)
	}

	// Out of patience the oldest goes first, except past it a quick one.
	s.mu.Lock()
	s.patience = 0
	s.mu.Unlock()
	if _, w := s.State(); w[0].SHA != "l1" || w[1].SHA != "h1" || w[2].SHA != "fix" {
		t.Fatalf("out of patience, waiting %+v", w)
	}
	hold()
	var order []string
	for range 5 {
		order = append(order, <-started)
	}
	if fmt.Sprint(order) != "[l1 h1 fix n1 h2]" {
		t.Fatalf("started in order %v", order)
	}
}

// What a run took is learnt as it ends, weighed with the runs before, and
// a waiter of its project and kind expects it from then on.
func TestSlotsLearnWhatARunTakes(t *testing.T) {
	s := NewSlots(func() int { return 1 })
	rel := s.Acquire("p", "a", "test")
	go func() { s.Acquire("p", "b", "test")() }()
	for {
		if _, w := s.State(); len(w) == 1 {
			if w[0].Expect != 0 {
				t.Fatalf("expects %v before any run ended", w[0].Expect)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	rel()
	s.mu.Lock()
	first := s.took[tookKey("p", "test")]
	s.learn(SlotUse{Project: "p", Kind: "test"}, first+2*time.Second)
	got := s.took[tookKey("p", "test")]
	other := s.took[tookKey("p", "score")]
	s.mu.Unlock()
	if first < 20*time.Millisecond || first > 2*time.Second {
		t.Fatalf("learnt %v from a run of ~20ms", first)
	}
	if got != first+time.Second {
		t.Fatalf("after a run of %v learnt %v, want the two weighed", first+2*time.Second, got)
	}
	if other != 0 {
		t.Fatalf("a score check expects %v from a test's runs", other)
	}
}
