package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

func sample() api.State {
	now := time.Now()
	long := strings.Repeat("a very long line that would run off any screen ", 10)
	five, four := 5.0, 4.0
	return api.State{
		Version: "test", Workspace: "/home/x/hive", Sandbox: "wsl:Ubuntu",
		Projects: []api.ProjectView{{
			Project: core.Project{Name: "textkit", Repo: "me/textkit"},
			Goals:   "# Goals\n\n- " + long,
			Tasks: []work.Task{
				{ID: "t1", Title: "implement " + long, Stage: work.StageWorking, Worker: "w-t1", Updated: now, PR: core.PR{Number: 3, URL: "https://github.com/me/textkit/pull/3"}},
				{ID: "t2", Title: "done thing", Stage: work.StageDone, Updated: now},
			},
			Scores: []improve.Measurement{{SHA: "abcdef123456", Score: &four, Kept: true, At: now}, {SHA: "bcdef1234567", Score: &five, Kept: true, At: now, Note: long}},
			Best:   &five,
		}},
		Runs:    []core.Run{{RunSpec: core.RunSpec{ID: "w-t1", Role: "worker", Project: "textkit", Task: "t1", Brief: long}, Status: core.StatusRunning, Progress: long, Updated: now}},
		Plans:   []core.Plan{{Harness: "claude", Limit: harness.RateLimit{Status: "allowed_warning", Utilization: 0.89, Window: "seven_day", Windows: map[string]harness.Window{"five_hour": {Utilization: 0.38}}}}},
		Asks:    []core.Ask{{ID: "a1", Title: "Merge? " + long, Detail: long, Options: []string{"merge", "leave open"}, Asked: now}},
		Alerts:  []api.Alert{{At: now, Kind: "quiet", Text: long}},
		Modules: []api.ModuleView{{Name: "agents", Description: long, Source: "builtin", Release: "test", Projects: []string{"*"}}},
		Chats: []api.ChatView{
			{Chat: chat.Chat{ID: "lead", Title: "lead", Kind: "lead"}, Unread: 2, LeadStatus: "idle"},
			{Chat: chat.Chat{ID: "project:textkit", Title: "textkit", Kind: "project", Project: "textkit"}},
		},
		Config: core.DefaultConfig(),
	}
}

func testModel(w, h int) *model {
	m := &model{scroll: -1, open: map[string]bool{}, closed: map[string]bool{}, msgs: map[string][]chat.Message{},
		runs: map[string]api.RunDetail{}, tasks: map[string]work.Task{}, input: textinput.New()}
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(stateMsg{st: sample()})
	now := time.Now()
	long := strings.Repeat("words that wrap ", 40)
	m.msgs["lead"] = []chat.Message{
		{ID: "0", Thread: "0", From: "person", Name: "you", Text: "earlier " + long, At: now},
		{ID: "1", Thread: "1", From: "person", Name: "you", Text: "please do " + long, At: now},
		{ID: "2", Thread: "1", From: "lead-x", Name: "lead", Text: "Understood. " + long, At: now},
		{ID: "3", Thread: "1", From: "hive", Name: "hive", Text: "Merged: https://github.com/me/textkit/pull/3", At: now},
	}
	return m
}

func checkWidth(t *testing.T, view string, w, h int, where string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Fatalf("%s: %d lines on a %d-line screen", where, len(lines), h)
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Fatalf("%s: line %d is %d wide on a %d-wide screen: %q", where, i, ansi.StringWidth(l), w, l)
		}
	}
}

func TestViewsFitTheScreen(t *testing.T) {
	keys := func(s ...string) []tea.KeyMsg {
		var out []tea.KeyMsg
		for _, k := range s {
			switch k {
			case "tab", "enter", "esc", "down", "up", "right", "left":
				out = append(out, tea.KeyMsg(map[string]tea.Key{
					"tab": {Type: tea.KeyTab}, "enter": {Type: tea.KeyEnter}, "esc": {Type: tea.KeyEsc},
					"down": {Type: tea.KeyDown}, "up": {Type: tea.KeyUp}, "right": {Type: tea.KeyRight}, "left": {Type: tea.KeyLeft},
				}[k]))
			default:
				out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
			}
		}
		return out
	}
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 16}} {
		w, h := size[0], size[1]
		m := testModel(w, h)
		checkWidth(t, m.View(), w, h, "chats")
		for _, k := range keys("right", "enter") {
			m.Update(k)
		}
		checkWidth(t, m.View(), w, h, "chat threads")
		m.Update(keys("esc")[0])
		for i, name := range []string{"dashboard", "waiting", "modules", "settings"} {
			m.Update(keys("tab")[0])
			checkWidth(t, m.View(), w, h, name)
			if i == 0 { // walk into the dashboard tree
				for _, k := range keys("down", "down", "right", "down", "right") {
					m.Update(k)
					checkWidth(t, m.View(), w, h, "dashboard nav")
				}
			}
		}
		m.Update(keys("?")[0])
		checkWidth(t, m.View(), w, h, "help")
	}
}

func TestPreviewDropsMarkers(t *testing.T) {
	if got := preview("**Done** with `code`\n- and [a link](https://x.io)"); got != "Done with code • and a link (https://x.io)" {
		t.Errorf("preview = %q", got)
	}
}

// A fence closes only on a bare run of backticks at least as long as the one
// that opened it, so a shorter run inside is literal code.
func TestLongFenceHoldsShorterRuns(t *testing.T) {
	src := "````\n```\n**not bold**\n```\n````\nafter **bold**"
	got := ansi.Strip(strings.Join(markdown(src, 80), "\n"))
	if want := "```\n**not bold**\n```\nafter bold"; got != want {
		t.Errorf("markdown = %q, want %q", got, want)
	}
}

func TestCodeInsideBoldOrItalic(t *testing.T) {
	for src, want := range map[string]string{
		"**run `hive update` now**": "run hive update now",
		"see *the `kb` module*":     "see the kb module",
		"**`x`**":                   "x",
	} {
		if got := ansi.Strip(inline(src)); got != want {
			t.Errorf("inline(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestMarkdown(t *testing.T) {
	src := "Use **bold** and `code` and [hive](https://x.io).\n- one item\n- two\n```\nfunc main() {}\n```"
	got := strings.Join(markdown(src, 60), "\n")
	plain := ansi.Strip(got)
	for _, bad := range []string{"**", "`", "](", "```"} {
		if strings.Contains(plain, bad) {
			t.Errorf("raw %q left in %q", bad, plain)
		}
	}
	for _, want := range []string{"Use bold and code and hive (https://x.io).", "• one item", "• two", "func main() {}"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in %q", want, plain)
		}
	}
	for _, l := range markdown(strings.Repeat("word ", 30)+"**bold words here**", 30) {
		if ansi.StringWidth(l) > 30 {
			t.Errorf("line too wide: %q", l)
		}
	}
}

func TestThreadPaneScrolls(t *testing.T) {
	m := testModel(80, 20)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	h := m.paneH()
	if lines, _ := m.threadLines(m.rightW()); len(lines) <= 2*h {
		t.Fatalf("fixture too short: %d lines for a %d-row pane", len(lines), h)
	}
	last := "pull/3"
	if strings.Contains(m.View(), last) {
		t.Fatal("last line visible before scrolling")
	}
	for i := 0; i < 200 && !strings.Contains(m.View(), last); i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if !strings.Contains(m.View(), last) {
		t.Fatal("down never reached the last line")
	}
	for i := 0; i < 200 && strings.Contains(m.View(), last); i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if strings.Contains(m.View(), last) {
		t.Fatal("up never left the last line")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(m.View(), last) {
		t.Fatal("End did not reach the last line")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if strings.Contains(m.View(), last) {
		t.Fatal("PgUp did not leave the last line")
	}
	top := m.scroll
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.scroll-top != h-1 {
		t.Fatalf("PgDn moved %d, want %d", m.scroll-top, h-1)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if m.scroll != 0 || !strings.Contains(m.viewThreads(m.rightW())[0], "idle") {
		t.Fatal("Home did not put the chat's title on the first pane row")
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionRelease})
	if m.scroll != 3 {
		t.Fatalf("wheel down: scroll %d, want 3", m.scroll)
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if m.scroll != 0 {
		t.Fatalf("wheel up: scroll %d, want 0", m.scroll)
	}
	if _, spans := m.threadLines(m.rightW()); spans[m.threadSel][0] < h {
		t.Fatal("fixture: Home left the selected thread on screen")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.scroll != -1 {
		t.Fatalf("down after Home: scroll %d, want -1 (back to the selection)", m.scroll)
	}
}

func TestThreadPaneUpMirrorsDown(t *testing.T) {
	m := testModel(80, 20)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	lines, spans := m.threadLines(m.rightW())
	prev := m.top(len(lines), spans)
	if prev < 2 {
		t.Fatalf("fixture: view starts at line %d, too near the top to tell a jump", prev)
	}
	for i := 0; i < 200 && (m.threadSel > 0 || prev > 0); i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
		lines, spans = m.threadLines(m.rightW())
		top := m.top(len(lines), spans)
		if top > prev || prev-top > 1 {
			t.Fatalf("up moved the view from line %d to %d", prev, top)
		}
		prev = top
	}
	if m.threadSel != 0 || prev != 0 {
		t.Fatalf("up stopped at thread %d, line %d", m.threadSel, prev)
	}
}

func TestThreadPanePinsTitleAtEnd(t *testing.T) {
	m := testModel(80, 20) // the list has focus and the pane follows the end
	pane := m.viewThreads(m.rightW())
	if len(pane) != m.paneH() || !strings.Contains(pane[0], "idle") || !strings.Contains(m.View(), "pull/3") {
		t.Fatalf("want the title on top and the last message below it, got %q", pane)
	}
}

func TestWheelScrollsDetail(t *testing.T) {
	m := testModel(80, 20)
	m.stack = []detail{{kind: "run"}}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionRelease})
	if m.stack[0].scroll != 3 {
		t.Fatalf("wheel down: scroll %d, want 3", m.stack[0].scroll)
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if m.stack[0].scroll != 0 {
		t.Fatalf("wheel up: scroll %d, want 0", m.stack[0].scroll)
	}
}
