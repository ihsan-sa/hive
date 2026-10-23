package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

type chatRow struct {
	key    string
	label  string
	chatID string
	depth  int
	group  bool
}

// chatRows lists chats like a simplified Slack: the lead, then each project
// (collapsible) with its lead's chat and its running agents, then the Hive's
// own agents.
func (m *model) chatRows() []chatRow {
	rows := []chatRow{{key: "lead", label: "lead", chatID: "lead"}}
	byProject := map[string][]core.Run{}
	for _, r := range m.st.Runs {
		if r.Terminal() || r.Role == "lead" && r.Project == "" {
			continue
		}
		if r.Role == "lead" {
			continue
		}
		byProject[r.Project] = append(byProject[r.Project], r)
	}
	for _, p := range m.st.Projects {
		rows = append(rows, chatRow{key: "p:" + p.Name, label: p.Name, group: true})
		if m.closed["p:"+p.Name] {
			continue
		}
		rows = append(rows, chatRow{key: "project:" + p.Name, label: "lead", chatID: "project:" + p.Name, depth: 1})
		for _, r := range byProject[p.Name] {
			rows = append(rows, chatRow{key: "agent:" + r.ID, label: runLabel(r), chatID: "agent:" + r.ID, depth: 1})
		}
	}
	if rs := byProject[""]; len(rs) > 0 {
		rows = append(rows, chatRow{key: "p:", label: "hive", group: true})
		if !m.closed["p:"] {
			for _, r := range rs {
				rows = append(rows, chatRow{key: "agent:" + r.ID, label: runLabel(r), chatID: "agent:" + r.ID, depth: 1})
			}
		}
	}
	// Agent chats with history whose run has ended stay reachable.
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.chatID] = true
	}
	for _, c := range m.st.Chats {
		if c.Kind == "agent" && !seen[c.ID] {
			rows = append(rows, chatRow{key: c.ID, label: c.Title + " (ended)", chatID: c.ID})
		}
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

func (m *model) currentChatID() string {
	if m.section != secChats {
		return ""
	}
	rows := m.chatRows()
	if m.chatSel >= len(rows) {
		m.chatSel = max(0, len(rows)-1)
	}
	if len(rows) == 0 {
		return ""
	}
	return rows[m.chatSel].chatID
}

func (m *model) currentChat() (api.ChatView, bool) {
	id := m.currentChatID()
	for _, c := range m.st.Chats {
		if c.ID == id {
			return c, true
		}
	}
	if strings.HasPrefix(id, "agent:") {
		for _, r := range m.chatRows() {
			if r.chatID == id {
				return api.ChatView{Chat: chat.Chat{ID: id, Title: r.label, Kind: "agent"}}, true
			}
		}
	}
	return api.ChatView{}, false
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

func (m *model) keyChats(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.chatRows()
	s := k.String()
	switch s {
	case "pgup":
		m.scrollBy(-(m.paneH() - 1))
		return m, nil
	case "pgdown":
		m.scrollBy(m.paneH() - 1)
		return m, nil
	case "home":
		m.scrollBy(-1 << 30)
		return m, nil
	case "end":
		m.scrollBy(1 << 30)
		return m, nil
	}
	if m.chatFocus == 0 {
		switch s {
		case "up", "k":
			if m.chatSel > 0 {
				m.chatSel--
			}
			m.scroll = -1
			return m, m.fetchChat(m.currentChatID())
		case "down", "j":
			if m.chatSel < len(rows)-1 {
				m.chatSel++
			}
			m.scroll = -1
			return m, m.fetchChat(m.currentChatID())
		case " ":
			if m.chatSel < len(rows) && rows[m.chatSel].group {
				m.closed[rows[m.chatSel].key] = !m.closed[rows[m.chatSel].key]
			}
			return m, nil
		case "right", "l":
			if m.chatSel < len(rows) && !rows[m.chatSel].group {
				m.chatFocus, m.threadSel, m.scroll = 1, max(0, len(threads(m.msgs[rows[m.chatSel].chatID]))-1), -1
				return m, m.markRead()
			}
			return m, nil
		case "enter":
			if m.chatSel < len(rows) && rows[m.chatSel].group {
				m.closed[rows[m.chatSel].key] = !m.closed[rows[m.chatSel].key]
				return m, nil
			}
			return m, m.compose("", "")
		}
		if k.Type == tea.KeyRunes && m.chatSel < len(rows) && !rows[m.chatSel].group {
			return m, m.compose("", string(k.Runes))
		}
		return m, nil
	}
	ts := threads(m.msgs[m.currentChatID()])
	lines, spans := m.threadLines(m.rightW())
	top, h := m.top(len(lines), spans), m.paneH()
	var sp [2]int
	if m.threadSel < len(spans) {
		sp = spans[m.threadSel]
	}
	switch s {
	case "esc", "left", "h":
		m.chatFocus, m.scroll = 0, -1
	case "up", "k", "down", "j":
		if sp[1] <= top || sp[0] >= top+h { // PgUp or Home left the selection off-screen
			m.scroll = -1
		} else if s == "up" || s == "k" {
			// Mirrors down: scroll through a tall thread (the first one up to the
			// title), then select the one before without moving the view.
			if top > 0 && (sp[0] <= top || m.threadSel == 0) {
				m.scrollBy(-1)
			} else if m.threadSel > 0 {
				m.threadSel--
				m.scroll = top
			}
		} else if sp[1] > top+h {
			m.scrollBy(1)
		} else if m.threadSel < len(ts)-1 {
			m.threadSel++
			m.scroll = -1
		}
	case "enter", " ", "right":
		if m.threadSel < len(ts) {
			id := "t:" + ts[m.threadSel].id
			m.open[id] = !m.isOpen(ts, m.threadSel)
		}
	case "r":
		if m.threadSel < len(ts) {
			return m, m.compose(ts[m.threadSel].id, "")
		}
	case "n", "i":
		return m, m.compose("", "")
	}
	return m, nil
}

func (m *model) compose(thread, initial string) tea.Cmd {
	m.composing, m.reply = true, thread
	m.input.SetValue(initial)
	m.input.CursorEnd()
	if thread != "" {
		m.input.Placeholder = "reply in this thread…"
	} else {
		m.input.Placeholder = "new message…"
	}
	return m.input.Focus()
}

func (m *model) markRead() tea.Cmd {
	id := m.currentChatID()
	return func() tea.Msg {
		m.c.Do("POST", "/api/chat/read", map[string]string{"chat": id}, nil)
		return nil
	}
}

// isOpen: a thread is open if toggled open, or (by default) if it's one of the last three.
func (m *model) isOpen(ts []thread, i int) bool {
	if v, ok := m.open["t:"+ts[i].id]; ok {
		return v
	}
	return i >= len(ts)-3
}

func (m *model) viewChats() []string {
	rows := m.chatRows()
	listW := m.listW()
	unread := map[string]int{}
	for _, c := range m.st.Chats {
		unread[c.ID] = c.Unread
	}
	var left []string
	for i, r := range rows {
		mark := "  "
		if unread[r.chatID] > 0 {
			mark = stAccent.Render("● ")
		}
		label := strings.Repeat("  ", r.depth) + r.label
		if r.group {
			arrow := "▾ "
			if m.closed[r.key] {
				arrow = "▸ "
			}
			label = arrow + r.label
			mark = "  "
		}
		line := clip(mark+label, listW)
		if i == m.chatSel {
			if m.chatFocus == 0 {
				line = stSel.Render(pad(line, listW))
			} else {
				line = stTitle.Render(line)
			}
		}
		left = append(left, pad(line, listW))
	}
	rightW := m.rightW()
	right := m.viewThreads(rightW)
	h := m.paneH()
	left = window(left, m.chatSel, h)
	var out []string
	for i := 0; i < h; i++ {
		l, r := strings.Repeat(" ", listW), ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, l+stDim.Render(" │ ")+clip(r, rightW))
	}
	if m.composing {
		target := "new thread"
		if m.reply != "" {
			target = "reply"
		}
		c, _ := m.currentChat()
		out = append(out, stDim.Render(fmt.Sprintf("  to %s · %s", c.Title, target)), m.input.View())
	}
	return out
}

func (m *model) listW() int  { return min(28, max(16, m.w/4)) }
func (m *model) rightW() int { return m.w - m.listW() - 3 }

func (m *model) paneH() int {
	if m.composing {
		return m.bodyHeight() - 2
	}
	return m.bodyHeight()
}

// top is the first visible line of the thread pane. m.scroll < 0 follows the
// selected thread (or the conversation's end when the list has focus).
func (m *model) top(n int, spans [][2]int) int {
	h := m.paneH()
	maxTop := max(0, n-h)
	if m.scroll >= 0 {
		return min(m.scroll, maxTop)
	}
	if m.chatFocus == 1 && m.threadSel < len(spans) {
		if sel := spans[m.threadSel][0]; sel >= h-2 {
			return min(sel-h+3, maxTop)
		}
		return 0
	}
	return maxTop
}

func (m *model) scrollBy(d int) {
	lines, spans := m.threadLines(m.rightW())
	m.scroll = max(0, min(m.top(len(lines), spans)+d, max(0, len(lines)-m.paneH())))
}

func (m *model) viewThreads(w int) []string {
	lines, spans := m.threadLines(w)
	t, h := m.top(len(lines), spans), m.paneH()
	if m.scroll < 0 && m.chatFocus == 0 && t > 0 && h >= 2 { // following the end: keep the title pinned
		return append(lines[:2:2], lines[len(lines)-h+2:]...)
	}
	return lines[t:min(len(lines), t+h)]
}

// threadLines renders the whole pane, plus the line span of each thread.
func (m *model) threadLines(w int) (lines []string, spans [][2]int) {
	c, ok := m.currentChat()
	if !ok {
		return []string{stDim.Render("select a chat")}, nil
	}
	header := stTitle.Render(c.Title)
	if c.LeadStatus != "" {
		header += "  " + stDim.Render(clip(c.LeadStatus, w/2))
	}
	lines = []string{header, ""}
	ts := threads(m.msgs[c.ID])
	if len(ts) == 0 {
		hint := "Nothing here yet. Start typing to write to the lead."
		if c.Kind == "agent" {
			hint = "Write to this agent; it reads your message after its current turn."
		}
		return append(lines, stDim.Render(hint)), nil
	}
	for i, t := range ts {
		start := len(lines)
		first := t.msgs[0]
		head := fmt.Sprintf("%s %s  %s", who(first), stDim.Render(first.At.Local().Format("Mon 15:04")), "")
		if !m.isOpen(ts, i) {
			more := ""
			if len(t.msgs) > 1 {
				more = stDim.Render(fmt.Sprintf("  (+%d)", len(t.msgs)-1))
			}
			line := head + oneLine(first.Text) + more
			if i == m.threadSel && m.chatFocus == 1 {
				line = stSel.Render(clip(line, w))
			}
			lines = append(lines, line)
			spans = append(spans, [2]int{start, len(lines)})
			continue
		}
		for j, msg := range t.msgs {
			prefix := ""
			if j > 0 {
				prefix = "  "
			}
			h := prefix + fmt.Sprintf("%s %s", who(msg), stDim.Render(msg.At.Local().Format("15:04")))
			if i == m.threadSel && m.chatFocus == 1 && j == 0 {
				h = stSel.Render(clip(h, w))
			}
			lines = append(lines, h)
			lines = append(lines, indent(wrap(msg.Text, w-len(prefix)-2), prefix+"  ")...)
		}
		lines = append(lines, "")
		spans = append(spans, [2]int{start, len(lines)})
	}
	return lines, spans
}

func who(msg chat.Message) string {
	switch msg.From {
	case "person":
		return stAccent.Render("you")
	case "hive":
		return stWarn.Render("hive")
	}
	name := msg.Name
	if name == "" {
		name = msg.From
	}
	return stTitle.Render(name)
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
