package core

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

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
	c.Runs.ModelFor = func(Run) string { return "opus" }
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
// started), no stored login and not the daemon's token.
func TestRunsReadTheStateButNoSecret(t *testing.T) {
	c, _ := testHive(t)
	os.WriteFile(c.WS.DaemonFile(), []byte(`{"token": "t"}`), 0o600)
	login := filepath.Join(c.WS.Hive(), "logins", "codex", "spare", "auth.json")
	os.MkdirAll(filepath.Dir(login), 0o700)
	os.WriteFile(login, []byte(`{"refresh_token": "planted"}`), 0o600)
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
	for _, p := range []string{c.WS.RunHome("r1"), codexHome, c.WS.RunHome("made-later"), c.WS.RunPrivate("made-later"), login, c.WS.DaemonFile()} {
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
	if err := (&Runs{ws: ws}).Migrate(); err != nil {
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
