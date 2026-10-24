package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// There is deliberately no theme: bold, faint and reverse, and red only for
// what failed.
var (
	stTitle = lipgloss.NewStyle().Bold(true)
	stDim   = lipgloss.NewStyle().Faint(true)
	stBoldF = lipgloss.NewStyle().Bold(true).Faint(true)
	stSel   = lipgloss.NewStyle().Reverse(true)
	stBad   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: "#E5534B", Light: "#C4352B"})
	// Kept for the status tree and run detail, which predate the design's palette.
	stGood   = lipgloss.NewStyle()
	stWarn   = stTitle
	stAccent = stTitle
)

// pane is one column of the screen: a header row and a body.
type pane struct {
	w    int
	head string
	body []string
}

// layout owns every width: the sidebar, the main pane and the side thread
// pane (0 when closed, or when a narrow screen lets the thread take over).
func (m *model) layout() (side, main, thread int) {
	if m.w < 120 {
		return 18, m.w - 19, 0
	}
	rest := m.w - 23
	if !m.sidePane() {
		return 22, rest, 0
	}
	thread = (rest - 1) / 2
	return 22, rest - 1 - thread, thread
}

// takeover: a pane opened beside the main one fills the screen when there is
// no room for both.
func (m *model) takeover() bool { return m.w < 120 && m.sidePane() }

// frame draws the shell every screen shares: headers, a rule, the bodies with
// a faint divider between panes, a rule and the key hints.
func (m *model) frame(ps []pane, hint string) string {
	bh := m.h - 4
	var b strings.Builder
	row := func(cell func(p pane) string, sep string) {
		for i, p := range ps {
			if i > 0 {
				b.WriteString(stDim.Render(sep))
			}
			b.WriteString(cell(p))
		}
		b.WriteString("\n")
	}
	row(func(p pane) string { return fit(p.head, p.w) }, "│")
	rule := func(p pane) string { return stDim.Render(strings.Repeat("─", p.w)) }
	row(rule, "┼")
	for i := 0; i < bh; i++ {
		row(func(p pane) string {
			if i < len(p.body) {
				return fit(p.body[i], p.w)
			}
			return strings.Repeat(" ", p.w)
		}, "│")
	}
	row(rule, "┴")
	b.WriteString(clip(hint, m.w))
	return b.String()
}

// fit clips or pads a line to exactly w cells.
func fit(s string, w int) string { return pad(clip(s, w), w) }

// head is a pane's header: the title at the content inset, a faint note
// after it and a faint note right-aligned.
func head(w int, title, note, right string) string {
	s := "   " + title
	if note != "" {
		s += "  " + stDim.Render(note)
	}
	if right != "" {
		s = pad(s, w-2-visibleWidth(right)) + stDim.Render(right)
	}
	return s
}

// hints lays out "key label" pairs, dropping pairs from the right that don't fit.
func hints(w int, pairs ...string) string {
	s, n := "", 0
	for i := 0; i+1 < len(pairs); i += 2 {
		p := stTitle.Render(pairs[i]) + " " + stDim.Render(pairs[i+1])
		sep := 3
		if n == 0 {
			sep = 1
		}
		if n+sep+visibleWidth(p) > w {
			break
		}
		s += strings.Repeat(" ", sep) + p
		n += sep + visibleWidth(p)
	}
	return s
}

// box draws a compose box w cells wide: a rounded border with the label set
// into the top edge. Unfocused boxes are faint.
func box(label string, lines []string, w int, focused bool) []string {
	st := stDim
	if focused {
		st = lipgloss.NewStyle()
	}
	lb := st.Render(label)
	if focused {
		lb = stTitle.Render(label)
	}
	out := []string{st.Render("╭─ ") + lb + st.Render(" "+strings.Repeat("─", max(0, w-5-visibleWidth(label)))+"╮")}
	for _, l := range lines {
		if !focused {
			l = stDim.Render(l)
		}
		out = append(out, st.Render("│ ")+fit(l, w-4)+st.Render(" │"))
	}
	return append(out, st.Render("╰"+strings.Repeat("─", w-2)+"╯"))
}
