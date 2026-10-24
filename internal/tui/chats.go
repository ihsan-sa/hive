package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// sideRow is one row of the sidebar: a section, a project group or a chat.
type sideRow struct {
	sec    int // section row, or -1
	key    string
	label  string
	chatID string
	group  bool
}

// sideRows lists the sections, then one chat per project lead and per agent,
// grouped by project, then the Hive's own lead and agents. A collapsed group
// hides its chats unless all is set, which lookups use.
func (m *model) sideRows() []sideRow { return m.rows(false) }

func (m *model) rows(all bool) []sideRow {
	var rows []sideRow
	for i, s := range sections {
		rows = append(rows, sideRow{sec: i, key: "sec:" + s, label: s})
	}
	rows = append(rows, sideRow{sec: -1, key: "gap"})
	byProject := map[string][]core.Run{}
	for _, r := range m.st.Runs {
		if !r.Terminal() && r.Role != "lead" {
			byProject[r.Project] = append(byProject[r.Project], r)
		}
	}
	seen := map[string]bool{}
	group := func(key, label string, chats []sideRow) {
		rows = append(rows, sideRow{sec: -1, key: key, label: label, group: true})
		for _, c := range chats {
			seen[c.chatID] = true
			if all || !m.closed[key] {
				c.sec = -1
				rows = append(rows, c)
			}
		}
	}
	agents := func(p string) []sideRow {
		var out []sideRow
		for _, r := range byProject[p] {
			seen["agent:"+r.ID] = true
			out = append(out, sideRow{key: "agent:" + r.ID, label: runLabel(r), chatID: "agent:" + r.ID})
		}
		// Agent chats with history whose run has ended stay reachable.
		for _, c := range m.st.Chats {
			if c.Kind == "agent" && c.Project == p && !seen[c.ID] {
				out = append(out, sideRow{key: c.ID, label: c.Title + " (ended)", chatID: c.ID})
			}
		}
		return out
	}
	for _, p := range m.st.Projects {
		group("p:"+p.Name, p.Name, append([]sideRow{{key: "project:" + p.Name, label: "lead", chatID: "project:" + p.Name}}, agents(p.Name)...))
	}
	var hive []sideRow
	for _, c := range m.st.Chats {
		if c.Kind == "lead" {
			hive = append(hive, sideRow{key: c.ID, label: "lead", chatID: c.ID})
		}
	}
	if hive = append(hive, agents("")...); len(hive) > 0 {
		group("p:", "hive", hive)
	}
	return rows
}

func runLabel(r core.Run) string {
	if r.Name != "" {
		return r.Name
	}
	id := r.ID
	if i := strings.LastIndexByte(id, '-'); i >= 0 && len(id)-i > 5 {
		id = id[i+1 : i+6]
	}
	return r.Role + " " + id
}

func (m *model) viewSidebar(w int) pane {
	unread := map[string]int{}
	for _, c := range m.st.Chats {
		unread[c.ID] = c.Unread
	}
	rows := m.sideRows()
	var body []string
	for i, r := range rows {
		mark, name, count := "  ", r.label, ""
		switch {
		case r.sec >= 0:
			if r.sec == m.section {
				mark, name = "› ", stTitle.Render(name)
			}
			if r.sec == secWaiting && len(m.st.Asks) > 0 {
				count = stTitle.Render(fmt.Sprint(len(m.st.Asks)))
			}
		case r.group:
			mark = "▾ "
			if m.closed[r.key] {
				mark = "▸ "
			}
			mark, name = stDim.Render(mark), stDim.Render(name)
		case r.chatID != "":
			if unread[r.chatID] > 0 {
				name, count = stTitle.Render(name), "●"
			}
			if m.section == secChats && r.chatID == m.chat {
				mark, name = "› ", stTitle.Render(r.label)
			}
		}
		line := " " + mark + name
		if count != "" {
			line = pad(clip(line, w-4), w-2-visibleWidth(count)) + count
		}
		if i == m.side && m.focus == 0 {
			line = stSel.Render(fit(" "+ansi.Strip(mark)+r.label, w))
		}
		body = append(body, line)
	}
	return pane{w: w, head: stTitle.Render(" hive"), body: window(body, m.side, m.h-4)}
}

// keySidebar moves the one cursor through sections and chats; the row under
// it is what the main pane shows.
func (m *model) keySidebar(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.sideRows()
	m.side = min(m.side, len(rows)-1)
	step := func(d int) tea.Cmd {
		for i := m.side + d; i >= 0 && i < len(rows); i += d {
			if rows[i].key != "gap" {
				m.side = i
				return m.pick(rows[i])
			}
		}
		return nil
	}
	r := rows[m.side]
	switch k.String() {
	case "up", "k":
		return m, step(-1)
	case "down", "j":
		return m, step(1)
	case " ", "enter", "right", "l":
		if r.group {
			m.closed[r.key] = !m.closed[r.key]
			return m, nil
		}
		m.focus = 1
		return m, m.markRead()
	}
	return m, nil
}

func (m *model) pick(r sideRow) tea.Cmd {
	m.stack = nil
	var cmd tea.Cmd
	switch {
	case r.sec >= 0:
		m.section = r.sec
	case r.chatID != "":
		if m.chat != r.chatID {
			m.chat, m.thread, m.threadSel, m.chScroll = r.chatID, "", -1, 0
		}
		m.section = secChats
		cmd = m.fetchChat(r.chatID)
	}
	return tea.Batch(cmd, m.settle())
}

// openChat jumps to a chat from elsewhere (an agent's log, the dashboard).
func (m *model) openChat(id string) tea.Cmd {
	for i, r := range m.sideRows() {
		if r.chatID == id {
			m.side = i
		}
	}
	m.focus = 1
	return m.pick(sideRow{sec: -1, chatID: id})
}

func (m *model) currentChatID() string {
	if m.section != secChats {
		return ""
	}
	return m.chat
}

func (m *model) currentChat() (api.ChatView, bool) {
	id := m.chat
	for _, c := range m.st.Chats {
		if c.ID == id {
			return c, true
		}
	}
	for _, r := range m.rows(true) {
		if r.chatID == id && id != "" {
			return api.ChatView{Chat: chat.Chat{ID: id, Title: r.label, Kind: "agent"}}, true
		}
	}
	return api.ChatView{}, false
}

// where names the open chat as the sidebar groups it: "textkit", "lead".
func (m *model) where() (group, name string) {
	for _, r := range m.rows(true) {
		if r.group {
			group = r.label
		}
		if r.chatID == m.chat && r.chatID != "" {
			return group, r.label
		}
	}
	c, _ := m.currentChat()
	return "", c.Title
}

func (m *model) path() string {
	g, n := m.where()
	if g == "" {
		return n
	}
	return g + " › " + n
}

type thread struct {
	id   string
	msgs []chat.Message
}

func threads(msgs []chat.Message) []thread {
	idx := map[string]int{}
	var out []thread
	for _, msg := range msgs {
		t := msg.Thread
		if t == "" {
			t = msg.ID
		}
		i, ok := idx[t]
		if !ok {
			idx[t] = len(out)
			out = append(out, thread{id: t})
			i = len(out) - 1
		}
		out[i].msgs = append(out[i].msgs, msg)
	}
	return out
}

func (m *model) keyChannel(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	ts := threads(m.msgs[m.chat])
	switch s := k.String(); s {
	case "up", "k":
		if m.threadSel < 0 {
			m.threadSel = len(ts)
		}
		m.threadSel = max(0, m.threadSel-1)
	case "down", "j":
		if m.threadSel < len(ts)-1 {
			m.threadSel++
		} else {
			m.threadSel = -1
			return m, m.focusBox("")
		}
	case "enter", "right":
		if m.threadSel >= 0 && m.threadSel < len(ts) {
			m.thread = ts[m.threadSel].id
			m.focus = 2
			return m, m.focusBox(m.thread)
		}
		return m, m.focusBox("")
	case " ":
		if m.threadSel >= 0 && m.threadSel < len(ts) {
			id := "t:" + ts[m.threadSel].id
			m.open[id] = !m.isOpen(ts, m.threadSel)
		}
	case "esc", "left", "h":
		m.focus = 0
	case "pgup", "pgdown", "home", "end":
		m.chScroll = scroll(m.chScroll, s, m.h-4)
	case "n", "i":
		return m, m.focusBox("")
	case "a":
		return m, m.toWaiting()
	}
	return m, nil
}

// toWaiting opens Waiting on what this chat was told is waiting on the person.
func (m *model) toWaiting() tea.Cmd {
	if len(m.st.Asks) == 0 {
		return nil
	}
	chatOf := map[string]string{}
	for _, p := range m.st.Projects {
		for _, t := range p.Tasks {
			chatOf[t.ID] = t.Chat
		}
	}
	m.waitSel, m.optSel = 0, 0
	for i, a := range m.st.Asks {
		if id, _, _ := strings.Cut(a.Ref, "@"); chatOf[id] == m.chat {
			m.waitSel = i
			break
		}
	}
	for i, r := range m.sideRows() {
		if r.sec == secWaiting {
			m.side = i
		}
	}
	m.focus = 1
	return m.pick(sideRow{sec: secWaiting})
}

// scroll moves an offset counted in lines up from the bottom.
func scroll(off int, key string, page int) int {
	switch key {
	case "pgup", "wheelup":
		return off + map[string]int{"pgup": page - 2, "wheelup": 3}[key]
	case "pgdown", "wheeldown":
		return max(0, off-map[string]int{"pgdown": page - 2, "wheeldown": 3}[key])
	case "home":
		return 1 << 20
	}
	return 0
}

// focusBox points the one text box at a target, a new thread in this chat
// ("") or a reply, keeping each target's draft.
func (m *model) focusBox(reply string) tea.Cmd {
	m.blurBox()
	m.reply, m.composing = reply, true
	m.composeN++
	m.input.SetValue(m.drafts[m.chat+"\x00"+reply])
	return m.input.Focus()
}

func (m *model) blurBox() {
	if m.composing {
		m.drafts[m.chat+"\x00"+m.reply] = m.input.Value()
	}
	m.composing, m.files = false, nil // a draft keeps its text, not its attachments
	m.input.Blur()
}

func (m *model) markRead() tea.Cmd {
	id := m.currentChatID()
	if id == "" {
		return nil
	}
	return func() tea.Msg {
		m.c.Do("POST", "/api/chat/read", map[string]string{"chat": id}, nil)
		return nil
	}
}

// isOpen: a thread shows its replies if toggled open, or by default if it's the last.
func (m *model) isOpen(ts []thread, i int) bool {
	if v, ok := m.open["t:"+ts[i].id]; ok {
		return v
	}
	return i == len(ts)-1
}

func (m *model) viewChannel(w int) pane {
	c, _ := m.currentChat()
	title := m.path()
	if m.focus == 1 {
		title = stTitle.Render(title)
	}
	p := pane{w: w, head: head(w, title, c.LeadStatus, "")}
	if m.chat == "" {
		p.body = []string{"", "   " + stDim.Render("Pick a chat on the left.")}
		return p
	}
	tw := w - 5
	ts := threads(m.msgs[m.chat])
	var lines []string
	selStart, selEnd := 0, 0
	for i, t := range ts {
		sel := m.focus == 1 && !m.composing && i == m.threadSel || t.id == m.thread
		gut := "  "
		if sel {
			gut = stTitle.Render("▌") + " "
			selStart = len(lines)
		}
		add := func(ls ...string) {
			for _, l := range ls {
				lines = append(lines, " "+gut+l)
			}
		}
		first := t.msgs[0]
		add(msgHead(first))
		add(m.body(first.Text, tw)...)
		n := len(t.msgs) - 1
		replies := fmt.Sprintf("%d %s", n, plural(n, "reply", "replies"))
		switch {
		case n == 0:
		case sel:
			add(stTitle.Render("▸ "+replies) + "  " + stDim.Render("open →"))
		case m.isOpen(ts, i):
			add(stDim.Render("▾ " + replies))
			for _, r := range t.msgs[1:] {
				add("  " + msgHead(r))
				add(indent(m.body(r.Text, tw-2), "  ")...)
			}
		default:
			add(stDim.Render(fmt.Sprintf("▸ %s  last %s", replies, t.msgs[n].At.Local().Format("15:04"))))
		}
		if sel {
			selEnd = len(lines)
		}
		lines = append(lines, "")
	}
	if len(ts) == 0 {
		hint := "Nothing here yet. Press n to write to the lead."
		if c.Kind == "agent" {
			hint = "Write to this agent; it reads your message after its current turn."
		}
		lines = append(lines, "   "+stDim.Render(hint), "")
	}
	_, name := m.where()
	bx := m.compose("new thread · "+name, "Start a new thread with "+name+"…", "", w-3)
	if n := len(m.st.Asks); n > 0 && len(bx) < m.h-6 { // above the box, so it stays on screen
		how := map[bool]string{true: "a to answer", false: "→ then a to answer"}[m.focus == 1 && !m.composing]
		bx = append([]string{" " + stWarn.Render(clip(fmt.Sprintf("%d waiting on you · %s", n, how), w-6))}, bx...)
	}
	p.body = stack(append(lines, ""), bx, m.h-4, &m.chScroll, selStart, selEnd, true)
	return p
}

// stack puts a box under the content: right after it when both fit (or on
// the last rows, bottom set), else on the last rows with the content scrolled
// (off lines up from its end, kept so lines [s0,s1) show).
func stack(lines, bx []string, h int, off *int, s0, s1 int, bottom bool) []string {
	for i := range bx {
		bx[i] = "  " + bx[i]
	}
	room := max(0, h-len(bx))
	if len(lines) <= room {
		*off = 0
		if bottom {
			lines = append(make([]string, room-len(lines)), lines...)
		}
		return append(lines, bx...)
	}
	*off = min(*off, len(lines)-room)
	end := len(lines) - *off
	if s1 > s0 { // keep the selected thread in view
		if s1 > end {
			end = min(s1, len(lines))
		}
		if s0 < end-room {
			end = min(len(lines), s0+room)
		}
		*off = len(lines) - end
	}
	return append(lines[end-room:end], bx...)
}

func (m *model) viewThread(w int) pane {
	ts := threads(m.msgs[m.chat])
	var t thread
	for _, x := range ts {
		if x.id == m.thread {
			t = x
		}
	}
	focused := m.focus == 2
	var p pane
	title := "Thread"
	if focused {
		title = stTitle.Render(title)
	}
	if m.takeover() {
		p = pane{w: w, head: pad(" ‹ "+m.path()+"  › thread", w-9) + stDim.Render("esc back")}
	} else {
		p = pane{w: w, head: pad(head(w, title, m.path(), ""), w-10) + stDim.Render("esc close")}
	}
	tw := w - 5
	var lines []string
	add := func(ls ...string) { lines = append(lines, indent(ls, "   ")...) }
	pending := 0
	for _, o := range m.outbox {
		if o.chat == m.chat && o.thread == m.thread {
			pending++
		}
	}
	for i, msg := range t.msgs {
		add(msgHead(msg))
		add(m.body(msg.Text, tw)...)
		add("")
		if i == 0 {
			n := len(t.msgs) - 1 + pending
			r := fmt.Sprintf("%d %s ", n, plural(n, "reply", "replies"))
			add(stDim.Render(r+strings.Repeat("─", max(0, tw-len(r)))), "")
		}
	}
	for _, o := range m.outbox {
		if o.chat == m.chat && o.thread == m.thread {
			add(stBoldF.Render("you") + "  " + stDim.Render("◌ sending…"))
			add(indent(m.body(o.text, tw), "")...)
			add("")
		}
	}
	_, name := m.where()
	bx := m.compose("reply · "+name, "Reply to this thread…", m.thread, w-3)
	p.body = stack(lines, bx, m.h-4, &m.thScroll, 0, 0, false)
	return p
}

// compose draws the box for one target: live when it has focus, else its
// draft or placeholder, faint.
func (m *model) compose(label, placeholder, reply string, w int) []string {
	if m.composing && m.reply == reply && !m.editing {
		m.input.Placeholder = placeholder
		m.input.SetWidth(w - 3)          // the textarea keeps its last cell for the cursor
		m.input.SetHeight(min(8, m.h-6)) // the whole box stays on a short screen
		lines := strings.Split(m.input.View(), "\n")
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, w-4, "")
		}
		for len(lines) > 1 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
			lines = lines[:len(lines)-1]
		}
		return box(m.composeLabel(label), lines, w, true)
	}
	text := m.drafts[m.chat+"\x00"+reply]
	if text == "" {
		text = placeholder
	}
	lines := wrap(text, w-6)
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	lines[0] = "›" + lines[0][1:]
	return box(label, lines[:min(len(lines), 8)], w, false)
}

// composeLabel is the live box's label, with a chip for each attachment.
func (m *model) composeLabel(label string) string {
	for _, f := range m.files {
		label += " · 📎 " + f.Name
	}
	return label
}

// body renders a message's markdown to width w.
func (m *model) body(text string, w int) []string { return markdown(text, w) }

func msgHead(msg chat.Message) string {
	return who(msg) + "  " + stDim.Render(msg.At.Local().Format("15:04"))
}

func who(msg chat.Message) string {
	switch msg.From {
	case "person":
		return stTitle.Render("you")
	case "hive":
		return stTitle.Render("hive")
	}
	name := msg.Name
	if name == "" {
		name = msg.From
	}
	return stTitle.Render(name)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func pad(s string, w int) string {
	if n := w - visibleWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
