package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

func TestSharingLevels(t *testing.T) {
	for _, tc := range []struct {
		level          string
		iso, priv, hid bool
		name           string
	}{{"", false, false, false, "shared"}, {"shared", false, false, false, "shared"},
		{"private", false, true, true, "private"}, {"isolated", true, false, true, "isolated"}} {
		p := Project{Isolated: true, Private: true}
		if err := p.SetSharing(tc.level); err != nil {
			t.Fatal(err)
		}
		if p.Isolated != tc.iso || p.Private != tc.priv || p.Hidden() != tc.hid || p.Sharing() != tc.name {
			t.Errorf("%q: %+v hidden %v sharing %s", tc.level, p, p.Hidden(), p.Sharing())
		}
	}
	if err := (&Project{}).SetSharing("public"); err == nil {
		t.Error("an unknown level was taken")
	}
	if (Project{Isolated: true, Private: true}).Sharing() != "isolated" {
		t.Error("isolated doesn't win over private")
	}
}

// A private project is covered from every other run as an isolated one is,
// while its own runs read the rest of the shared Hive: other projects, their
// runs and the journal, but not another hidden project's records.
func TestPrivateProjectSeesOutButIsNotSeen(t *testing.T) {
	c, _ := testHive(t)
	for _, p := range []Project{{Name: "open", Repo: "me/open"}, {Name: "mine", Repo: "me/mine", Private: true}, {Name: "secret", Repo: "me/secret", Isolated: true}} {
		if _, err := c.Projects.Add(p); err != nil {
			t.Fatal(err)
		}
		os.MkdirAll(c.WS.Project(p.Name), 0o755)
		id := p.Name + "-run"
		c.Runs.mu.Lock()
		c.Runs.runs[id] = &Run{RunSpec: RunSpec{ID: id, Project: p.Name, Isolated: p.Isolated}}
		c.Runs.mu.Unlock()
		os.MkdirAll(c.WS.Run(id), 0o755)
		os.WriteFile(filepath.Join(c.WS.Chats(), "project_"+p.Name+".jsonl"), nil, 0o644)
	}
	h, _ := harness.Get("fake")
	spec := func(r Run) []string {
		s, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
		if err != nil {
			t.Fatal(err)
		}
		var env []string
		for _, p := range []string{"/hive/projects/open", "/hive/projects/mine", "/hive/projects/secret",
			"/hive/state/runs/open-run/box/notes.md", "/hive/state/runs/mine-run/box/notes.md", "/hive/state/runs/secret-run/box/notes.md",
			"/hive/state/chats/project_mine.jsonl", "/hive/state/leads/project-mine/notes.md", "/hive/state/leads/project-open/notes.md", "/hive/kb/isolated/mine/n.md", "/hive/kb/isolated/secret/n.md", "/hive/state/events.jsonl"} {
			if readable(s, p) {
				env = append(env, p)
			}
		}
		return env
	}
	open := spec(Run{RunSpec: RunSpec{ID: "open-run", Project: "open", Harness: "fake"}})
	for _, p := range open {
		if p != "/hive/projects/open" && p != "/hive/state/runs/open-run/box/notes.md" && p != "/hive/state/leads/project-open/notes.md" {
			t.Errorf("a shared project's run reads %s", p)
		}
	}
	want := map[string]bool{"/hive/projects/open": true, "/hive/projects/mine": true, "/hive/state/runs/open-run/box/notes.md": true,
		"/hive/state/runs/mine-run/box/notes.md": true, "/hive/state/chats/project_mine.jsonl": true, "/hive/kb/isolated/mine/n.md": true,
		"/hive/state/leads/project-mine/notes.md": true, "/hive/state/leads/project-open/notes.md": true}
	got := map[string]bool{}
	for _, p := range spec(Run{RunSpec: RunSpec{ID: "mine-run", Project: "mine", Harness: "fake"}}) {
		got[p] = true
		if !want[p] {
			t.Errorf("a private project's run reads %s", p)
		}
	}
	for p := range want {
		if !got[p] {
			t.Errorf("a private project's run can't read %s", p)
		}
	}
}

// No other project reaches a private one, and it reaches no other: what it
// reads stays in it.
func TestPrivateProjectsAreOutOfReach(t *testing.T) {
	c, _ := testHive(t)
	for _, p := range []Project{{Name: "open", Repo: "me/open"}, {Name: "mine", Repo: "me/mine", Private: true}} {
		c.Projects.Add(p)
		id := p.Name + "-run"
		c.Runs.mu.Lock()
		c.Runs.runs[id] = &Run{RunSpec: RunSpec{ID: id, Project: p.Name}}
		c.Runs.mu.Unlock()
	}
	o := c.Runs.Ops()
	o.SetSpawner(func(Run, string, string, string, string) (RunSpec, error) {
		return RunSpec{}, errors.New("spawner reached")
	})
	open, _ := c.Runs.Get("open-run")
	mine, _ := c.Runs.Get("mine-run")
	sweep := Run{RunSpec: RunSpec{ID: "sweep", Role: "improver"}}
	for _, tc := range []struct {
		from    Run
		project string
		ok      bool
	}{{sweep, "mine", false}, {open, "mine", false}, {mine, "open", false}, {mine, "", true}, {open, "open", true}} {
		_, err := o.Call(tc.from, "spawn", json.RawMessage(`{"brief":"x","project":"`+tc.project+`"}`))
		if reached := err != nil && err.Error() == "spawner reached"; reached != tc.ok {
			t.Errorf("%s spawning into %q: %v", tc.from.ID, tc.project, err)
		}
	}
	if _, err := o.Call(mine, "message", json.RawMessage(`{"to":"open-run","text":"x"}`)); err == nil {
		t.Error("a private project's agent messaged another project's agent")
	}
	if _, err := o.Call(open, "message", json.RawMessage(`{"to":"mine-run","text":"x"}`)); err == nil {
		t.Error("an agent messaged a private project's agent")
	}
}
