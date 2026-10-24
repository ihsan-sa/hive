package core

import (
	"strings"
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
