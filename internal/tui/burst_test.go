package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keys types s as a Windows console delivers a paste: one key per
// character, Enter for a line break.
func keys(m *model, s string) {
	for _, r := range s {
		switch r {
		case '\n':
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		case ' ':
			m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		default:
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

// settle ends the burst as burstGap's quiet would.
func settle(m *model) { m.Update(burstMsg(m.burstN)) }

// TestWindowsPasteAsKeys: on a Windows console a paste arrives as keys, and
// reads as the one paste a Unix terminal sends: text with its line breaks in
// the box (not a message sent at the first break), a dropped file's path as
// an attachment, and a paste outside the box opens it.
func TestWindowsPasteAsKeys(t *testing.T) {
	m := testModel(100, 30)
	m.keyBurst = true
	m.section = secChats
	m.focusBox("")
	keys(m, "first line\nsecond line")
	if m.input.Value() != "" {
		t.Fatalf("keys showed before the burst ended: %q", m.input.Value())
	}
	settle(m)
	if got := m.input.Value(); got != "first line\nsecond line" || m.sent != 0 {
		t.Fatalf("pasted text: %q, sent %d", got, m.sent)
	}

	m.input.SetValue("")
	f := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(f, []byte("\x89PNG..."), 0o644)
	keys(m, `"`+f+`"`)
	settle(m)
	if m.input.Value() != "" || len(m.files) != 1 || m.files[0].Name != "shot.png" {
		t.Fatalf("dropped path: input %q files %+v", m.input.Value(), m.files)
	}

	// Outside the box, a paste starts a message instead of typing shortcuts.
	o := testModel(100, 30)
	o.keyBurst, o.focus = true, 0
	keys(o, "hello there")
	settle(o)
	if !o.composing || o.input.Value() != "hello there" {
		t.Fatalf("paste outside the box: composing %v input %q", o.composing, o.input.Value())
	}

	// Bracketed-paste marks that reach the console as keys are dropped.
	if s, ok := burstText([]tea.KeyMsg{{Type: tea.KeyEscape}, {Type: tea.KeyRunes, Runes: []rune("[200~hi")}, {Type: tea.KeyEscape}, {Type: tea.KeyRunes, Runes: []rune("[201~")}}); !ok || s != "hi" {
		t.Fatalf("marks: %q %v", s, ok)
	}
}

// TestTypedKeysStayKeys: keys a person types, a held key, and Enter on its
// own act as keys once the burst window closes.
func TestTypedKeysStayKeys(t *testing.T) {
	m := testModel(100, 30)
	m.keyBurst = true
	m.section = secChats
	m.focusBox("")
	keys(m, "ok")
	settle(m)
	if m.input.Value() != "ok" {
		t.Fatalf("two typed keys: %q", m.input.Value())
	}
	keys(m, "!!!!")
	settle(m)
	if m.input.Value() != "ok!!!!" {
		t.Fatalf("a held key: %q", m.input.Value())
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.input.Value() != "ok!!!!" {
		t.Fatalf("enter was not held for the burst")
	}
	settle(m)
	if m.sent != 1 || m.input.Value() != "" {
		t.Fatalf("enter alone didn't send: sent %d input %q", m.sent, m.input.Value())
	}

	// A key that can't be pasted (an arrow) hands the held keys on first, in order.
	keys(m, "ab")
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.input.Value() != "ab" || len(m.burst) != 0 {
		t.Fatalf("arrow: input %q held %d", m.input.Value(), len(m.burst))
	}
	// A burst that closes after a later one started is ignored.
	keys(m, "c")
	m.Update(burstMsg(m.burstN - 1))
	if len(m.burst) != 1 {
		t.Fatalf("a stale tick flushed the burst")
	}
}
