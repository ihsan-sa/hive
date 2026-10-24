package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// TestKnowledgeSection: a person finds the knowledge base in the sidebar,
// sees where it lives and what its folders hold, opens a record, searches it,
// and climbs back out.
func TestKnowledgeSection(t *testing.T) {
	m := testModel(120, 30)
	m.ws = core.Workspace{Root: t.TempDir()}
	kbDir := m.ws.KB()
	os.MkdirAll(filepath.Join(kbDir, "record", "tasks"), 0o755)
	os.MkdirAll(filepath.Join(kbDir, "practice"), 0o755)
	os.MkdirAll(filepath.Join(kbDir, ".git"), 0o755)
	os.WriteFile(filepath.Join(kbDir, "record", "tasks", "t1.md"), []byte("# task t1\n\n### merged\nmerged the zebra parser\x1b[2J\n"), 0o644)
	os.WriteFile(filepath.Join(kbDir, "sandbox-notes.md"), []byte("bubblewrap needs userns\n"), 0o644)

	if !strings.Contains(strings.Join(sections, " "), "Knowledge") {
		t.Fatal("no Knowledge section in the sidebar")
	}
	m.pick(sideRow{sec: secKB})
	m.focus = 1
	v := ansi.Strip(m.View())
	for _, want := range []string{kbDir, "record/", "everything the Hive did", "practice/", "sandbox-notes.md", "/ search"} {
		if !strings.Contains(v, want) {
			t.Fatalf("Knowledge lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, ".git") {
		t.Fatalf("Knowledge lists the repository:\n%s", v)
	}
	checkWidth(t, m.View(), 120, 30, "knowledge")

	key := func(s string) {
		switch s {
		case "enter":
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		case "down":
			m.Update(tea.KeyMsg{Type: tea.KeyDown})
		default:
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
		}
	}
	key("down") // practice/, record/: folders first
	key("enter")
	key("enter")
	if m.kbDir != "record/tasks" {
		t.Fatalf("opened %q, want record/tasks", m.kbDir)
	}
	key("enter")
	if len(m.stack) != 1 || !strings.Contains(m.stack[0].text, "zebra parser") || strings.Contains(m.stack[0].text, "\x1b") {
		t.Fatalf("the record didn't open, or kept its escapes: %+v", m.stack)
	}
	key("esc")
	key("esc")
	if m.kbDir != "record" || m.focus != 1 || m.kbEntries()[m.kbSel].name != "tasks" {
		t.Fatalf("esc should climb one folder: dir %q focus %d", m.kbDir, m.focus)
	}
	key("esc")
	key("esc")
	if m.kbDir != "" || m.focus != 0 {
		t.Fatalf("esc at the top should reach the sidebar: dir %q focus %d", m.kbDir, m.focus)
	}

	m.focus = 1
	key("/")
	if !m.editing {
		t.Fatal("/ opened no search box")
	}
	m.input.SetValue("zebra")
	key("enter")
	if len(m.stack) != 1 || !strings.Contains(m.stack[0].text, "record/tasks/t1.md") {
		t.Fatalf("search didn't find the record: %+v", m.stack)
	}
}
