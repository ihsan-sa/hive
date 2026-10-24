package core

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// An answer is one of the options the ask offered; anything else is refused,
// naming the options, and leaves the ask open.
func TestAnswerIsAnOfferedOption(t *testing.T) {
	c, _ := testHive(t)
	a := c.Person.Ask(Ask{Kind: "merge", Ref: "t@1", Title: "Merge?", Options: []string{"merge", "leave open"}})
	_, err := c.Person.Answer(a.ID, "ship it", "")
	if err == nil || !strings.Contains(err.Error(), "merge or leave open") {
		t.Fatalf("an answer not offered: %v, want a refusal naming the options", err)
	}
	if got, _ := c.Person.Get(a.ID); got.Answer != "" {
		t.Fatalf("a refused answer was recorded: %+v", got)
	}
	if got, err := c.Person.Answer(a.ID, "leave open", "not yet"); err != nil || got.Answer != "leave open" {
		t.Fatalf("an offered answer: %+v, %v", got, err)
	}
}

// An ask on file with no options takes the person's own text.
func TestAnswerToAFreeTextAsk(t *testing.T) {
	c, _ := testHive(t)
	c.J.MustAppend("person.asked", Ask{ID: "q1", Kind: "question", Title: "What should it be called?"})
	if got, err := c.Person.Answer("q1", "hive", ""); err != nil || got.Answer != "hive" {
		t.Fatalf("a free-text answer: %+v, %v", got, err)
	}
}

// Two answers at once to one ask: one is recorded, the other refused.
func TestConcurrentAnswersRecordOne(t *testing.T) {
	c, _ := testHive(t)
	a := c.Person.Ask(Ask{Kind: "merge", Ref: "t@1", Title: "Merge?", Options: []string{"merge", "leave open"}})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, ans := range []string{"merge", "leave open"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.Person.Answer(a.ID, ans, "")
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("two answers at once: %v, %v; want one taken and one refused", errs[0], errs[1])
	}
	n := 0
	c.J.Replay(func(e Event) {
		if e.Kind == "person.answered" {
			n++
		}
	})
	if n != 1 {
		t.Fatalf("%d answers recorded, want 1", n)
	}
}

// Something done for the person goes to the feed, never to Waiting, and a
// "told" ask filed before Tell is cleared into the feed on the next start,
// leaving the asks that need a decision.
func TestToldGoesToTheFeed(t *testing.T) {
	c, forge := testHive(t)
	told := c.Person.Tell(Ask{Ref: "r1", Title: "A T2 step ran (done): fetch a job log", Detail: "curl ...\n\nok"})
	if n := len(c.Person.Open()); n != 0 {
		t.Fatalf("a told is waiting on the person: %d asks", n)
	}
	if got := c.Person.Told(5); len(got) != 1 || got[0].ID != told.ID || got[0].Kind != "told" || len(got[0].Options) != 0 {
		t.Fatalf("told: %+v", got)
	}
	c.J.MustAppend("person.asked", Ask{ID: "old", Kind: "told", Ref: "r0", Title: "A T2 step ran (done): old", Detail: "out", Options: []string{"seen"}})
	merge := c.Person.Ask(Ask{Kind: "merge", Ref: "t@1", Title: "Merge?", Options: []string{"merge", "leave open"}})
	c.Close()
	c, err := Open(c.WS, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if open := c.Person.Open(); len(open) != 1 || open[0].ID != merge.ID {
		t.Fatalf("after the restart, waiting: %+v, want only the merge", open)
	}
	got := c.Person.Told(5)
	if len(got) != 2 || got[0].Title != "A T2 step ran (done): old" || got[0].Detail != "out" || got[1].ID != told.ID {
		t.Fatalf("the old told wasn't moved to the feed: %+v", got)
	}
	c.Close()
	if c, _ = Open(c.WS, forge); len(c.Person.Told(5)) != 2 {
		t.Fatalf("moved twice: %+v", c.Person.Told(5))
	}
	c.Close()
}

// Tell records once per ID and once per (Ref, Title), even from callers at
// once, so a step that runs twice after a restart is told once; and only the
// newest are kept in memory, still found by ID.
func TestTellOnce(t *testing.T) {
	c, _ := testHive(t)
	t.Cleanup(func() { c.Close() })
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); c.Person.Tell(Ask{ID: "spend-x", Title: "spent"}) }()
		go func() { defer wg.Done(); c.Person.Tell(Ask{Ref: "req1", Title: "A T2 step ran (done): x"}) }()
	}
	wg.Wait()
	c.Person.Tell(Ask{Ref: "req1", Title: "Decided for you (yes, by a warden): x"})
	if got := c.Person.Told(10); len(got) != 3 {
		t.Fatalf("told %d, want 3: %+v", len(got), got)
	}
	for i := range 2 * keepTold {
		c.Person.Tell(Ask{ID: fmt.Sprint("n", i), Title: "t"})
	}
	if n := len(c.Person.told); n > 2*keepTold {
		t.Fatalf("%d told in memory, want at most %d", n, 2*keepTold)
	}
	last := fmt.Sprint("n", 2*keepTold-1)
	if a, ok := c.Person.Get(last); !ok || a.ID != last {
		t.Fatalf("the newest told isn't found by ID: %+v", a)
	}
	if got := c.Person.Told(1); got[0].ID != last {
		t.Fatalf("newest: %+v", got)
	}
}
