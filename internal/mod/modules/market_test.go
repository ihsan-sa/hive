package modules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func newRegistry(t *testing.T) (*Registry, core.Workspace) {
	t.Helper()
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(ws.Hive(), "journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return New(ws, j), ws
}

// commit writes files into a git repository (made if new) and returns the commit.
func commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if !core.Exists(filepath.Join(dir, ".git")) {
		os.MkdirAll(dir, 0o755)
		core.Git(dir, "init", "-q", "-b", "main")
	}
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	core.Git(dir, "add", "-A")
	if out, err := core.Git(dir, "commit", "-q", "--allow-empty", "-m", "c"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	sha, _ := core.Git(dir, "rev-parse", "HEAD")
	return sha
}

// list writes an index in a folder and points the Hive at it.
func list(t *testing.T, r *Registry, ws core.Workspace, dir string, ls ...Listing) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"modules": ls})
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Indexes = []string{dir}
	ws.SaveConfig(cfg)
	r.listings(true)
}

const slackJSON = `{"name": "slack", "description": "the lead's chat in Slack", "exec": ["./slack"], "hooks": ["channel"], "hosts": ["slack.com"]}`

var slackNeeds = Needs{Program: true, Hooks: []string{"channel"}, Hosts: []string{"slack.com"}}

// Installing by name takes the index's pinned commit of the module's folder
// in its repository, moves it in whole, and records its trust, read and
// reach; an index other than the Hive's own can't say core-team.
func TestInstallByName(t *testing.T) {
	r, ws := newRegistry(t)
	src := filepath.Join(t.TempDir(), "extras")
	sha := commit(t, src, map[string]string{"slack/module.json": slackJSON, "slack/slack": "#!/bin/sh\n", "README.md": "extras\n"})
	os.Chmod(filepath.Join(src, "slack", "slack"), 0o755)
	sha = commit(t, src, nil)
	commit(t, src, map[string]string{"slack/later.md": "not pinned\n"}) // HEAD moves past the pin
	list(t, r, ws, t.TempDir(), Listing{Name: "slack", Description: "Slack", Repo: src, Path: "slack", Sha: sha, Trust: "core-team", Read: "read by the Hive's security reviewer at this commit", Needs: slackNeeds})

	p, err := r.Resolve("slack")
	if err != nil {
		t.Fatal(err)
	}
	if p.Trust != "reviewed" || p.Sha != sha || !strings.Contains(p.Describe(), "hosts: slack.com") || !strings.Contains(p.Describe(), "read by the Hive's security reviewer") {
		t.Fatalf("plan %+v\n%s", p, p.Describe())
	}
	m, err := r.Install(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Source.Commit != sha || m.Source.Path != "slack" || m.Source.Trust != "reviewed" || m.Source.Needs == nil || len(m.Undeclared()) != 0 {
		t.Fatalf("installed %+v", m.Source)
	}
	if core.Exists(filepath.Join(m.Dir, "later.md")) || core.Exists(filepath.Join(m.Dir, "README.md")) || core.Exists(filepath.Join(m.Dir, ".git")) {
		t.Fatal("installed more than the module's folder at its pinned commit")
	}
	if fi, err := os.Stat(filepath.Join(m.Dir, "slack")); err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("its program lost its executable bit: %v", err)
	}
	if _, err := r.Install(p); err == nil {
		t.Fatal("installed over an installed module")
	}
	if left, _ := filepath.Glob(filepath.Join(ws.Modules(), ".*")); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	// Its manifest grown past what it was installed with keeps its program stopped.
	os.WriteFile(filepath.Join(m.Dir, "module.json"), []byte(strings.Replace(slackJSON, `["channel"]`, `["channel", "source"]`, 1)), 0o644)
	if m, _ := r.Get("slack"); strings.Join(m.Undeclared(), ",") != "hook source" {
		t.Fatalf("undeclared reach: %v", m.Undeclared())
	}
}

// A listing whose module asks for more than the listing declares is refused,
// and so is a pin the repository doesn't have; another commit than the pin
// is unlisted, and a name no index lists is not found.
func TestUndeclaredReachIsRefused(t *testing.T) {
	r, ws := newRegistry(t)
	src := filepath.Join(t.TempDir(), "slack")
	sha := commit(t, src, map[string]string{"module.json": slackJSON})
	other := commit(t, src, map[string]string{"x.md": "x\n"})
	less := slackNeeds
	less.Hosts = nil
	list(t, r, ws, t.TempDir(),
		Listing{Name: "slack", Repo: src, Sha: sha, Trust: "reviewed", Needs: less},
		Listing{Name: "ghost", Repo: src, Sha: strings.Repeat("ab", 20), Trust: "reviewed", Needs: slackNeeds})
	if _, err := r.Resolve("slack"); err == nil || !strings.Contains(err.Error(), "host slack.com") {
		t.Fatalf("undeclared host: %v", err)
	}
	if _, err := r.Resolve("ghost"); err == nil {
		t.Fatal("a pin the repository doesn't have resolved")
	}
	p, err := r.Resolve("slack@" + other[:12])
	if err != nil || p.Trust != "unlisted" || p.Sha != other || p.Needs.Hosts[0] != "slack.com" || !strings.Contains(p.Read, "another commit") {
		t.Fatalf("another commit: %+v %v", p, err)
	}
	if _, err := r.PlanFor("nothing"); err == nil || !strings.Contains(err.Error(), "git URL") {
		t.Fatalf("an unknown name: %v", err)
	}
	// A git URL is unlisted, pinned to its newest release, and reads back from its Ref.
	core.Git(src, "tag", "v1.2.0", sha)
	p, err = r.Resolve(src)
	if err != nil || p.Trust != "unlisted" || p.Sha != sha || p.Release != "v1.2.0" {
		t.Fatalf("by URL: %+v %v", p, err)
	}
	if back, err := r.PlanFor(p.Ref()); err != nil || back.Sha != sha || back.Name != "slack" || back.Repo != src || back.Trust != "unlisted" {
		t.Fatalf("Ref %s read back as %+v %v", p.Ref(), back, err)
	}
}

// An index's new pin is an update that comes with the diff from the
// installed commit; a skipped one isn't offered again, and an upgrade keeps
// the module's grants.
func TestUpdateComesWithItsDiff(t *testing.T) {
	r, ws := newRegistry(t)
	src := filepath.Join(t.TempDir(), "notes")
	v1 := commit(t, src, map[string]string{"module.json": `{"name": "notes", "description": "a skill"}`, "skill.md": "the codeword is PLUM\n"})
	idx := t.TempDir()
	list(t, r, ws, idx, Listing{Name: "notes", Repo: src, Sha: v1, Trust: "reviewed"})
	p, _ := r.Resolve("notes")
	if _, err := r.Install(p); err != nil {
		t.Fatal(err)
	}
	r.SetAccess("notes", "*", false)
	r.SetAccess("notes", "app", true)
	if ups, notes := r.Outdated(); len(ups) != 0 || len(notes) != 0 {
		t.Fatalf("an unchanged pin is an update: %+v %v", ups, notes)
	}
	v2 := commit(t, src, map[string]string{"skill.md": "the codeword is FIG\n"})
	list(t, r, ws, idx, Listing{Name: "notes", Repo: src, Sha: v2, Trust: "reviewed"})
	ups, _ := r.Outdated()
	if len(ups) != 1 || ups[0].Sha != v2 || !strings.Contains(ups[0].Diff, "+the codeword is FIG") {
		t.Fatalf("update %+v", ups)
	}
	if err := r.Skip("notes", v2); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.Outdated(); len(again) != 0 {
		t.Fatal("a skipped update was offered again")
	}
	m, err := r.Upgrade(ups[0])
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(m.Dir, "skill.md")); string(got) != "the codeword is FIG\n" || m.Source.Commit != v2 || strings.Join(m.Source.Projects, ",") != "app" {
		t.Fatalf("upgraded to %q %+v", got, m.Source)
	}
}

// Remove takes the folder and the program's home; a standard module stays.
func TestRemove(t *testing.T) {
	r, ws := newRegistry(t)
	if err := r.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("kb"); err == nil {
		t.Fatal("a standard module was removed")
	}
	src := filepath.Join(t.TempDir(), "door")
	commit(t, src, map[string]string{"module.json": `{"name": "door"}`})
	p, err := r.Resolve(src)
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.Install(p)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(ws.Hive(), "modules", "door")
	os.MkdirAll(home, 0o700)
	if err := r.Remove("door"); err != nil {
		t.Fatal(err)
	}
	if core.Exists(m.Dir) || core.Exists(home) {
		t.Fatal("left its folder or home")
	}
	if _, ok := r.Get("door"); ok {
		t.Fatal("still listed")
	}
}

// A module being installed isn't listed until its copy is whole and recorded.
func TestInstallListsOnlyAWholeCopy(t *testing.T) {
	r, ws := newRegistry(t)
	src := filepath.Join(t.TempDir(), "ledger")
	files := map[string]string{"module.json": `{"name": "ledger"}`, "zz-last.md": "# the ledger\n"}
	for i := range 300 {
		files[fmt.Sprintf("f%03d.md", i)] = "x\n"
	}
	commit(t, src, files)
	p, err := r.Resolve(src)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { _, err := r.Install(p); done <- err }()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if left, _ := filepath.Glob(filepath.Join(ws.Modules(), ".ledger-*")); len(left) != 0 {
				t.Fatalf("left behind: %v", left)
			}
			return
		default:
			if m, ok := r.Get("ledger"); ok && (!core.Exists(filepath.Join(m.Dir, "zz-last.md")) || m.Source.Commit == "") {
				t.Fatalf("listed before it was whole: %+v", m.Source)
			}
		}
	}
}
