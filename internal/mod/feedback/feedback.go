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
	"maps"
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
	// Task is the task an agent named as the fix; the item closes when that
	// task lands (merged or done), so sweeps stop reading it as still open.
	Task string `json:"task,omitempty"`
}

const (
	Open   = "open"
	Closed = "closed"
	Passed = "passed"
)

// Module is the feedback module.
type Module struct {
	C *core.Core

	mu       sync.Mutex
	items    map[string]*Item
	known    map[string]bool // every task id task.saved has named
	landed   map[string]bool // tasks merged or done, and not reverted since
	reverted map[string]bool // tasks a merged revert has undone
}

// taskOf reads a task.saved event: its task, whether it landed, and the task
// it reverts if it is a revert.
func taskOf(e core.Event) (id string, landed bool, revertOf string) {
	var t struct {
		ID, Stage, Kind string
		RevertOf        string `json:"revert_of"`
	}
	if e.Decode(&t) != nil || t.ID == "" {
		return "", false, ""
	}
	landed = t.Stage == "merged" || t.Stage == "done"
	if t.Kind == "revert" && landed {
		revertOf = t.RevertOf
	}
	return t.ID, landed, revertOf
}

// New loads the record and registers the feedback op.
func New(c *core.Core) *Module {
	m := &Module{C: c, items: map[string]*Item{}, known: map[string]bool{}, landed: map[string]bool{}, reverted: map[string]bool{}}
	apply := func(e core.Event) {
		if e.Kind == "task.saved" {
			id, landed, revertOf := taskOf(e)
			if id == "" {
				return
			}
			m.mu.Lock()
			m.known[id] = true
			if landed && !m.reverted[id] {
				m.landed[id] = true
			}
			if revertOf != "" {
				m.reverted[revertOf], m.landed[revertOf] = true, false
			}
			m.mu.Unlock()
			return
		}
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
	c.J.Subscribe(func(e core.Event) { // a fix that lands closes what it fixed; its revert reopens it
		if e.Kind != "task.saved" {
			return
		}
		id, landed, revertOf := taskOf(e)
		if landed && m.isLanded(id) {
			for _, it := range m.List(func(it Item) bool { return it.Status == Open && it.Task == id }) {
				m.Close(it.ID, fixedBy+id)
			}
		}
		if revertOf != "" {
			for _, it := range m.List(func(it Item) bool { return it.Status == Closed && it.Task == revertOf && it.Note == fixedBy+revertOf }) {
				m.save(it.ID, func(it *Item) error {
					it.Status, it.Seen, it.Task, it.Note = Open, false, "", "task "+revertOf+" fixed it but was reverted by task "+id
					return nil
				})
			}
		}
	})
	c.Runs.Ops().Register(core.Op{Name: "feedback", Help: `{"text": "what about the Hive got in your way or helped", "about": "(optional) the module, prompt, tool or part it is about"} -> shown to the person in the Feedback section and read by the next sweep; or {"fixed": "<feedback id>", "task": "<task id>"} -> the item closes when that task lands (at once if it has)`, Fn: func(r core.Run, args json.RawMessage) (any, error) {
		var a struct{ Text, About, Fixed, Task string }
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
		if a.Fixed != "" || a.Task != "" {
			it, err := m.Fix(r, a.Fixed, a.Task)
			if err != nil {
				return nil, err
			}
			if it.Status == Closed {
				return "closed " + it.ID + ": task " + it.Task + " has landed", nil
			}
			return it.ID + " closes when task " + it.Task + " lands; until then sweeps see it as being fixed", nil
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

// Fix links an open item to the task that fixes it, for run r: a run with a
// project may link that project's feedback and the Hive's own. An item whose
// task has already landed closes at once. The task must be one the journal
// knows and not reverted, so a typo can't leave a mark that never resolves.
func (m *Module) Fix(r core.Run, id, task string) (Item, error) {
	id, task = strings.TrimSpace(id), strings.TrimSpace(task)
	if id == "" || task == "" {
		return Item{}, errors.New("name both --fixed <feedback id> and --task <task id>")
	}
	if it, ok := m.Get(id); ok && r.Project != "" && it.Project != "" && it.Project != r.Project {
		return Item{}, core.ErrNotFound
	}
	m.mu.Lock()
	known, landed, reverted := m.known[task], m.landed[task], m.reverted[task]
	m.mu.Unlock()
	if !known {
		return Item{}, fmt.Errorf("no task %s in the journal: name the fix's task id as hive agent brief or status gives it", task)
	}
	if reverted {
		return Item{}, fmt.Errorf("task %s was reverted, so it fixes nothing: name the task that fixes it now", task)
	}
	return m.save(id, func(it *Item) error {
		if it.Status != Open {
			return fmt.Errorf("%s is no longer open (%s)", it.ID, it.Status)
		}
		it.Task = task
		if landed {
			it.Status, it.Note = Closed, fixedBy+task
		}
		return nil
	})
}

const fixedBy = "fixed by task "

func (m *Module) isLanded(task string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.landed[task]
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
// improvement sweep, with each item's id and the task named to fix it.
// Project-less feedback (the lead's, a sweep's) is the Hive's own and is
// admitted as mine("") says. An item whose fix has landed is left out even
// if its close was never journaled.
func (m *Module) Digest(mine func(project string) bool) string {
	m.mu.Lock()
	landed := maps.Clone(m.landed)
	m.mu.Unlock()
	var b strings.Builder
	for i, it := range m.List(func(it Item) bool {
		return it.Status == Open && mine(it.Project) && !landed[it.Task]
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
		fix := ""
		if it.Task != "" {
			fix = " [fix filed: task " + it.Task + ", not landed yet]"
		} else if it.Note != "" {
			fix = " [" + it.Note + "]"
		}
		fmt.Fprintf(&b, "- %s %s %s %s (%s)%s%s: %s\n", it.ID, it.At.Format("01-02 15:04"), where, it.Role, it.Run, about, fix, core.Clip(it.Text, 600))
	}
	return b.String()
}
