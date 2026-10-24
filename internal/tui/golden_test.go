package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/feedback"
)

// designModel holds the data the design's frames show.
func designModel(w, h int) *model {
	at := func(hm string) time.Time {
		t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-22 "+hm, time.Local)
		return t
	}
	run := func(id, role, name, project string) core.Run {
		return core.Run{RunSpec: core.RunSpec{ID: id, Role: role, Name: name, Project: project}, Status: core.StatusRunning}
	}
	st := api.State{
		Projects: []api.ProjectView{{Project: core.Project{Name: "textkit-2"}}, {Project: core.Project{Name: "hive-web"}}, {Project: core.Project{Name: "website"}}},
		Runs: []core.Run{run("tk-w", "worker", "writer", "textkit-2"), run("tk-r", "reviewer", "reviewer", "textkit-2"),
			run("hw-w", "worker", "worker-2", "hive-web"), run("hw-r", "reviewer", "reviewer", "hive-web"), run("hw-t", "tribunal", "tribunal", "hive-web"),
			run("ws-w", "worker", "worker-1", "website")},
		Asks: []core.Ask{
			{ID: "a1", Project: "hive-web", From: "lead", Kind: "permission", Asked: time.Now().Add(-12*time.Minute - time.Second),
				Title: "Warden blocked worker-2 writing to ../shared/.env, outside the workspace. Allow it once?", Options: []string{"allow once", "always", "deny"}},
			{ID: "a2", Project: "textkit-2", From: "lead", Kind: "question", Asked: time.Now().Add(-13*time.Minute - time.Second),
				Title: "Keep the old import path as an alias for one release, or break it now?", Options: []string{"alias", "break it"}},
			{ID: "a3", Project: "website", From: "lead", Kind: "budget", Asked: time.Now().Add(-7*time.Minute - time.Second),
				Title: "The OG image rebuild will use about $3.40 of tokens. Go ahead?", Options: []string{"yes", "no"}},
		},
		Modules: []api.ModuleView{
			{Name: "tribunal", Projects: []string{"*"}, Source: "builtin", Release: "0.8.2", Description: "Rules on merges that reviewers dispute"},
			{Name: "warden", Projects: []string{"*"}, Source: "builtin", Release: "0.8.2", Description: "Blocks writes outside the workspace"},
			{Name: "reviewer", Projects: []string{"*"}, Source: "builtin", Release: "0.8.2", Description: "Reviews every change before it merges"},
			{Name: "memory", Projects: []string{"textkit-2", "hive-web"}, Source: ".modules/memory", Release: "1.0", Description: "Keeps notes between sessions"},
			{Name: "scheduler", Source: ".modules/scheduler", Release: "0.3", Description: "Starts agents on a schedule"},
			{Name: "budget", Projects: []string{"*"}, Source: ".modules/budget", Release: "0.2", Description: "Asks before runs over $1"},
		},
		Feedback: []feedback.Item{
			{ID: "f1", Run: "w-261022-140210-9c1e", Role: "worker", Name: "writer", Project: "textkit-2", About: "agents/worker.md", Status: feedback.Open, At: time.Now().Add(-9*time.Minute - time.Second),
				Text: "The worker prompt says to run the score before starting, but textkit-2's score command needs a built wheel first, so the first run always fails. A line saying `make build` comes first would have saved two turns."},
			{ID: "f2", Run: "hw-r", Role: "reviewer", Project: "hive-web", Status: feedback.Open, Seen: true, At: time.Now().Add(-40*time.Minute - time.Second),
				Text: "The brief named the sha but not the base, so I read the whole branch to find what changed."},
			{ID: "f3", Run: "sweep-261022-0400", Role: "sweep", Status: feedback.Passed, Seen: true, Note: "passed to lead", At: time.Now().Add(-26 * time.Hour),
				Text: "The digest lists runs that stopped but not why the person stopped them."},
		},
		Config: core.DefaultConfig(), Version: "0.8.2", Workspace: "/home/you/hive",
		Chats: []api.ChatView{
			{Chat: chat.Chat{ID: "project:textkit-2", Title: "textkit-2", Kind: "project", Project: "textkit-2"}, LeadStatus: "working"},
			{Chat: chat.Chat{ID: "agent:tk-r", Title: "reviewer", Kind: "agent", Project: "textkit-2"}, Unread: 1},
			{Chat: chat.Chat{ID: "project:hive-web", Title: "hive-web", Kind: "project", Project: "hive-web"}, Unread: 1},
		},
	}
	m := newModel(nil, core.Workspace{})
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(stateMsg{st: st})
	m.closed["p:website"] = true
	msg := func(id, thread, from, hm, text string) chat.Message {
		name := "lead"
		if from == "person" {
			name = "you"
		}
		return chat.Message{ID: id, Chat: "project:textkit-2", Thread: thread, From: from, Name: name, At: at(hm), Text: text}
	}
	m.msgs["project:textkit-2"] = []chat.Message{
		msg("m1", "m1", "person", "19:58", "Hello?"),
		msg("m2", "m1", "lead-1", "19:58", "Hi. What do you need?"),
		msg("m3", "m3", "person", "20:22", "The parser and the renderer are tangled together. Can you split the parser into its own package so reviewer can check it on its own?"),
		msg("m4", "m3", "lead-1", "20:23", "On it. I'll hand the split to writer and ask reviewer to check the public API before anything merges."),
		msg("m5", "m3", "lead-1", "20:31", "Done. parser/ is its own package now, with 14 tests passing, and reviewer approved the API. One question: keep the old import path as an alias for a release, or break it now?"),
		msg("m6", "m6", "person", "20:40", "Also check whether the docs build still passes."),
		msg("m7", "m6", "lead-1", "20:41", "It passes. I fixed two broken links in guide/install.md on the same branch."),
	}
	m.openChat("project:textkit-2")
	return m
}

// designFeed is the dashboard feed the frames show.
func designFeed(m *model) {
	for _, r := range [][5]string{
		{"20:31:02", "lead", "you", "textkit-2", "Done. parser/ is its own package now. Keep th and more"},
		{"20:31:40", "lead", "writer", "textkit-2", "Hold the merge until you hear back on the ali and more"},
		{"20:32:15", "worker-2", "lead", "hive-web", "Login form migrated to the new field componen and more"},
		{"20:32:51", "lead", "reviewer", "hive-web", "Review PR #212: login form migration."},
		{"20:33:30", "reviewer", "lead", "hive-web", "Two snapshot changes look unrelated to the mi and more"},
		{"20:33:58", "lead", "tribunal", "hive-web", "Dispute on #212: are the snapshot changes in  and more"},
		{"20:34:40", "lead", "worker-1", "website", "Rebuild OG images after the font change."},
		{"20:35:12", "warden", "worker-2", "hive-web", "Blocked: write to ../shared/.env is outside t and more"},
		{"20:35:13", "warden", "lead", "hive-web", "worker-2 tried to write outside the workspace and more"},
		{"20:35:40", "lead", "you", "hive-web", "Warden blocked a write to ../shared/.env. All and more"},
		{"20:36:22", "tribunal", "lead", "hive-web", "Ruling on #212: in scope, approve 2–1. Dissen and more"},
		{"20:37:05", "worker-1", "lead", "website", "Estimate for OG rebuild: about $3.40 of token and more"},
		{"20:37:06", "lead", "you", "website", "The OG image rebuild will use about $3.40. Go and more"},
		{"20:38:02", "lead", "reviewer", "textkit-2", "Review the parser split on branch split-parse and more"},
		{"20:38:44", "writer", "lead", "textkit-2", "Branch split-parser pushed. 14/14 tests pass."},
		{"20:40:10", "you", "lead", "textkit-2", "Also check whether the docs build still passe and more"},
		{"20:40:31", "reviewer", "lead", "textkit-2", "API diff clean. One unused export: parser.Leg and more"},
		{"20:40:52", "lead", "writer", "textkit-2", "Run the docs build on split-parser and fix an and more"},
		{"20:41:05", "lead", "writer", "textkit-2", "Remove parser.Legacy and push."},
		{"20:41:30", "writer", "lead", "textkit-2", "Docs build passes after fixing 2 links in gui and more"},
		{"20:41:44", "lead", "you", "textkit-2", "It passes. I fixed two broken links in guide/ and more"},
		{"20:42:20", "worker-2", "lead", "hive-web", "Merged #212."},
		{"20:42:58", "writer", "lead", "textkit-2", "Removed parser.Legacy. Tests 14/14."},
		{"20:43:10", "lead", "reviewer", "hive-web", "Next: PR #213, password reset flow."},
	} {
		at, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-22 "+r[0], time.Local)
		m.feed = append(m.feed, feedRow{at: at, from: r[1], to: r[2], project: r[3], text: r[4]})
	}
	m.pick(sideRow{sec: secDash})
	m.focus = 1
}

// replyDraft opens the 20:22 thread with a reply being written.
func replyDraft(m *model) {
	m.thread, m.focus = "m3", 2
	m.focusBox("m3")
	m.input.SetValue("Keep the alias for one release, then remove it in 0.9.")
}

func newThread(m *model) {
	m.focusBox("")
	m.input.SetValue("Plan the 0.9 release: what's left, what's risky, and who's doing what. Short list, please.")
}

func longReply(m *model) {
	replyDraft(m)
	m.input.SetValue(m.input.Value() + "\n\nBefore you merge:\n- add a deprecation notice to the alias\n- note it in CHANGELOG under Breaking\n- ask reviewer to check the docs examples import from the new path")
}

func sending(m *model) {
	m.thread, m.focus = "m3", 2
	m.focusBox("m3")
	m.outbox = []outMsg{{chat: m.chat, thread: "m3", text: "Keep the alias for one release, then remove it in 0.9."}}
}

func sent(m *model) {
	m.thread, m.focus = "m3", 2
	m.focusBox("m3")
	m.Update(sentMsg{out: outMsg{chat: m.chat, thread: "m3"}, msg: chat.Message{ID: "m8", Thread: "m3", From: "person", At: m.msgs[m.chat][4].At.Add(13 * time.Minute), Text: "Keep the alias for one release, then remove it in 0.9."}})
}

// failed is the PR's deviation from 02d: the text goes back in the box and
// the hint line says why, so the whole screen is the frame.
func failed(m *model) {
	sending(m)
	m.Update(sentMsg{out: m.outbox[0], err: errors.New("connection lost")})
}

// TestDesignFrames renders each screen at a fixed size and compares the text
// with testdata, which is the design's frame plus the deviations the PR lists.
func TestDesignFrames(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
		set  func(m *model)
	}{
		{"01-thread-open-reply", 120, 36, replyDraft},
		{"03-new-thread", 120, 36, newThread},
		{"03-new-thread-80x24", 80, 24, newThread},
		{"02a-long-reply", 120, 36, longReply},
		{"02a-long-reply-80x24", 80, 24, longReply},
		{"02b-sending", 120, 18, sending},
		{"02b-sending-80x24", 80, 24, sending},
		{"02c-sent", 120, 18, sent},
		{"02c-sent-80x24", 80, 24, sent},
		{"02d-failed", 120, 18, failed},
		{"02d-failed-80x24", 80, 24, failed},
		{"04a-80x24-channel", 80, 24, func(m *model) { m.threadSel = 1 }},
		{"05a-dashboard-all", 120, 36, func(m *model) { designFeed(m); m.feedSel = 13 }},
		{"05a-dashboard-80x24", 80, 24, func(m *model) { designFeed(m); m.feedSel = 13 }},
		{"06a-waiting-80x24", 80, 24, func(m *model) { m.pick(sideRow{sec: secWaiting}) }},
		{"06b-modules-80x24", 80, 24, func(m *model) { m.pick(sideRow{sec: secModules}) }},
		{"06c-settings-80x24", 80, 24, func(m *model) { m.pick(sideRow{sec: secSettings}); m.setSel = 1 }},
		{"06a-waiting", 120, 36, func(m *model) { m.pick(sideRow{sec: secWaiting}) }},
		{"06b-modules", 120, 36, func(m *model) { m.pick(sideRow{sec: secModules}) }},
		{"06d-knowledge", 120, 36, func(m *model) { m.pick(sideRow{sec: secKB}); m.focus = 1 }},
		{"06c-settings", 120, 36, func(m *model) { m.pick(sideRow{sec: secSettings}); m.setSel = 1 }},
		{"06d-feedback-80x24", 80, 24, func(m *model) { m.pick(sideRow{sec: secFeedback}); m.focus = 1 }},
		{"06d-feedback", 120, 36, func(m *model) { m.pick(sideRow{sec: secFeedback}); m.focus = 1 }},
		{"04b-80x24-thread-takeover", 80, 24, replyDraft},
	} {
		m := designModel(c.w, c.h)
		c.set(m)
		got := plain(m.View())
		if c.name == "02b-sending" || c.name == "02c-sent" { // the frame is the thread pane alone
			got = column(got, 72, 48, 16)
		}
		if os.Getenv("HIVE_UPDATE_FRAMES") != "" { // rewrite the frames; list the change in the PR
			os.WriteFile("testdata/"+c.name+".txt", []byte(got+"\n"), 0o644)
			continue
		}
		want, err := os.ReadFile("testdata/" + c.name + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		if d := diffLines(plain(string(want)), got); d != "" {
			t.Errorf("%s differs from its frame (-want +got):\n%s", c.name, d)
		}
	}
}

// plain drops styling and trailing blanks, so the text frame alone compares.
func plain(s string) string {
	lines := strings.Split(strings.TrimRight(ansi.Strip(s), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func diffLines(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, c string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			c = g[i]
		}
		if a != c {
			fmt.Fprintf(&b, "%2d -%s\n%2d +%s\n", i, a, i, c)
		}
	}
	return b.String()
}

// column cuts w cells from x on the first h rows.
func column(s string, x, w, h int) string {
	var out []string
	for _, l := range strings.Split(s, "\n")[:h] {
		r := []rune(l + strings.Repeat(" ", x+w))
		out = append(out, strings.TrimRight(string(r[x:x+w]), " "))
	}
	return strings.Join(out, "\n")
}

// TestTinySizes renders every section, in each pane, at sizes too small for the layout.
func TestTinySizes(t *testing.T) {
	for _, sz := range [][2]int{{30, 8}, {50, 16}, {40, 10}, {41, 11}} {
		m := designModel(sz[0], sz[1])
		designFeed(m)
		for sec := secChats; sec <= secSettings; sec++ {
			m.section = sec
			for f := 0; f < 3; f++ {
				m.focus = f
				_ = m.View()
			}
		}
	}
}
