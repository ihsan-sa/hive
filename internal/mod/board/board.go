// Package board is messaging as a module: every message between agents is a
// post on one public board, kept in threads. An agent adds itself to the
// threads it cares about, searches the board for what it needs, and is woken
// only when a post names it: the wake says where to read, not what was said,
// so nothing is pushed into its context (the person, 10-30: "all messages are
// public on a board and agents can add themselves"; "they dont read anything
// but they find whats needed").
//
// The core keeps only what a wake needs (Runs.Send, Find, LeadOf, Walled);
// who an address means, the threads and the reading are the board's.
package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Post is one message on the board.
type Post struct {
	ID      string    `json:"id"`
	Thread  string    `json:"thread"` // the first post's id
	At      time.Time `json:"at"`
	From    string    `json:"from"` // a run id
	Name    string    `json:"name,omitempty"`
	Project string    `json:"project,omitempty"` // the sender's
	To      []string  `json:"to,omitempty"`      // the runs it woke
	Text    string    `json:"text"`
}

// Router takes a message to an address the board doesn't own (the person, a
// chat's room, a task's filer): ok says it took it.
type Router func(from core.Run, to, text string) (ok bool, err error)

// Module is the board.
type Module struct {
	C *core.Core

	mu      sync.Mutex
	posts   []Post
	at      map[string]int             // post id -> index in posts
	members map[string]map[string]bool // thread -> runs in it
	seen    map[string]int             // run -> posts it has looked through (in memory: a restart shows them again)
	routers []Router
}

var (
	ofMu sync.Mutex
	of   = map[*core.Core]*Module{}
)

// The board's ops are there from the moment a core opens, so a module that
// wraps one (risk wraps "message") finds it whatever order it is built in.
func init() { core.OnOpen(func(c *core.Core) { Of(c) }) }

// Of is the board of c, made as c opens, so every module that sends a
// message (chat, work) reaches the same one without wiring it through.
func Of(c *core.Core) *Module {
	ofMu.Lock()
	defer ofMu.Unlock()
	if m := of[c]; m != nil {
		return m
	}
	m := &Module{C: c, at: map[string]int{}, members: map[string]map[string]bool{}, seen: map[string]int{}}
	c.J.ReplayExcept(m.apply, "task.saved", "run.created")
	c.J.Subscribe(m.apply)
	m.register()
	of[c] = m
	return m
}

// AddRouter installs a router; the first that takes an address wins.
func (m *Module) AddRouter(r Router) {
	m.mu.Lock()
	m.routers = append(m.routers, r)
	m.mu.Unlock()
}

// Every post is a journal event: "msg.sent" for each run it woke (as before
// the board, so the feed and the tallies read it unchanged) and "board.post"
// for one that woke nobody. A msg.sent from before the board is its own post.
func (m *Module) apply(e core.Event) {
	switch e.Kind {
	case "msg.sent", "board.post":
		var s struct {
			Post, Thread, From, Name, Project, To, Text string
		}
		if e.Decode(&s) != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if i, ok := m.at[s.Post]; ok && s.Post != "" {
			if s.To != "" {
				m.posts[i].To = append(m.posts[i].To, s.To)
				m.join(s.Thread, s.To)
			}
			return
		}
		id := s.Post
		if id == "" {
			id = fmt.Sprintf("m%d", e.Seq)
		}
		p := Post{ID: id, Thread: s.Thread, At: e.At, From: s.From, Name: s.Name, Project: s.Project, Text: s.Text}
		if p.Thread == "" {
			p.Thread = id
		}
		if s.To != "" {
			p.To = []string{s.To}
			m.join(p.Thread, s.To)
		}
		m.join(p.Thread, s.From)
		m.at[id] = len(m.posts)
		m.posts = append(m.posts, p)
	case "board.join", "board.leave":
		var s struct{ Run, Thread string }
		if e.Decode(&s) != nil {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if e.Kind == "board.join" {
			m.join(s.Thread, s.Run)
		} else {
			delete(m.members[s.Thread], s.Run)
		}
	}
}

func (m *Module) join(thread, run string) {
	if m.members[thread] == nil {
		m.members[thread] = map[string]bool{}
	}
	m.members[thread][run] = true
}

// Deliver posts text from a run, naming one address: a run id or name,
// "lead" (the sender's own project's lead, issue #2040), "parent" (whoever
// started it, else that lead), or one a router owns ("person", "room").
func (m *Module) Deliver(from core.Run, to, text string) (any, error) {
	return m.Post(from, "", []string{to}, text)
}

// Post puts text on the board from a run, in thread (a new one when empty),
// and wakes each address in to and each @run named in the text.
func (m *Module) Post(from core.Run, thread string, to []string, text string) (any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("empty message")
	}
	if thread != "" {
		m.mu.Lock()
		i, ok := m.at[thread]
		if ok {
			thread = m.posts[i].Thread // a reply to any post goes in its thread
		}
		m.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("no thread %q on the board (try op board)", thread)
		}
		if err := m.reach(from, thread); err != nil {
			return nil, err
		}
	}
	var runs []core.Run
	var routed []string
	for _, addr := range to {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		r, took, err := m.resolve(from, addr, text)
		if err != nil {
			return nil, err
		}
		if took {
			routed = append(routed, addr)
		} else {
			runs = append(runs, r)
		}
	}
	for _, name := range mentioned(text) {
		if r, ok := m.C.Runs.Find(name); ok && (r.ID == name || r.Name == name) && !r.Terminal() && m.C.Runs.Walled(from, r.Project) == nil {
			runs = append(runs, r)
		}
	}
	id := "p-" + core.NewID()
	about := text
	if thread == "" {
		thread = id
	} else {
		about = m.first(thread)
	}
	base := map[string]string{"post": id, "thread": thread, "from": from.ID, "name": from.Name, "project": from.Project, "text": core.Clip(text, 2000)}
	woke := map[string]bool{}
	for _, r := range runs {
		if woke[r.ID] || r.ID == from.ID {
			continue
		}
		woke[r.ID] = true
		if err := m.C.Runs.Send(r.ID, wake(from, thread, about)); err != nil {
			return nil, err
		}
		ev := map[string]string{"to": r.ID}
		for k, v := range base {
			ev[k] = v
		}
		if _, err := m.C.J.Append("msg.sent", ev); err != nil {
			return nil, err
		}
	}
	if len(woke) == 0 {
		// Said to an address a router took (the person, the room), or to
		// nobody: on the board all the same.
		base["to"] = strings.Join(routed, ",")
		if _, err := m.C.J.Append("board.post", base); err != nil {
			return nil, err
		}
	}
	return map[string]string{"post": id, "thread": thread, "note": "on the board; whoever it named is woken (end your turn to wait for a reply)"}, nil
}

// resolve finds the run an address means, or hands it to a router.
func (m *Module) resolve(from core.Run, to, text string) (core.Run, bool, error) {
	if to == "parent" && from.Parent != "" {
		to = from.Parent
	}
	m.mu.Lock()
	routers := append([]Router{}, m.routers...)
	m.mu.Unlock()
	for _, route := range routers {
		if ok, err := route(from, to, text); ok || err != nil {
			return core.Run{}, true, err
		}
	}
	// A worker's "parent" with none of its own (its task's filer, when a
	// router didn't take it) is its project's lead; Find("") matches any
	// run, so a run with no parent fails here.
	if to == "parent" && from.Task == "" {
		return core.Run{}, false, fmt.Errorf("%s has no parent to message", from.ID)
	}
	if to == "lead" || to == "parent" {
		project := from.Project
		if from.Lead() { // a project's lead's "lead" is the Hive's
			project = ""
		}
		lead, ok := m.C.Runs.LeadOf(project)
		if !ok {
			return core.Run{}, false, fmt.Errorf("%s has no %s to message", from.ID, to)
		}
		to = lead.ID
	}
	target, ok := m.C.Runs.Find(to)
	if !ok || m.C.Runs.Walled(from, target.Project) != nil {
		return core.Run{}, false, fmt.Errorf("no agent %q (try op agents)", to)
	}
	return target, false, nil
}

// wake is all a named run is given: who named it and where to read. The
// thread's first line says what it is about, so the run can tell whether
// to look now.
func wake(from core.Run, thread, about string) string {
	label := from.Name
	if label == "" {
		label = from.Role + " " + from.ID
	}
	return fmt.Sprintf("[board · %s] %s (%s) named you in thread %s (%q). Read it: `hive agent board --thread %s`; answer in it: `hive agent post --thread %s --to %s --text \"...\"`.",
		time.Now().Format("15:04"), label, from.ID, thread, core.Clip(firstLine(about), 80), thread, thread, from.ID)
}

// first is the text of a thread's first post.
func (m *Module) first(thread string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i, ok := m.at[thread]; ok {
		return m.posts[i].Text
	}
	return ""
}

var mention = regexp.MustCompile(`(?:^|\s)@([A-Za-z0-9][A-Za-z0-9:._-]*[A-Za-z0-9])`)

func mentioned(text string) []string {
	var out []string
	for _, s := range mention.FindAllStringSubmatch(text, -1) {
		out = append(out, s[1])
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// reach refuses a thread r can't see: one begun on a project hidden from r's.
func (m *Module) reach(r core.Run, thread string) error {
	m.mu.Lock()
	i, ok := m.at[thread]
	var p Post
	if ok {
		p = m.posts[i]
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no thread %q on the board (try op board)", thread)
	}
	if m.visible(r, p) {
		return nil
	}
	return fmt.Errorf("no thread %q on the board (try op board)", thread)
}

// visible says r may read p: it was named in it, or p's project isn't walled
// off from r's.
func (m *Module) visible(r core.Run, p Post) bool {
	if p.From == r.ID {
		return true
	}
	for _, to := range p.To {
		if to == r.ID {
			return true
		}
	}
	return m.C.Runs.Walled(r, p.Project) == nil
}

// Query is what an agent asks the board for.
type Query struct {
	Thread string `json:"thread"`
	Query  string `json:"query"`
	From   string `json:"from"`
	Limit  int    `json:"limit"`
}

// Read answers a query as r: a thread whole, the posts matching words, or,
// with neither, what is new for r since it last looked (posts naming it and
// posts in its threads) and the newest threads.
func (m *Module) Read(r core.Run, q Query) any {
	if q.Limit <= 0 {
		q.Limit = 30
	}
	words := strings.Fields(strings.ToLower(q.Query))
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Post
	keep := func(p Post) bool {
		if !m.visible(r, p) {
			return false
		}
		if q.From != "" && p.From != q.From && p.Name != q.From {
			return false
		}
		hay := strings.ToLower(p.Text + " " + p.Name + " " + p.From + " " + p.Project + " " + strings.Join(p.To, " "))
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
		return true
	}
	if q.Thread != "" {
		thread := q.Thread
		if i, ok := m.at[thread]; ok {
			thread = m.posts[i].Thread
		}
		for _, p := range m.posts {
			if p.Thread == thread && keep(p) {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return fmt.Sprintf("no thread %q on the board", q.Thread)
		}
		return map[string]any{"thread": thread, "members": m.list(thread), "posts": last(out, q.Limit)}
	}
	if len(words) > 0 || q.From != "" {
		for _, p := range m.posts {
			if keep(p) {
				out = append(out, p)
			}
		}
		return last(out, q.Limit)
	}
	from := m.seen[r.ID]
	for _, p := range m.posts[min(from, len(m.posts)):] {
		if p.From != r.ID && m.members[p.Thread][r.ID] && keep(p) {
			out = append(out, p)
		}
	}
	m.seen[r.ID] = len(m.posts)
	type thread struct {
		ID      string    `json:"id"`
		About   string    `json:"about"`
		Posts   int       `json:"posts"`
		Last    time.Time `json:"last"`
		Members []string  `json:"members"`
	}
	threads := map[string]*thread{}
	for _, p := range m.posts {
		if !m.visible(r, p) {
			continue
		}
		t := threads[p.Thread]
		if t == nil {
			t = &thread{ID: p.Thread, About: core.Clip(firstLine(p.Text), 100)}
			threads[p.Thread] = t
		}
		t.Posts++
		t.Last = p.At
	}
	var newest []*thread
	for _, t := range threads {
		t.Members = m.list(t.ID)
		newest = append(newest, t)
	}
	sort.Slice(newest, func(i, j int) bool { return newest[i].Last.After(newest[j].Last) })
	if len(newest) > 10 {
		newest = newest[:10]
	}
	return map[string]any{"new_for_you": last(out, q.Limit), "threads": newest}
}

func (m *Module) list(thread string) []string {
	var out []string
	for id := range m.members[thread] {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func last(ps []Post, n int) []Post {
	if len(ps) > n {
		return ps[len(ps)-n:]
	}
	return ps
}

func (m *Module) register() {
	ops := m.C.Runs.Ops()
	ops.Register(core.Op{Name: "message", Help: `{"to": "<run id | name | parent | lead | person>", "text": "...", "thread": "(optional: a post or thread id, to answer in it)"} -> a post on the board that wakes them (lead: your own project's lead; parent: whoever started or filed you, else that lead)`, Fn: func(r core.Run, a json.RawMessage) (any, error) {
		var s struct{ To, Text, Thread string }
		if err := core.Bind(a, &s); err != nil {
			return nil, err
		}
		if strings.TrimSpace(s.To) == "" {
			return nil, errors.New(`message needs --to; to post with no one named, use op post`)
		}
		return m.Post(r, s.Thread, []string{s.To}, s.Text)
	}})
	ops.Register(core.Op{Name: "post", Help: `{"text": "...", "thread": "(optional: a post or thread id; a new thread otherwise)", "to": "(optional: addresses to wake, comma-separated; @<run id> in the text wakes too)"} -> a post on the public board; it wakes only whom it names`, Fn: func(r core.Run, a json.RawMessage) (any, error) {
		var s struct{ Text, Thread, To string }
		if err := core.Bind(a, &s); err != nil {
			return nil, err
		}
		var to []string
		if s.To != "" {
			to = strings.Split(s.To, ",")
		}
		return m.Post(r, s.Thread, to, s.Text)
	}})
	ops.Register(core.Op{Name: "board", Help: `{"thread": "(a thread or post id: the thread whole)", "query": "(words: every post holding them all)", "from": "(a run id or name)", "limit": 30} -> with none, what is new for you (posts naming you or in threads you are in) and the newest threads`, Fn: func(r core.Run, a json.RawMessage) (any, error) {
		var q Query
		if err := core.Bind(a, &q); err != nil {
			return nil, err
		}
		return m.Read(r, q), nil
	}})
	for _, kind := range []string{"join", "leave"} {
		ops.Register(core.Op{Name: kind, Help: `{"thread": "<thread or post id>"} -> ` + map[string]string{"join": "add yourself to a thread: its new posts show in your op board (only a post naming you wakes you)", "leave": "take yourself off a thread"}[kind], Fn: func(r core.Run, a json.RawMessage) (any, error) {
			var s struct{ Thread string }
			if err := core.Bind(a, &s); err != nil {
				return nil, err
			}
			if err := m.reach(r, s.Thread); err != nil {
				return nil, err
			}
			m.mu.Lock()
			thread := m.posts[m.at[s.Thread]].Thread
			m.mu.Unlock()
			if _, err := m.C.J.Append("board."+kind, map[string]string{"run": r.ID, "thread": thread}); err != nil {
				return nil, err
			}
			return map[string]string{"join": "in thread ", "leave": "off thread "}[kind] + thread, nil
		}})
	}
}
