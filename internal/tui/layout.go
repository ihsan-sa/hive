package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Styling is bold, faint and reverse, with hints of colour: red for what
// failed, green for what runs or was kept, amber for what waits on the
// person, blue for the person, and a hue per role (lead, worker, reviewer,
// warden, tribunal, judge) wherever an agent shows. Each colour has
// a dark and a light terminal's shade; NO_COLOR drops every hue (noHue).
var (
	stTitle = lipgloss.NewStyle().Bold(true)
	stDim   = lipgloss.NewStyle().Faint(true)
	stBoldF = lipgloss.NewStyle().Bold(true).Faint(true)
	stSel   = lipgloss.NewStyle().Reverse(true)

	stBad, stGood, stWarn, stAccent, stYou lipgloss.Style
	stAgents                               []lipgloss.Style // speakers other than the person, by name
	stRoles                                map[string]lipgloss.Style
)

func hue(dark, light string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Dark: dark, Light: light})
}

func init() { setHue(true) }

// setHue sets the palette, or with on false drops its hues for NO_COLOR:
// what was coloured to stand out stays bold.
func setHue(on bool) {
	if !on {
		stBad, stGood, stWarn, stAccent, stYou = stTitle, lipgloss.NewStyle(), stTitle, stTitle, stTitle
		stAgents, stRoles = []lipgloss.Style{stTitle}, nil
		stCode, stLink = lipgloss.NewStyle(), lipgloss.NewStyle().Underline(true)
		return
	}
	stBad = hue("#E5534B", "#C4352B")
	stGood = hue("#57AB5A", "#2A7A3B")
	stWarn = hue("#DAAA3F", "#9A6700")
	stAccent = hue("#39C5CF", "#1B7C83")
	stYou = hue("#6CB6FF", "#0969DA").Bold(true)
	stAgents = []lipgloss.Style{
		hue("#DCBDFB", "#8250DF").Bold(true), // the lead's: every lead hashes here
		hue("#F69D50", "#BC4C00").Bold(true),
		hue("#56D4DD", "#1B7C83").Bold(true),
		hue("#FC8DC7", "#BF3989").Bold(true),
		hue("#8DDB8C", "#2A7A3B").Bold(true),
	}
	stRoles = map[string]lipgloss.Style{
		"lead":     stAgents[0],
		"worker":   stAgents[1],
		"reviewer": stAgents[2],
		"security": stAgents[2],
		"warden":   stAgents[3],
		"tribunal": stAgents[4],
		"judge":    hue("#ADBAC7", "#57606A").Bold(true),
	}
	stCode = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stLink = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Underline(true)
}

// speaker is the colour a name speaks in: the person's own, the lead's, or
// one picked by the name, so the same agent keeps its colour.
func speaker(name string) lipgloss.Style {
	if name == "lead" || len(stAgents) == 1 {
		return stAgents[0]
	}
	h := 0
	for _, r := range name {
		h = h*31 + int(r)
	}
	return stAgents[1+(h&0x7fffffff)%(len(stAgents)-1)]
}

// roles lists the roles with a colour of their own, in the help's order.
var roles = []string{"lead", "worker", "reviewer", "warden", "tribunal", "judge"}

// roleStyle is the colour every agent of a role speaks and shows in, so a
// warden reads as a warden wherever it appears; the Hive's housekeeping
// (sweeps, filers, step-backs) stays plain bold.
func roleStyle(role string) lipgloss.Style {
	if s, ok := stRoles[role]; ok {
		return s
	}
	return stTitle
}

// roleOfName is the role a label names, as runLabel writes one ("warden
// 3f2a1"), or "".
func roleOfName(name string) string {
	first, _, _ := strings.Cut(name, " ")
	if slices.Contains(roles, first) || first == "security" {
		return first
	}
	return ""
}

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
