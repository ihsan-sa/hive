// Package problems is the Hive's problems board: what agents run into while
// they work (a flaky check, a cause found in main, a prompt that misleads, a
// tool that breaks), posted with the run that met it so others can reach it.
// Every agent reads the open ones at the top of its brief and adds to them, so
// the Hive works on its blockers as a hive rather than through the lead alone.
// Feedback is for the person; the board is for the agents.
package problems

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

// Note is a later word on a problem: met it too, a clue, the cause, the fix.
type Note struct {
	At   time.Time `json:"at"`
	Run  string    `json:"run"`
	Role string    `json:"role"`
	Text string    `json:"text"`
}

// Problem is one entry on the board.
type Problem struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Run     string    `json:"run"` // who met it: the address to message
	Role    string    `json:"role"`
	Name    string    `json:"name,omitempty"`
	Project string    `json:"project,omitempty"`
	Where   string    `json:"where,omitempty"` // the part of the Hive or project, in the agent's words
	Text    string    `json:"text"`
	Notes   []Note    `json:"notes,omitempty"`
	Solved  string    `json:"solved,omitempty"` // how, once solved
	Last    time.Time `json:"last"`             // the latest word on it
}

// Open says whether the problem still stands.
func (p Problem) Open() bool { return p.Solved == "" }

// Module is the problems board.
type Module struct {
	C *core.Core

	mu    sync.Mutex
	items map[string]*Problem
}

// quiet is the roles that weigh one thing on its own (a step, a change), whom
// the board would only distract.
var quiet = map[string]bool{"judge": true, "warden": true, "tribunal": true, "filer": true, "module-reviewer": true, "stepback": true, "reviewer": true}

// New loads the board from the journal and registers its ops.
func New(c *core.Core) *Module {
	m := &Module{C: c, items: map[string]*Problem{}}
	apply := func(e core.Event) {
		if e.Kind != "problem.saved" {
			return
		}
		var p Problem
		if e.Decode(&p) != nil || p.ID == "" {
			return
		}
		m.mu.Lock()
		m.items[p.ID] = &p
		m.mu.Unlock()
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	c.Runs.Ops().Register(core.Op{Name: "problem", Help: `{"text": "what you ran into, what you found, how to reproduce", "where": "(optional) the part it is in"} -> posted on the Hive's problems board with your run id; {"id": "...", "also": "met it too / a clue / the cause"} adds to one; {"id": "...", "solved": "how (commit, task)"} closes one`, Fn: func(r core.Run, args json.RawMessage) (any, error) {
		var a struct{ ID, Text, Where, Also, Solved string }
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
		switch {
		case a.ID == "":
			p, err := m.Post(r, a.Where, a.Text)
			if err != nil {
				return nil, err
			}
			out := "posted as " + p.ID + "; every agent started from now carries it until it is solved"
			if like := m.like(p, r.Project); len(like) > 0 {
				out += ". Open problems that look alike (add to one with --id <id> --also, and close yours with --solved \"same as <id>\"):\n" + lines(like)
			}
			return out, nil
		case a.Solved != "":
			p, err := m.Solve(r, a.ID, a.Solved)
			if err != nil {
				return nil, err
			}
			return p.ID + " is solved; its poster " + p.Run + " isn't told on its own, so message it if it waits on this", nil
		default:
			p, err := m.Add(r, a.ID, cmpOr(a.Also, a.Text))
			if err != nil {
				return nil, err
			}
			return "added to " + p.ID + "; its poster is " + p.Run, nil
		}
	}})
	c.Runs.Ops().Register(core.Op{Name: "problems", Help: `{"all": "(optional) yes: the solved ones too"} -> the Hive's problems board, latest first`, Fn: func(r core.Run, args json.RawMessage) (any, error) {
		var a struct{ All string }
		if err := core.Bind(args, &a); err != nil {
			return nil, err
		}
		list := m.List(func(p Problem) bool { return (a.All != "" || p.Open()) && m.visible(p, r.Project) })
		if len(list) == 0 {
			return "the board is clear: post what you run into with `hive agent problem --text \"...\"`", nil
		}
		return lines(list), nil
	}})
	return m
}

func cmpOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// Post puts a new problem on the board from run r.
func (m *Module) Post(r core.Run, where, text string) (Problem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Problem{}, errors.New("empty problem: say what you ran into and what you found")
	}
	now := time.Now().UTC()
	p := Problem{ID: "p-" + core.NewID(), At: now, Last: now, Run: r.ID, Role: r.Role, Name: r.Name, Project: r.Project,
		Where: core.Clip(strings.TrimSpace(where), 80), Text: core.Clip(text, 2000)}
	_, err := m.C.J.Append("problem.saved", p)
	return p, err
}

// Standing keeps run r's one open problem under where in r.Project: posted
// when none is open, its text replaced when it says something new, and
// solved when text is "" (a module's standing report, such as improve's
// quarantined tests).
func (m *Module) Standing(r core.Run, where, text string) error {
	text = strings.TrimSpace(text)
	open := m.List(func(p Problem) bool { return p.Open() && p.Run == r.ID && p.Project == r.Project && p.Where == where })
	switch {
	case len(open) == 0 && text == "":
		return nil
	case len(open) == 0:
		_, err := m.Post(r, where, text)
		return err
	case text == "":
		_, err := m.save(r, open[0].ID, func(p *Problem) { p.Solved = "nothing stands any more (" + r.ID + ")" })
		return err
	case open[0].Text == core.Clip(text, 2000):
		return nil
	}
	_, err := m.save(r, open[0].ID, func(p *Problem) { p.Text = core.Clip(text, 2000) })
	return err
}

// Add puts run r's word on problem id.
func (m *Module) Add(r core.Run, id, text string) (Problem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Problem{}, errors.New("say what you add: --also \"...\"")
	}
	return m.save(r, id, func(p *Problem) {
		p.Notes = append(p.Notes, Note{At: time.Now().UTC(), Run: r.ID, Role: r.Role, Text: core.Clip(text, 1000)})
		if len(p.Notes) > 20 {
			p.Notes = p.Notes[len(p.Notes)-20:]
		}
	})
}

// Solve closes problem id, saying how.
func (m *Module) Solve(r core.Run, id, how string) (Problem, error) {
	how = strings.TrimSpace(how)
	if how == "" {
		return Problem{}, errors.New("say how it was solved: --solved \"...\"")
	}
	return m.save(r, id, func(p *Problem) { p.Solved = core.Clip(how+" ("+r.ID+")", 500) })
}

func (m *Module) save(r core.Run, id string, change func(*Problem)) (Problem, error) {
	p, ok := m.Get(id)
	if !ok || !m.visible(p, r.Project) {
		return Problem{}, fmt.Errorf("no problem %q on the board (`hive agent problems` lists them)", id)
	}
	change(&p)
	p.Last = time.Now().UTC()
	_, err := m.C.J.Append("problem.saved", p)
	return p, err
}

// Get finds one problem.
func (m *Module) Get(id string) (Problem, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.items[id]
	if !ok {
		return Problem{}, false
	}
	return *p, true
}

// List returns problems keep admits, the latest word first.
func (m *Module) List(keep func(Problem) bool) []Problem {
	m.mu.Lock()
	var out []Problem
	for _, p := range m.items {
		if keep == nil || keep(*p) {
			out = append(out, *p)
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// visible says whether a run in project may see p: an isolated project's
// problems stay in it, and its runs see only its own.
func (m *Module) visible(p Problem, project string) bool {
	if p.Project == project {
		return true
	}
	return !m.isolated(p.Project) && !m.isolated(project)
}

func (m *Module) isolated(project string) bool {
	if project == "" {
		return false
	}
	pr, ok := m.C.Projects.Get(project)
	return ok && pr.Isolated
}

// like is the open problems besides p that share two or more words with it.
func (m *Module) like(p Problem, project string) []Problem {
	words := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(p.Where + " " + p.Text)) {
		if len(w) > 4 {
			words[w] = true
		}
	}
	return m.List(func(o Problem) bool {
		if o.ID == p.ID || !o.Open() || !m.visible(o, project) {
			return false
		}
		n := 0
		seen := map[string]bool{}
		for _, w := range strings.Fields(strings.ToLower(o.Where + " " + o.Text)) {
			if words[w] && !seen[w] {
				seen[w] = true
				n++
			}
		}
		return n >= 2
	})
}

// lines renders problems one paragraph each.
func lines(list []Problem) string {
	var b strings.Builder
	for _, p := range list {
		where := p.Project
		if where == "" {
			where = "hive"
		}
		if p.Where != "" {
			where += ", " + p.Where
		}
		fmt.Fprintf(&b, "- **%s** (%s; from %s %s, %s", p.ID, where, p.Role, p.Run, p.At.Format("01-02 15:04"))
		if n := len(p.Notes); n > 0 {
			fmt.Fprintf(&b, "; %d more", n)
		}
		fmt.Fprintf(&b, "): %s\n", oneLine(core.Clip(p.Text, 300)))
		if n := len(p.Notes); n > 0 {
			last := p.Notes[n-1]
			fmt.Fprintf(&b, "  - latest, %s %s: %s\n", last.Role, last.Run, oneLine(core.Clip(last.Text, 200)))
		}
		if !p.Open() {
			fmt.Fprintf(&b, "  - solved: %s\n", oneLine(p.Solved))
		}
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Brief is the board's section of a run's system prompt: the open problems a
// run of role in project may see, latest first, or "" when there are none or
// the role weighs one thing on its own.
func (m *Module) Brief(role, project string) string {
	if quiet[role] {
		return ""
	}
	list := m.List(func(p Problem) bool { return p.Open() && m.visible(p, project) })
	if len(list) == 0 {
		return ""
	}
	more := ""
	if len(list) > 8 {
		more = fmt.Sprintf("\n%d more: `hive agent problems`.\n", len(list)-8)
		list = list[:8]
	}
	return "## The Hive's open problems\n\nWhat other agents are running into now, from the problems board. If your work touches one, or you find its cause or a fix on the way, add to it (`hive agent problem --id <id> --also \"...\"`) and message its poster; close it with `--solved \"how\"` once it is. Post what you run into yourself with `hive agent problem --text \"...\" --where \"...\"`.\n\n" + lines(list) + more
}
