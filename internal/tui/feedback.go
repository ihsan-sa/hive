package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/feedback"
)

// ---- feedback from agents --------------------------------------------------

// fbIndex is the selected item's place in the list, found by its id so a list
// that shifts under the person keeps the same item selected.
func (m *model) fbIndex() int {
	for i, it := range m.st.Feedback {
		if it.ID == m.fbSel {
			return i
		}
	}
	return 0
}

func (m *model) fbSelected() (feedback.Item, bool) {
	if len(m.st.Feedback) == 0 {
		return feedback.Item{}, false
	}
	return m.st.Feedback[m.fbIndex()], true
}

// fbUnseen counts open feedback the person hasn't had selected yet.
func (m *model) fbUnseen() int {
	n := 0
	for _, it := range m.st.Feedback {
		if it.Status == feedback.Open && !it.Seen {
			n++
		}
	}
	return n
}

// fbSee marks the selected item seen, once.
func (m *model) fbSee() tea.Cmd {
	it, ok := m.fbSelected()
	if !ok || it.Seen || m.focus != 1 {
		return nil
	}
	m.st.Feedback[m.fbIndex()].Seen = true
	return m.post("/api/feedback", map[string]string{"id": it.ID, "do": "seen"}, "")
}

func (m *model) keyFeedback(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.st.Feedback
	if len(items) == 0 {
		return m, nil
	}
	i := m.fbIndex()
	it := items[i]
	switch k.String() {
	case "up", "k":
		if i > 0 {
			m.fbSel = items[i-1].ID
		}
	case "down", "j":
		if i < len(items)-1 {
			m.fbSel = items[i+1].ID
		}
	case "x":
		if it.Status == feedback.Open {
			m.st.Feedback[i].Status = feedback.Closed
			return m, m.post("/api/feedback", map[string]string{"id": it.ID, "do": "close"}, "set aside")
		}
		m.st.Feedback[i].Status = feedback.Open
		return m, m.post("/api/feedback", map[string]string{"id": it.ID, "do": "reopen"}, "open again")
	case "p":
		if it.Status == feedback.Passed {
			return m, nil
		}
		m.st.Feedback[i].Status = feedback.Passed
		return m, m.post("/api/feedback", map[string]string{"id": it.ID, "do": "pass"}, "passed to the lead")
	}
	m.fbSel = m.st.Feedback[m.fbIndex()].ID
	return m, m.fbSee()
}

func (m *model) viewFeedback() []string {
	if len(m.st.Feedback) == 0 {
		return []string{"", "  No feedback from agents yet.", "",
			stDim.Render("  Agents send it with `hive agent feedback --text \"...\"` when something about the Hive gets in their way.")}
	}
	w := m.mainW() - 5
	cur := m.fbIndex()
	var lines []string
	sel, aside := 0, false
	for i, it := range m.st.Feedback {
		if it.Status != feedback.Open && !aside {
			aside = true
			lines = append(lines, "  "+stDim.Render("set aside"), "")
		}
		gut := "  "
		if i == cur {
			gut, sel = stTitle.Render("▌")+" ", len(lines)
		}
		from := it.Project
		if from == "" {
			from = "hive"
		}
		from += " › " + it.Role
		if it.Name != "" {
			from += " " + it.Name
		}
		if it.About != "" {
			from += " · " + visible(it.About)
		}
		mark, age := "", ago(it.At)
		if it.Status == feedback.Open && !it.Seen {
			mark = stTitle.Render("● ")
		}
		top := mark + stTitle.Render(clip(from, w-len(age)-3))
		if it.Status != feedback.Open {
			top = stDim.Render(clip(from, w-len(age)-3))
		}
		ls := []string{pad(top, w-len(age)) + stDim.Render(age)}
		text := agentText(it.Text, w)
		if i != cur && len(text) > 2 {
			text = append(text[:2:2], stDim.Render("…"))
		}
		ls = append(ls, text...)
		if i == cur {
			if it.Note != "" {
				ls = append(ls, stDim.Render(clip(it.Note, w)))
			}
			ls = append(ls, stDim.Render(clip("from "+it.Run, w)))
		}
		lines = append(append(lines, indent(ls, gut)...), "")
	}
	return window(lines, sel, m.bodyHeight())
}

// agentText breaks an agent's words at w cells, with control characters
// spelled out so the text can't restyle the terminal.
func agentText(s string, w int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(strings.ReplaceAll(s, "\r", ""), "\n"), "\n") {
		out = append(out, strings.Split(ansi.Wrap(visible(l), max(10, w), " "), "\n")...)
	}
	return out
}

func (m *model) feedbackKeys() []string {
	it, ok := m.fbSelected()
	switch {
	case !ok:
		return []string{"tab", "sidebar"}
	case it.Status == feedback.Open:
		return []string{"↑↓", "move", "p", "pass to the lead", "x", "set aside", "tab", "sidebar"}
	case it.Status == feedback.Passed:
		return []string{"↑↓", "move", "x", "open again", "tab", "sidebar"}
	}
	return []string{"↑↓", "move", "p", "pass to the lead", "x", "open again", "tab", "sidebar"}
}
