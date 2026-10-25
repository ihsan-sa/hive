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
	// A relative root, so the pane's path fits: macOS's temp folders are too long for it.
	t.Chdir(t.TempDir())
	m.ws = core.Workspace{Root: "."}
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

// TestKnowledgeRecentlyUpdated: the top of the Knowledge tab lists the
// newest and latest-changed entries from the knowledge base's history,
// each with its title, when, and new or updated, and one opens with enter.
func TestKnowledgeRecentlyUpdated(t *testing.T) {
	m := testModel(120, 30)
	t.Chdir(t.TempDir())
	m.ws = core.Workspace{Root: "."}
	dir := m.ws.KB()
	os.MkdirAll(filepath.Join(dir, "practice"), 0o755)
	git := func(args ...string) {
		t.Helper()
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(hm, name, text string) {
		t.Helper()
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o644)
		git("add", "-A")
		when := "2026-10-24T" + hm + ":00Z"
		t.Setenv("GIT_COMMITTER_DATE", when)
		t.Setenv("GIT_AUTHOR_DATE", when)
		git("commit", "-q", "-m", "note")
	}
	git("init", "-q", "-b", "main")
	commit("09:00", "practice/risk.md", "# Risk\n")
	commit("10:00", "zebra.md", "---\ntitle: The zebra parser splits on tabs\n---\n\nbody\n")
	commit("11:00", "practice/risk.md", "# Risk\n\nmore\n")

	m.pick(sideRow{sec: secKB})
	m.focus = 1
	v := ansi.Strip(m.View())
	head := strings.Index(v, "Recently updated")
	risk, zebra, folders := strings.Index(v, "Risk"), strings.Index(v, "The zebra parser"), strings.Index(v, "practice/ ")
	if head < 0 || risk < head || zebra < risk || folders < zebra {
		t.Fatalf("want Recently updated, then Risk, then the zebra note, then the folders:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "The zebra parser") && !strings.Contains(l, " new ") ||
			strings.Contains(l, "Risk ") && !strings.Contains(l, "updated") {
			t.Fatalf("new or updated is wrong on %q", l)
		}
	}
	checkWidth(t, m.View(), 120, 30, "knowledge recent")
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.stack) != 1 || m.stack[0].id != "zebra.md" || !strings.Contains(m.stack[0].text, "zebra parser") {
		t.Fatalf("enter on a recent entry didn't open it: %+v", m.stack)
	}
}

// The newest dream digest, what the Hive learned since last time, is pinned
// at the top of the Knowledge tab and opens on enter.
func TestKnowledgeDigestPinned(t *testing.T) {
	m := testModel(120, 30)
	t.Chdir(t.TempDir())
	m.ws = core.Workspace{Root: "."}
	dir := filepath.Join(m.ws.KB(), "dreams")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "2026-10-25-0000.md"), []byte("---\ntitle: What the Hive learned since an old pass\n---\n\nold\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "2026-10-25-0400.md"), []byte("---\ntitle: What the Hive learned since Sat Oct 25 00:00 UTC\n---\n\nMerged 3 notes.\n"), 0o644)
	m.pick(sideRow{sec: secKB})
	m.focus = 1
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Since last time") || !strings.Contains(v, "What the Hive learned since Sat Oct 25") || strings.Contains(v, "an old pass") {
		t.Fatalf("the latest digest isn't pinned:\n%s", v)
	}
	checkWidth(t, m.View(), 120, 30, "knowledge digest")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.stack) != 1 || !strings.Contains(m.stack[0].text, "Merged 3 notes.") {
		t.Fatalf("enter didn't open the digest: %+v", m.stack)
	}
}

// TestKnowledgeRemote: over ssh the knowledge base is on the Hive's machine,
// so the pane says so and its keys don't list or search the empty folder here.
func TestKnowledgeRemote(t *testing.T) {
	m := testModel(120, 30)
	t.Chdir(t.TempDir())
	m.ws = core.Workspace{Root: "."}
	m.remote = true
	m.pick(sideRow{sec: secKB})
	m.focus = 1
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if m.editing {
		t.Fatal("/ opened a search of the empty folder here")
	}
	v := ansi.Strip(m.View())
	for _, bad := range []string{m.ws.KB(), "/ search", "enter open"} {
		if strings.Contains(v, bad) {
			t.Fatalf("remote Knowledge shows %q:\n%s", bad, v)
		}
	}
	if !strings.Contains(v, "over ssh") {
		t.Fatalf("remote Knowledge doesn't say why:\n%s", v)
	}
}
