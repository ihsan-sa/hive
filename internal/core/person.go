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
	answerMu sync.Mutex // held by Answer and Tell from the checks through the append, so one wins
	asks     map[string]*Ask
	told     []Ask          // what was done for the person, oldest first: the newest keepTold
	toldAt   map[string]int // told ID to its index in told
	handlers map[string]func(Ask)
	handling sync.WaitGroup // answers' handlers still running
}

// keepTold is how many things told stay in memory. Only the newest few are
// read; the journal keeps them all.
const keepTold = 500

// NewPerson replays asks.
func NewPerson(j *Journal) *Person {
	p := &Person{j: j, asks: map[string]*Ask{}, toldAt: map[string]int{}, handlers: map[string]func(Ask){}}
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
			p.addTold(a)
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

// addTold keeps a told, dropping the oldest past keepTold. p.mu is held.
func (p *Person) addTold(a Ask) {
	p.toldAt[a.ID] = len(p.told)
	p.told = append(p.told, a)
	if len(p.told) < 2*keepTold {
		return
	}
	p.told = append([]Ask(nil), p.told[len(p.told)-keepTold:]...)
	clear(p.toldAt)
	for i, x := range p.told {
		p.toldAt[x.ID] = i
	}
}

// Tell records something already done for the person, which needs no
// answer: it goes to their "done for you" feed, never to Waiting. Once per
// ID, and once per (Ref, Title), so a step run twice after a restart, or a
// caller's stable ID, keeps a notice from repeating.
func (p *Person) Tell(a Ask) Ask {
	// answerMu keeps the check and the append together, as in Answer.
	p.answerMu.Lock()
	defer p.answerMu.Unlock()
	p.mu.Lock()
	if i, ok := p.toldAt[a.ID]; ok && a.ID != "" {
		x := p.told[i]
		p.mu.Unlock()
		return x
	}
	if x, ok := p.asks[a.ID]; ok && a.ID != "" {
		c := *x
		p.mu.Unlock()
		return c
	}
	if a.Ref != "" {
		for i := len(p.told) - 1; i >= 0; i-- {
			if x := p.told[i]; x.Ref == a.Ref && x.Title == a.Title {
				p.mu.Unlock()
				return x
			}
		}
	}
	p.mu.Unlock()
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
		p.handling.Add(1)
		go func() {
			defer p.handling.Done()
			h(c)
		}()
	}
	return c, nil
}

// Handled waits until every answer's handler started so far has returned,
// with what it appended filed by the journal's subscribers.
func (p *Person) Handled() { p.handling.Wait() }

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
	if i, ok := p.toldAt[id]; ok {
		return p.told[i], true
	}
	return Ask{}, false
}
