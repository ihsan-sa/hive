package core

import (
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
