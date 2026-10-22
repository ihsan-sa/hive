// Package tui is the control center: chats, the dashboard, what's waiting on
// the person, modules and settings. A breadcrumb says where you are, Esc goes
// one level up, everything expands and collapses level by level, and no line
// runs off the screen. Quitting never stops anything.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

var sections = []string{"Chats", "Dashboard", "Waiting", "Modules", "Settings"}

const (
	secChats = iota
	secDash
	secWaiting
	secModules
	secSettings
)

var (
	stTitle  = lipgloss.NewStyle().Bold(true)
	stDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stSel    = lipgloss.NewStyle().Reverse(true)
	stTab    = lipgloss.NewStyle().Padding(0, 1)
	stTabOn  = lipgloss.NewStyle().Padding(0, 1).Bold(true).Underline(true)
	stGood   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stBad    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

// detail is one level pushed on top of a section (a run's log, a task, an ask...).
type detail struct {
	kind   string // run | task | sweep | ask | module | text
	id     string
	title  string
	scroll int
	sel    int
	text   string
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

	// chats
	chatSel   int
	chatFocus int // 0 list, 1 threads
	threadSel int
	open      map[string]bool // expanded threads and tree nodes
	closed    map[string]bool // collapsed project groups
	msgs      map[string][]chat.Message
	input     textinput.Model
	composing bool
	files     []chat.File // attached to the message being written
	reply     string      // thread being replied to; "" = new thread

	// dashboard and lists
	dashSel int
	waitSel int
	modSel  int
	setSel  int
	editing bool
	optSel  int
	stack   []detail
	runs    map[string]api.RunDetail
	tasks   map[string]work.Task

	since     []string
	showSince bool
	help      bool
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
type sinceMsg []string
type flashMsg string
type tickMsg time.Time

// Run opens the control center.
func Run(c *api.Client, ws core.Workspace) error {
	in := textinput.New()
	in.Placeholder = "write to the lead…"
	in.Prompt = "› "
	in.CharLimit = 8000
	m := &model{c: c, ws: ws, open: map[string]bool{}, closed: map[string]bool{}, msgs: map[string][]chat.Message{},
		runs: map[string]api.RunDetail{}, tasks: map[string]work.Task{}, input: in}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.fetchState(), m.fetchSince(), tick())
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
		case "sweep.saved":
			var s struct{ ID, Status string }
			e.Decode(&s)
			if s.Status == core.StatusDone {
				out = append(out, fmt.Sprintf("%s  improvement sweep finished", e.At.Local().Format("Mon 15:04")))
			}
		case "hive.updated":
			var u struct{ To string }
			e.Decode(&u)
			out = append(out, fmt.Sprintf("%s  updated to %s", e.At.Local().Format("Mon 15:04"), u.To))
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
		m.input.Width = max(10, m.w-6)
	case tickMsg:
		return m, tea.Batch(m.fetchState(), m.fetchChat(m.currentChatID()), m.fetchDetail(), tick())
	case stateMsg:
		if msg.err != nil {
			m.err = "the Hive isn't answering: " + msg.err.Error()
		} else {
			m.err, m.st, m.loaded = "", msg.st, true
		}
	case chatMsg:
		m.msgs[msg.id] = msg.msgs
	case runMsg:
		m.runs[msg.d.Run.ID] = msg.d
	case taskMsg:
		m.tasks[msg.t.ID] = msg.t
	case sinceMsg:
		m.since = msg
		m.showSince = len(msg) > 0
	case flashMsg:
		m.setFlash(string(msg))
		return m, m.fetchState()
	case tea.KeyMsg:
		return m.key(msg)
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
	if m.help {
		m.help = false
		return m, nil
	}
	if m.composing || m.editing {
		return m.keyInput(k)
	}
	switch s {
	case "q":
		m.saveSeq()
		return m, tea.Quit
	case "?":
		m.help = true
		return m, nil
	case "tab":
		m.section, m.stack = (m.section+1)%len(sections), nil
		return m, nil
	case "shift+tab":
		m.section, m.stack = (m.section+len(sections)-1)%len(sections), nil
		return m, nil
	}
	if len(m.stack) > 0 {
		return m.keyDetail(k)
	}
	switch m.section {
	case secChats:
		return m.keyChats(k)
	case secDash:
		return m.keyDash(k)
	case secWaiting:
		return m.keyWaiting(k)
	case secModules:
		return m.keyModules(k)
	case secSettings:
		return m.keySettings(k)
	}
	return m, nil
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
	default:
		return m.keyDetailAction(d, k.String())
	}
	return m, nil
}

func (m *model) keyInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.composing, m.editing, m.files = false, false, nil
		m.input.Blur()
		return m, nil
	case "ctrl+v", "alt+v":
		// As in Claude Code: an image on the clipboard attaches; otherwise
		// ctrl+v pastes text as before.
		if m.composing {
			if f, err := clipboardImage(); err != errNoImage {
				if err != nil {
					return m, flash(err.Error())
				}
				m.files = append(m.files, f)
				return m, nil
			}
		}
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		m.input.SetValue("")
		m.input.Blur()
		if m.editing {
			m.editing = false
			return m, m.saveSetting(text)
		}
		m.composing = false
		files := m.files
		m.files = nil
		if text == "" && len(files) == 0 {
			return m, nil
		}
		id := m.currentChatID()
		reply := m.reply
		return m, func() tea.Msg {
			var msg chat.Message
			if err := m.c.Do("POST", "/api/chat", map[string]any{"chat": id, "thread": reply, "text": text, "files": files}, &msg); err != nil {
				return flashMsg("error: " + err.Error())
			}
			return flashMsg("sent")
		}
	}
	if m.composing && k.Paste {
		// A drop arrives as a pasted path; an empty paste may be an image.
		if len(k.Runes) == 0 {
			f, err := clipboardImage()
			if err != nil {
				return m, flash(err.Error())
			}
			m.files = append(m.files, f)
			return m, nil
		}
		if paths := pastedPaths(string(k.Runes)); paths != nil {
			for _, p := range paths {
				f, err := readAttachment(p)
				if err != nil {
					return m, flash("error: " + err.Error())
				}
				m.files = append(m.files, f)
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

func flash(s string) tea.Cmd { return func() tea.Msg { return flashMsg(s) } }

func (m *model) bodyHeight() int { return max(3, m.h-4) }

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	var b strings.Builder
	// breadcrumb
	crumbs := []string{"Hive", sections[m.section]}
	if m.section == secChats {
		if c, ok := m.currentChat(); ok && m.chatFocus == 1 {
			crumbs = append(crumbs, c.Title)
		}
	}
	for _, d := range m.stack {
		crumbs = append(crumbs, d.title)
	}
	b.WriteString(clip(stTitle.Render(strings.Join(crumbs, " › ")), m.w) + "\n")
	var tabs []string
	for i, name := range sections {
		label := name
		if i == secWaiting && len(m.st.Asks) > 0 {
			label = fmt.Sprintf("%s (%d)", name, len(m.st.Asks))
		}
		if i == secChats && m.unreadTotal() > 0 {
			label += " ●"
		}
		if i == m.section {
			tabs = append(tabs, stTabOn.Render(label))
		} else {
			tabs = append(tabs, stTab.Render(label))
		}
	}
	b.WriteString(clip(strings.Join(tabs, ""), m.w) + "\n")

	var body []string
	switch {
	case !m.loaded && m.err == "":
		body = []string{"", "  connecting to the Hive…"}
	case m.showSince:
		body = append([]string{"", stTitle.Render("  While you were away"), ""}, indent(m.since, "  ")...)
		body = append(body, "", stDim.Render("  any key to continue"))
	case m.help:
		body = helpLines()
	case len(m.stack) > 0:
		body = m.viewDetail(&m.stack[len(m.stack)-1])
	default:
		switch m.section {
		case secChats:
			body = m.viewChats()
		case secDash:
			body = m.viewDash()
		case secWaiting:
			body = m.viewWaiting()
		case secModules:
			body = m.viewModules()
		case secSettings:
			body = m.viewSettings()
		}
	}
	h := m.bodyHeight()
	for len(body) < h {
		body = append(body, "")
	}
	for _, l := range body[:h] {
		b.WriteString(clip(l, m.w) + "\n")
	}
	foot := m.footer()
	if m.err != "" {
		foot = stBad.Render(m.err)
	} else if m.flash != "" && time.Since(m.flashAt) < 4*time.Second {
		foot = stAccent.Render(m.flash) + "  " + stDim.Render(foot)
	}
	b.WriteString(clip(foot, m.w))
	return b.String()
}

func (m *model) footer() string {
	if m.composing || m.editing {
		if m.composing {
			return "enter send · esc cancel · drop a file or ctrl+v an image to attach"
		}
		return "enter send · esc cancel"
	}
	if len(m.stack) > 0 {
		return "↑↓ scroll · esc back · tab section · q quit (the Hive keeps running)"
	}
	switch m.section {
	case secChats:
		if m.chatFocus == 0 {
			return "↑↓ chat · type or enter to write · → threads · space collapse · tab section · q quit"
		}
		return "↑↓ thread · enter expand · r reply · n new · ← chats · q quit"
	case secDash:
		return "↑↓ move · → or enter open · ← collapse · tab section · q quit"
	case secWaiting:
		return "↑↓ item · ←→ option · enter answer · q quit"
	case secModules:
		return "↑↓ module · enter details · q quit"
	case secSettings:
		return "↑↓ setting · enter change · q quit"
	}
	return ""
}

func helpLines() []string {
	return []string{"", stTitle.Render("  Keys"), "",
		"  tab / shift+tab   switch section",
		"  ↑ ↓               move",
		"  → or enter        go deeper, open, expand",
		"  ← or esc          one level up",
		"  space             expand or collapse",
		"  typing            in Chats, starts a message to the selected chat",
		"  r / n             reply in a thread / new thread",
		"  q                 quit the control center; the Hive keeps working",
		"", stDim.Render("  any key to close")}
}

func (m *model) unreadTotal() int {
	n := 0
	for _, c := range m.st.Chats {
		n += c.Unread
	}
	return n
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
		out = append(out, strings.Split(ansi.Wordwrap(para, w, " "), "\n")...)
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

// window returns the slice of lines that keeps line sel visible in h rows.
func window(lines []string, sel, h int) []string {
	if len(lines) <= h {
		return lines
	}
	start := 0
	if sel >= h-2 {
		start = sel - h + 3
	}
	if start+h > len(lines) {
		start = len(lines) - h
	}
	return lines[start : start+h]
}
