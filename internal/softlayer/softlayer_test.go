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
	journal := strings.Join([]string{
		`{"at":"2026-10-25T01:00:00Z","kind":"run.created","data":{"id":"w-1","role":"worker"}}`,
		`{"at":"2026-10-25T01:00:00Z","kind":"task.saved","data":{"id":"t-old","from":"w-1","kind":"work"}}`,
		`{"at":"2026-10-25T02:00:00Z","kind":"run.created","data":{"id":"w-2","role":"worker"}}`,
		`{"at":"2026-10-25T02:00:01Z","kind":"run.created","data":{"id":"lead-lead-x","role":"lead"}}`,
		`{"at":"2026-10-25T02:01:00Z","kind":"msg.sent","data":{"from":"w-1","to":"w-2","text":"hi"}}`,
		`{"at":"2026-10-25T02:02:00Z","kind":"msg.sent","data":{"from":"w-2","to":"lead","text":"hi"}}`,
		`{"at":"2026-10-25T02:03:00Z","kind":"msg.sent","data":{"from":"lead-lead-x","to":"w-1","text":"hi"}}`,
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
		`{"at":"2026-10-25T02:07:00Z","kind":"effect.merged","data":{}}`,
		`not json`,
		`{"at":"2026-10-25T03:00:00Z","kind":"msg.sent","data":{"from":"w-1","to":"w-2","text":"late"}}`,
	}, "\n")
	since, until := time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	rep, err := Observe(strings.NewReader(journal), since, until)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Messages != 4 || rep.Peer != 1 || rep.Pairs["sweep→worker"] != 1 || rep.Pairs["worker→worker"] != 1 || rep.Pairs["worker→lead"] != 1 || rep.Pairs["lead→worker"] != 1 {
		t.Errorf("messages %d, peer %d, pairs %v", rep.Messages, rep.Peer, rep.Pairs)
	}
	if rep.Briefs != 1 || rep.Helpers != 1 || rep.Asks != 1 {
		t.Errorf("briefs %d, helpers %d, asks %d: want 1 each", rep.Briefs, rep.Helpers, rep.Asks)
	}
	if rep.Runs["worker"] != 1 || rep.Runs["lead"] != 1 || rep.Runs["sweep"] != 1 || rep.Finished["done"] != 1 || rep.Merged != 1 {
		t.Errorf("runs %v, finished %v, merged %d", rep.Runs, rep.Finished, rep.Merged)
	}
	rep.Variant = "vague"
	if tab := Table([]Report{rep}); !strings.Contains(tab, "| vague |") || !strings.Contains(tab, "worker→worker 1") || !strings.HasSuffix(strings.Split(tab, "\n")[2], "| - | - |") {
		t.Errorf("table:\n%s", tab)
	}
	rep.PeakMB, rep.PeakLoad = 2900, 6.25
	if tab := Table([]Report{rep}); !strings.Contains(tab, "| 2900 | 6.2 |") {
		t.Errorf("an arm's peaks aren't in the table:\n%s", tab)
	}
}

// TestOverlaps: worker runs alive at once make a pair, whether the Hive
// introduced them or not; a message between them makes it one that talked,
// and one back an answered one. A worker that ended before another started
// pairs with no one.
func TestOverlaps(t *testing.T) {
	journal := strings.Join([]string{
		`{"at":"2026-10-25T01:00:00Z","kind":"run.created","data":{"id":"w-a","role":"worker"}}`,
		`{"at":"2026-10-25T01:01:00Z","kind":"run.created","data":{"id":"w-b","role":"worker"}}`,
		`{"at":"2026-10-25T01:01:01Z","kind":"work.met","data":{"task":"t-b","run":"w-b","with":[{"task":"t-a","run":"w-a","files":["x.go"]}]}}`,
		`{"at":"2026-10-25T01:02:00Z","kind":"msg.sent","data":{"from":"w-b","to":"w-a","text":"I change run"}}`,
		`{"at":"2026-10-25T01:03:00Z","kind":"msg.sent","data":{"from":"w-a","to":"w-b","text":"I change execute"}}`,
		`{"at":"2026-10-25T01:03:30Z","kind":"msg.sent","data":{"from":"w-a","to":"w-b","text":"and its test"}}`,
		`{"at":"2026-10-25T01:04:00Z","kind":"run.created","data":{"id":"w-c","role":"worker"}}`,
		`{"at":"2026-10-25T01:05:00Z","kind":"msg.sent","data":{"from":"w-c","to":"w-a","text":"hi"}}`,
		`{"at":"2026-10-25T01:10:00Z","kind":"run.turn_ended","data":{"id":"w-a"}}`,
		`{"at":"2026-10-25T01:11:00Z","kind":"run.finish","data":{"id":"w-b","finish":{"status":"done"}}}`,
		`{"at":"2026-10-25T01:12:00Z","kind":"run.turn_ended","data":{"id":"w-c"}}`,
		`{"at":"2026-10-25T02:00:00Z","kind":"run.created","data":{"id":"w-d","role":"worker"}}`,
		`{"at":"2026-10-25T02:05:00Z","kind":"run.turn_ended","data":{"id":"w-d"}}`,
	}, "\n")
	rep, err := Observe(strings.NewReader(journal), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// a-b, a-c and b-c overlap; d is alone. a-b met, talked (3) and answered;
	// a-c talked (1); b-c didn't.
	if rep.Overlaps != 3 || rep.Met != 1 || rep.PairMsgs != 4 || rep.Talked != 2 || rep.Answered != 1 {
		t.Errorf("overlaps %d, met %d, pair msgs %d, talked %d, answered %d; want 3, 1, 4, 2, 1", rep.Overlaps, rep.Met, rep.PairMsgs, rep.Talked, rep.Answered)
	}
	if tab := Table([]Report{rep}); !strings.Contains(tab, "| 4 | 4 | 3 | 1 | 4 | 2 | 1 |") {
		t.Errorf("the pairs aren't in the table:\n%s", tab)
	}
	// The window keeps the pairs alive in it: from 01:30, only d.
	if rep, _ := Observe(strings.NewReader(journal), time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), time.Time{}); rep.Overlaps != 0 {
		t.Errorf("from 01:30 %d pairs overlap, want 0", rep.Overlaps)
	}
}
