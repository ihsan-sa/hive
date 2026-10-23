package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestSelectionTextStaysInItsPane: the copy of a selection over the right
// pane of a styled two-pane screen has none of the left pane or the border.
func TestSelectionTextStaysInItsPane(t *testing.T) {
	screen := []string{
		stSel.Render("lead      ") + stDim.Render(" │ ") + stTitle.Render("first line"),
		"textkit   " + stDim.Render(" │ ") + "second line   ",
		"          " + stDim.Render(" │ ") + "third",
	}
	s := selection{on: true, a: [2]int{19, 0}, b: [2]int{16, 2}, box: [4]int{13, 0, 30, 3}}
	if got, want := s.text(screen), "line\nsecond line\nthir"; got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
	s.a, s.b = s.b, s.a // dragged upwards
	if got, want := s.text(screen), "line\nsecond line\nthir"; got != want {
		t.Fatalf("upward drag: text %q, want %q", got, want)
	}
}

// TestDragSelectsChatText drives a drag over the thread pane of the real
// view, past its left edge into the chat list, and checks the copy is the
// pane's own text; the wheel still scrolls the chat afterwards.
func TestDragSelectsChatText(t *testing.T) {
	m := testModel(80, 20)
	x0 := m.listW() + 3
	m.Update(tea.MouseMsg{X: x0, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: 79, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	pane := m.viewThreads(m.rightW())
	var want []string
	for _, l := range pane[1:5] {
		want = append(want, strings.TrimRight(ansi.Strip(clip(l, m.rightW())), " "))
	}
	if got := m.sel.text(strings.Split(m.View(), "\n")); got != strings.Join(want, "\n") {
		t.Fatalf("copy %q, want the pane's lines %q", got, want)
	}
	m.Update(tea.MouseMsg{X: 0, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	got := strings.Split(m.sel.text(strings.Split(m.View(), "\n")), "\n")
	for i, l := range got {
		if i >= len(want) || !strings.HasPrefix(want[i], l) {
			t.Fatalf("a drag into the chat list copied more than the pane: %q", got)
		}
	}
	_, cmd := m.Update(tea.MouseMsg{X: 0, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if cmd == nil || m.sel.on || m.flash != "copied" {
		t.Fatalf("release: cmd %v, selecting %v, flash %q", cmd != nil, m.sel.on, m.flash)
	}
	before := m.scroll
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.scroll == before {
		t.Fatal("the wheel no longer scrolls the chat after a drag")
	}
}

// TestClickIsNotACopy: pressing and letting go in place copies nothing.
func TestClickIsNotACopy(t *testing.T) {
	m := testModel(80, 20)
	m.Update(tea.MouseMsg{X: 40, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if _, cmd := m.Update(tea.MouseMsg{X: 40, Y: 4, Action: tea.MouseActionRelease}); cmd != nil || m.flash == "copied" {
		t.Fatal("a click copied")
	}
}
