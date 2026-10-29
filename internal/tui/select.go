package tui

import (
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Selection works as in Claude Code's full screen: the control center keeps
// the mouse so the wheel scrolls the chat, a drag highlights text inside the
// body of the one pane it started in, and letting go copies it. A drag can't
// leave its pane, so the copy never carries the sidebar or a divider.
type selection struct {
	on   bool
	a, b [2]int // press and current cell, {x, y}
	box  [4]int // the pane's cells, {x0, y0, x1, y1} with x1 and y1 past its edge
}

// paneAt is the body of the pane under (x, y) on a screen drawn by frame,
// whose rule under the headers marks each divider with ┼; ok is false off
// the bodies or on a divider.
func paneAt(screen []string, x, y int) (box [4]int, ok bool) {
	if len(screen) < 4 || y < 2 || y >= len(screen)-2 {
		return box, false
	}
	x0, i := 0, 0
	for _, r := range ansi.Strip(screen[1]) + "┼" {
		if r == '┼' {
			if x >= x0 && x < i {
				return [4]int{x0, 2, i, len(screen) - 2}, true
			}
			x0 = i + 1
		}
		i++
	}
	return box, false
}

func (m *model) mouseSelect(msg tea.MouseMsg) (handled bool, cmd tea.Cmd) {
	p := [2]int{msg.X, msg.Y}
	switch {
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		box, ok := paneAt(strings.Split(m.View(), "\n"), msg.X, msg.Y)
		m.sel = selection{on: ok, a: p, b: p, box: box}
		return true, nil
	case !m.sel.on:
		return false, nil
	case msg.Action == tea.MouseActionMotion:
		m.sel.b = [2]int{min(max(p[0], m.sel.box[0]), m.sel.box[2]-1), min(max(p[1], m.sel.box[1]), m.sel.box[3]-1)}
		return true, nil
	case msg.Action == tea.MouseActionRelease:
		s := m.sel
		m.sel.on = false
		if s.a == s.b { // a click, not a drag: on a feed line, it opens it
			return true, m.click(s.a[0], s.a[1])
		}
		text := s.text(strings.Split(m.View(), "\n"))
		if text == "" {
			return true, nil
		}
		m.setFlash("copied")
		return true, func() tea.Msg { copyText(text); return nil }
	}
	return false, nil
}

// span is the columns [l, r) the selection covers on row y.
func (s selection) span(y int) (l, r int) {
	a, b := s.ordered()
	l, r = s.box[0], s.box[2]
	if y == a[1] {
		l = a[0]
	}
	if y == b[1] {
		r = b[0] + 1
	}
	return l, r
}

func (s selection) ordered() (a, b [2]int) {
	a, b = s.a, s.b
	if b[1] < a[1] || b[1] == a[1] && b[0] < a[0] {
		a, b = b, a
	}
	return a, b
}

// text is the selected text of a rendered screen, without styling or the
// spaces that pad each pane's lines.
func (s selection) text(screen []string) string {
	a, b := s.ordered()
	var out []string
	for y := a[1]; y <= b[1] && y < len(screen); y++ {
		l, r := s.span(y)
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(screen[y], l, r)), " "))
	}
	return strings.Join(out, "\n")
}

// paint highlights the selection on a rendered screen.
func (s selection) paint(screen []string, w int) {
	a, b := s.ordered()
	for y := a[1]; y <= b[1] && y < len(screen); y++ {
		l, r := s.span(y)
		line := pad(screen[y], w)
		screen[y] = ansi.Cut(line, 0, l) + stSel.Render(ansi.Strip(ansi.Cut(line, l, r))) + ansi.Cut(line, r, w)
	}
}

// copyText puts text on the clipboard with OSC 52, which the terminal itself
// handles, so it works over SSH too. Terminal.app has no OSC 52, so on macOS
// pbcopy sets it as well.
func copyText(text string) {
	os.Stdout.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a")
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	}
}
