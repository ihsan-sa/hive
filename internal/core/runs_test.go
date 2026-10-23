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
	spec, err := c.Runs.sandboxSpec(isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"), nil, codexHome)
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
	spec, _ = c.Runs.sandboxSpec(isolatedSB{}, h, Run{RunSpec: RunSpec{ID: "r2", Role: "worker", Harness: "fake", Isolated: true}}, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
	for _, m := range spec.Mounts {
		if strings.HasPrefix(m.Inside, "/hive/state") {
			t.Errorf("an isolated project sees the rest of the Hive: %+v", m)
		}
	}
}

// A run laid out by an earlier version has its home and CODEX_HOME in its
// readable folders, and maybe a turn spec that carried a token: starting the
// daemon moves the first two to Private and deletes the rest.
func TestTidyMovesAccessOutOfTheState(t *testing.T) {
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
	(&Runs{ws: ws}).tidy()
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
