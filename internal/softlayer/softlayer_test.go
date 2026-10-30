package softlayer

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

const essay = "# The Hive\n\nThe essay's text.\n"

// TestEveryArmBuilds: each arm has every role's prompt, and the arms differ
// the way their names say, so a reworded standard prompt can't leave the quiet
// arm talking or the essay arm carrying procedure.
func TestEveryArmBuilds(t *testing.T) {
	std, err := Standard()
	if err != nil {
		t.Fatal(err)
	}
	built := map[string]map[string]string{}
	for _, v := range Variants {
		files, err := v.Build(essay)
		if err != nil {
			t.Fatal(err)
		}
		built[v.Name] = files
	}
	if built["standard"]["worker"] != std["worker"] {
		t.Error("the standard arm changed the worker prompt")
	}
	if !strings.Contains(built["chatty"]["orientation"], "## Talk, a lot") {
		t.Error("the chatty arm doesn't ask for talk")
	}
	for _, r := range []string{"orientation", "worker"} {
		if strings.Contains(built["quiet"][r], "message them directly") || strings.Contains(built["quiet"][r], "message that run directly") {
			t.Errorf("the quiet arm's %s still asks to talk", r)
		}
	}
	if built["essay"]["orientation"] != essay || built["essay"]["worker"] != "You are a worker.\n" {
		t.Errorf("the essay arm: %q / %q", built["essay"]["orientation"], built["essay"]["worker"])
	}
	tools := built["essay-tools"]["orientation"]
	if !strings.HasPrefix(tools, essay) || !strings.Contains(tools, "- `hive agent finish") || strings.Contains(tools, "Talk to each other") {
		t.Errorf("the essay-tools arm should be the essay and the bare command list:\n%s", tools)
	}
	for _, v := range Variants {
		if want := strings.HasPrefix(v.Name, "essay"); v.NeedsEssay() != want {
			t.Errorf("%s: NeedsEssay %v, want %v", v.Name, !want, want)
		}
	}
	for _, name := range []string{"essay", "essay-tools", "philosophy", "vague"} {
		for _, r := range Roles[1:] {
			if n := len(strings.Fields(built[name][r])); n > 8 {
				t.Errorf("%s's %s prompt is %d words, not one line", name, r, n)
			}
		}
	}
	if strings.Contains(tools, "## Your reach") {
		t.Errorf("the command list carries the next section's heading:\n%s", tools)
	}
	o := built[OpenArm]
	for _, want := range []string{"--to room", "Nobody assigns you work", "## Your reach", "## Where things are", "- `hive agent warden"} {
		if !strings.Contains(o["orientation"], want) {
			t.Errorf("the open arm's orientation has no %q", want)
		}
	}
	for _, r := range []string{"reviewer", "warden", "tribunal", "judge"} {
		if o[r] != std[r] {
			t.Errorf("the open arm changed the %s prompt: the safety layer keeps its own", r)
		}
	}
	if o["worker"] == std["worker"] || o["lead"] == std["lead"] {
		t.Error("the open arm kept the standard worker or lead procedure")
	}
	if a, _ := OpenBrief(1, 3); a == func() string { b, _ := OpenBrief(2, 3); return b }() {
		t.Error("the open briefs share a title, so they'd be filed once")
	}
	if _, err := Pick("standard,nope"); err == nil {
		t.Error("an unknown arm was accepted")
	}
}

// TestInstallSurvivesTheDefaults: an arm written into a workspace is what the
// agents' system prompt is built from, and the daemon's install of the default
// modules leaves it alone.
func TestInstallSurvivesTheDefaults(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(ws.Hive(), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	r := modules.New(ws, j)
	if err := r.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	v, _ := Find("vague")
	if err := v.Install(ws.Modules(), essay); err != nil {
		t.Fatal(err)
	}
	if err := r.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	r.Update()
	sys := (&agents.Agents{Mod: r}).System("warden", "p")
	if !strings.HasPrefix(sys, "You are an agent in a team") || !strings.Contains(sys, "You are a warden.") || strings.Contains(sys, "Your role: warden") {
		t.Fatalf("the warden's system prompt isn't the vague arm's:\n%s", sys)
	}
}

// TestObserve: messages are counted by role, peer talk is worker to worker
// (not a sweep's to a worker), a task is filed once however often it is saved, and the
// window holds what it should.
func TestObserve(t *testing.T) {
	if chat.RoomMark != "(to the room) " {
		t.Fatalf("the journal below marks room posts as the chat module did: %q", chat.RoomMark)
	}
	journal := strings.Join([]string{
		`{"at":"2026-10-25T01:00:00Z","kind":"run.created","data":{"id":"w-1","role":"worker"}}`,
		`{"at":"2026-10-25T01:00:00Z","kind":"task.saved","data":{"id":"t-old","from":"w-1","kind":"work"}}`,
		`{"at":"2026-10-25T02:00:00Z","kind":"run.created","data":{"id":"w-2","role":"worker"}}`,
		`{"at":"2026-10-25T02:00:01Z","kind":"run.created","data":{"id":"lead-lead-x","role":"lead"}}`,
		`{"at":"2026-10-25T02:01:00Z","kind":"msg.sent","data":{"from":"w-1","to":"w-2","text":"hi"}}`,
		`{"at":"2026-10-25T02:02:00Z","kind":"msg.sent","data":{"from":"w-2","to":"lead","text":"hi"}}`,
		`{"at":"2026-10-25T02:03:00Z","kind":"msg.sent","data":{"from":"lead-lead-x","to":"w-1","text":"hi"}}`,
		`{"at":"2026-10-25T02:03:00Z","kind":"msg.sent","data":{"from":"w-1","to":"lead-lead-x","text":"(to the room) mine: the gate"}}`,
		`{"at":"2026-10-25T02:03:00Z","kind":"msg.sent","data":{"from":"w-1","to":"w-2","text":"(to the room) mine: the gate"}}`,
		`{"at":"2026-10-25T02:03:01Z","kind":"run.created","data":{"id":"s-1","role":"sweep"}}`,
		`{"at":"2026-10-25T02:03:02Z","kind":"msg.sent","data":{"from":"s-1","to":"w-1","text":"hi"}}`,
		`{"at":"2026-10-25T02:04:00Z","kind":"task.saved","data":{"id":"t-old","from":"w-1","kind":"work"}}`,
		`{"at":"2026-10-25T02:04:00Z","kind":"task.saved","data":{"id":"t-new","from":"w-2","kind":"work"}}`,
		`{"at":"2026-10-25T02:04:01Z","kind":"task.saved","data":{"id":"t-new","from":"w-2","kind":"work"}}`,
		`{"at":"2026-10-25T02:04:02Z","kind":"task.saved","data":{"id":"t-h","from":"w-2","kind":"helper"}}`,
		`{"at":"2026-10-25T02:04:03Z","kind":"task.saved","data":{"id":"t-p","from":"person","kind":"work"}}`,
		`{"at":"2026-10-25T02:05:00Z","kind":"risk.saved","data":{"id":"q","kind":"request"}}`,
		`{"at":"2026-10-25T02:05:01Z","kind":"risk.saved","data":{"id":"q","kind":"request"}}`,
		`{"at":"2026-10-25T02:06:00Z","kind":"run.finish","data":{"id":"w-2","finish":{"status":"done"}}}`,
		`{"at":"2026-10-25T02:07:00Z","kind":"effect.merged","data":{"project":"hive","pr":5}}`,
		`{"at":"2026-10-25T02:08:00Z","kind":"task.saved","data":{"id":"t-r","project":"hive","from":"improve","kind":"revert","pr":{"number":7}}}`,
		`{"at":"2026-10-25T02:09:00Z","kind":"effect.merged","data":{"project":"hive","pr":7}}`,
		`not json`,
		`{"at":"2026-10-25T03:00:00Z","kind":"msg.sent","data":{"from":"w-1","to":"w-2","text":"late"}}`,
	}, "\n")
	since, until := time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	rep, err := Observe(strings.NewReader(journal), since, until)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Messages != 6 || rep.Peer != 2 || rep.Room != 1 || rep.Pairs["sweep→worker"] != 1 || rep.Pairs["worker→worker"] != 2 || rep.Pairs["worker→lead"] != 2 || rep.Pairs["lead→worker"] != 1 {
		t.Errorf("messages %d, peer %d, room %d, pairs %v", rep.Messages, rep.Peer, rep.Room, rep.Pairs)
	}
	if rep.Briefs != 1 || rep.Helpers != 1 || rep.Asks != 1 {
		t.Errorf("briefs %d, helpers %d, asks %d: want 1 each", rep.Briefs, rep.Helpers, rep.Asks)
	}
	if rep.Runs["worker"] != 1 || rep.Runs["lead"] != 1 || rep.Runs["sweep"] != 1 || rep.Finished["done"] != 1 || rep.Merged != 1 || rep.Reverted != 1 {
		t.Errorf("runs %v, finished %v, merged %d, reverted %d", rep.Runs, rep.Finished, rep.Merged, rep.Reverted)
	}
	rep.Variant = "vague"
	if tab := Table([]Report{rep}); !strings.Contains(tab, "| vague |") || !strings.Contains(tab, "worker→worker 2") || !strings.HasSuffix(strings.Split(tab, "\n")[2], "| - | - |") {
		t.Errorf("table:\n%s", tab)
	}
	rep.PeakMB, rep.PeakLoad = 2900, 6.25
	if tab := Table([]Report{rep}); !strings.Contains(tab, "| 2900 | 6.2 |") {
		t.Errorf("an arm's peaks aren't in the table:\n%s", tab)
	}
}
