package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

type isolatedBox struct{}

func (isolatedBox) Name() string   { return "isolated" }
func (isolatedBox) Isolated() bool { return true }
func (isolatedBox) Check() error   { return nil }
func (isolatedBox) Start(sandbox.Spec) (sandbox.Proc, error) {
	return sandbox.Proc{}, errors.New("not started")
}
func (isolatedBox) Run(sandbox.Spec, time.Duration) (int, error) { return 0, errors.New("not run") }

// An isolated run sees only its granted modules, so none granted means none,
// even after the empty list is dropped on its way through the journal.
func TestIsolatedRunWithNoGrantsSeesNoModules(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	h, _ := harness.Get("fake")
	spec, err := rs.sandboxSpec(rs.ws.LoadConfig(), isolatedBox{}, h, Run{RunSpec: RunSpec{ID: "r1", Checkout: t.TempDir(), Isolated: true}}, 1, false, "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range spec.Mounts {
		if m.Inside == "/hive/modules" && (m.Host == ws.Modules() || !Exists(m.Host)) {
			t.Fatalf("an isolated run with no grants doesn't get an empty modules dir: %+v", m)
		}
	}
}

// A check sees the installed modules read-only at /hive/modules, as the
// project's agents do; an isolated project's check sees only those granted it.
func TestChecksSeeInstalledModules(t *testing.T) {
	c, f := testHive(t)
	for _, m := range []string{"shared", "granted"} {
		os.MkdirAll(filepath.Join(c.WS.Modules(), m, "bin"), 0o755)
		os.WriteFile(filepath.Join(c.WS.Modules(), m, "bin", "say"), []byte("#!/bin/sh\necho from "+m+"\n"), 0o755)
	}
	c.Effects.Granted = func(project string) []string { return []string{"granted"} }
	var specs []sandbox.Spec
	c.Effects.sandbox = func() (sandbox.Sandbox, error) { return mapped{specs: &specs}, nil }
	for _, iso := range []bool{false, true} {
		name := fmt.Sprintf("mods%v", iso)
		seedRepo(t, f, "me/"+name, map[string]string{".hive/project.json": `{"test": "true"}`})
		p, err := c.Projects.Add(Project{Name: name, Repo: "me/" + name, Isolated: iso})
		if err != nil {
			t.Fatal(err)
		}
		main, err := c.Effects.Sync(p)
		if err != nil {
			t.Fatal(err)
		}
		specs = nil
		ch := c.Effects.RunCheck(p, main, "test", "/hive/modules/shared/bin/say")
		if iso {
			var inside []string
			for _, m := range specs[0].Mounts {
				if strings.HasPrefix(m.Inside, "/hive/modules") {
					if m.RW || m.Host == c.WS.Modules() {
						t.Fatalf("isolated check's modules mount %+v", m)
					}
					inside = append(inside, m.Inside)
				}
			}
			if !slices.Equal(inside, []string{"/hive/modules", "/hive/modules/granted"}) {
				t.Fatalf("an isolated check sees %v, want only its grants", inside)
			}
			continue
		}
		if !ch.OK || !strings.Contains(ch.Output, "from shared") {
			t.Fatalf("a check can't run an installed module's program: %+v", ch)
		}
		if !slices.ContainsFunc(specs[0].Mounts, func(m sandbox.Mount) bool {
			return m.Inside == "/hive/modules" && m.Host == c.WS.Modules() && !m.RW
		}) || !slices.Contains(specs[0].Env, "HIVE_MODULES=/hive/modules") {
			t.Fatalf("modules not mounted read-only with HIVE_MODULES: %+v", specs[0])
		}
	}
}

// A check sees the other projects' clones read-only at /hive/projects, as the
// project's agents do, with every hidden project but its own covered, so a
// score built on another project's main measures the same as in a turn; an
// isolated project's check sees none.
func TestChecksSeeOtherProjects(t *testing.T) {
	c, f := testHive(t)
	var specs []sandbox.Spec
	c.Effects.sandbox = func() (sandbox.Sandbox, error) { return mapped{specs: &specs}, nil }
	add := func(name, sharing string) (Project, string) {
		seedRepo(t, f, "me/"+name, map[string]string{".hive/project.json": `{"test": "true"}`})
		p := Project{Name: name, Repo: "me/" + name}
		p.SetSharing(sharing)
		p, err := c.Projects.Add(p)
		if err != nil {
			t.Fatal(err)
		}
		main, err := c.Effects.Sync(p)
		if err != nil {
			t.Fatal(err)
		}
		return p, main
	}
	add("board", SharingShared)
	add("secret", SharingPrivate)
	reader, main := add("reader", SharingShared)
	walled, wmain := add("walled", SharingIsolated)
	ch := c.Effects.RunCheck(reader, main, "test", "git -C /hive/projects/board rev-parse --verify -q main")
	if !ch.OK {
		t.Fatalf("a check can't read another project's main: %+v", ch)
	}
	ms := specs[0].Mounts
	if !slices.ContainsFunc(ms, func(m sandbox.Mount) bool {
		return m.Inside == "/hive/projects" && m.Host == c.WS.Projects() && !m.RW
	}) || !slices.Contains(specs[0].Env, "HIVE_PROJECTS=/hive/projects") {
		t.Fatalf("projects not mounted read-only with HIVE_PROJECTS: %+v", specs[0])
	}
	if !slices.ContainsFunc(ms, func(m sandbox.Mount) bool { return m.Inside == "/hive/projects/secret" && m.Hide }) {
		t.Fatalf("a private project's clone is not covered from another's check: %+v", ms)
	}
	for _, m := range ms {
		if m.Inside == "/hive/projects/board" || m.Inside == "/hive/projects/reader" {
			t.Fatalf("a shared project's clone is covered: %+v", m)
		}
	}
	specs = nil
	c.Effects.RunCheck(walled, wmain, "test", "true")
	for _, m := range specs[0].Mounts {
		if strings.HasPrefix(m.Inside, "/hive/projects") {
			t.Fatalf("an isolated project's check sees other projects: %+v", m)
		}
	}
	if slices.ContainsFunc(specs[0].Env, func(v string) bool { return strings.HasPrefix(v, "HIVE_PROJECTS=") }) {
		t.Fatalf("an isolated project's check gets HIVE_PROJECTS: %v", specs[0].Env)
	}
}

// A module project's score check reads a read-only copy of its module's
// records at HIVE_HOME, laid out as the Hive's own, and nothing else of the
// module's HOME; its test check gets none.
func TestScoreCheckReadsModuleRecords(t *testing.T) {
	c, f := testHive(t)
	home := filepath.Join(t.TempDir(), "mod")
	os.MkdirAll(home, 0o700)
	os.WriteFile(filepath.Join(home, "turns.jsonl"), []byte(`{"turn":"w-1","ended":true}`+"\n"), 0o600)
	os.WriteFile(filepath.Join(home, "id_ed25519"), []byte("secret\n"), 0o600)
	os.Symlink(filepath.Join(home, "id_ed25519"), filepath.Join(home, "key.jsonl"))
	c.Effects.ModuleHomes = func(project string) []string {
		if project == "mod" {
			return []string{home}
		}
		return nil
	}
	var specs []sandbox.Spec
	c.Effects.sandbox = func() (sandbox.Sandbox, error) { return mapped{specs: &specs}, nil }
	seedRepo(t, f, "me/mod", map[string]string{".hive/project.json": `{"test": "true"}`})
	p, err := c.Projects.Add(Project{Name: "mod", Repo: "me/mod"})
	if err != nil {
		t.Fatal(err)
	}
	main, err := c.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := "/hive/records/.hive/modules/mod/"
	ch := c.Effects.RunCheck(p, main, "score", "cat "+dir+"turns.jsonl; ls -a "+dir+"; echo 1")
	if !ch.OK || !strings.Contains(ch.Output, `"turn":"w-1"`) || strings.Contains(ch.Output, "id_ed25519") || strings.Contains(ch.Output, "key.jsonl") {
		t.Fatalf("score check didn't read only the module's records: %+v", ch)
	}
	if !slices.ContainsFunc(specs[0].Mounts, func(m sandbox.Mount) bool { return m.Inside == "/hive/records" && !m.RW }) ||
		!slices.Contains(specs[0].Env, "HIVE_HOME=/hive/records") {
		t.Fatalf("records not mounted read-only with HIVE_HOME: %+v", specs[0])
	}
	specs = nil
	c.Effects.RunCheck(p, main, "test", "true")
	if slices.ContainsFunc(specs[0].Mounts, func(m sandbox.Mount) bool { return m.Inside == "/hive/records" }) {
		t.Fatalf("a test check got the module's records: %+v", specs[0].Mounts)
	}
}

// A score check of a commit main has been at keeps a cache across measures
// at HIVE_SCORE_CACHE; a branch's commit gets none, so it can't leave an
// entry a later measure counts; clearing the cache empties it (#2176).
func TestScoreCacheOutlivesTheMeasureOnMainOnly(t *testing.T) {
	c, _, p, main := setupProject(t)
	var specs []sandbox.Spec
	c.Effects.sandbox = func() (sandbox.Sandbox, error) { return mapped{specs: &specs}, nil }
	put := `if [ -n "$HIVE_SCORE_CACHE" ]; then cat /hive/score-cache/n 2>/dev/null; echo kept >/hive/score-cache/n; else echo none; fi; echo 1`
	if ch := c.Effects.RunCheck(p, main, "score", put); !ch.OK || strings.Contains(ch.Output, "kept") || strings.Contains(ch.Output, "none") {
		t.Fatalf("main's first score check had no empty cache: %+v", ch)
	}
	if !slices.ContainsFunc(specs[0].Mounts, func(m sandbox.Mount) bool { return m.Inside == "/hive/score-cache" && m.RW }) ||
		!slices.Contains(specs[0].Env, "HIVE_SCORE_CACHE=/hive/score-cache") {
		t.Fatalf("score cache not mounted writable: %+v", specs[0].Mounts)
	}
	if ch := c.Effects.RunCheck(p, main, "score", "cat /hive/score-cache/n; echo 1"); !strings.Contains(ch.Output, "kept") {
		t.Fatalf("the cache didn't outlive the measure: %+v", ch)
	}
	r, _ := commitInCheckout(t, c, p, "w1", main, "feature.go", "package demo\n")
	sha, err := c.Effects.Extract(p, r, main)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Effects.Push(p, "hive/t1", sha); err != nil {
		t.Fatal(err)
	}
	specs = nil
	if ch := c.Effects.RunCheck(p, sha, "score", put); !strings.Contains(ch.Output, "none") {
		t.Fatalf("a branch's score check got the cache: %+v", ch)
	}
	if slices.ContainsFunc(specs[0].Mounts, func(m sandbox.Mount) bool { return m.Inside == "/hive/score-cache" }) {
		t.Fatalf("a branch's score check mounted the cache: %+v", specs[0].Mounts)
	}
	c.Effects.ClearScoreCache(p.Name, "test")
	if Exists(filepath.Join(c.WS.ScoreCache(p.Name), "n")) {
		t.Fatal("ClearScoreCache left the entry")
	}
}
