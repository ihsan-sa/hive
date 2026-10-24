package core

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Ask is something waiting on the person: a decision that is really theirs.
type Ask struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // step | merge | release | baseline | question | bare | login
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	Options  []string  `json:"options"`
	Diff     string    `json:"diff,omitempty"` // what a merge ask changes in the protected files, fixed at its commit
	Ref      string    `json:"ref,omitempty"`  // what it is about (step id, task id)
	From     string    `json:"from,omitempty"`
	Project  string    `json:"project,omitempty"`
	Answer   string    `json:"answer,omitempty"`
	Note     string    `json:"note,omitempty"`
	Asked    time.Time `json:"asked"`
	Answered time.Time `json:"answered,omitempty"`
}

// Person holds what is waiting on the person and routes their answers.
type Person struct {
	j        *Journal
	mu       sync.Mutex
	answerMu sync.Mutex // held by Answer from the checks through the append, so one answer wins
	asks     map[string]*Ask
	told     []Ask // what was done for the person, oldest first
	handlers map[string]func(Ask)
}

// NewPerson replays asks.
func NewPerson(j *Journal) *Person {
	p := &Person{j: j, asks: map[string]*Ask{}, handlers: map[string]func(Ask){}}
	apply := func(e Event) {
		switch e.Kind {
		case "person.asked":
			var a Ask
			e.Decode(&a)
			p.mu.Lock()
			p.asks[a.ID] = &a
			p.mu.Unlock()
		case "person.told":
			var a Ask
			e.Decode(&a)
			p.mu.Lock()
			p.told = append(p.told, a)
			p.mu.Unlock()
		case "person.answered":
			var a Ask
			e.Decode(&a)
			p.mu.Lock()
			if x := p.asks[a.ID]; x != nil {
				x.Answer, x.Note, x.Answered = a.Answer, a.Note, e.At
			}
			p.mu.Unlock()
		}
	}
	j.Replay(apply)
	j.Subscribe(apply)
	return p
}

// Handle registers what happens when an ask of kind is answered.
func (p *Person) Handle(kind string, fn func(Ask)) {
	p.mu.Lock()
	p.handlers[kind] = fn
	p.mu.Unlock()
}

// Ask records a question for the person, once per (kind, ref).
func (p *Person) Ask(a Ask) Ask {
	p.mu.Lock()
	for _, x := range p.asks {
		if a.Ref != "" && x.Kind == a.Kind && x.Ref == a.Ref && x.Answer == "" {
			p.mu.Unlock()
			return *x
		}
	}
	p.mu.Unlock()
	if a.ID == "" {
		a.ID = NewID()
	}
	if len(a.Options) == 0 {
		a.Options = []string{"yes", "no"}
	}
	a.Asked = time.Now().UTC()
	p.j.MustAppend("person.asked", a)
	return a
}

// Tell records something already done for the person, which needs no
// answer: it goes to their "done for you" feed, never to Waiting. Once per
// ID, so a caller can give one to keep a notice from repeating.
func (p *Person) Tell(a Ask) Ask {
	if a.ID != "" {
		if x, ok := p.Get(a.ID); ok {
			return x
		}
	}
	if a.ID == "" {
		a.ID = NewID()
	}
	a.Kind, a.Options = "told", nil
	if a.Asked.IsZero() {
		a.Asked = time.Now().UTC()
	}
	p.j.MustAppend("person.told", a)
	return a
}

// Told returns the newest n things done for the person, newest first.
func (p *Person) Told(n int) []Ask {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Ask
	for i := len(p.told) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, p.told[i])
	}
	return out
}

// moveTold clears "told" asks, which only ever waited for a "seen", out of
// Waiting and into the feed: before Tell, a step that had already run was
// filed as an ask.
func (p *Person) moveTold() {
	for _, a := range p.Open() {
		if a.Kind != "told" {
			continue
		}
		p.Tell(Ask{ID: a.ID + "-told", Title: a.Title, Detail: a.Detail, Diff: a.Diff, Ref: a.Ref, From: a.From, Project: a.Project, Asked: a.Asked})
		p.Answer(a.ID, "seen", "the Hive: moved to the feed, it needed no decision")
	}
}

// Answer records the person's answer and runs the kind's handler. The answer
// must be one of the ask's options; an ask with none takes free text. An ask
// already answered refuses another answer and returns the one on record.
func (p *Person) Answer(id, answer, note string) (Ask, error) {
	// p.mu can't be held over the append: the journal runs subscribers, which
	// take it, synchronously. answerMu keeps the check and the append together.
	p.answerMu.Lock()
	defer p.answerMu.Unlock()
	p.mu.Lock()
	a, ok := p.asks[id]
	if !ok {
		p.mu.Unlock()
		return Ask{}, ErrNotFound
	}
	if a.Answer != "" {
		c := *a
		p.mu.Unlock()
		return c, fmt.Errorf("already answered: %s", c.Answer)
	}
	if len(a.Options) > 0 && !slices.Contains(a.Options, answer) {
		p.mu.Unlock()
		return Ask{}, fmt.Errorf("%q isn't an answer to this ask: answer %s", answer, strings.Join(a.Options, " or "))
	}
	h := p.handlers[a.Kind]
	p.mu.Unlock()
	p.j.MustAppend("person.answered", Ask{ID: id, Answer: answer, Note: note})
	p.mu.Lock()
	c := *p.asks[id]
	p.mu.Unlock()
	if h != nil {
		go h(c)
	}
	return c, nil
}

// Open returns unanswered asks, oldest first.
func (p *Person) Open() []Ask {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Ask
	for _, a := range p.asks {
		if a.Answer == "" {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Asked.Before(out[j].Asked) })
	return out
}

// Get returns one ask, or one thing told.
func (p *Person) Get(id string) (Ask, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a, ok := p.asks[id]; ok {
		return *a, true
	}
	for _, a := range p.told {
		if a.ID == id {
			return a, true
		}
	}
	return Ask{}, false
}
