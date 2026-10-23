package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// isolatedSB is a sandbox whose inside paths differ from host paths.
type isolatedSB struct{ sandbox.Bare }

func (isolatedSB) Isolated() bool { return true }

// A mount nested in a read-only mount needs its mount point on the host,
// because bwrap can't make it inside the read-only bind.
func TestNestedMountPointsExist(t *testing.T) {
	c, _ := testHive(t)
	r := Run{ID: "r1", Role: "lead", Harness: "fake", Isolated: true, Modules: []string{"agents", "kb"}}
	h, _ := harness.Get("fake")
	spec, err := c.Runs.sandboxSpec(isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	nested := 0
	for _, m := range spec.Mounts {
		for _, ro := range spec.Mounts {
			if ro.RW || !strings.HasPrefix(m.Inside, ro.Inside+"/") {
				continue
			}
			nested++
			if _, err := os.Stat(filepath.Join(ro.Host, strings.TrimPrefix(m.Inside, ro.Inside))); err != nil {
				t.Errorf("%s has no mount point in read-only %s: %v", m.Inside, ro.Inside, err)
			}
		}
	}
	if nested != 2 {
		t.Fatalf("nested mounts: %d, want 2 (%+v)", nested, spec.Mounts)
	}
}

// Every run but an isolated project's reads the whole .hive at /hive/state,
// writes none of it, and finds the daemon's token and the stored logins
// covered; each cover has its mount point in the read-only state.
func TestRunsReadTheStateButNoSecret(t *testing.T) {
	c, _ := testHive(t)
	os.WriteFile(c.WS.DaemonFile(), []byte(`{"token": "t"}`), 0o600)
	h, _ := harness.Get("fake")
	spec, err := c.Runs.sandboxSpec(isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "r1", Role: "worker", Harness: "fake"}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	hidden := map[string]bool{}
	state := false
	for _, m := range spec.Mounts {
		switch {
		case m.Inside == "/hive/state":
			state = m.Host == c.WS.Hive() && !m.RW && !m.Hide
		case strings.HasPrefix(m.Inside, "/hive/state/"):
			if m.RW || !m.Hide || !Exists(m.Host) {
				t.Errorf("%+v: only existing covers belong under /hive/state", m)
			}
			hidden[strings.TrimPrefix(m.Inside, "/hive/state/")] = true
		}
	}
	if !state || !hidden["logins"] || !hidden["daemon.json"] {
		t.Fatalf("state mounted %v, covered %v: %+v", state, hidden, spec.Mounts)
	}
	if !slices.Contains(spec.Env, "HIVE_STATE=/hive/state") {
		t.Errorf("HIVE_STATE not set: %v", spec.Env)
	}
	spec, _ = c.Runs.sandboxSpec(isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "r2", Role: "worker", Harness: "fake", Isolated: true}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	for _, m := range spec.Mounts {
		if strings.HasPrefix(m.Inside, "/hive/state") {
			t.Errorf("an isolated project sees the rest of the Hive: %+v", m)
		}
	}
}

// readable reports whether path inside the sandbox shows the host's file: the
// last mount over it decides, as in bwrap, where a later mount covers an
// earlier one.
func readable(spec sandbox.Spec, path string) bool {
	ok := false
	for _, m := range spec.Mounts {
		if path == m.Inside || strings.HasPrefix(path, m.Inside+"/") {
			ok = !m.Hide
		}
	}
	return ok
}

// A run outside an isolated project reads none of its clone, runs, chats,
// notes or journal, and still reads a normal project's; the isolated
// project's own run sees what it always did.
func TestIsolatedProjectsStayOutOfSharedState(t *testing.T) {
	c, _ := testHive(t)
	for _, p := range []Project{{Name: "open", Repo: "me/open"}, {Name: "secret", Repo: "me/secret", Isolated: true}} {
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
		os.WriteFile(filepath.Join(c.WS.Chats(), "agent_"+id+".jsonl"), nil, 0o644)
	}
	h, _ := harness.Get("fake")
	spec, err := c.Runs.sandboxSpec(isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "sweep", Role: "improver", Harness: "fake"}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/hive/projects/secret", "/hive/projects/secret/.checkouts/x", "/hive/state/runs/secret-run/box/notes.md",
		"/hive/state/chats/project_secret.jsonl", "/hive/state/chats/agent_secret-run.jsonl", "/hive/state/events.jsonl",
		"/hive/state/kb/isolated/secret/n.md", "/hive/kb/isolated/secret/n.md", "/hive/state/checks/secret/x.log", "/hive/state/gates/secret/x"} {
		if readable(spec, p) {
			t.Errorf("a normal run reads %s", p)
		}
	}
	for _, p := range []string{"/hive/projects/open", "/hive/state/runs/open-run/box/notes.md", "/hive/state/chats/project_open.jsonl",
		"/hive/state/chats/agent_open-run.jsonl", "/hive/kb/note.md", "/hive/state/checks/open/x.log"} {
		if !readable(spec, p) {
			t.Errorf("a normal run can't read %s", p)
		}
	}
	for _, m := range spec.Mounts {
		if m.Hide && !Exists(m.Host) {
			t.Errorf("cover %s has no mount point", m.Inside)
		}
	}
	spec, _ = c.Runs.sandboxSpec(isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "secret-run", Project: "secret", Harness: "fake", Isolated: true}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	if !readable(spec, "/hive/kb/isolated/secret/n.md") {
		t.Error("the isolated project's own run lost its notes")
	}
}
