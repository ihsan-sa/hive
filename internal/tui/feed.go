package tui

import (
	"fmt"
	"github.com/ihsan-sa/hive-standalone-build/internal/text"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// feedRow is one line of the dashboard's live feed: who told whom what.
type feedRow struct {
	at                time.Time
	from, to, project string
	text              string
	chat, msg, thread string // a chat message; its text is looked up when drawn
	task              string
	ask               bool
	told              *core.Ask // done for the person: opens to what ran, why and its output
}

type feedMsg []core.Event

func (m *model) fetchFeed() tea.Cmd {
	after := m.feedSeq
	return func() tea.Msg {
		var evs []core.Event
		if m.c.Do("GET", fmt.Sprintf("/api/events?after=%d", after), nil, &evs) != nil {
			return nil
		}
		return feedMsg(evs)
	}
}

// addEvents writes journal events into the feed (the newest 500 rows).
func (m *model) addEvents(evs []core.Event) tea.Cmd {
	var fetch []tea.Cmd
	fetching := map[string]bool{}
	for _, e := range evs {
		if e.Seq <= m.feedSeq {
			continue
		}
		m.feedSeq = e.Seq
		r := feedRow{at: e.At}
		switch e.Kind {
		case "chat.posted":
			var p struct{ Chat, ID, Thread, From string }
			e.Decode(&p)
			name, project := m.chatName(p.Chat)
			r.chat, r.msg, r.thread, r.project = p.Chat, p.ID, or(p.Thread, p.ID), project
			r.from, r.to = name, "you"
			switch p.From {
			case "person":
				r.from, r.to = "you", name
			case "hive":
				r.from = "hive"
			}
			if !fetching[p.Chat] { // once per chat, so the new message's text shows
				fetching[p.Chat] = true
				fetch = append(fetch, m.fetchChat(p.Chat))
			}
		case "task.saved":
			var t work.Task
			e.Decode(&t)
			if m.stages[t.ID] == t.Stage {
				continue
			}
			m.stages[t.ID] = t.Stage
			to := "worker"
			if w, ok := m.run(t.Worker); ok {
				to = runLabel(w)
			}
			r.from, r.to, r.project, r.text, r.task = "lead", to, t.Project, t.Stage+": "+t.Label(), t.ID
		case "person.asked":
			var a core.Ask
			e.Decode(&a)
			r.from, r.to, r.project, r.text, r.ask = or(a.From, "hive"), "you", a.Project, a.Title, true
		case "person.told":
			var a core.Ask
			e.Decode(&a)
			r.from, r.to, r.project, r.text, r.told = or(a.From, "hive"), "you", a.Project, "done for you: "+a.Title, &a
		case "alert.raised":
			var a struct{ Kind, Project, Text string }
			e.Decode(&a)
			r.from, r.to, r.project, r.text = "hive", "you", a.Project, a.Kind+": "+a.Text
		default:
			continue
		}
		m.feed = append(m.feed, r)
	}
	if len(m.feed) > 500 {
		m.feed = m.feed[len(m.feed)-500:]
	}
	return tea.Batch(fetch...)
}

// toldDetail opens something done for the person: what it was, then what
// ran, why and its output, as the Hive filed them.
func toldDetail(a core.Ask) detail {
	text := a.Title + "\n" + a.Asked.Local().Format("Mon 15:04")
	if a.Project != "" {
		text += " · " + a.Project
	}
	if a.Detail != "" {
		text += "\n\n" + a.Detail
	}
	if a.Diff != "" {
		text += "\n\n" + a.Diff
	}
	return detail{kind: "text", id: "told:" + a.ID, title: "done for you", text: shown(text)}
}

// chatName names a chat as the sidebar does, with its project.
func (m *model) chatName(id string) (name, project string) {
	if p, ok := strings.CutPrefix(id, "project:"); ok {
		return "lead", p
	}
	if r, ok := m.run(strings.TrimPrefix(id, "agent:")); ok {
		return runLabel(r), r.Project
	}
	for _, c := range m.st.Chats {
		if c.ID == id {
			return c.Title, c.Project
		}
	}
	return id, ""
}

// feedRows is the feed as followed: following applies when drawn, never to the buffer.
func (m *model) feedRows() []feedRow {
	var out []feedRow
	for _, r := range m.feed {
		if m.follow != "" && r.project != m.follow || m.onlyMe && r.from != "you" && r.to != "you" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// feedCursor: -1 sits on the newest row and follows it as rows arrive.
func (m *model) feedCursor(n int) int {
	if m.feedSel < 0 || m.feedSel >= n {
		return n - 1
	}
	return m.feedSel
}

func (m *model) keyDash(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.dashTree {
		if k.String() == "s" {
			m.dashTree = false
			return m, nil
		}
		return m.keyTree(k)
	}
	rows := m.feedRows()
	sel := m.feedCursor(len(rows))
	switch k.String() {
	case "up", "k":
		m.feedSel = max(0, sel-1)
	case "down", "j":
		if m.feedSel = sel + 1; m.feedSel >= len(rows)-1 {
			m.feedSel = -1
		}
	case "f":
		if sel >= 0 {
			m.follow, m.feedSel = rows[sel].project, -1
		}
	case "F":
		m.follow, m.onlyMe, m.feedSel = "", false, -1
	case "m":
		m.onlyMe, m.feedSel = !m.onlyMe, -1
	case "s":
		m.dashTree = true
	case "left", "h":
		m.focus = 0
	case "enter", "right", "l":
		if sel < 0 {
			return m, nil
		}
		switch r := rows[sel]; {
		case r.chat != "":
			cmd := m.openChat(r.chat)
			m.thread, m.focus, m.chFree = r.thread, 2, false
			return m, tea.Batch(cmd, m.focusBox(r.thread))
		case r.task != "":
			return m, m.push(detail{kind: "task", id: r.task, title: core.Clip(r.text, 40)})
		case r.told != nil:
			return m, m.push(toldDetail(*r.told))
		case r.ask:
			m.pick(sideRow{sec: secWaiting})
		}
	}
	return m, nil
}

func (m *model) viewFeed() []string {
	if m.dashTree {
		return m.viewTree()
	}
	w := m.mainW() - 5
	working, idle := 0, 0
	for _, r := range m.st.Runs {
		switch {
		case r.Terminal():
		case r.Status == core.StatusRunning:
			working++
		default:
			idle++
		}
	}
	lines := []string{"  " + stDim.Render(fmt.Sprintf("%d agents · %d working · %d idle · ", working+idle, working, idle)) +
		waitStyle(len(m.st.Asks)).Render(fmt.Sprintf("%d waiting on you", len(m.st.Asks)))}
	if g := m.glance(w); g != "" {
		lines = append(lines, "  "+g)
	}
	lines = append(lines, "  "+m.usage(w))
	pw := 12 // the project column, dropped when the message would get too narrow
	if w < 90 {
		pw = 0
	}
	lines = append(lines, "  "+stDim.Render(fmt.Sprintf("%-10s%-24s%s%s", "time", "from → to", pad(clip("project", pw), pw), "message")), "")
	rows := m.feedRows()
	sel := m.feedCursor(len(rows))
	room := m.bodyHeight() - len(lines)
	start := max(0, min(len(rows)-room, sel-room/2))
	var feed []string
	for i := start; i < len(rows) && len(feed) < room; i++ {
		r := rows[i]
		row := func(from, to, text string) string {
			return clip(fmt.Sprintf("%-10s%s%s", r.at.Local().Format("15:04:05"), pad(clip(from+" → "+to, 22), 24), pad(clip(r.project, pw-2), pw))+text, w)
		}
		var line string
		switch {
		case i == sel && m.focus == 1: // one reversed run, so no colour inside it
			line = stSel.Render(pad(row(r.from, r.to, m.feedText(r)), w))
		case r.from != "you" && r.to != "you":
			line = stDim.Render(row(r.from, r.to, m.feedText(r)))
		default: // with the person: who speaks keeps their chat colour, and an ask is amber
			text := m.feedText(r)
			if r.ask {
				text = stWarn.Render(text)
			}
			line = row(nameStyle(r.from).Render(r.from), nameStyle(r.to).Render(r.to), text)
		}
		feed = append(feed, "  "+line)
	}
	if len(rows) == 0 {
		feed = append(feed, "  "+stDim.Render("Nothing yet. Messages between agents and with you show here as they happen."))
	}
	for len(feed) < room {
		feed = append([]string{""}, feed...)
	}
	return append(lines, feed...)
}

func (m *model) feedText(r feedRow) string {
	for _, msg := range m.msgs[r.chat] {
		if msg.ID == r.msg {
			return preview(msg.Text)
		}
	}
	return oneLine(r.text)
}

// usage is the plan usage line, or why agents can't start.
func (m *model) usage(w int) string {
	if m.st.SandboxErr != "" {
		return stBad.Render(clip("Agents can't start: "+m.st.SandboxErr, w))
	}
	var parts []string
	now := time.Now()
	for _, p := range m.st.Plans {
		s := text.Visible(p.Harness) + " " + standing(p, now)
		for _, w := range p.Limit.WindowUses(now) {
			s += " · " + windowShort(w, now, false)
		}
		parts = append(parts, s)
	}
	for _, l := range m.st.Logins {
		if l.Error != "" {
			parts = append(parts, stBad.Render(text.Visible(l.Login)+" login can't renew"))
		}
	}
	if n := len(m.st.Alerts); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "alert", "alerts")))
	}
	if n := len(m.st.Requests); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s past the sandbox", n, plural(n, "request", "requests")))
	}
	if len(parts) == 0 {
		return ""
	}
	return stDim.Render(strings.Join(parts, "   ") + "   s status")
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// nameStyle is how a name shows in the feed: the colour it speaks in.
func nameStyle(name string) lipgloss.Style {
	switch name {
	case "you":
		return stYou
	case "hive", "worker":
		return stTitle
	}
	return speaker(name)
}

// waitStyle hints what waits on the person in amber; nothing waiting stays plain bold.
func waitStyle(n int) lipgloss.Style {
	if n > 0 {
		return stWarn.Bold(true)
	}
	return stTitle
}

// standing is the plan's state in plain words, coloured by how near its
// limit the login is.
func standing(p core.Plan, now time.Time) string {
	s := text.Visible(p.Limit.Standing(now))
	switch {
	case p.Parked(now):
		return stBad.Render(s)
	case p.Limit.Status == "allowed_warning" && p.Limit.ResetsAt.After(now):
		return stWarn.Render(s)
	}
	return s
}

// windowShort is one usage window and when it resets; a window that has reset
// since the reading shows that instead of its old share. Long adds how far
// off the reset is.
func windowShort(w harness.WindowUse, now time.Time, long bool) string {
	name := text.Visible(w.Name)
	switch {
	case w.ResetsAt.IsZero():
		return fmt.Sprintf("%s %.0f%%", name, w.Used*100)
	case w.Stale:
		return fmt.Sprintf("%s reset %s", name, stDim.Render(w.ResetsAt.Local().Format("Mon 15:04")))
	}
	s := fmt.Sprintf("%s %.0f%%, resets %s", name, w.Used*100, w.ResetsAt.Local().Format("Mon 15:04"))
	if long {
		s += stDim.Render(" (in " + harness.Until(w.ResetsAt, now) + ")")
	}
	return s
}
