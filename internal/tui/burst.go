package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A Windows console hands a paste over as keystrokes: bubbletea reads it
// with ReadConsoleInput, which knows no bracketed paste, so each character
// arrives as its own key and a newline as Enter. A paste then typed shortcuts
// outside the box, sent a message at its first line break, and a dropped
// file's path stayed text. keyBurst gathers keys that arrive faster than
// anyone types and replays a burst of them as the one paste a Unix terminal
// would have sent.

// burstGap is the longest pause inside a paste. A person's keys come tens of
// milliseconds apart at the quickest; a paste's within a millisecond, or one
// console read (16ms) later for a long one.
const burstGap = 25 * time.Millisecond

// burstMin is the fewest keys that read as a paste; fewer replay as keys.
const burstMin = 3

// burstPace is the slowest a paste comes on average, key to key. A console
// read hands over a paste's keys microseconds apart, many at a time, so even
// a long one split across reads averages well under it. Fast typing with key
// rollover can land three keys inside burstGap of each other, but each a few
// milliseconds apart at least, so it reads as keys: "ok" and Enter typed in a
// rush still sends the message instead of adding a line.
const burstPace = 3 * time.Millisecond

// burstClock is when a key arrives; tests set it.
var burstClock = time.Now

type burstMsg int // the burst it closes, by number

// burstable keys can be part of a paste: what a pasted character turns into.
func burstable(k tea.KeyMsg) bool {
	if k.Paste || k.Alt {
		return false
	}
	switch k.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyEnter, tea.KeyTab, tea.KeyEscape:
		return true
	}
	return false
}

// burstKey holds a key for the burst it may start or extend, and flushes
// the burst first when a key that can't be pasted arrives.
func (m *model) burstKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !burstable(k) {
		flush := m.flushBurst()
		_, cmd := m.key(k)
		return m, tea.Batch(flush, cmd)
	}
	now := burstClock()
	if len(m.burst) == 0 {
		m.burstT0 = now
	}
	m.burstT1 = now
	m.burst = append(m.burst, k)
	m.burstN++
	n := m.burstN
	return m, tea.Tick(burstGap, func(time.Time) tea.Msg { return burstMsg(n) })
}

// flushBurst hands the held keys on: as one paste when there are enough of
// them, they came faster than fingers, and they aren't one key repeating (a
// held key arrives as a burst too), otherwise one by one.
func (m *model) flushBurst() tea.Cmd {
	keys := m.burst
	m.burst = nil
	if len(keys) == 0 {
		return nil
	}
	fast := len(keys) > 1 && m.burstT1.Sub(m.burstT0) < burstPace*time.Duration(len(keys)-1)
	if text, ok := burstText(keys); ok && fast {
		_, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true})
		return cmd
	}
	var cmds []tea.Cmd
	for _, k := range keys {
		_, cmd := m.key(k)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// burstText is the text a burst of keys pasted, if it reads as a paste.
// Bracketed-paste marks that reached the console as keys are dropped.
func burstText(keys []tea.KeyMsg) (string, bool) {
	if len(keys) < burstMin {
		return "", false
	}
	var b strings.Builder
	same := true
	for _, k := range keys {
		if k.Type != keys[0].Type || string(k.Runes) != string(keys[0].Runes) {
			same = false
		}
		switch k.Type {
		case tea.KeyEnter:
			b.WriteByte('\n')
		case tea.KeyTab:
			b.WriteByte('\t')
		case tea.KeySpace:
			b.WriteByte(' ')
		case tea.KeyEscape:
			b.WriteByte('\x1b')
		default:
			b.WriteString(string(k.Runes))
		}
	}
	if same {
		return "", false
	}
	s := strings.NewReplacer("\x1b[200~", "", "\x1b[201~", "").Replace(b.String())
	if strings.ContainsRune(s, '\x1b') {
		return "", false // escape typed among keys: keys, not text
	}
	return s, true
}
