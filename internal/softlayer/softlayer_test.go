package softlayer

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
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
	for _, name := range []string{"essay", "essay-tools", "philosophy", "vague"} {
		for _, r := range Roles[1:] {
			if n := len(strings.Fields(built[name][r])); n > 8 {
				t.Errorf("%s's %s prompt is %d words, not one line", name, r, n)
			}
		}
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

// TestObserve: messages are counted by role, peer talk is between two runs
// that aren't a lead, a task is filed once however often it is saved, and the
// window holds what it should.
func TestObserve(t *testing.T) {
	journal := strings.Join([]string{
		`{"at":"2026-10-25T01:00:00Z","kind":"run.created","data":{"id":"w-1","role":"worker"}}`,
		`{"at":"2026-10-25T01:00:00Z","kind":"task.saved","data":{"id":"t-old","from":"w-1","kind":"work"}}`,
		`{"at":"2026-10-25T02:00:00Z","kind":"run.created","data":{"id":"w-2","role":"worker"}}`,
		`{"at":"2026-10-25T02:00:01Z","kind":"run.created","data":{"id":"lead-lead-x","role":"lead"}}`,
		`{"at":"2026-10-25T02:01:00Z","kind":"msg.sent","data":{"from":"w-1","to":"w-2","text":"hi"}}`,
		`{"at":"2026-10-25T02:02:00Z","kind":"msg.sent","data":{"from":"w-2","to":"lead","text":"hi"}}`,
		`{"at":"2026-10-25T02:03:00Z","kind":"msg.sent","data":{"from":"lead-lead-x","to":"w-1","text":"hi"}}`,
		`{"at":"2026-10-25T02:04:00Z","kind":"task.saved","data":{"id":"t-old","from":"w-1","kind":"work"}}`,
		`{"at":"2026-10-25T02:04:00Z","kind":"task.saved","data":{"id":"t-new","from":"w-2","kind":"work"}}`,
		`{"at":"2026-10-25T02:04:01Z","kind":"task.saved","data":{"id":"t-new","from":"w-2","kind":"work"}}`,
		`{"at":"2026-10-25T02:04:02Z","kind":"task.saved","data":{"id":"t-h","from":"w-2","kind":"helper"}}`,
		`{"at":"2026-10-25T02:04:03Z","kind":"task.saved","data":{"id":"t-p","from":"person","kind":"work"}}`,
		`{"at":"2026-10-25T02:05:00Z","kind":"risk.saved","data":{"id":"q","kind":"request"}}`,
		`{"at":"2026-10-25T02:05:01Z","kind":"risk.saved","data":{"id":"q","kind":"request"}}`,
		`{"at":"2026-10-25T02:06:00Z","kind":"run.finish","data":{"id":"w-2","finish":{"status":"done"}}}`,
		`{"at":"2026-10-25T02:07:00Z","kind":"effect.merged","data":{}}`,
		`not json`,
		`{"at":"2026-10-25T03:00:00Z","kind":"msg.sent","data":{"from":"w-1","to":"w-2","text":"late"}}`,
	}, "\n")
	since, until := time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	rep, err := Observe(strings.NewReader(journal), since, until)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Messages != 3 || rep.Peer != 1 || rep.Pairs["worker→worker"] != 1 || rep.Pairs["worker→lead"] != 1 || rep.Pairs["lead→worker"] != 1 {
		t.Errorf("messages %d, peer %d, pairs %v", rep.Messages, rep.Peer, rep.Pairs)
	}
	if rep.Briefs != 1 || rep.Helpers != 1 || rep.Asks != 1 {
		t.Errorf("briefs %d, helpers %d, asks %d: want 1 each", rep.Briefs, rep.Helpers, rep.Asks)
	}
	if rep.Runs["worker"] != 1 || rep.Runs["lead"] != 1 || rep.Finished["done"] != 1 || rep.Merged != 1 {
		t.Errorf("runs %v, finished %v, merged %d", rep.Runs, rep.Finished, rep.Merged)
	}
	rep.Variant = "vague"
	if tab := Table([]Report{rep}); !strings.Contains(tab, "| vague |") || !strings.Contains(tab, "worker→worker 1") {
		t.Errorf("table:\n%s", tab)
	}
}
