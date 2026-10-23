package core

import (
	"encoding/json"
	"errors"
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
	spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
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
	spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "r1", Role: "worker", Harness: "fake"}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
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
	spec, _ = c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "r2", Role: "worker", Harness: "fake", Isolated: true}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
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
	for _, p := range []Project{{Name: "open", Repo: "me/open"}, {Name: "secret", Repo: "me/secret", Isolated: true}, {Name: "other", Repo: "me/other", Isolated: true}} {
		if _, err := c.Projects.Add(p); err != nil {
			t.Fatal(err)
		}
		if p.Name != "other" { // other's clone never landed
			os.MkdirAll(c.WS.Project(p.Name), 0o755)
		}
		id := p.Name + "-run"
		c.Runs.mu.Lock()
		c.Runs.runs[id] = &Run{RunSpec: RunSpec{ID: id, Project: p.Name, Isolated: p.Isolated}}
		c.Runs.mu.Unlock()
		os.MkdirAll(c.WS.Run(id), 0o755)
		os.MkdirAll(filepath.Join(c.WS.Hive(), "scratch", id), 0o755)
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
		"/hive/state/kb/isolated/secret/n.md", "/hive/kb/isolated/secret/n.md", "/hive/state/checks/secret/x.log", "/hive/state/gates/secret/x",
		"/hive/state/scratch/secret-run/proof"} {
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
	if readable(spec, "/hive/kb/isolated/other/n.md") {
		t.Error("an isolated project reads another's notes")
	}
	// A tribunal seat is isolated but belongs to no isolated project.
	spec, _ = c.Runs.sandboxSpec(isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "seat", Project: "open", Harness: "fake", Isolated: true}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	if readable(spec, "/hive/kb/isolated/secret/n.md") || !readable(spec, "/hive/kb/note.md") {
		t.Error("an isolated seat of a normal project reads the wrong notes")
	}
}

// No agent outside an isolated project can spawn into it, message its agents
// or list them, and its agents reach no other project.
func TestIsolatedProjectsAreOutOfReach(t *testing.T) {
	c, _ := testHive(t)
	for _, p := range []Project{{Name: "open", Repo: "me/open"}, {Name: "secret", Repo: "me/secret", Isolated: true}} {
		c.Projects.Add(p)
		id := p.Name + "-run"
		c.Runs.mu.Lock()
		c.Runs.runs[id] = &Run{RunSpec: RunSpec{ID: id, Project: p.Name, Isolated: p.Isolated}}
		c.Runs.mu.Unlock()
	}
	o := c.Runs.Ops()
	o.SetSpawner(func(Run, string, string, string, string) (RunSpec, error) {
		return RunSpec{}, errors.New("spawner reached")
	})
	open, _ := c.Runs.Get("open-run")
	secret, _ := c.Runs.Get("secret-run")
	sweep := Run{RunSpec: RunSpec{ID: "sweep", Role: "improver"}}
	for _, tc := range []struct {
		from    Run
		project string
		ok      bool
	}{{sweep, "secret", false}, {open, "secret", false}, {secret, "open", false}, {secret, "", true}, {open, "open", true}, {sweep, "open", true}} {
		_, err := o.Call(tc.from, "spawn", json.RawMessage(`{"brief":"x","project":"`+tc.project+`"}`))
		if reached := err != nil && err.Error() == "spawner reached"; reached != tc.ok {
			t.Errorf("%s spawning into %q: %v", tc.from.ID, tc.project, err)
		}
	}
	if _, err := o.Call(sweep, "message", json.RawMessage(`{"to":"secret-run","text":"x"}`)); err == nil {
		t.Error("a sweep messaged an isolated project's agent")
	}
	if _, err := o.Call(secret, "message", json.RawMessage(`{"to":"open-run","text":"x"}`)); err == nil {
		t.Error("an isolated agent messaged another project's agent")
	}
	if _, err := o.Call(sweep, "message", json.RawMessage(`{"to":"open-run","text":"x"}`)); err != nil {
		t.Errorf("a sweep can't message a normal agent: %v", err)
	}
	got, _ := o.Call(sweep, "agents", nil)
	if b, _ := json.Marshal(got); strings.Contains(string(b), "secret") || !strings.Contains(string(b), "open-run") {
		t.Errorf("agents shows a sweep %s", b)
	}
}

// Check logs from before they went in their project's folder move there on
// start: a gate's by the clone holding its commit, a snapshot's by its run.
func TestOldCheckLogsMoveToTheirProject(t *testing.T) {
	c, _ := testHive(t)
	c.Projects.Add(Project{Name: "secret", Repo: "me/secret", Isolated: true})
	clone := c.WS.Project("secret")
	os.MkdirAll(clone, 0o755)
	Git(clone, "init", "-q")
	Git(clone, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x")
	sha, _ := Git(clone, "rev-parse", "HEAD")
	c.Runs.mu.Lock()
	c.Runs.runs["secret-run"] = &Run{RunSpec: RunSpec{ID: "secret-run", Project: "secret", Isolated: true}}
	c.Runs.mu.Unlock()
	checks := filepath.Join(c.WS.Hive(), "checks")
	os.MkdirAll(checks, 0o755)
	logs := []string{"gate-" + short(strings.TrimSpace(sha)) + "-test-abc.log", "secret-run-snapshot.log", "gate-00000000-test-abc.log"}
	for _, n := range logs {
		os.WriteFile(filepath.Join(checks, n), nil, 0o644)
	}
	c.moveOldChecks()
	for i, n := range logs[:2] {
		if !Exists(filepath.Join(checks, "secret", n)) {
			t.Errorf("log %d stayed in the shared folder", i)
		}
	}
	if !Exists(filepath.Join(checks, logs[2])) {
		t.Error("a log with no known project moved")
	}
}

// A fork an open and an isolated project share holds the same commits, so an
// old gate log goes to the isolated one, where it stays covered.
func TestOldGateLogOfASharedForkGoesToTheIsolatedProject(t *testing.T) {
	c, _ := testHive(t)
	c.Projects.Add(Project{Name: "secret", Repo: "me/secret", Isolated: true})
	c.Projects.Add(Project{Name: "open", Repo: "me/open"})
	clone := c.WS.Project("secret")
	os.MkdirAll(clone, 0o755)
	Git(clone, "init", "-q")
	Git(clone, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x")
	if _, err := Git("", "clone", "-q", clone, c.WS.Project("open")); err != nil {
		t.Fatal(err)
	}
	sha, _ := Git(clone, "rev-parse", "HEAD")
	checks := filepath.Join(c.WS.Hive(), "checks")
	os.MkdirAll(checks, 0o755)
	log := "gate-" + short(strings.TrimSpace(sha)) + "-test-abc.log"
	os.WriteFile(filepath.Join(checks, log), nil, 0o644)
	c.moveOldChecks()
	if !Exists(filepath.Join(checks, "secret", log)) {
		t.Error("the shared fork's gate log went to the open project")
	}
}

// When two isolated projects hold a gate's commit, its old log stays in the
// shared folder rather than going to one of them, where the other's agents
// would read it.
func TestOldGateLogOfTwoIsolatedProjectsStays(t *testing.T) {
	c, _ := testHive(t)
	c.Projects.Add(Project{Name: "secret", Repo: "me/secret", Isolated: true})
	c.Projects.Add(Project{Name: "other", Repo: "me/other", Isolated: true})
	clone := c.WS.Project("secret")
	os.MkdirAll(clone, 0o755)
	Git(clone, "init", "-q")
	Git(clone, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x")
	if _, err := Git("", "clone", "-q", clone, c.WS.Project("other")); err != nil {
		t.Fatal(err)
	}
	sha, _ := Git(clone, "rev-parse", "HEAD")
	checks := filepath.Join(c.WS.Hive(), "checks")
	os.MkdirAll(checks, 0o755)
	log := "gate-" + short(strings.TrimSpace(sha)) + "-test-abc.log"
	os.WriteFile(filepath.Join(checks, log), nil, 0o644)
	c.moveOldChecks()
	if !Exists(filepath.Join(checks, log)) {
		t.Error("a gate log two isolated projects share moved to one of them")
	}
}

// Covering an isolated project's notes never makes their folder through a
// link left in the knowledge base.
func TestHidingIsolatedNotesFollowsNoLink(t *testing.T) {
	c, _ := testHive(t)
	c.Projects.Add(Project{Name: "secret", Repo: "me/secret", Isolated: true})
	elsewhere := t.TempDir()
	os.MkdirAll(c.WS.KB(), 0o755)
	if err := os.Symlink(elsewhere, filepath.Join(c.WS.KB(), "isolated")); err != nil {
		t.Fatal(err)
	}
	c.Runs.hideIsolated(Run{RunSpec: RunSpec{ID: "r", Project: "open"}})
	if Exists(filepath.Join(elsewhere, "secret")) {
		t.Error("the notes folder was made where the link points")
	}
}
