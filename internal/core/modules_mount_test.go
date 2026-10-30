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
