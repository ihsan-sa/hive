package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	stCode, stLink lipgloss.Style // set with the palette (setHue)
	stBold         = lipgloss.NewStyle().Bold(true)
	stEm           = lipgloss.NewStyle().Italic(true)

	reInline = regexp.MustCompile("`([^`]+)`|\\*\\*([^*]+)\\*\\*|\\[([^\\]]+)\\]\\(([^)\\s]+)\\)|(?:^|\\s)\\*([^*\\s][^*]*)\\*")
	reBullet = regexp.MustCompile(`^(\s*)[-*+] `)
	reHead   = regexp.MustCompile(`^#{1,6} +`)
)

// markdown renders a chat message's markdown to styled lines of at most w
// cells. Styles are plain SGR codes, so a terminal without colour (NO_COLOR,
// TERM=dumb) gets the text with the markers removed and nothing else.
func markdown(s string, w int) []string {
	var out []string
	fence := 0 // backticks in the open fence's run; 0 is outside a fence
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		t := strings.TrimSpace(l)
		run := len(t) - len(strings.TrimLeft(t, "`"))
		switch {
		case fence == 0 && run >= 3:
			fence = run
		case fence > 0 && run >= fence && run == len(t):
			// Only a bare run at least as long as the opener closes it.
			fence = 0
		case fence > 0:
			for _, c := range wrap(l, w) {
				out = append(out, stCode.Render(c))
			}
		case reHead.MatchString(l):
			out = append(out, wrapStyled(stBold, reHead.ReplaceAllString(l, ""), w)...)
		case reBullet.MatchString(l):
			m := reBullet.FindStringSubmatch(l)
			body := wrap(inline(l[len(m[0]):]), w-len(m[1])-2)
			for i, c := range body {
				p := m[1] + "  "
				if i == 0 {
					p = m[1] + "• "
				}
				out = append(out, p+c)
			}
		default:
			out = append(out, wrap(inline(l), w)...)
		}
	}
	return out
}

func wrapStyled(st lipgloss.Style, s string, w int) []string {
	ls := wrap(inline(s), w)
	for i := range ls {
		ls[i] = st.Render(ls[i])
	}
	return ls
}

// inline styles `code`, **bold**, *italic* and [text](url) within one line.
func inline(s string) string {
	return reInline.ReplaceAllStringFunc(s, func(m string) string {
		g := reInline.FindStringSubmatch(m)
		switch {
		case g[1] != "":
			return stCode.Render(g[1])
		case g[2] != "":
			return withCode(stBold, g[2])
		case g[3] != "":
			return stLink.Render(g[3]) + " (" + g[4] + ")"
		}
		return m[:len(m)-len(g[5])-2] + withCode(stEm, g[5])
	})
}

var reCode = regexp.MustCompile("`([^`]+)`")

// withCode renders s in st, with any `code` inside it styled as code too, so
// a bold or italic span that holds inline code shows no backticks.
func withCode(st lipgloss.Style, s string) string {
	var b strings.Builder
	last := 0
	for _, ix := range reCode.FindAllStringSubmatchIndex(s, -1) {
		if ix[0] > last {
			b.WriteString(st.Render(s[last:ix[0]]))
		}
		b.WriteString(stCode.Inherit(st).Render(s[ix[2]:ix[3]]))
		last = ix[1]
	}
	if last < len(s) {
		b.WriteString(st.Render(s[last:]))
	}
	return b.String()
}

// preview is a message on one plain line, Markdown markers gone, for a
// collapsed thread whose row carries its own selection style.
func preview(s string) string {
	return oneLine(ansi.Strip(strings.Join(markdown(s, 1<<16), " ")))
}
