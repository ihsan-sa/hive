// Package tui is the control center: chats, the dashboard, what's waiting on
// the person, the knowledge base, modules and settings. A sidebar picks the section or chat, a
// chat's threads open in their own pane with the reply box under them, Esc
// goes one level up, and no line runs off the screen. Quitting never stops
// anything.
package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/feedback"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/kb"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

var sections = []string{"Chats", "Dashboard", "Waiting", "Feedback", "Knowledge", "Modules", "Settings"}

const (
	secChats = iota
	secDash
	secWaiting
	secFeedback
	secKB
	secModules
	secSettings
)

// detail is one level pushed on top of a section (a run's log, a task, an ask...).
type detail struct {
	kind   string // run | task | sweep | ask | module | diff | text
	id     string
	title  string
	scroll int
	sel    int
	text   string
	total  int // lines last drawn, for the header's "of n"
}

type model struct {
	c  *api.Client
	ws core.Workspace
	w  int
	h  int

	section int
	st      api.State
	loaded  bool
	err     string
	flash   string
	flashAt time.Time

	side    int    // sidebar cursor
	sideKey string // the row it's on, so rows coming and going above don't move it
	focus   int    // 0 sidebar, 1 main pane, 2 thread pane

	// chats
	chat      string // open chat
	thread    string // thread open in its pane; "" = none
	threadSel int    // channel cursor; -1 = none
	chScroll  int
	thScroll  int
	places    map[string]place // each chat's open thread and scroll while another is shown
	chFree    bool             // scrolled by hand, so the picked thread no longer pulls the channel back
	open      map[string]bool  // expanded threads and tree nodes
	closed    map[string]bool  // collapsed project groups
	msgs      map[string][]chat.Message
	input     textarea.Model
	composing bool              // keys go to the text box
	files     []chat.File       // attached to the message being written
	composeN  int               // counts composes, so a clipboard read can't land in a later one
	sent      int               // messages sent, to match answers
	reply     string            // thread being replied to; "" = new thread
	drafts    map[string]string // by chat and target
	outbox    []outMsg          // sent, not yet answered

	// dashboard: the live feed, or the status tree
	feed     []feedRow
	feedSeq  int64
	feedSel  int
	follow   string // a project, or "" for everyone
	onlyMe   bool
	dashTree bool
	stages   map[string]string // task stages seen, so the feed shows changes

	dashSel int
	dashKey string // the status row the cursor is on, as sideKey
	waitSel int
	fbSel   string // the feedback item selected, by id
	modSel  int
	kbDir   string // the knowledge base folder shown, relative to its root
	kbSel   int
	kbNew   []kb.Change // the knowledge base's recently updated entries
	kbNewAt time.Time   // when they were read
	setSel  int
	editing bool
	picking bool // a setting's pick-list is open
	pickSel int
	optSel  int
	stack   []detail
	runs    map[string]api.RunDetail
	tasks   map[string]work.Task

	since     []string
	showSince bool
	seen      int64 // the journal seq the person has seen up to, for the update notice
	notice    *api.Updated
	help      bool

	// whether the Hive answers: a look at its state under way since inflight,
	// unanswered since down (zero while it answers), daemon.json saying it
	// restarts, and the version it came back on, until backAt is old
	inflight   time.Time
	down       time.Time
	restarting bool
	back       string
	backAt     time.Time
	beat       time.Time // the last tick, shown as the status line's clock
	sel        selection // a mouse drag in progress
}

type stateMsg struct {
	st  api.State
	err error
}
type chatMsg struct {
	id   string
	msgs []chat.Message
}
type runMsg struct{ d api.RunDetail }
type taskMsg struct{ t work.Task }
type askMsg struct{ a core.Ask }
type sinceMsg []string
type flashMsg string
type tickMsg time.Time
type probeMsg bool // daemon.json says a restart is under way

// slowAfter is how long a look at the Hive's state may take before the
// status line says it isn't answering; backFor how long "back on" shows.
const slowAfter, backFor = 3 * time.Second, 10 * time.Second

type outMsg struct {
	n                  int
	chat, thread, text string
}
type sentMsg struct {
	out outMsg
	msg chat.Message
	err error
}

// Run opens the control center.
func Run(c *api.Client, ws core.Workspace) error {
	if os.Getenv("NO_COLOR") != "" { // NO_COLOR drops hue, not bold, faint and reverse
		lipgloss.SetColorProfile(termenv.ANSI)
		setHue(false)
	}
	m := newModel(c, ws)
	var ui struct{ Seq int64 }
	if raw, err := os.ReadFile(m.uiPath()); err == nil {
		json.Unmarshal(raw, &ui)
	}
	m.seen = ui.Seq
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func newModel(c *api.Client, ws core.Workspace) *model {
	in := textarea.New()
	in.ShowLineNumbers, in.Prompt, in.CharLimit = false, "", 8000
	in.SetPromptFunc(2, func(i int) string {
		if i == 0 {
			return "› "
		}
		return "  "
	})
	in.FocusedStyle.CursorLine = lipgloss.NewStyle()
	// The textarea's own greys and white read poorly on a light terminal and
	// ignore NO_COLOR: plain and faint instead.
	for _, st := range []*textarea.Style{&in.FocusedStyle, &in.BlurredStyle} {
		st.Prompt, st.EndOfBuffer, st.LineNumber, st.CursorLineNumber = lipgloss.NewStyle(), stDim, stDim, stDim
	}
	in.BlurredStyle.Text, in.BlurredStyle.CursorLine = stDim, stDim
	in.FocusedStyle.Placeholder, in.BlurredStyle.Placeholder = stDim, stDim
	in.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")
	return &model{c: c, ws: ws, open: map[string]bool{}, closed: map[string]bool{}, msgs: map[string][]chat.Message{},
		runs: map[string]api.RunDetail{}, tasks: map[string]work.Task{}, drafts: map[string]string{}, places: map[string]place{}, stages: map[string]string{}, input: in, threadSel: -1, feedSel: -1}
}

// sidePane: a pane is open beside the main one.
func (m *model) sidePane() bool { return m.section == secChats && m.thread != "" }

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.fetchState(), m.fetchSince(), m.fetchFeed(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) fetchState() tea.Cmd {
	return func() tea.Msg {
		st, err := m.c.State()
		return stateMsg{st, err}
	}
}

// probe reads daemon.json for a restart, off the UI loop.
func (m *model) probe() tea.Cmd {
	return func() tea.Msg { return probeMsg(m.c.Restarting()) }
}

func (m *model) fetchChat(id string) tea.Cmd {
	if id == "" {
		return nil
	}
	return func() tea.Msg {
		var msgs []chat.Message
		m.c.Do("GET", "/api/chat?id="+id, nil, &msgs)
		return chatMsg{id, msgs}
	}
}

func (m *model) fetchDetail() tea.Cmd {
	if len(m.stack) == 0 {
		return nil
	}
	d := m.stack[len(m.stack)-1]
	switch d.kind {
	case "run":
		return func() tea.Msg {
			var rd api.RunDetail
			m.c.Do("GET", "/api/run?id="+d.id, nil, &rd)
			return runMsg{rd}
		}
	case "task":
		return func() tea.Msg {
			var t work.Task
			m.c.Do("GET", "/api/task?id="+d.id, nil, &t)
			return taskMsg{t}
		}
	case "diff": // state carries a preview; the whole diff comes when it's opened
		return func() tea.Msg {
			var a core.Ask
			m.c.Do("GET", "/api/waiting?id="+d.id, nil, &a)
			return askMsg{a}
		}
	}
	return nil
}

func (m *model) post(path string, body any, ok string) tea.Cmd {
	return func() tea.Msg {
		if err := m.c.Do("POST", path, body, nil); err != nil {
			return flashMsg("error: " + err.Error())
		}
		return flashMsg(ok)
	}
}

// ui.json remembers where the person left off, to show what happened since.
func (m *model) uiPath() string { return filepath.Join(m.ws.Hive(), "ui.json") }

func (m *model) fetchSince() tea.Cmd {
	return func() tea.Msg {
		var ui struct{ Seq int64 }
		raw, err := os.ReadFile(m.uiPath())
		if err != nil {
			return sinceMsg(nil)
		}
		json.Unmarshal(raw, &ui)
		var evs []core.Event
		if m.c.Do("GET", fmt.Sprintf("/api/events?after=%d", ui.Seq), nil, &evs) != nil {
			return sinceMsg(nil)
		}
		return sinceMsg(summarize(evs))
	}
}

func (m *model) saveSeq() {
	raw, _ := json.Marshal(map[string]int64{"seq": m.st.Seq})
	core.WriteFileAtomic(m.uiPath(), raw, 0o644)
}

// summarize turns events into "while you were away" lines.
func summarize(evs []core.Event) []string {
	tasks := map[string]work.Task{}
	var out []string
	for _, e := range evs {
		switch e.Kind {
		case "task.saved":
			var t work.Task
			e.Decode(&t)
			prev, seen := tasks[t.ID]
			if (!seen || prev.Stage != t.Stage) && (t.Stage == work.StageMerged || t.Stage == work.StageBlocked || t.Stage == work.StageStopped || t.Stage == work.StagePerson) {
				label := map[string]string{work.StageMerged: "merged", work.StageBlocked: "blocked", work.StageStopped: "stopped", work.StagePerson: "needs you"}[t.Stage]
				if t.Kind == "revert" && t.Stage == work.StageMerged {
					label = "reverted"
				}
				out = append(out, fmt.Sprintf("%s  %s: %s", e.At.Local().Format("Mon 15:04"), label, t.Label()))
			}
			tasks[t.ID] = t
		case "person.asked":
			var a core.Ask
			e.Decode(&a)
			out = append(out, fmt.Sprintf("%s  waiting on you: %s", e.At.Local().Format("Mon 15:04"), a.Title))
		case "person.told":
			var a core.Ask
			e.Decode(&a)
			out = append(out, fmt.Sprintf("%s  done for you: %s", e.At.Local().Format("Mon 15:04"), a.Title))
		case "sweep.saved":
			var s struct{ ID, Status string }
			e.Decode(&s)
			if s.Status == core.StatusDone {
				out = append(out, fmt.Sprintf("%s  improvement sweep finished", e.At.Local().Format("Mon 15:04")))
			}
		case "hive.updated":
			var u api.Updated
			e.Decode(&u)
			line := fmt.Sprintf("%s  updated to %s", e.At.Local().Format("Mon 15:04"), u.To)
			if u.Unsigned {
				line += ", an unsigned release"
			}
			out = append(out, line)
		}
	}
	if len(out) > 30 {
		out = append([]string{fmt.Sprintf("(%d earlier)", len(out)-30)}, out[len(out)-30:]...)
	}
	return out
}

func (m *model) setFlash(s string) {
	m.flash, m.flashAt = s, time.Now()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, m.settle()
	case sentMsg:
		for i, o := range m.outbox {
			if o.n == msg.out.n {
				m.outbox = append(m.outbox[:i], m.outbox[i+1:]...)
				break
			}
		}
		if msg.err != nil { // the text goes back in the box, above anything typed since
			k := msg.out.chat + "\x00" + msg.out.thread
			back := func(cur string) string { return strings.TrimSpace(msg.out.text + "\n" + cur) }
			if m.composing && m.chat+"\x00"+m.reply == k {
				m.input.SetValue(back(m.input.Value()))
			} else {
				m.drafts[k] = back(m.drafts[k])
			}
			m.setFlash("✗ not sent · " + msg.err.Error())
			return m, nil
		}
		m.msgs[msg.out.chat] = append(m.msgs[msg.out.chat], msg.msg) // a new thread stays in the channel; the person opens it
	case tickMsg:
		m.beat = time.Time(msg)
		if !m.inflight.IsZero() { // the last look hasn't come back: no more piled on a Hive that isn't answering
			if m.down.IsZero() && time.Since(m.inflight) > slowAfter {
				m.down = m.inflight
			}
			return m, tea.Batch(m.probe(), tick())
		}
		m.inflight = time.Now()
		if !m.down.IsZero() {
			return m, tea.Batch(m.fetchState(), m.probe(), tick())
		}
		return m, tea.Batch(m.fetchState(), m.fetchChat(m.currentChatID()), m.fetchDetail(), m.fetchFeed(), tick())
	case probeMsg:
		m.restarting = bool(msg)
	case stateMsg:
		m.inflight = time.Time{}
		if msg.err != nil {
			m.err = "the Hive isn't answering: " + msg.err.Error()
			if m.down.IsZero() {
				m.down = time.Now()
			}
		} else {
			first := !m.loaded
			switch {
			case first:
			case msg.st.Version != m.st.Version:
				m.back, m.backAt = "back on "+msg.st.Version, time.Now()
			case !m.down.IsZero():
				m.back, m.backAt = "the Hive is answering again", time.Now()
			}
			m.down, m.restarting = time.Time{}, false
			m.err, m.st, m.loaded = "", msg.st, true
			if u := msg.st.Updated; u != nil && u.Seq > m.seen {
				m.notice, m.seen = u, u.Seq
			}
			for _, c := range m.st.Chats {
				if first && m.chat == "" && c.Kind == "lead" { // open on the Hive's lead
					return m, m.openChat(c.ID)
				}
			}
		}
	case feedMsg:
		return m, m.addEvents(msg)
	case chatMsg:
		m.msgs[msg.id] = msg.msgs
	case runMsg:
		m.runs[msg.d.Run.ID] = msg.d
	case taskMsg:
		m.tasks[msg.t.ID] = msg.t
	case askMsg:
		for i := range m.stack {
			if d := &m.stack[i]; d.kind == "diff" && d.id == msg.a.ID && msg.a.Diff != "" {
				d.text = msg.a.Diff
			}
		}
	case sinceMsg:
		m.since = msg
		m.showSince = len(msg) > 0
	case flashMsg:
		m.setFlash(string(msg))
		return m, m.fetchState()
	case clipMsg:
		switch {
		case msg.text != nil:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg.text)
			return m, cmd
		case msg.compose != m.composeN || !m.composing:
			// read for a message that has since been sent or dropped
		case msg.err != nil:
			m.setFlash(msg.err.Error())
		default:
			m.files = append(m.files, msg.f)
		}
	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		if ok, cmd := m.mouseSelect(msg); ok {
			return m, cmd
		}
		s := map[tea.MouseButton]string{tea.MouseButtonWheelUp: "wheelup", tea.MouseButtonWheelDown: "wheeldown"}[msg.Button]
		if s == "" || msg.Action != tea.MouseActionPress || m.help || m.showSince || m.notice != nil || m.editing {
			return m, nil
		}
		return m.wheel(m.paneUnder(msg.X), s)
	}
	return m, nil
}

// paneUnder is the pane under column x (0 sidebar, 1 main, 2 thread), so the
// wheel scrolls what the pointer is on, not what has the keys.
func (m *model) paneUnder(x int) int {
	if m.takeover() {
		return 2
	}
	sw, mw, tw := m.layout()
	switch {
	case x < sw:
		return 0
	case x <= sw+mw || tw == 0:
		return 1
	}
	return 2
}

// wheel scrolls a pane: text panes move their offset, lists move their
// cursor (one row a notch), and the sidebar stays put, since moving its
// cursor would change what the main pane shows.
func (m *model) wheel(p int, s string) (tea.Model, tea.Cmd) {
	key := tea.KeyMsg{Type: tea.KeyUp}
	if s == "wheeldown" {
		key = tea.KeyMsg{Type: tea.KeyDown}
	}
	switch {
	case p == 0:
	case p == 2:
		m.thScroll = scroll(m.thScroll, s, m.h-4)
	case len(m.stack) > 0:
		top := &m.stack[len(m.stack)-1]
		if top.kind == "module" { // the list follows the selection
			return m.keyDetailAction(top, key.String())
		}
		top.scroll = max(0, top.scroll+map[string]int{"wheelup": -3, "wheeldown": 3}[s])
	case m.section == secChats:
		m.chScroll, m.chFree = scroll(m.chScroll, s, m.h-4), true
	default: // a list: the cursor shows only with the focus, so the pane takes it
		if m.focus == 0 {
			m.focus = 1
		}
		switch m.section {
		case secDash:
			return m.keyDash(key)
		case secWaiting:
			return m.keyWaiting(key)
		case secKB:
			return m.keyKB(key)
		case secModules:
			return m.keyModules(key)
		case secSettings:
			return m.keySettings(key)
		}
	}
	return m, nil
}

func (m *model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		m.saveSeq()
		return m, tea.Quit
	}
	if m.showSince {
		m.showSince = false
		return m, nil
	}
	if m.notice != nil {
		m.notice = nil
		return m, nil
	}
	if m.help {
		m.help = false
		return m, nil
	}
	if m.editing {
		return m.keyInput(k)
	}
	if m.picking && (s == "q" || s == "tab" || s == "shift+tab") {
		m.picking = false // these close an open pick-list first
		return m, nil
	}
	if s == "tab" || s == "shift+tab" {
		return m, m.cycle(s == "tab")
	}
	if m.composing {
		return m.keyInput(k)
	}
	if k.Paste && m.section == secChats && m.chat != "" { // a drop starts a message and attaches to it
		m.focus = 1
		if m.thread != "" {
			m.focus = 2
		}
		focus := m.focusBox(m.thread)
		_, cmd := m.keyInput(k)
		return m, tea.Batch(focus, cmd)
	}
	switch s {
	case "q":
		m.saveSeq()
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	}
	if m.focus == 0 {
		return m.keySidebar(k)
	}
	if s == "esc" && len(m.stack) == 0 && m.section == secKB && m.kbDir != "" { // one folder up first
		return m.keyKB(k)
	}
	if s == "esc" && len(m.stack) == 0 && m.section != secChats { // one level up is the sidebar
		m.focus = 0
		return m, nil
	}
	if len(m.stack) > 0 {
		return m.keyDetail(k)
	}
	switch m.section {
	case secChats:
		return m.keyChannel(k)
	case secDash:
		return m.keyDash(k)
	case secWaiting:
		return m.keyWaiting(k)
	case secFeedback:
		return m.keyFeedback(k)
	case secKB:
		return m.keyKB(k)
	case secModules:
		return m.keyModules(k)
	case secSettings:
		return m.keySettings(k)
	}
	return m, nil
}

// cycle moves focus sidebar → main → thread pane and round.
func (m *model) cycle(fwd bool) tea.Cmd {
	order := []int{0, 1}
	switch {
	case m.takeover():
		return nil
	case m.sidePane():
		order = append(order, 2)
	}
	i := 0
	for j, f := range order {
		if f == m.focus {
			i = j
		}
	}
	if fwd {
		i++
	} else {
		i += len(order) - 1
	}
	m.blurBox()
	m.focus = order[i%len(order)]
	switch m.focus {
	case 1:
		return m.markRead()
	case 2:
		return m.focusBox(m.thread)
	}
	return nil
}

// settle gives a thread that takes over the screen the focus, since nothing
// else is on screen to take the keys.
func (m *model) settle() tea.Cmd {
	if !m.takeover() || m.focus == 2 {
		return nil
	}
	m.focus = 2
	return m.focusBox(m.thread)
}

func (m *model) push(d detail) tea.Cmd {
	m.stack = append(m.stack, d)
	return m.fetchDetail()
}

func (m *model) keyDetail(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.stack[len(m.stack)-1]
	switch k.String() {
	case "esc", "left", "backspace":
		m.stack = m.stack[:len(m.stack)-1]
		return m, nil
	}
	if d.kind == "module" { // ↑↓ pick a project and space grants, rather than scroll
		return m.keyDetailAction(d, k.String())
	}
	switch k.String() {
	case "up", "k":
		if d.scroll > 0 {
			d.scroll--
		}
	case "down", "j":
		d.scroll++
	case "pgdown", " ":
		d.scroll += m.bodyHeight() - 2
	case "pgup":
		d.scroll = max(0, d.scroll-m.bodyHeight()+2)
	case "home", "g":
		d.scroll = 0
	case "end", "G":
		d.scroll = 1 << 20 // drawing clamps it to the last page
	default:
		return m.keyDetailAction(d, k.String())
	}
	return m, nil
}

func (m *model) keyInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch s := k.String(); s {
	case "esc":
		if m.editing {
			m.editing = false
			m.input.Blur()
			m.input.SetValue("")
			return m, nil
		}
		m.blurBox()
		if m.reply != "" { // close the thread
			m.thread, m.focus, m.thScroll = "", 1, 0
		}
		return m, nil
	case "left":
		if !m.editing && m.reply != "" && m.input.Value() == "" && !m.takeover() { // over to the channel; the thread stays open
			m.blurBox()
			m.focus = 1
			return m, m.markRead()
		}
	case "up":
		if !m.editing && m.reply == "" && m.input.Value() == "" { // pick a thread
			m.blurBox()
			m.threadSel, m.chFree = len(threads(m.msgs[m.chat]))-1, false
			return m, nil
		}
	case "pgup", "pgdown":
		if m.reply != "" {
			m.thScroll = scroll(m.thScroll, s, m.h-4)
			return m, nil
		}
		if !m.editing { // the new-thread box scrolls the channel above it
			m.chScroll, m.chFree = scroll(m.chScroll, s, m.h-4), true
			return m, nil
		}
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		m.input.SetValue("")
		if m.editing {
			m.editing = false
			m.input.Blur()
			if m.section == secKB {
				return m, m.searchKB(text)
			}
			return m, m.saveSetting(text)
		}
		if text == "" && len(m.files) == 0 {
			return m, nil
		}
		return m, m.send(text)
	case "ctrl+v", "alt+v":
		// As in Claude Code: an image on the clipboard attaches; otherwise
		// ctrl+v pastes text as before.
		if m.composing {
			return m, readClipboard(true, m.composeN)
		}
	}
	if m.composing && k.Paste {
		// A drop arrives as a pasted path; an empty paste may be an image.
		if len(k.Runes) == 0 {
			return m, readClipboard(false, m.composeN)
		}
		if paths := pastedPaths(string(k.Runes)); paths != nil {
			maxMB := m.st.Config.ChatMaxMB
			if maxMB <= 0 { // no state yet
				maxMB = core.DefaultConfig().ChatMaxMB
			}
			var have int64
			for _, f := range m.files {
				have += int64(len(f.Data))
			}
			for _, p := range paths {
				f, err := readAttachment(p, have, maxMB)
				if err != nil {
					return m, flash("error: " + err.Error())
				}
				m.files = append(m.files, f)
				have += int64(len(f.Data))
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

// send posts at once; a reply shows as sending until the Hive answers.
func (m *model) send(text string) tea.Cmd {
	m.sent++
	o := outMsg{n: m.sent, chat: m.chat, thread: m.reply, text: text}
	files := m.files
	m.files, m.composeN = nil, m.composeN+1 // a clipboard read still running was for the sent message
	m.drafts[o.chat+"\x00"+o.thread] = ""
	if o.thread != "" {
		m.outbox = append(m.outbox, o)
	}
	return func() tea.Msg {
		var msg chat.Message
		err := m.c.Do("POST", "/api/chat", map[string]any{"chat": o.chat, "thread": o.thread, "text": text, "files": files}, &msg)
		return sentMsg{o, msg, err}
	}
}

func flash(s string) tea.Cmd { return func() tea.Msg { return flashMsg(s) } }

// clipMsg is a clipboard read: an image to attach, why there is none, or,
// for ctrl+v with no image, the box's own text paste.
type clipMsg struct {
	f       chat.File
	err     error
	text    tea.Msg
	compose int // the compose it was read for
}

// readClipboard reads the clipboard's image off the UI loop, since a tool
// like powershell.exe can take seconds.
func readClipboard(text bool, compose int) tea.Cmd {
	return func() tea.Msg {
		f, err := clipboardImage()
		if text && (err == errNoImage || errors.Is(err, errNoTool)) {
			return clipMsg{text: textarea.Paste(), compose: compose}
		}
		return clipMsg{f: f, err: err, compose: compose}
	}
}

func (m *model) bodyHeight() int { return max(3, m.h-4) }

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.w < 40 || m.h < 10 {
		return "Make the window larger, at least 40×10."
	}
	sw, mw, tw := m.layout()
	ps := []pane{m.viewSidebar(sw), m.viewMain(mw)}
	if m.takeover() {
		ps = []pane{m.viewThread(m.w)}
	} else if tw > 0 {
		ps = append(ps, m.viewThread(tw))
	}
	v := m.frame(ps, m.hintLine())
	if m.sel.on {
		screen := strings.Split(v, "\n")
		m.sel.paint(screen, m.w)
		v = strings.Join(screen, "\n")
	}
	return v
}

func (m *model) viewMain(w int) pane {
	title := sections[m.section]
	var body []string
	switch {
	case !m.loaded && m.err == "":
		body = []string{"", "connecting to the Hive…"}
	case m.showSince:
		body = append([]string{"", stTitle.Render("While you were away"), ""}, m.since...)
		body = append(body, "", stDim.Render("any key to continue"))
	case m.notice != nil:
		body = []string{"", stTitle.Render("Updated to "+m.notice.To) + stDim.Render("  any key to dismiss"), ""}
		if m.notice.Unsigned {
			body = append(append(body, wrapStyled(stWarn, release.Unsigned, w-2)...), "")
		}
		body = append(body, strings.Split(m.notice.Notes, "\n")...)
	case m.help:
		body = helpLines()
	case m.section == secChats:
		return m.viewChannel(w)
	case len(m.stack) > 0:
		for _, d := range m.stack {
			title += " › " + d.title
		}
		body = m.viewDetail(&m.stack[len(m.stack)-1])
	default:
		switch m.section {
		case secDash:
			body = m.viewFeed()
		case secWaiting:
			body = m.viewWaiting()
		case secFeedback:
			body = m.viewFeedback()
		case secKB:
			body = m.viewKB()
		case secModules:
			body = m.viewModules()
		case secSettings:
			body = m.viewSettings()
		}
	}
	note, right := "", ""
	switch {
	case len(m.stack) > 0 && !m.help && !m.showSince:
		if d := m.stack[len(m.stack)-1]; d.kind != "module" && d.total > m.bodyHeight() {
			right = fmt.Sprintf("%d–%d of %d", d.scroll+1, min(d.total, d.scroll+m.bodyHeight()), d.total)
		}
	case len(m.stack) > 0 || m.help || m.showSince:
	case m.section == secWaiting && len(m.st.Asks) > 0:
		note, right = fmt.Sprintf("%d need you", len(m.st.Asks)), "oldest first"
	case m.section == secFeedback && len(m.st.Feedback) > 0:
		open := 0
		for _, it := range m.st.Feedback {
			if it.Status == feedback.Open {
				open++
			}
		}
		note, right = fmt.Sprintf("%d open", open), "newest first"
	case m.section == secDash && m.dashTree:
		title += " › status"
	case m.section == secDash:
		note, right = "live · all projects", "following everyone"
		if m.follow != "" {
			note, right = "live", "following "+m.follow+"   F clear"
		}
		if m.onlyMe {
			right = "only you   F clear"
		}
	case m.section == secModules:
		on := 0
		for _, mod := range m.st.Modules {
			if len(mod.Projects) > 0 {
				on++
			}
		}
		note = fmt.Sprintf("%d installed · %d on", len(m.st.Modules), on)
	case m.section == secKB:
		note = filepath.Join(m.ws.KB(), filepath.FromSlash(m.kbDir))
	case m.section == secSettings:
		note = filepath.Join(m.st.Workspace, ".hive", "config.json")
	}
	if m.focus == 1 {
		title = stTitle.Render(title)
	}
	return pane{w: w, head: head(w, title, note, right), body: indent(body, " ")}
}

func (m *model) mainW() int {
	_, w, _ := m.layout()
	return w
}

// hintLine is the status line: the Hive's banner when it is updating, away
// or just back, else a flash, else the keys; and at its right a clock that
// moves with every screen update, so a still clock means the terminal or
// its connection is what froze, not the Hive.
func (m *model) hintLine() string {
	w, clock := m.w, ""
	if !m.beat.IsZero() {
		clock = stDim.Render(m.beat.Local().Format("15:04:05"))
		w -= visibleWidth(clock) + 2
	}
	line := m.statusLine(w)
	if clock == "" {
		return line
	}
	return pad(clip(line, w), w) + " " + clock + " "
}

func (m *model) statusLine(w int) string {
	if b := m.banner(); b != "" {
		return " " + stWarn.Render(b)
	}
	if m.err != "" {
		return " " + stBad.Render(m.err)
	}
	if m.flash != "" && time.Since(m.flashAt) < 4*time.Second {
		if strings.HasPrefix(m.flash, "✗") || strings.HasPrefix(m.flash, "error") {
			return " " + stBad.Render(m.flash)
		}
		return " " + stDim.Render(m.flash)
	}
	if m.back != "" && time.Since(m.backAt) < backFor {
		return " " + stGood.Render("✓ "+m.back)
	}
	return hints(w, m.keys()...)
}

// banner says what the Hive is doing when it can't be worked with as usual:
// not answering (restarting after an update, or for some other reason) or
// updating, with the step it is on.
func (m *model) banner() string {
	u := m.st.Updating
	if !m.down.IsZero() {
		n := int(time.Since(m.down).Seconds())
		if m.restarting || u != nil {
			to := ""
			if u != nil {
				to = " to " + u.To
			}
			return fmt.Sprintf("⟳ reconnecting… (the Hive is restarting after an update%s, %ds)", to, n)
		}
		why := ""
		if m.err != "" {
			why = ": " + strings.TrimPrefix(m.err, "the Hive isn't answering: ")
		}
		return fmt.Sprintf("⟳ reconnecting… (the Hive is not answering for %ds%s)", n, why)
	}
	if u != nil {
		return "⟳ updating to " + u.To + ": " + u.Text()
	}
	return ""
}

// keys are the hint line's "key label" pairs for where the focus is.
func (m *model) keys() []string {
	switch {
	case m.showSince || m.help:
		return []string{"any key", "close"}
	case m.editing && m.section == secKB:
		return []string{"enter", "search", "esc", "cancel"}
	case m.editing:
		return []string{"enter", "save", "esc", "cancel"}
	case m.focus == 0:
		return []string{"↑↓", "move", "enter", "open", "space", "collapse", "tab", "switch pane", "q", "quit"}
	case m.section == secChats && m.takeover():
		return []string{"enter", "send", "ctrl+j", "newline", "esc", "back to chat"}
	case m.section == secChats && m.focus == 2:
		return []string{"enter", "send", "ctrl+j", "newline", "esc", "close thread", "← tab", "to the channel", "pgup pgdn", "scroll"}
	case m.section == secChats && m.composing:
		return []string{"enter", "start thread", "ctrl+j", "newline", "esc", "leave box", "↑", "pick a thread", "tab", "switch pane"}
	case m.section == secChats && m.thread != "":
		return []string{"↑↓", "thread", "→", "to the open thread", "pgup pgdn", "scroll", "space", "collapse", "n", "new thread"}
	case m.section == secChats:
		return []string{"↑↓", "thread", "enter", "open", "n", "new thread", "space", "collapse", "tab", "pane"}
	case len(m.stack) > 0 && m.stack[len(m.stack)-1].kind == "module":
		return []string{"↑↓", "project", "space", "grant or remove", "r", "review again", "esc", "back", "q", "quit"}
	case len(m.stack) > 0:
		return []string{"↑↓", "scroll", "esc", "back", "tab", "sidebar", "q", "quit"}
	case m.section == secDash:
		if m.dashTree {
			return []string{"↑↓", "move", "enter", "open", "←", "collapse", "s", "feed", "tab", "sidebar"}
		}
		return []string{"enter", "open thread", "f", "follow", "m", "only me", "s", "status", "tab", "sidebar"}
	case m.section == secWaiting && m.waitSel < len(m.st.Asks) && m.st.Asks[m.waitSel].Diff != "":
		return []string{"↑↓", "move", "←→", "pick", "enter", "answer", "d", "whole diff", "tab", "sidebar"}
	case m.section == secWaiting:
		return []string{"↑↓", "move", "←→", "pick", "enter", "answer", "tab", "sidebar"}
	case m.section == secFeedback:
		return m.feedbackKeys()
	case m.section == secKB:
		return []string{"↑↓", "move", "enter", "open", "/", "search", "esc", "up a folder", "tab", "sidebar"}
	case m.section == secModules:
		return []string{"enter", "configure", "↑↓", "move", "tab", "sidebar"}
	case m.section == secSettings && m.picking:
		return []string{"↑↓", "choose", "enter", "pick", "esc", "cancel"}
	}
	return []string{"↑↓", "move", "enter", "change", "tab", "sidebar"}
}

func helpLines() []string {
	return []string{"", stTitle.Render("Keys"), "",
		"tab / shift+tab   switch pane: sidebar, main, thread",
		"↑ ↓               move",
		"enter             open; in a box, send",
		"esc               leave a box, close a thread, one level up",
		"space             expand or collapse",
		"← →               in the sidebar and status, back up a level or go deeper",
		"n                 new thread",
		"a                 in a chat, answer what's waiting on you",
		"d                 in Waiting, the whole diff of a change",
		"p / x             in Feedback, pass it to the lead / set it aside",
		"/                 in Knowledge, search the knowledge base",
		"ctrl+j            new line in a box",
		"pgup pgdn, wheel  scroll; the wheel scrolls the pane under it",
		"home end          the top or the end",
		"q                 quit the control center; the Hive keeps working",
		"", stDim.Render("any key to close")}
}

// clip keeps a line within the screen width.
func clip(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// wrap breaks text into lines of at most w cells.
func wrap(s string, w int) []string {
	if w < 10 {
		w = 10
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(para, w, " "), "\n")...)
	}
	return out
}

func indent(lines []string, p string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = p + l
	}
	return out
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// window returns the slice of lines that keeps line sel visible in h rows,
// with two lines of what follows it.
func window(lines []string, sel, h int) []string { return span(lines, sel, sel+2, h) }

// span returns the slice of lines that keeps lines top to bot visible in h
// rows, so what opens under a row (a pick-list, an ask's options) is never cut
// at the bottom; when they can't all fit, top wins.
func span(lines []string, top, bot, h int) []string {
	if len(lines) <= h {
		return lines
	}
	h = max(h, 0)
	start := max(0, bot-h+1)
	start = min(start, top, len(lines)-h)
	return lines[max(start, 0) : max(start, 0)+h]
}
