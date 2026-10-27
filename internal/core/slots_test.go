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
