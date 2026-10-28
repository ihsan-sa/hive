package tui

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
)

// The control center follows the commands a client sends through the
// daemon (POST /api/ui, docs/ui-api.md): it waits on GET /api/ui, acts on
// each command as the keys it stands for, says on the hint line who did
// what, and reports where it is (POST /api/ui/view) so the client can show
// the same.

type uiMsg struct {
	st  api.UIState
	err error
}

// uiWait is how long one GET /api/ui waits for a command, well inside the
// client's own timeout.
const uiWait = 25

// followUI asks for the commands after seq; a negative seq asks only for
// the newest seq, so commands sent before this control center opened aren't
// acted on.
func (m *model) followUI(after int64, delay time.Duration) tea.Cmd {
	if m.c == nil {
		return nil
	}
	c := m.c
	return func() tea.Msg {
		time.Sleep(delay)
		var st api.UIState
		wait := uiWait
		if after < 0 {
			wait = 0
		}
		err := c.Do("GET", fmt.Sprintf("/api/ui?after=%d&wait=%d", after, wait), nil, &st)
		return uiMsg{st, err}
	}
}

// gotUI acts on the commands that came and asks for the next.
func (m *model) gotUI(msg uiMsg) tea.Cmd {
	if msg.err != nil { // a daemon from before /api/ui, or one restarting: ask again later
		return m.followUI(m.uiSeq, 5*time.Second)
	}
	var cmds []tea.Cmd
	if m.uiSeq >= 0 {
		for _, c := range msg.st.Commands {
			if c.Seq > m.uiSeq {
				cmds = append(cmds, m.steer(c))
			}
		}
	}
	m.uiSeq = msg.st.Seq
	return tea.Batch(append(cmds, m.reportView(), m.followUI(m.uiSeq, 0))...)
}

// uiKeys are the keys each moving command stands for.
var uiKeys = map[string]tea.KeyType{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"pageup": tea.KeyPgUp, "pagedown": tea.KeyPgDown, "open": tea.KeyEnter, "back": tea.KeyEsc}

// steer acts on one command as the person's own keys would.
func (m *model) steer(c api.UICommand) tea.Cmd {
	m.setFlash("⌁ " + c.Label())
	m.showSince, m.notice, m.help = false, nil, false // an overlay would eat the key
	if k, ok := uiKeys[c.Action]; ok {
		var cmds []tea.Cmd
		for range max(c.N, 1) {
			_, cmd := m.key(tea.KeyMsg{Type: k})
			cmds = append(cmds, cmd)
			if c.Action == "open" || c.Action == "back" {
				break
			}
		}
		return tea.Batch(cmds...)
	}
	switch c.Action {
	case "view":
		return m.showView(c.View)
	case "answer":
		if m.section != secWaiting || len(m.stack) > 0 || m.waitSel >= len(m.st.Asks) {
			m.setFlash("✗ " + c.Label() + ": no Waiting item is selected")
			return nil
		}
		a := m.st.Asks[m.waitSel]
		opt := a.Options[min(m.optSel, len(a.Options)-1)]
		if c.Answer != "" {
			opt = ""
			for _, o := range a.Options {
				if strings.EqualFold(o, c.Answer) {
					opt = o
				}
			}
			if opt == "" {
				m.setFlash(fmt.Sprintf("✗ %s: not one of %s", c.Label(), strings.Join(a.Options, ", ")))
				return nil
			}
		}
		m.optSel = 0
		return m.post("/api/answer", map[string]string{"id": a.ID, "answer": opt}, "⌁ "+c.Label()+" · answered: "+opt)
	}
	return nil
}

// showView switches to one of api.UIViews, as the sidebar would.
func (m *model) showView(v string) tea.Cmd {
	m.blurBox()
	m.editing, m.picking = false, false
	m.input.Blur()
	switch v {
	case "chats", "threads", "thread":
		var cmd tea.Cmd
		if m.chat == "" {
			for _, c := range m.st.Chats {
				if c.Kind == "lead" {
					cmd = m.openChat(c.ID)
				}
			}
		} else {
			cmd = m.openChat(m.chat)
		}
		switch {
		case v == "chats":
			m.focus = 0
		case v == "thread" && m.thread != "":
			m.focus = 2
			return tea.Batch(cmd, m.focusBox(m.thread))
		default:
			m.focus = 1
		}
		return cmd
	}
	sec := map[string]int{"dashboard": secDash, "agents": secDash, "waiting": secWaiting, "feedback": secFeedback,
		"knowledge": secKB, "modules": secModules, "settings": secSettings}[v]
	m.side, m.sideKey = sec, "sec:"+sections[sec]
	cmd := m.pick(sideRow{sec: sec})
	m.focus, m.dashTree = 1, v == "agents" || sec == secDash && m.dashTree && v != "dashboard"
	return cmd
}

// uiView is where the control center is and what is selected.
func (m *model) uiView() api.UIView {
	v := api.UIView{Focus: []string{"sidebar", "main", "thread"}[min(max(m.focus, 0), 2)], Sel: -1, Applied: m.uiSeq}
	v.View = strings.ToLower(sections[m.section])
	switch {
	case m.section == secChats:
		v.View, v.Chat = []string{"chats", "threads", "thread"}[min(max(m.focus, 0), 2)], m.chat
	case m.section == secDash && m.dashTree:
		v.View = "agents"
	case m.section == secKB:
		v.View = "knowledge"
	}
	switch {
	case m.focus == 0:
		rows := m.sideRows()
		v.Sel, v.Rows = m.sideAt(rows), len(rows)
		if v.Sel < len(rows) {
			r := rows[v.Sel]
			v.Item, v.Title = or(r.chatID, r.key), strings.TrimSpace(r.label)
		}
	case len(m.stack) > 0:
		d := m.stack[len(m.stack)-1]
		v.Detail, v.Item, v.Title, v.Sel, v.Rows = d.kind, d.id, d.title, d.scroll, d.total
	case m.section == secChats && m.focus == 2:
		v.Item = m.thread
	case m.section == secChats:
		ts := threads(m.msgs[m.chat])
		v.Rows = len(ts)
		if m.threadSel >= 0 && m.threadSel < len(ts) {
			v.Sel, v.Item = m.threadSel, ts[m.threadSel].id
			v.Title = preview(ts[m.threadSel].msgs[0].Text)
		}
	case m.section == secDash && m.dashTree:
		rows := m.dashRows()
		if v.Rows = len(rows); v.Rows > 0 {
			v.Sel = m.treeAt(rows)
			v.Item, v.Title = rows[v.Sel].key, oneLine(rows[v.Sel].text)
		}
	case m.section == secDash:
		rows := m.feedRows()
		if v.Rows = len(rows); v.Rows > 0 {
			v.Sel = m.feedCursor(len(rows))
			r := rows[v.Sel]
			v.Item, v.Title = or(r.task, r.thread), m.feedText(r)
		}
	case m.section == secWaiting:
		if v.Rows = len(m.st.Asks); m.waitSel < v.Rows {
			a := m.st.Asks[m.waitSel]
			v.Sel, v.Item, v.Title, v.Options, v.Option = m.waitSel, a.ID, a.Title, a.Options, min(m.optSel, len(a.Options)-1)
		}
	case m.section == secFeedback:
		v.Sel = m.fbIndex()
		if it, ok := m.fbSelected(); ok {
			v.Item = it.ID
		}
	case m.section == secKB:
		v.Sel = m.kbSel
	case m.section == secModules:
		v.Sel, v.Rows, v.Item = m.modSel, len(m.st.Modules), m.modName()
	case m.section == secSettings:
		v.Sel = m.setSel
	}
	return v
}

var uiBy = func() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s pid %d", or(host, "?"), os.Getpid())
}()

// reportView tells the daemon where the control center is, when that moved.
func (m *model) reportView() tea.Cmd {
	if m.c == nil || m.uiSeq < 0 {
		return nil
	}
	v := m.uiView()
	if m.uiSent != nil && reflect.DeepEqual(*m.uiSent, v) {
		return nil
	}
	m.uiSent = &v
	c := m.c
	v.By = uiBy
	return func() tea.Msg {
		c.Do("POST", "/api/ui/view", v, nil)
		return nil
	}
}
