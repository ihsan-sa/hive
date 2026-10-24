package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

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
	m := newModel(nil, core.Workspace{})
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

func TestStackSurvivesTinyWindows(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	bx := []string{"1", "2", "3", "4", "5"}
	for h := -1; h <= 8; h++ {
		for _, sel := range [][2]int{{0, 0}, {0, 2}, {3, 9}} {
			off := 5
			stack(lines, append([]string(nil), bx...), h, &off, sel[0], sel[1], true)
		}
	}
	for h := 1; h <= 8; h++ {
		m := testModel(60, h)
		m.View()
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m.View()
	}
}

func TestSchedulesAreHours(t *testing.T) {
	for stored, shown := range map[string]string{"every 12h": "12 hours", "every 90m": "every 90m", "daily 04:00": "daily 04:00", "off": "off"} {
		if got := scheduleText(stored); got != shown {
			t.Errorf("scheduleText(%q) = %q, want %q", stored, got, shown)
		}
	}
	for typed, stored := range map[string]string{"12": "every 12h", "12 hours": "every 12h", "0": "off", "off": "off",
		scheduleText("daily 04:00"): "daily 04:00", "daily 05:30": "daily 05:30", " every  90m ": "every 90m"} {
		if got, err := scheduleHours(typed); err != nil || got != stored {
			t.Errorf("scheduleHours(%q) = %q, %v, want %q", typed, got, err, stored)
		}
	}
	for _, bad := range []string{"daily", "daily 25:00", "-3"} {
		if _, err := scheduleHours(bad); err == nil {
			t.Errorf("scheduleHours took %q", bad)
		}
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

func TestModelIsAPickList(t *testing.T) {
	if core.DefaultConfig().Model != "opus" || core.DefaultConfig().LeadModel != "opus" {
		t.Fatal("Opus is not the default model")
	}
	c := core.DefaultConfig()
	c.ModelChoices = []string{"sonnet", "my-model"}
	ch := modelChoices(c)
	if ch[0] != "opus" || ch[len(ch)-1] != custom || ch[len(ch)-2] != "my-model" || len(slices.Compact(slices.Sorted(slices.Values(ch)))) != len(ch) {
		t.Fatalf("choices %v: want opus first, the config's own added once, custom last", ch)
	}
	m := testModel(80, 24)
	m.section = secSettings
	for i, s := range m.settings() {
		if s.key == "lead_model" {
			m.setSel = i
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.picking || m.settings()[m.setSel].choices[m.pickSel] != "opus" {
		t.Fatalf("enter on the lead model should open its list at opus (picking %v, sel %d)", m.picking, m.pickSel)
	}
	if !strings.Contains(m.View(), custom) {
		t.Fatal("the open list does not show the custom entry")
	}
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyTab}} {
		if _, cmd := m.Update(k); m.picking || m.section != secSettings || cmd != nil {
			t.Fatalf("%v should close the open list and stay in Settings", k)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp}) // already at the top
	for range len(m.settings()[m.setSel].choices) {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.picking || !m.editing {
		t.Fatal("custom should open a text field")
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

// TestModuleDetailReachesEveryProject: with more projects than rows, moving
// down the list scrolls it, so the last project can be selected.
func TestModuleDetailReachesEveryProject(t *testing.T) {
	m := testModel(80, 14)
	for i := 0; i < 30; i++ {
		m.st.Projects = append(m.st.Projects, api.ProjectView{Project: core.Project{Name: fmt.Sprintf("proj%02d", i)}})
	}
	m.pick(sideRow{sec: secModules}) // detail shows in the main pane outside Chats
	m.stack = []detail{{kind: "module", id: "agents"}}
	for range m.projectNames() {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if view := m.View(); !strings.Contains(view, "proj29") {
		t.Fatalf("the last project isn't reachable:\n%s", view)
	}
	checkWidth(t, m.View(), 80, 14, "module detail")
}

// TestModuleReviewKey: in the module detail, r asks for the code to be read
// again, and the hint line says so.
func TestModuleReviewKey(t *testing.T) {
	m := testModel(160, 20)
	m.pick(sideRow{sec: secModules})
	m.stack = []detail{{kind: "module", id: "agents"}}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); cmd == nil {
		t.Fatal("r in the module detail sent nothing")
	}
	if h := ansi.Strip(m.hintLine()); !strings.Contains(h, "review again") {
		t.Fatalf("module hint line lacks r: %q", h)
	}
}

// TestWheelMovesModuleSelection: the module detail windows on its selection,
// so the wheel moves the selection rather than the unused scroll offset.
func TestWheelMovesModuleSelection(t *testing.T) {
	m := testModel(80, 20)
	m.stack = []detail{{kind: "module", id: "agents"}}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.stack[0].sel != 1 {
		t.Fatalf("wheel down: sel %d, want 1", m.stack[0].sel)
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if m.stack[0].sel != 0 {
		t.Fatalf("wheel up: sel %d, want 0", m.stack[0].sel)
	}
}

// The thread-pane tests from main, moved onto the redesigned channel pane:
// it opens on the conversation's end, under the chat's title and lead status.
func TestChannelScrolls(t *testing.T) {
	m := testModel(80, 20)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	last, first := "pull/3", "earlier"
	if v := m.View(); !strings.Contains(v, last) || strings.Contains(v, first) || !strings.Contains(v, "idle") {
		t.Fatalf("want the title and the end, with the first thread above the screen:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp}) // from the new-thread box, where the channel opens
	if strings.Contains(m.View(), last) {
		t.Fatal("PgUp did not leave the last line")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !strings.Contains(m.View(), last) {
		t.Fatal("PgDn did not come back to the last line")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // leave the box; Home and End belong to it
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if v := m.View(); !strings.Contains(v, first) || !strings.Contains(v, "idle") {
		t.Fatal("Home did not show the first thread under the title")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionRelease})
	if m.chScroll != 3 {
		t.Fatalf("wheel up: scroll %d, want 3", m.chScroll)
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.chScroll != 0 {
		t.Fatalf("wheel down: scroll %d, want 0", m.chScroll)
	}
}

func TestChannelUpKeepsSelectionInView(t *testing.T) {
	m := testModel(80, 20)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	for i := 0; i < 5 && m.threadSel != 0; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
		if !strings.Contains(m.View(), "▌") {
			t.Fatalf("up %d left the selected thread off screen", i+1)
		}
	}
	if m.threadSel != 0 {
		t.Fatalf("up stopped at thread %d", m.threadSel)
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

// A run's brief and steps wrap to the main pane, so no line loses its end.
func TestDetailWrapsToItsPane(t *testing.T) {
	words := strings.Repeat("every word of this brief stays readable ", 8)
	for _, w := range []int{80, 120} {
		m := testModel(w, 40)
		m.pick(sideRow{sec: secDash})
		m.runs["r1"] = api.RunDetail{Run: core.Run{RunSpec: core.RunSpec{ID: "r1", Role: "worker", Brief: words}},
			Steps: []api.Step{{Turn: 1, Kind: "text", Text: words}}}
		m.stack = []detail{{kind: "run", id: "r1", title: "r1"}}
		if v := plain(m.View()); strings.Count(v, "readable") != 16 || strings.Contains(v, "…") {
			t.Errorf("%d columns: detail lines are cut:\n%s", w, v)
		}
	}
}

// A new message in a chat the TUI already holds shows its text, rendered, in
// the live feed: each batch of events fetches each chat it names once.
func TestFeedRefreshesHeldChats(t *testing.T) {
	m := testModel(120, 40)
	m.pick(sideRow{sec: secDash})
	posted := func(seq int64, chat, id string) core.Event {
		data, _ := json.Marshal(map[string]string{"chat": chat, "id": id, "thread": id, "from": "lead-x"})
		return core.Event{Seq: seq, At: time.Now(), Kind: "chat.posted", Data: data}
	}
	cmd := m.addEvents([]core.Event{posted(1, "lead", "4"), posted(2, "lead", "5"), posted(3, "project:textkit", "p1")})
	if b, ok := cmd().(tea.BatchMsg); !ok || len(b) != 2 {
		t.Fatalf("want one fetch per chat, got %#v", cmd())
	}
	m.Update(chatMsg{"lead", append(m.msgs["lead"],
		chat.Message{ID: "4", Thread: "4", From: "lead-x", Text: "**First** new reply"},
		chat.Message{ID: "5", Thread: "5", From: "lead-x", Text: "Second `new` reply"})})
	v := plain(m.View())
	for _, want := range []string{"First new reply", "Second new reply"} {
		if !strings.Contains(v, want) {
			t.Errorf("feed lacks %q:\n%s", want, v)
		}
	}
	if m.addEvents([]core.Event{posted(4, "lead", "6")}) == nil {
		t.Error("a later message in a held chat fetched nothing")
	}
}

// A thread that takes over the screen, on a resize or on coming back to its
// chat, gets the keys, and Escape closes it.
func TestTakeoverTakesTheFocus(t *testing.T) {
	m := testModel(120, 40)
	for _, k := range []tea.KeyType{tea.KeyUp, tea.KeyEnter, tea.KeyTab} { // open the last thread, then go to the sidebar
		m.Update(tea.KeyMsg{Type: k})
	}
	if m.thread != "1" || m.focus != 0 {
		t.Fatalf("setup: thread %q focus %d", m.thread, m.focus)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.focus != 2 || !m.composing || m.reply != "1" {
		t.Fatalf("after the resize: focus %d, box on %q (%v)", m.focus, m.reply, m.composing)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.thread != "" || m.focus != 1 {
		t.Fatalf("esc: thread %q focus %d", m.thread, m.focus)
	}

	m = testModel(120, 40)
	m.thread, m.focus = "1", 0
	m.pick(sideRow{sec: secDash})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.pick(sideRow{sec: secChats})
	if m.focus != 2 || m.reply != "1" {
		t.Fatalf("back in the chat: focus %d, box on %q", m.focus, m.reply)
	}
}

// Message bodies render markdown in the channel and in the thread pane.
func TestMessagesRenderMarkdown(t *testing.T) {
	m := testModel(120, 40)
	m.msgs["lead"] = append(m.msgs["lead"], chat.Message{ID: "9", Thread: "9", From: "lead-x", Name: "lead", Text: "**Done**: see `parser/`\n- one item", At: time.Now()})
	m.thread, m.focus = "9", 2
	m.focusBox("9")
	v := plain(m.View())
	if strings.Count(v, "Done: see parser/") != 2 || strings.Count(v, "• one item") != 2 || strings.Contains(v, "**") {
		t.Errorf("want the message rendered in both panes:\n%s", v)
	}
}

// On a short screen a long draft keeps the whole box, border and all.
func TestShortScreenKeepsTheBox(t *testing.T) {
	for _, sz := range [][2]int{{40, 10}, {80, 12}} {
		m := testModel(sz[0], sz[1])
		m.focusBox("")
		m.input.SetValue("one\ntwo\nthree\nfour\nfive\nsix\nseven")
		if v := m.View(); !strings.Contains(v, "╭") || !strings.Contains(v, "╰") {
			t.Errorf("%dx%d: the box is cut:\n%s", sz[0], sz[1], v)
		}
	}
}

// A config.json the daemon couldn't read is named on the Settings page, on a
// screen too short to show every row, whichever row is selected.
func TestSettingsShowsTheConfigLoadError(t *testing.T) {
	m := testModel(100, 10)
	m.section = secSettings
	m.st.ConfigErr = "tribunal seat {\"model\": 5} isn't a model name"
	for _, sel := range []int{0, len(m.settings()) - 1} {
		m.setSel = sel
		if view := m.View(); !strings.Contains(view, "isn't a model name") {
			t.Fatalf("no load error on Settings with row %d selected:\n%s", sel, view)
		}
	}
}

// The cursor on a group row reverses the whole row, not just the dim arrow,
// and collapsing the open chat's group keeps its name in the header.
func TestSidebarGroupRow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := testModel(120, 40)
	m.pick(sideRow{sec: -1, chatID: "project:textkit"})
	want := m.path()
	for i, r := range m.sideRows() {
		if r.group && r.label == "textkit" {
			m.side = i
		}
	}
	m.focus = 0
	line := m.viewSidebar(30).body[m.side]
	if inner := strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[7m"), "\x1b[0m"); strings.Contains(inner, "\x1b") || ansi.Strip(line) != fit(" ▾ textkit", 30) {
		t.Fatalf("selected group row is not one reversed run: %q", line)
	}
	m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !m.closed["p:textkit"] || m.path() != want {
		t.Fatalf("collapsed %v: path %q, want %q", m.closed, m.path(), want)
	}
}

// After a self-update the daemon's state carries the new version's notes:
// they show once, any key dismisses them, and a notice already seen stays away.
func TestUpdateNoticeShowsOnce(t *testing.T) {
	m := testModel(100, 24)
	m.seen = 10
	st := sample()
	st.Updated = &api.Updated{Seq: 5, To: "v0.1.3", Notes: "old notes"}
	m.Update(stateMsg{st: st})
	if view := m.View(); strings.Contains(view, "old notes") {
		t.Fatalf("a notice seen before shows again:\n%s", view)
	}
	st.Updated = &api.Updated{Seq: 12, To: "v0.1.4", Notes: "v0.1.4: https://github.com/me/hive/releases/tag/v0.1.4\nFixed the thing"}
	m.Update(stateMsg{st: st})
	view := m.View()
	checkWidth(t, view, 100, 24, "update notice")
	if !strings.Contains(view, "Updated to v0.1.4") || !strings.Contains(view, "Fixed the thing") || !strings.Contains(view, "releases/tag/v0.1.4") {
		t.Fatalf("no update notice:\n%s", view)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m.Update(stateMsg{st: st})
	if view := m.View(); strings.Contains(view, "Fixed the thing") {
		t.Fatalf("notice back after dismissing:\n%s", view)
	}
}
