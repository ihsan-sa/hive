package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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
