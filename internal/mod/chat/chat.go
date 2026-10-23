// Package chat is the chat module: the lead's chat, each project lead's chat,
// and a chat with any running agent. Threads keep an ask and everything that
// follows it together. Status noise stays out; it belongs on the dashboard.
package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
)

// Chat is one conversation.
type Chat struct {
	ID      string    `json:"id"` // lead | project:<name> | agent:<run>
	Title   string    `json:"title"`
	Kind    string    `json:"kind"` // lead | project | agent
	Project string    `json:"project,omitempty"`
	Lead    string    `json:"lead,omitempty"` // lead run id
	Created time.Time `json:"created"`
}

// Message is one line in a chat.
type Message struct {
	ID     string    `json:"id"`
	Chat   string    `json:"chat"`
	Thread string    `json:"thread"`
	From   string    `json:"from"` // person | hive | <run id>
	Name   string    `json:"name"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// File is an attachment the person pasted or dropped into a message.
type File struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// Limit caps a posted message's body at mb megabytes (the chat_max_mb
// setting), so the daemon never buffers an unbounded POST, and going over
// says which setting raises it.
func Limit(body io.ReadCloser, mb int) io.ReadCloser {
	return limited{http.MaxBytesReader(nil, body, int64(mb)<<20), mb}
}

type limited struct {
	io.ReadCloser
	mb int
}

func (l limited) Read(p []byte) (int, error) {
	n, err := l.ReadCloser.Read(p)
	var over *http.MaxBytesError
	if errors.As(err, &over) {
		err = fmt.Errorf("the message is over the %d MB limit; chat_max_mb in .hive/config.json raises it", l.mb)
	}
	return n, err
}

// cleanName is the base name with control characters and brackets dropped,
// so a file's name can't break the prompt's note or add text to it.
func cleanName(n string) string {
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '[' || r == ']' {
			return -1
		}
		return r
	}, filepath.Base(n))
	if n == "" || n == "." || n == "/" {
		return "file"
	}
	return n
}

// Module holds chats.
type Module struct {
	C      *core.Core
	Agents *agents.Agents

	mu    sync.Mutex
	chats map[string]*Chat
	msgs  map[string][]Message
	read  map[string]int
}

// New loads chats and messages.
func New(c *core.Core, a *agents.Agents) *Module {
	m := &Module{C: c, Agents: a, chats: map[string]*Chat{}, msgs: map[string][]Message{}, read: map[string]int{}}
	apply := func(e core.Event) {
		if e.Kind == "chat.created" || e.Kind == "chat.updated" {
			var ch Chat
			e.Decode(&ch)
			m.mu.Lock()
			m.chats[ch.ID] = &ch
			m.mu.Unlock()
		}
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	c.J.Subscribe(m.onEvent)
	for id := range m.chats {
		core.ReadJSONL(m.file(id), func(raw []byte) {
			var msg Message
			if json.Unmarshal(raw, &msg) == nil {
				m.msgs[id] = append(m.msgs[id], msg)
			}
		})
	}
	if raw, err := os.ReadFile(filepath.Join(c.WS.Chats(), "read.json")); err == nil {
		json.Unmarshal(raw, &m.read)
	}
	m.ensure(Chat{ID: "lead", Title: "lead", Kind: "lead"})
	c.Runs.Ops().AddRouter(m.route)
	return m
}

func (m *Module) file(id string) string {
	return filepath.Join(m.C.WS.Chats(), strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(id)+".jsonl")
}

func (m *Module) ensure(ch Chat) *Chat {
	m.mu.Lock()
	existing := m.chats[ch.ID]
	m.mu.Unlock()
	if existing != nil {
		return existing
	}
	ch.Created = time.Now().UTC()
	m.C.J.MustAppend("chat.created", ch)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chats[ch.ID]
}

// EnsureProject creates a project's chat.
func (m *Module) EnsureProject(project string) *Chat {
	return m.ensure(Chat{ID: "project:" + project, Title: project, Kind: "project", Project: project})
}

// EnsureAgent creates a chat with a run.
func (m *Module) EnsureAgent(runID string) (*Chat, error) {
	r, ok := m.C.Runs.Get(runID)
	if !ok {
		return nil, core.ErrNotFound
	}
	title := r.Role + " " + shortID(r.ID)
	if r.Name != "" {
		title = r.Name
	}
	return m.ensure(Chat{ID: "agent:" + r.ID, Title: title, Kind: "agent", Project: r.Project, Lead: r.ID}), nil
}

func shortID(id string) string {
	if i := strings.LastIndexByte(id, '-'); i >= 0 && len(id)-i > 5 {
		return id[i+1 : i+6]
	}
	return id
}

func (m *Module) add(msg Message) Message {
	if msg.ID == "" {
		msg.ID = core.NewID()
	}
	if msg.Thread == "" {
		msg.Thread = msg.ID
	}
	msg.At = time.Now().UTC()
	core.AppendJSONL(m.file(msg.Chat), msg)
	m.mu.Lock()
	m.msgs[msg.Chat] = append(m.msgs[msg.Chat], msg)
	m.mu.Unlock()
	m.C.J.MustAppend("chat.posted", map[string]string{"chat": msg.Chat, "id": msg.ID, "thread": msg.Thread, "from": msg.From, "name": msg.Name, "text": msg.Text})
	return msg
}

// Post is the person, or the module from names, writing in a chat; it reaches
// the chat's lead or agent. Files go into the run's own dir ($HIVE_RUN/attach),
// and the prompt names their paths: the agent opens them with its own tools.
func (m *Module) Post(chatID, thread, from, text string, files ...File) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" && len(files) == 0 {
		return Message{}, fmt.Errorf("empty message")
	}
	shown := text
	for i, f := range files {
		files[i].Name = cleanName(f.Name)
		shown += "\n📎 " + files[i].Name
	}
	m.mu.Lock()
	ch := m.chats[chatID]
	m.mu.Unlock()
	if ch == nil {
		if strings.HasPrefix(chatID, "agent:") {
			var err error
			if ch, err = m.EnsureAgent(strings.TrimPrefix(chatID, "agent:")); err != nil {
				return Message{}, err
			}
		} else {
			return Message{}, core.ErrNotFound
		}
	}
	who, name, said, speaker := "person", "you", "the person", "Person"
	if from != "" {
		who, name, said, speaker = from, from, "module "+from, "Module "+from
	}
	msg := m.add(Message{Chat: chatID, Thread: thread, From: who, Name: name, Text: strings.TrimSpace(shown)})
	switch ch.Kind {
	case "agent":
		err := m.C.Runs.Takes(ch.Lead)
		note := ""
		if err == nil {
			note, err = m.attach(ch.Lead, files)
		}
		if err == nil {
			err = m.C.Runs.Send(ch.Lead, "["+said+", in your chat] "+text+note)
		}
		if err != nil {
			return msg, err
		}
	default:
		m.syncFor(ch)
		prompt := fmt.Sprintf("[chat %s · thread %s · %s] %s: %s", chatID, msg.Thread, time.Now().Format("Mon 15:04"), speaker, text)
		if err := m.deliverToLead(ch, prompt, files...); err != nil {
			return msg, err
		}
	}
	return msg, nil
}

// attach writes files into a run's dir and returns the note naming them.
func (m *Module) attach(run string, files []File) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	dir := filepath.Join(m.C.WS.RunRO(run), "attach")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	note := "\n[attached; the person's files, each at its path:"
	for i, f := range files {
		name := fmt.Sprintf("%s-%d-%s", core.NewID(), i, f.Name)
		if err := os.WriteFile(filepath.Join(dir, name), f.Data, 0o644); err != nil {
			return "", err
		}
		note += "\n" + m.C.Runs.RunPath(run) + "/attach/" + name
	}
	return note + "]", nil
}

// syncWait bounds the sync that runs before a person's post reaches its lead.
var syncWait = 10 * time.Second

// syncFor brings the clones a lead reads ($HIVE_PROJECTS) up to each remote's
// default branch, so it sees what the person pushed since it last looked. A
// sync that fails or outlasts syncWait is journaled, and the post goes on
// with what the clone has.
func (m *Module) syncFor(ch *Chat) {
	var wg sync.WaitGroup
	for _, p := range m.C.Projects.List() {
		if ch.Kind == "project" && p.Name != ch.Project {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := make(chan error, 1)
			go func() { _, err := m.C.Effects.Sync(p); done <- err }()
			var err error
			select {
			case err = <-done:
			case <-time.After(syncWait):
				err = fmt.Errorf("no answer from the remote within %s", syncWait)
			}
			if err != nil {
				m.C.J.MustAppend("chat.sync_failed", map[string]string{"chat": ch.ID, "project": p.Name, "error": err.Error()})
			}
		}()
	}
	wg.Wait()
}

// deliverToLead sends text to the chat's lead, starting the lead if needed.
// A lead never pushes, so it reads its project in the clone syncFor keeps
// current ($HIVE_PROJECTS). An isolated project's lead can't see that, so it
// works in a checkout of its own, and before each message its run dir gets
// main.bundle, the clone's tip, to fetch: host git never runs in its checkout.
// Files go in the lead's run dir once its id is known.
func (m *Module) deliverToLead(ch *Chat, text string, files ...File) error {
	var p core.Project
	if ch.Kind == "project" {
		var ok bool
		if p, ok = m.C.Projects.Get(ch.Project); !ok {
			return fmt.Errorf("project %s not found", ch.Project)
		}
	}
	bundle := func(id string) { m.bundleFor(ch, p, id) }
	if ch.Lead != "" {
		if r, ok := m.C.Runs.Get(ch.Lead); ok && (!r.Terminal()) {
			bundle(r.ID)
			note, err := m.attach(r.ID, files)
			if err != nil {
				return err
			}
			return m.C.Runs.Send(r.ID, text+note)
		}
	}
	id := "lead-" + strings.NewReplacer(":", "-").Replace(ch.ID) + "-" + core.NewID()[14:]
	note, err := m.attach(id, files)
	if err != nil {
		return err
	}
	text += note
	name, project := "lead", ""
	dir := filepath.Join(m.C.WS.Hive(), "leads", "main")
	if ch.Kind == "project" {
		project, name = ch.Project, "lead:"+ch.Project
		dir = filepath.Join(m.C.WS.Hive(), "leads", "project-"+project)
		if p.Isolated {
			bundle(id) // Runs.Start only makes missing dirs, so this first-turn bundle survives it
			// The clone's tip, or "" (an orphan branch) for an empty repository.
			base, err := core.Git(m.C.WS.Project(project), "rev-parse", "-q", "--verify", "refs/heads/"+p.Branch)
			if err != nil {
				base = ""
			}
			if dir, err = m.C.Effects.NewCheckout(p, id, base); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	spec := m.Agents.Spec(id, "lead", project, dir, text)
	spec.Name, spec.Persistent = name, true
	if _, err := m.C.Runs.Start(spec); err != nil {
		return err
	}
	c := *ch
	c.Lead = id
	m.C.J.MustAppend("chat.updated", c)
	return nil
}

// bundleFor puts main.bundle in an isolated project's run dir for its lead.
func (m *Module) bundleFor(ch *Chat, p core.Project, id string) {
	if !p.Isolated {
		return
	}
	// An empty repository has no main to bundle (git ran and exited 1); the
	// lead reads its checkout as is. Any other failure is journaled below.
	var exit *exec.ExitError
	if _, err := core.Git(m.C.WS.Project(p.Name), "rev-parse", "-q", "--verify", "refs/heads/"+p.Branch); errors.As(err, &exit) && exit.ExitCode() == 1 {
		return
	}
	os.MkdirAll(m.C.WS.RunRO(id), 0o755)
	if err := m.C.Effects.MainBundle(p, filepath.Join(m.C.WS.RunRO(id), "main.bundle")); err != nil {
		m.C.J.MustAppend("chat.sync_failed", map[string]string{"chat": ch.ID, "project": p.Name, "error": "bundle: " + err.Error()})
	}
}

// Notify posts an outcome into a thread and tells the chat's lead.
func (m *Module) Notify(chatID, thread, text string) {
	if chatID == "" {
		chatID = "lead"
	}
	m.mu.Lock()
	ch := m.chats[chatID]
	m.mu.Unlock()
	if ch == nil {
		return
	}
	m.add(Message{Chat: chatID, Thread: thread, From: "hive", Name: "hive", Text: text})
	if ch.Lead != "" {
		if r, ok := m.C.Runs.Get(ch.Lead); ok && !r.Terminal() {
			m.C.Runs.Send(r.ID, fmt.Sprintf("[notification · thread %s] %s", thread, text))
		}
	}
}

var threadRe = regexp.MustCompile(`thread ([0-9a-z-]+)`)

// CurrentThread is the chat thread a lead is working in this turn.
func (m *Module) CurrentThread(r core.Run) (chatID, thread string) {
	m.mu.Lock()
	for _, ch := range m.chats {
		if ch.Lead == r.ID && ch.Kind != "agent" {
			chatID = ch.ID
		}
	}
	m.mu.Unlock()
	raw, _ := os.ReadFile(filepath.Join(m.C.WS.RunHost(r.ID), fmt.Sprintf("turn-%03d.prompt", r.Turn)))
	if r.Turn == 0 || len(raw) == 0 {
		raw = []byte(r.Brief)
	}
	if all := threadRe.FindAllStringSubmatch(string(raw), -1); len(all) > 0 {
		thread = all[len(all)-1][1]
	}
	return chatID, thread
}

// onEvent posts a lead's turn result into the thread it was answering.
func (m *Module) onEvent(e core.Event) {
	switch e.Kind {
	case "run.turn_ended":
		var t struct {
			ID     string `json:"id"`
			Turn   int    `json:"turn"`
			Status string `json:"status"`
			Result *struct {
				Text    string `json:"text"`
				IsError bool   `json:"is_error"`
			} `json:"result"`
		}
		e.Decode(&t)
		if t.Result == nil || t.Result.IsError {
			return
		}
		r, ok := m.C.Runs.Get(t.ID)
		if !ok || r.Role != "lead" {
			return
		}
		text := strings.TrimSpace(t.Result.Text)
		if text == "" || strings.HasPrefix(text, "NOOP") {
			return
		}
		r.Turn = t.Turn
		chatID, thread := m.CurrentThread(r)
		if chatID == "" {
			return
		}
		m.add(Message{Chat: chatID, Thread: thread, From: r.ID, Name: r.Name, Text: text})
	case "msg.sent":
		var s struct{ From, To, Text string }
		e.Decode(&s)
		for _, id := range []string{s.From, s.To} {
			m.mu.Lock()
			_, ok := m.chats["agent:"+id]
			m.mu.Unlock()
			if ok {
				m.add(Message{Chat: "agent:" + id, From: s.From, Name: s.From + " → " + s.To, Text: s.Text})
			}
		}
	}
}

// route handles messages addressed to the person.
func (m *Module) route(from core.Run, to, text string) (bool, error) {
	if to != "person" && to != "you" {
		return false, nil
	}
	if from.Role == "lead" {
		chatID, thread := m.CurrentThread(from)
		if chatID == "" {
			chatID = "lead"
		}
		m.add(Message{Chat: chatID, Thread: thread, From: from.ID, Name: from.Name, Text: text})
		return true, nil
	}
	ch, err := m.EnsureAgent(from.ID)
	if err != nil {
		return true, err
	}
	m.add(Message{Chat: ch.ID, From: from.ID, Name: ch.Title, Text: text})
	return true, nil
}

// Chats returns all chats, leads first, then projects, then agents.
func (m *Module) Chats() []Chat {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Chat
	for _, c := range m.chats {
		out = append(out, *c)
	}
	rank := map[string]int{"lead": 0, "project": 1, "agent": 2}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns a chat.
func (m *Module) Get(id string) (Chat, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.chats[id]
	if !ok {
		return Chat{}, false
	}
	return *c, true
}

// Messages returns a chat's messages.
func (m *Module) Messages(id string) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message{}, m.msgs[id]...)
}

// Unread counts messages after the person last read the chat.
func (m *Module) Unread(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.msgs[id]) - m.read[id]
	if n < 0 {
		return 0
	}
	return n
}

// MarkRead records that the person has seen a chat.
func (m *Module) MarkRead(id string) {
	m.mu.Lock()
	m.read[id] = len(m.msgs[id])
	raw, _ := json.Marshal(m.read)
	m.mu.Unlock()
	core.WriteFileAtomic(filepath.Join(m.C.WS.Chats(), "read.json"), raw, 0o644)
}
