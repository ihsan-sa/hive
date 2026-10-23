package core

import (
	"sort"
	"sync"
	"time"
)

// Ask is something waiting on the person: a decision that is really theirs.
type Ask struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // step | merge | release | baseline | question | bare
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	Options  []string  `json:"options"`
	Ref      string    `json:"ref,omitempty"` // what it is about (step id, task id)
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
	asks     map[string]*Ask
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

// Answer records the person's answer and runs the kind's handler.
func (p *Person) Answer(id, answer, note string) (Ask, error) {
	p.mu.Lock()
	a, ok := p.asks[id]
	if !ok {
		p.mu.Unlock()
		return Ask{}, ErrNotFound
	}
	if a.Answer != "" {
		c := *a
		p.mu.Unlock()
		return c, nil
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

// Get returns one ask.
func (p *Person) Get(id string) (Ask, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.asks[id]
	if !ok {
		return Ask{}, false
	}
	return *a, true
}
