package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// isolatedSB is a sandbox whose inside paths differ from host paths.
type isolatedSB struct{ sandbox.Bare }

func (isolatedSB) Isolated() bool { return true }

// A mount nested in a read-only mount needs its mount point on the host,
// because bwrap can't make it inside the read-only bind.
func TestNestedMountPointsExist(t *testing.T) {
	c, _ := testHive(t)
	r := Run{RunSpec: RunSpec{ID: "r1", Role: "lead", Harness: "fake", Isolated: true, Modules: []string{"agents", "kb"}}}
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

// startSB records each turn's command instead of starting it.
type startSB struct {
	sandbox.Bare
	args *[]string
}

func (s startSB) Start(spec sandbox.Spec) (sandbox.Proc, error) {
	*s.args = spec.Argv
	return sandbox.Proc{}, nil
}

// A lead is long-lived, so its next turn takes the model the settings name
// now, and the run says which model that turn is on.
func TestLeadFollowsModelSetting(t *testing.T) {
	c, _ := testHive(t)
	var args []string
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return startSB{args: &args}, nil }
	c.Runs.Route = func(Run) []Pick { return []Pick{{Harness: "claude", Model: "opus"}} }
	r, err := c.Runs.Start(RunSpec{ID: "lead1", Role: "lead", Harness: "claude", Model: "sonnet", Persistent: true, Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, c.WS.LoadConfig())
	got, _ := c.Runs.Get("lead1")
	if got.Model != "opus" || !strings.Contains(strings.Join(args, " "), "opus") {
		t.Fatalf("model %q, args %q: want the turn on opus", got.Model, args)
	}
}

// Every run but an isolated project's reads the whole .hive at /hive/state,
// writes none of it, and reads nothing there that grants access: no run's
// home or CODEX_HOME (its own, another's, or one made after the turn
// started), no stored login, no module program's home and not the daemon's token.
func TestRunsReadTheStateButNoSecret(t *testing.T) {
	c, _ := testHive(t)
	os.WriteFile(c.WS.DaemonFile(), []byte(`{"token": "t"}`), 0o600)
	login := filepath.Join(c.WS.Hive(), "logins", "codex", "spare", "auth.json")
	os.MkdirAll(filepath.Dir(login), 0o700)
	os.WriteFile(login, []byte(`{"refresh_token": "planted"}`), 0o600)
	moduleHome := filepath.Join(c.WS.Hive(), "modules", "door", "home")
	os.MkdirAll(moduleHome, 0o700)
	h, _ := harness.Get("fake")
	r := Run{RunSpec: RunSpec{ID: "r1", Role: "worker", Harness: "codex"}}
	os.MkdirAll(c.WS.RunHome(r.ID), 0o755)
	codexHome, err := c.Runs.freshCodexHome(r)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"), nil, codexHome)
	if err != nil {
		t.Fatal(err)
	}
	state, covers := "", []string{}
	for _, m := range spec.Mounts {
		switch {
		case m.Inside == "/hive/state":
			if m.Host != c.WS.Hive() || m.RW || m.Hide {
				t.Errorf("state mount %+v", m)
			}
			state = m.Host
		case strings.HasPrefix(m.Inside, "/hive/state/"):
			if m.RW || !m.Hide || !Exists(m.Host) || m.Inside != "/hive/state/"+strings.TrimPrefix(filepath.ToSlash(m.Host), filepath.ToSlash(c.WS.Hive())+"/") {
				t.Errorf("%+v: only covers of existing paths belong under /hive/state", m)
			}
			covers = append(covers, m.Host)
		}
	}
	under := func(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) }
	for _, p := range []string{c.WS.RunHome("r1"), codexHome, c.WS.RunHome("made-later"), c.WS.RunPrivate("made-later"), login, moduleHome, c.WS.DaemonFile()} {
		if state == "" || !under(p, state) {
			t.Fatalf("%s is outside the state %q, so this test checks nothing", p, state)
		}
		if !slices.ContainsFunc(covers, func(d string) bool { return under(p, d) }) {
			t.Errorf("%s can be read under /hive/state (covers %v)", p, covers)
		}
	}
	if under(c.WS.TurnOut("r1", 1), c.WS.Private()) {
		t.Errorf("turn logs are covered; agents read one another's")
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

// A run laid out by an earlier version has its home and CODEX_HOME in its
// readable folders, and maybe a turn spec that carried a token: starting the
// daemon moves the first two to Private and deletes the rest.
func TestMigrateMovesAccessOutOfTheState(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	run, host := ws.Run("old"), ws.RunHost("old")
	for _, d := range []string{filepath.Join(run, "home", ".codex"), filepath.Join(host, "codex"), filepath.Join(host, "codex-sessions")} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(run, "home", ".codex", "auth.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(host, "codex", "auth.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(host, "turn-001.jsonl.sandbox.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(host, "turn-001.jsonl"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(ws.Hive(), "daemon.json"), []byte(`{"token": "t"}`), 0o600)
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, nil, func() []string { return nil })
	// NewRuns alone moves nothing: only daemon start migrates, so no other
	// caller of NewRuns deletes a live daemon's token.
	for _, p := range []string{filepath.Join(run, "home", ".codex", "auth.json"), filepath.Join(host, "codex", "auth.json"), filepath.Join(host, "turn-001.jsonl.sandbox.json"), filepath.Join(ws.Hive(), "daemon.json")} {
		if !Exists(p) {
			t.Fatalf("NewRuns moved or deleted %s before Migrate", p)
		}
	}
	if err := rs.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(run, "home"), filepath.Join(host, "codex"), filepath.Join(host, "codex-sessions"), filepath.Join(host, "turn-001.jsonl.sandbox.json"), filepath.Join(ws.Hive(), "daemon.json")} {
		if Exists(p) {
			t.Errorf("%s is still in the readable tree", p)
		}
	}
	for _, p := range []string{filepath.Join(ws.RunHome("old"), ".codex", "auth.json"), filepath.Join(ws.RunPrivate("old"), "codex", "auth.json"), filepath.Join(ws.RunPrivate("old"), "codex-sessions"), filepath.Join(host, "turn-001.jsonl")} {
		if !Exists(p) {
			t.Errorf("%s is gone", p)
		}
	}
}

// A place already taken under private/ (a run that got a home or a Codex
// home on the new layout before an old daemon wrote to the old one) keeps
// what it has, and the old token still leaves the readable tree.
func TestMigrateResolvesATakenPlace(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	run, host := ws.Run("old"), ws.RunHost("old")
	for _, d := range []string{filepath.Join(run, "home"), filepath.Join(host, "codex"), filepath.Join(ws.RunHome("old")), filepath.Join(ws.RunPrivate("old"), "codex")} {
		os.MkdirAll(d, 0o700)
	}
	os.WriteFile(filepath.Join(run, "home", "old.txt"), []byte("old"), 0o600)
	os.WriteFile(filepath.Join(ws.RunHome("old"), "new.txt"), []byte("new"), 0o600)
	os.WriteFile(filepath.Join(host, "codex", "auth.json"), []byte("old token"), 0o600)
	os.WriteFile(filepath.Join(ws.RunPrivate("old"), "codex", "auth.json"), []byte("new token"), 0o600)
	if err := (&Runs{ws: ws}).Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(run, "home"), filepath.Join(host, "codex")} {
		if Exists(p) {
			t.Errorf("%s is still in the readable tree", p)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(ws.RunPrivate("old"), "codex", "auth.json")); string(raw) != "new token" {
		t.Errorf("the Codex home in place was replaced: %q", raw)
	}
	if !Exists(filepath.Join(ws.RunHome("old"), "new.txt")) {
		t.Error("the home in place was replaced")
	}
	kept, _ := filepath.Glob(filepath.Join(ws.RunPrivate("old"), "home.old-*", "old.txt"))
	if len(kept) != 1 {
		t.Errorf("the old home was not kept beside the new one: %v", kept)
	}
}

// A move that fails is an error, not a token left where every agent reads.
func TestMigrateReportsAMoveItCouldNotMake(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	home := filepath.Join(ws.Run("old"), "home")
	os.MkdirAll(home, 0o700)
	os.WriteFile(filepath.Join(home, "auth.json"), []byte("{}"), 0o600)
	os.MkdirAll(ws.Private(), 0o700)
	os.WriteFile(filepath.Join(ws.Private(), "runs"), []byte("in the way"), 0o600) // private/runs can't be made
	err := (&Runs{ws: ws}).Migrate()
	if err == nil || !strings.Contains(err.Error(), home) {
		t.Fatalf("Migrate returned %v, want an error naming %s", err, home)
	}
}

// A turn the earlier daemon started mounts .hive with no cover over private/,
// so it is ended before anything moves there.
func TestMigrateEndsTheOldDaemonsTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sleep")
	}
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, nil, func() []string { return nil })
	if _, err := rs.Start(RunSpec{ID: "old", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	h, err := proc.StartDetached([]string{"sleep", "60"}, os.Environ(), t.TempDir(), "", filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Kill(h)
	rs.append("run.turn_started", turnStarted{ID: "old", Turn: 1, Proc: h})
	home := filepath.Join(ws.Run("old"), "home")
	os.MkdirAll(home, 0o700)
	os.WriteFile(filepath.Join(home, "auth.json"), []byte("{}"), 0o600)
	if err := rs.Migrate(); err != nil {
		t.Fatal(err)
	}
	if proc.Alive(h) {
		t.Error("the old daemon's turn is still running")
	}
	moved, _ := filepath.Glob(filepath.Join(ws.RunPrivate("old"), "home*", "auth.json")) // Start made the new home, so the old one sits beside it
	if Exists(home) || len(moved) != 1 {
		t.Errorf("the home did not move: %v", moved)
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
	spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "sweep", Role: "improver", Harness: "fake"}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/hive/projects/secret", "/hive/projects/secret/.checkouts/x", "/hive/state/runs/secret-run/box/notes.md",
		"/hive/state/chats/project_secret.jsonl", "/hive/state/chats/agent_secret-run.jsonl", "/hive/state/events.jsonl",
		"/hive/state/kb/isolated/secret/n.md", "/hive/kb/isolated/secret/n.md", "/hive/state/checks/secret/x.log", "/hive/state/gates/secret/x",
		"/hive/state/scratch/secret-run/proof", "/hive/kb/record/run/secret-run.md", "/hive/state/kb/record/run/secret-run.md"} {
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
	spec, _ = c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "secret-run", Project: "secret", Harness: "fake", Isolated: true}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	if !readable(spec, "/hive/kb/isolated/secret/n.md") {
		t.Error("the isolated project's own run lost its notes")
	}
	if readable(spec, "/hive/kb/isolated/other/n.md") || readable(spec, "/hive/kb/record/run/other-run.md") {
		t.Error("an isolated project reads another's notes or the record")
	}
	// A tribunal seat is isolated but belongs to no isolated project.
	spec, _ = c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "seat", Project: "open", Harness: "fake", Isolated: true}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
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
	// Its own lead still reaches it: the wall stands only between projects.
	lead := Run{RunSpec: RunSpec{ID: "secret-lead", Role: "lead", Project: "secret", Isolated: true}}
	if _, err := o.Call(lead, "message", json.RawMessage(`{"to":"secret-run","text":"x"}`)); err != nil {
		t.Errorf("an isolated project's lead can't message its agent: %v", err)
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

// While a restart waits for a quiet moment no new turn starts, and Live
// counts the turns still running.
func TestHoldKeepsNewTurnsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep stands in for a turn")
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, nil, func() []string { return nil }) // a launch past the hold would call the nil sandbox
	r, err := rs.Start(RunSpec{ID: "new", Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	rs.Hold("waiting: a restart")
	rs.launch(r, Config{})
	if got, _ := rs.Get("new"); got.Status != StatusQueued {
		t.Fatalf("a held run went to %s", got.Status)
	}
	if rs.Live() != 0 {
		t.Fatal("no turn runs yet")
	}
	h, err := proc.StartDetached([]string{"sleep", "60"}, os.Environ(), t.TempDir(), "", filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Kill(h)
	rs.append("run.turn_started", turnStarted{ID: "new", Turn: 1, Proc: h})
	if rs.Live() != 1 {
		t.Fatal("the running turn isn't counted")
	}
}

// A launch already past the hold counts as live until its turn is
// journalled, so a restart waiting for a quiet moment doesn't miss it.
func TestLiveCountsLaunchesInFlight(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(Workspace{Root: t.TempDir()}, j, nil, nil, func() []string { return nil })
	r, err := rs.Start(RunSpec{ID: "new", Harness: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	in, release := make(chan struct{}), make(chan struct{})
	rs.sandbox = func() (sandbox.Sandbox, error) { close(in); <-release; return nil, errors.New("no sandbox") }
	go rs.launch(r, Config{})
	<-in
	rs.Hold("waiting: a restart")
	if rs.Live() != 1 {
		t.Fatal("a launch past the hold isn't counted")
	}
	close(release)
	for i := 0; rs.Live() != 0; i++ {
		if i == 100 {
			t.Fatal("a finished launch still counts")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A message sent while a turn ends blocked wakes the run whichever event the
// journal applies last: here the turn's end is applied first, having read the
// run before the message was queued, as a warden's answer in the same tick can.
func TestAMessageRacingABlockedEndWakesTheRun(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, nil, func() []string { return nil })
	if _, err := rs.Start(RunSpec{ID: "w", Harness: "fake"}); err != nil {
		t.Fatal(err)
	}
	rs.append("run.turn_started", turnStarted{ID: "w", Turn: 1})
	if err := rs.Takes("w"); err != nil { // Send's check, on the running turn
		t.Fatal(err)
	}
	rs.append("run.turn_ended", turnEnded{ID: "w", Turn: 1, Status: StatusBlocked, Reason: "asked a warden"})
	rs.wakeAnswered("w") // endTurn's, before the message is in
	if r, _ := rs.Get("w"); r.Status != StatusBlocked {
		t.Fatalf("woken with nothing to read: %s", r.Status)
	}
	rs.append("run.queued", runNote{ID: "w", Text: "[request x] the warden won't"})
	rs.wakeAnswered("w") // Send's, after its message
	if r, _ := rs.Get("w"); r.Status != StatusIdle || len(r.Pending) != 1 {
		t.Fatalf("the answer was left on a blocked run: %s, %d pending", r.Status, len(r.Pending))
	}
	rs.wakeAnswered("w")
	n := 0
	j.Replay(func(e Event) {
		if e.Kind == "run.reopened" {
			n++
		}
	})
	if n != 1 {
		t.Fatalf("woken %d times", n)
	}
}
