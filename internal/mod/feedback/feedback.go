// Package feedback is what agents tell the person about the Hive itself: a
// prompt that misled them, a tool that was missing, a step that took three
// tries. An agent sends it with `hive agent feedback`; the person reads it in
// the control center's Feedback section, closes it or passes it to a lead,
// and the improvement sweep reads what is still open.
package feedback

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Item is one piece of feedback and what became of it.
type Item struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Run     string    `json:"run"`
	Role    string    `json:"role"`
	Name    string    `json:"name,omitempty"`
	Project string    `json:"project,omitempty"`
	About   string    `json:"about,omitempty"` // a module, prompt, tool or part of the Hive, in the agent's words
	Text    string    `json:"text"`
	Status  string    `json:"status"` // open | closed | passed (to a lead's chat)
	Seen    bool      `json:"seen,omitempty"`
	Note    string    `json:"note,omitempty"` // what the person did with it
}

const (
	Open   = "open"
	Closed = "closed"
	Passed = "passed"
)

// Module is the feedback module.
type Module struct {
	C *core.Core

	mu    sync.Mutex
	items map[string]*Item
}

// New loads the record and registers the feedback op.
func New(c *core.Core) *Module {
	m := &Module{C: c, items: map[string]*Item{}}
	apply := func(e core.Event) {
		if e.Kind != "feedback.saved" {
			return
		}
		var it Item
		if e.Decode(&it) != nil || it.ID == "" {
			return
		}
		m.mu.Lock()
		m.items[it.ID] = &it
		m.mu.Unlock()
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	c.Runs.Ops().Register(core.Op{Name: "feedback", Help: `{"text": "what about the Hive got in your way or helped", "about": "(optional) the module, prompt, tool or part it is about"} -> shown to the person in the Feedback section and read by the next sweep`, Fn: func(r core.Run, args json.RawMessage) (any, error) {
		var a struct{ Text, About string }
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
		it, err := m.Add(r, a.About, a.Text)
		if err != nil {
			return nil, err
		}
		return "noted as " + it.ID + "; the person sees it in Feedback and the next sweep reads it", nil
	}})
	return m
}

// Add records feedback from run r.
func (m *Module) Add(r core.Run, about, text string) (Item, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Item{}, errors.New("empty feedback: say what got in your way, or what helped")
	}
	it := Item{ID: core.NewID(), At: time.Now().UTC(), Run: r.ID, Role: r.Role, Name: r.Name, Project: r.Project,
		About: core.Clip(strings.TrimSpace(about), 80), Text: core.Clip(text, 4000), Status: Open}
	_, err := m.C.J.Append("feedback.saved", it)
	return it, err
}

// List returns feedback newest first; keep filters it when set.
func (m *Module) List(keep func(Item) bool) []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, it := range m.items {
		if keep == nil || keep(*it) {
			out = append(out, *it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// Get finds one item.
func (m *Module) Get(id string) (Item, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return Item{}, false
	}
	return *it, true
}

// Unseen counts open feedback the person hasn't opened.
func (m *Module) Unseen() int {
	return len(m.List(func(it Item) bool { return it.Status == Open && !it.Seen }))
}

func (m *Module) save(id string, change func(*Item) error) (Item, error) {
	it, ok := m.Get(id)
	if !ok {
		return Item{}, core.ErrNotFound
	}
	if err := change(&it); err != nil {
		return Item{}, err
	}
	_, err := m.C.J.Append("feedback.saved", it)
	return it, err
}

// Seen marks an item read by the person; already seen is not journaled again.
func (m *Module) Seen(id string) error {
	if it, ok := m.Get(id); ok && it.Seen {
		return nil
	}
	_, err := m.save(id, func(it *Item) error { it.Seen = true; return nil })
	return err
}

// Close is the person setting an item aside, with an optional note.
func (m *Module) Close(id, note string) (Item, error) {
	return m.save(id, func(it *Item) error {
		it.Status, it.Seen = Closed, true
		if note = strings.TrimSpace(note); note != "" {
			it.Note = core.Clip(note, 500)
		}
		return nil
	})
}

// Reopen puts a closed or passed item back on the open list.
func (m *Module) Reopen(id string) (Item, error) {
	return m.save(id, func(it *Item) error { it.Status, it.Note = Open, ""; return nil })
}

// Passed records that the person handed an item to a lead's chat.
func (m *Module) Passed(id, chat string) (Item, error) {
	return m.save(id, func(it *Item) error {
		if it.Status == Passed {
			return fmt.Errorf("already passed: %s", it.Note)
		}
		it.Status, it.Seen, it.Note = Passed, true, "passed to "+chat
		return nil
	})
}

// Message is what the person's "pass to the lead" posts in its chat.
func Message(it Item) string {
	from := it.Role + " " + it.Run
	if it.Name != "" {
		from = it.Name + " (" + it.Run + ")"
	}
	about := ""
	if it.About != "" {
		about = " about " + it.About
	}
	return fmt.Sprintf("Feedback from %s%s, sent %s:\n\n> %s\n\nWeigh it and file what's worth doing, or tell me why not.",
		from, about, it.At.Local().Format("Mon 02 Jan 15:04"), strings.ReplaceAll(it.Text, "\n", "\n> "))
}

// Digest lists the open feedback of the projects mine admits, for the
// improvement sweep. Project-less feedback (the lead's, a sweep's) is the
// Hive's own and is admitted as mine("") says.
func (m *Module) Digest(mine func(project string) bool) string {
	var b strings.Builder
	for i, it := range m.List(func(it Item) bool {
		return it.Status == Open && mine(it.Project)
	}) {
		if i >= 30 {
			break
		}
		where := it.Project
		if where == "" {
			where = "hive"
		}
		about := ""
		if it.About != "" {
			about = " about " + it.About
		}
		fmt.Fprintf(&b, "- %s %s %s (%s)%s: %s\n", it.At.Format("01-02 15:04"), where, it.Role, it.Run, about, core.Clip(it.Text, 600))
	}
	return b.String()
}
