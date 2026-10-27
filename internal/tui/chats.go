package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

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
	fold   bool   // a fold inside a group, closed until opened: ended agents
	under  string // the fold this row sits in
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
			if c.under != "" && !all && !m.open[c.under] {
				continue
			}
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
		// Agent chats with history whose run has ended stay reachable, out of
		// the way: in a fold of their own, and only for a day.
		fold := "ended:" + p
		var ended []sideRow
		for _, c := range m.st.Chats {
			if c.Kind == "agent" && c.Project == p && !seen[c.ID] && (all || time.Since(m.endedAt(c)) < endedFor) {
				ended = append(ended, sideRow{key: c.ID, label: "  " + c.Title, chatID: c.ID, under: fold})
			}
		}
		if len(ended) > 0 {
			out = append(out, sideRow{key: fold, label: fmt.Sprintf("ended (%d)", len(ended)), fold: true})
		}
		return append(out, ended...)
	}
	known := map[string]bool{"": true}
	for _, p := range m.st.Projects {
		known[p.Name] = true
		group("p:"+p.Name, groupLabel(p.Name), append([]sideRow{{key: "project:" + p.Name, label: "lead", chatID: "project:" + p.Name}}, agents(p.Name)...))
	}
	// Agents of a project the state no longer lists (removed, or not yet
	// loaded) keep a group of their own rather than dropping out of sight.
	others := map[string]bool{}
	for p := range byProject {
		others[p] = !known[p]
	}
	for _, c := range m.st.Chats {
		if c.Kind == "agent" && !known[c.Project] {
			others[c.Project] = true
		}
	}
	for _, p := range sortedKeys(others) {
		if others[p] {
			group("p:"+p, groupLabel(p), agents(p))
		}
	}
	var hive []sideRow
	for _, c := range m.st.Chats {
		if c.Kind == "lead" {
			hive = append(hive, sideRow{key: c.ID, label: "lead", chatID: c.ID})
		}
	}
	if hive = append(hive, agents("")...); len(hive) > 0 {
		group("p:", hiveGroup, hive)
	}
	return rows
}

// hiveGroup labels the Hive's own lead and agents, apart from any project's,
// a project named hive among them.
const hiveGroup = "Hive-wide"

// groupLabel is a project's group label, which never reads as the Hive's.
func groupLabel(project string) string {
	if strings.EqualFold(project, hiveGroup) {
		return "project " + project
	}
	return project
}

// chatHint says in a line what a lead's chat is for, so the Hive's lead and
// a project's lead never read alike; short, it only names the lead.
func chatHint(c api.ChatView, short bool) string {
	who, what := "the Hive-wide lead", ", for every project and the Hive itself"
	if c.Kind == "project" {
		who, what = "the lead of "+c.Project, ", for this project only"
	}
	if short {
		return who
	}
	return who + what
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
	news := map[string]int{} // a collapsed group carries its chats' mark
	g := ""
	for _, r := range m.rows(true) {
		if r.group {
			g = r.key
		} else if r.chatID != "" {
			news[g] += unread[r.chatID]
			news[r.under] += unread[r.chatID]
		}
	}
	rows := m.sideRows()
	m.sideAt(rows)
	var body []string
	for i, r := range rows {
		mark, name, count := "  ", r.label, ""
		switch {
		case r.sec >= 0:
			if r.sec == m.section {
				mark, name = "› ", stTitle.Render(name)
			}
			if r.sec == secWaiting && len(m.st.Asks) > 0 {
				count = waitStyle(len(m.st.Asks)).Render(fmt.Sprint(len(m.st.Asks)))
			}
			if n := m.fbUnseen(); r.sec == secFeedback && n > 0 {
				count = stTitle.Render(fmt.Sprint(n))
			}
		case r.fold:
			mark = "  ▸ "
			if m.open[r.key] {
				mark = "  ▾ "
			}
			mark, name = stDim.Render(mark), stDim.Render(name)
			if !m.open[r.key] && news[r.key] > 0 {
				count = stAccent.Render("●")
			}
		case r.group:
			mark = "▾ "
			if m.closed[r.key] {
				mark = "▸ "
			}
			mark, name = stDim.Render(mark), stDim.Render(name)
			if m.closed[r.key] && news[r.key] > 0 {
				count = stAccent.Render("●")
			}
		case r.chatID != "":
			if unread[r.chatID] > 0 {
				name, count = stTitle.Render(name), stAccent.Render("●")
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
			line = " " + ansi.Strip(mark) + r.label
			if c := ansi.Strip(count); c != "" {
				line = pad(clip(line, w-4), w-2-visibleWidth(c)) + c
			}
			line = stSel.Render(fit(line, w))
		}
		body = append(body, line)
	}
	return pane{w: w, head: stTitle.Render(" hive"), body: window(body, m.side, m.h-4)}
}

// keySidebar moves the one cursor through sections and chats; the row under
// it is what the main pane shows.
func (m *model) keySidebar(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.sideRows()
	step := func(d int) tea.Cmd {
		for i := m.sideAt(rows) + d; i >= 0 && i < len(rows); i += d {
			if rows[i].key != "gap" {
				m.side, m.sideKey = i, rows[i].key
				return m.pick(rows[i])
			}
		}
		return nil
	}
	r := rows[m.sideAt(rows)]
	switch k.String() {
	case "up", "k":
		return m, step(-1)
	case "down", "j":
		return m, step(1)
	case " ", "enter", "right", "l", "left", "h":
		if r.fold { // opens and closes in place; it holds no chat of its own
			s := k.String()
			m.open[r.key] = s == "right" || s == "l" || (s != "left" && s != "h" && !m.open[r.key])
			return m, nil
		}
	}
	switch k.String() {
	case "e", "E":
		m.foldSidebar(k.String() == "e")
		return m, nil
	case " ", "enter":
		if r.group {
			m.closed[r.key] = !m.closed[r.key]
			return m, nil
		}
	case "right", "l": // deeper: open a group, then into its first chat
		if r.group && m.closed[r.key] {
			m.closed[r.key] = false
			return m, nil
		}
		if r.group {
			return m, step(1)
		}
	case "left", "h": // back: close a group, or from a chat up to its group
		if r.group && !m.closed[r.key] {
			m.closed[r.key] = true
			return m, nil
		}
		if g := m.groupOf(r.key); g != "" {
			for i, x := range rows {
				if x.key == g {
					m.side, m.sideKey = i, g
				}
			}
		}
		return m, nil
	default:
		return m, nil
	}
	m.focus = 1
	return m, m.markRead()
}

// foldSidebar opens the sidebar a level at a time, the project groups and
// then the ended agents' folds in them, or closes it the same way back.
func (m *model) foldSidebar(open bool) {
	var groups, folds []string
	for _, r := range m.rows(true) {
		switch {
		case r.group:
			groups = append(groups, r.key)
		case r.fold:
			folds = append(folds, r.key)
		}
	}
	anyOf := func(keys []string, f func(string) bool) bool {
		for _, k := range keys {
			if f(k) {
				return true
			}
		}
		return false
	}
	switch {
	case open && anyOf(groups, func(g string) bool { return m.closed[g] }):
		for _, g := range groups {
			m.closed[g] = false
		}
	case open:
		for _, f := range folds {
			m.open[f] = true
		}
	case anyOf(folds, func(f string) bool { return m.open[f] }):
		for _, f := range folds {
			m.open[f] = false
		}
	default:
		for _, g := range groups {
			m.closed[g] = true
		}
	}
}

// sideAt finds the cursor's row by its key, since chats come and go above it
// as agents start and end; a row hidden in a collapsed group puts it on the
// group, and a row that's gone leaves it where it was.
func (m *model) sideAt(rows []sideRow) int {
	for _, key := range []string{m.sideKey, m.foldOf(m.sideKey), m.groupOf(m.sideKey)} {
		for i, r := range rows {
			if key != "" && r.key == key {
				m.side = i
				return i
			}
		}
	}
	m.side = max(0, min(m.side, len(rows)-1))
	return m.side
}

// foldOf is the key of the fold a chat row sits in, or "".
func (m *model) foldOf(key string) string {
	for _, r := range m.rows(true) {
		if r.key == key {
			return r.under
		}
	}
	return ""
}

// endedFor is how long an ended agent's chat stays in the sidebar.
const endedFor = 24 * time.Hour

// endedAt is when an agent's chat went quiet: its run's last update, or the
// chat's start for a run the state no longer lists.
func (m *model) endedAt(c api.ChatView) time.Time {
	if r, ok := m.run(strings.TrimPrefix(c.ID, "agent:")); ok {
		return r.Updated
	}
	return c.Created
}

// groupOf is the key of the group a chat row sits in, or "".
func (m *model) groupOf(key string) string {
	g := ""
	for _, r := range m.rows(true) {
		switch {
		case r.group:
			g = r.key
		case r.key == key && r.sec < 0:
			return g
		}
	}
	return ""
}

func (m *model) pick(r sideRow) tea.Cmd {
	m.stack = nil
	var cmd tea.Cmd
	switch {
	case r.sec >= 0:
		m.section = r.sec
	case r.chatID != "":
		if m.chat != r.chatID {
			m.switchChat(r.chatID)
		}
		m.section = secChats
		cmd = m.fetchChat(r.chatID)
	}
	return tea.Batch(cmd, m.settle())
}

// place is where the person was in a chat: its open thread, the pick and
// the scrolls, kept while they look at another chat.
type place struct {
	thread             string
	threadSel          int
	chScroll, thScroll int
	chFree             bool
}

// switchChat leaves the current chat where it is and goes back to where the
// person was in the next one; a chat not seen yet opens at its end.
func (m *model) switchChat(id string) {
	if m.chat != "" {
		m.places[m.chat] = place{m.thread, m.threadSel, m.chScroll, m.thScroll, m.chFree}
	}
	p, ok := m.places[id]
	if !ok {
		p = place{threadSel: -1}
	}
	m.chat, m.thread, m.threadSel, m.chScroll, m.thScroll, m.chFree = id, p.thread, p.threadSel, p.chScroll, p.thScroll, p.chFree
}

// openChat jumps to a chat from elsewhere (an agent's log, the dashboard).
func (m *model) openChat(id string) tea.Cmd {
	for i, r := range m.sideRows() {
		if r.chatID == id {
			m.side, m.sideKey = i, r.key
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
			return group, strings.TrimSpace(r.label)
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
	switch k.String() {
	case "up", "down", "k", "j", "enter", "right": // moving or opening the pick brings it back into view
		m.chFree = false
	}
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
		if s == "right" && m.thread != "" && (m.threadSel < 0 || m.threadSel >= len(ts) || ts[m.threadSel].id == m.thread) {
			m.focus = 2 // back over to the open thread
			return m, m.focusBox(m.thread)
		}
		if m.threadSel >= 0 && m.threadSel < len(ts) {
			if m.thread != ts[m.threadSel].id {
				m.thread, m.thScroll = ts[m.threadSel].id, 0 // another thread opens at its newest
			}
			m.focus = 2
			return m, m.focusBox(m.thread)
		}
		return m, m.focusBox("")
	case " ":
		if m.threadSel >= 0 && m.threadSel < len(ts) {
			id := "t:" + ts[m.threadSel].id
			m.open[id] = !m.isOpen(ts, m.threadSel)
		}
	case "e", "E": // a channel has the one level: every thread open or closed
		for _, t := range ts {
			m.open["t:"+t.id] = s == "e"
		}
	case "esc", "left", "h":
		m.focus = 0
	case "pgup", "pgdown", "home", "g":
		m.chScroll, m.chFree = scroll(m.chScroll, s, m.h-4), true
	case "end", "G", "ctrl+end": // the newest line, letting go of the pick so nothing pulls it back
		m.chScroll, m.chFree, m.threadSel = 0, true, -1
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
			m.side, m.sideKey = i, r.key
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
	case "home", "g":
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
	p := pane{w: w, head: head(w, title, leadStatus(c.LeadStatus), "")}
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
		case m.isOpen(ts, i): // space toggles this whether or not the thread is selected
			if sel {
				add(stTitle.Render("▾ "+replies) + "  " + stDim.Render("open →"))
			} else {
				add(stDim.Render("▾ " + replies))
			}
			for _, r := range t.msgs[1:] {
				add("  " + msgHead(r))
				add(indent(m.body(r.Text, tw-2), "  ")...)
			}
		case sel:
			add(stTitle.Render("▸ "+replies) + "  " + stDim.Render("open →"))
		default:
			add(stDim.Render(fmt.Sprintf("▸ %s  last %s", replies, t.msgs[n].At.Local().Format("15:04"))))
		}
		if sel {
			selEnd = len(lines)
		}
		lines = append(lines, "")
	}
	if len(ts) == 0 {
		hint := "This is " + chatHint(c, false) + ". Press n to write."
		if c.Kind == "agent" {
			hint = "Write to this agent; it reads your message after its current turn."
		}
		for _, l := range wrap(hint, tw) {
			lines = append(lines, "   "+stDim.Render(l))
		}
		lines = append(lines, "")
	}
	_, name := m.where()
	invite := "Start a new thread with " + name + "…"
	if c.Kind != "agent" { // the whole hint where the box has room for it on one line
		if invite = "Write to " + chatHint(c, false) + "…"; visibleWidth(invite) > w-9 {
			invite = "Write to " + chatHint(c, true) + "…"
		}
	}
	bx := m.compose("new thread · "+name, invite, "", w-3)
	if n := len(m.st.Asks); n > 0 && len(bx) < m.h-6 { // above the box, so it stays on screen
		how := map[bool]string{true: "a to answer", false: "→ then a to answer"}[m.focus == 1 && !m.composing]
		bx = append([]string{" " + stWarn.Render(clip(fmt.Sprintf("%d waiting on you · %s", n, how), w-6))}, bx...)
	}
	if m.chFree { // scrolled by hand: the offset rules, not the selection
		selStart, selEnd = 0, 0
	}
	p.body = stack(lines, bx, m.h-4, &m.chScroll, selStart, selEnd, true, m.moreBelow(1))
	return p
}

// stack puts a box under the content, a blank row apart: right after it
// when both fit (or on the last rows, bottom set), else on the last rows with
// the content scrolled (off lines up from its end, kept so lines [s0,s1)
// show). Scrolled up, the row between says more is below and how to get there.
func stack(lines, bx []string, h int, off *int, s0, s1 int, bottom bool, more string) []string {
	for i := range bx {
		bx[i] = "  " + bx[i]
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1] // the gap row is the one blank above the box
	}
	gap := min(1, max(0, h-len(bx)-1)) // a screen too short for it keeps the box whole
	room := max(0, h-len(bx)-gap)
	if len(lines) <= room {
		*off = 0
		if bottom {
			lines = append(make([]string, room-len(lines)), lines...)
		}
		return append(append(lines, make([]string, gap)...), bx...)
	}
	*off = min(*off, len(lines)-room)
	end := len(lines) - *off
	if s1 = min(s1, len(lines)); s1 > s0 { // keep the selected thread in view
		if s1 > end {
			end = s1
		}
		if s0 < end-room {
			end = min(len(lines), s0+room)
		}
		*off = len(lines) - end
	}
	out := append([]string(nil), lines[end-room:end]...)
	if gap > 0 {
		row := ""
		if *off > 0 {
			row = "   " + stDim.Render(more)
		}
		out = append(out, row)
	}
	return append(out, bx...)
}

// bottomKey is the key that jumps to the newest line from where the keys are.
func (m *model) bottomKey() string {
	if m.composing && m.input.Value() != "" {
		return "ctrl+end"
	}
	return "end"
}

// moreBelow says what's below a scrolled pane, and the key that gets there
// when the pane (pane 2 the thread) holds the keys.
func (m *model) moreBelow(pane int) string {
	if m.focus != pane || pane == 2 && !m.composing || pane == 1 && m.composing && m.reply != "" {
		return "↓ newer below"
	}
	return "↓ newer below · " + m.bottomKey()
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
	var pending []outMsg // sent, and not yet in the thread
	for _, o := range m.outbox {
		if o.chat == m.chat && o.thread == m.thread && m.echoes(o) <= o.had {
			pending = append(pending, o)
		}
	}
	for i, msg := range t.msgs {
		add(msgHead(msg))
		add(m.body(msg.Text, tw)...)
		add("")
		if i == 0 {
			n := len(t.msgs) - 1 + len(pending)
			r := fmt.Sprintf("%d %s ", n, plural(n, "reply", "replies"))
			add(stDim.Render(r+strings.Repeat("─", max(0, tw-len(r)))), "")
		}
	}
	for _, o := range pending {
		add(stBoldF.Render("you") + "  " + stDim.Render("◌ sending…"))
		add(indent(m.body(o.text, tw), "")...)
		add("")
	}
	_, name := m.where()
	bx := m.compose("reply · "+name, "Reply to this thread…", m.thread, w-3)
	p.body = stack(lines, bx, m.h-4, &m.thScroll, 0, 0, false, m.moreBelow(2))
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
		return stYou.Render("you")
	case "hive":
		return stTitle.Render("hive")
	}
	name := msg.Name
	if name == "" {
		name = msg.From
	}
	return speaker(name).Render(name)
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

// leadStatus hints a lead at work in green; idle stays faint.
func leadStatus(s string) string {
	if s == "working" {
		return stGood.Render(s)
	}
	return s
}
