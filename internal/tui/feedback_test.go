package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/feedback"
)

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// The selection follows the item, not its place: new feedback arriving above
// it leaves the same item selected, so x sets aside what the person read.
func TestFeedbackSelectionFollowsTheItem(t *testing.T) {
	m := designModel(120, 36)
	m.pick(sideRow{sec: secFeedback})
	m.focus = 1
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.fbSel != "f2" {
		t.Fatalf("selected %q after one down, want f2", m.fbSel)
	}
	st := m.st
	st.Feedback = append([]feedback.Item{{ID: "f0", Role: "worker", Status: feedback.Open, Text: "new"}}, st.Feedback...)
	m.Update(stateMsg{st: st})
	if it, _ := m.fbSelected(); it.ID != "f2" {
		t.Fatalf("after new feedback arrived the selection moved to %q", it.ID)
	}
	if _, cmd := m.Update(runes("x")); cmd == nil || m.st.Feedback[m.fbIndex()].ID != "f2" || m.st.Feedback[m.fbIndex()].Status != feedback.Closed {
		t.Fatalf("x did not set aside f2: %+v", m.st.Feedback)
	}
}

// An agent's text can't restyle the terminal: control characters are spelled
// out, and the sidebar counts open feedback not yet seen.
func TestFeedbackTextIsShownNotObeyed(t *testing.T) {
	m := designModel(80, 24)
	st := m.st
	st.Feedback = []feedback.Item{{ID: "f9", Role: "worker", Status: feedback.Open, About: "x\x1b[2J", Text: "hide\x1b[8m me\nsecond line"}}
	m.Update(stateMsg{st: st})
	m.pick(sideRow{sec: secFeedback})
	v := m.View()
	if strings.Contains(v, "\x1b[8m") || strings.Contains(v, "\x1b[2J") || !strings.Contains(v, `hide\x1b[8m me`) || !strings.Contains(v, "second line") {
		t.Fatalf("feedback text was not shown as text:\n%s", v)
	}
	if m.fbUnseen() != 1 || !strings.Contains(plain(v), "Feedback    1") {
		t.Fatalf("the sidebar should count one unseen item:\n%s", plain(v))
	}
}
