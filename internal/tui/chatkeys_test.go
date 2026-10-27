package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
)

// End, G and ctrl+end go to the newest message and never open a thread,
// whether a thread is picked or the box has the keys with a draft in it.
func TestEndGoesToTheNewest(t *testing.T) {
	m := testModel(80, 20)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	for _, k := range []tea.KeyMsg{{Type: tea.KeyEnd}, runes("G"), {Type: tea.KeyCtrlEnd}} {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m.Update(tea.KeyMsg{Type: tea.KeyUp}) // the first thread, at the top
		m.View()
		m.Update(k)
		if m.thread != "" || m.focus != 1 || m.threadSel != -1 {
			t.Fatalf("%s: thread %q focus %d pick %d", k, m.thread, m.focus, m.threadSel)
		}
		if v := m.View(); !strings.Contains(v, "pull/3") || m.chScroll != 0 {
			t.Fatalf("%s did not show the newest message:\n%s", k, v)
		}
	}
	m.Update(runes("n"))
	m.Update(runes("draft"))
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if m.chScroll != 0 || !m.composing || m.input.Value() != "draft" {
		t.Fatalf("ctrl+end from the box: scroll %d composing %v draft %q", m.chScroll, m.composing, m.input.Value())
	}
}

// In a thread the reply box has the keys; with nothing typed, ↑ and ↓
// scroll the thread, and End comes back to its newest reply.
func TestThreadScrollsFromItsBox(t *testing.T) {
	m := testModel(160, 20)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != 2 || !m.composing || m.reply == "" {
		t.Fatalf("no reply box: focus %d", m.focus)
	}
	m.View()
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.thScroll != 6 {
		t.Fatalf("↑↑ in the empty reply box: scroll %d, want 6", m.thScroll)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.thScroll != 3 {
		t.Fatalf("↓: scroll %d, want 3", m.thScroll)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m.thScroll != 0 || m.focus != 2 {
		t.Fatalf("End: scroll %d focus %d", m.thScroll, m.focus)
	}
	m.Update(runes("hi"))
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.thScroll != 0 {
		t.Fatal("↑ with a draft scrolled rather than moving in it")
	}
}

// A message shows once however the post's answer and the chat's refresh
// cross: the refresh first, or a refresh that left before the post after.
func TestSentMessageShowsOnce(t *testing.T) {
	m := testModel(80, 24)
	now := time.Now()
	hi := chat.Message{ID: "9", Thread: "9", From: "person", Name: "you", Text: "hi there", At: now}
	before := append([]chat.Message(nil), m.msgs["lead"]...)
	count := func() (n int) {
		for _, x := range m.msgs["lead"] {
			if x.ID == "9" {
				n++
			}
		}
		return n
	}
	m.Update(chatMsg{"lead", append(append([]chat.Message(nil), before...), hi)})
	m.Update(sentMsg{out: outMsg{chat: "lead", text: "hi there"}, msg: hi})
	if count() != 1 {
		t.Fatalf("refresh then answer: %d copies", count())
	}
	m.msgs["lead"] = before
	m.Update(sentMsg{out: outMsg{chat: "lead", text: "hi there"}, msg: hi})
	m.Update(chatMsg{"lead", before}) // fetched before the post
	if count() != 1 {
		t.Fatalf("answer then a stale refresh: %d copies", count())
	}
	m.Update(chatMsg{"lead", append(append([]chat.Message(nil), before...), hi)})
	if count() != 1 || len(m.mine) != 0 {
		t.Fatalf("the refresh that has it: %d copies, %d held", count(), len(m.mine))
	}

	// A reply still in the outbox that a refresh already brought shows once.
	m.thread, m.reply = "1", "1"
	m.outbox = []outMsg{{n: 1, chat: "lead", thread: "1", text: "thanks", at: now}}
	if len(m.sending(threads(m.msgs["lead"])[1].msgs)) != 1 {
		t.Fatal("a reply not yet back doesn't show as sending")
	}
	m.Update(chatMsg{"lead", append(append([]chat.Message(nil), m.msgs["lead"]...), chat.Message{ID: "10", Thread: "1", From: "person", Text: "thanks", At: now})})
	ts := threads(m.msgs["lead"])
	if n := len(m.sending(ts[len(ts)-2].msgs)); n != 0 {
		t.Fatalf("a reply back from the Hive still shows as sending (%d)", n)
	}
}

// However a long thread is cut to keep the pick in view, a blank row parts
// it from the box.
func TestRoomAboveTheBox(t *testing.T) {
	m := testModel(100, 24)
	m.msgs["lead"] = append(m.msgs["lead"], chat.Message{ID: "4", Thread: "1", From: "lead-x", Name: "lead", Text: strings.Repeat("more words ", 200), At: time.Now()})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(tea.KeyMsg{Type: tea.KeyUp}) // the long last thread, taller than the screen
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for i, l := range lines {
		if strings.Contains(l, "waiting on you") {
			if above := lines[i-1]; strings.TrimSpace(above[strings.Index(above, "│")+3:]) != "" {
				t.Fatalf("the row above the box has text: %q", above)
			}
			return
		}
	}
	t.Fatal("no box")
}

// e opens one level more and E (shift+e) closes one: a channel's threads
// all at once, the status tree level by level.
func TestExpandByLevel(t *testing.T) {
	m := testModel(100, 60)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.Update(runes("E"))
	if strings.Contains(m.View(), "pull/3") {
		t.Fatal("E left the last thread open")
	}
	m.Update(runes("e"))
	ts := threads(m.msgs["lead"])
	for i := range ts {
		if !m.isOpen(ts, i) {
			t.Fatalf("e left thread %d closed", i)
		}
	}

	m.dashTree, m.section = true, secDash
	depth := func() (d int) {
		for _, r := range m.dashRows() {
			d = max(d, r.depth)
		}
		return d
	}
	start := len(m.dashRows())
	m.Update(runes("e"))
	if len(m.dashRows()) <= start {
		t.Fatalf("e didn't open a level: %d rows, was %d", len(m.dashRows()), start)
	}
	opened := len(m.dashRows())
	m.Update(runes("E"))
	if len(m.dashRows()) >= opened {
		t.Fatalf("E didn't close a level: %d rows, was %d", len(m.dashRows()), opened)
	}
	for i := 0; i < 5; i++ {
		m.Update(runes("E"))
	}
	if depth() != 0 {
		t.Fatalf("E all the way: depth %d, want 0", depth())
	}

	m.focus = 0
	m.Update(runes("E"))
	for _, r := range m.sideRows() {
		if !r.group && r.sec < 0 && r.key != "gap" {
			t.Fatalf("E in the sidebar left %q showing", r.key)
		}
	}
	m.Update(runes("e"))
	if len(m.sideRows()) == len(sections)+1 {
		t.Fatal("e in the sidebar opened nothing")
	}
}
