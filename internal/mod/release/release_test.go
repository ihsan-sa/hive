package release

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// releaseAt is the release module in a build stamped version, with v9.9.9 out.
func releaseAt(t *testing.T, version string) (*Module, *bool) {
	dir := t.TempDir()
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	forge.CreateRelease(c.WS.LoadConfig().ReleaseRepo, "v9.9.9", "", "")
	restarted := false
	m := &Module{C: c, Restart: func() { restarted = true }}
	v := core.Version
	t.Cleanup(func() { core.Version = v })
	core.Version = version
	return m, &restarted
}

// A source build (no release version) never replaces itself with a release.
func TestSourceBuildNeverUpdates(t *testing.T) {
	m, restarted := releaseAt(t, "0.0.0-dev")
	msg, err := m.Update()
	if err != nil || !strings.Contains(msg, "source build") || *restarted {
		t.Fatalf("source build: %q, %v, restarted=%v", msg, err, *restarted)
	}
}

// A release build, stamped with its tag as release.yml does, is offered the
// newer release and goes on to fetch it (the local forge has no assets to give).
func TestReleaseBuildUpdates(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.0")
	if tag, ok, err := m.Check(); err != nil || !ok || tag != "v9.9.9" {
		t.Fatalf("check: %q %v %v", tag, ok, err)
	}
	if _, err := m.Update(); err == nil || !strings.Contains(err.Error(), "release assets") {
		t.Fatalf("the update didn't try to download v9.9.9: %v", err)
	}
}

// notes keeps each release's notes, which the local forge drops.
type notes struct {
	*core.Local
	got map[string]string
}

func (n *notes) CreateRelease(repo, tag, sha, body string) error {
	n.got[tag] = body
	return n.Local.CreateRelease(repo, tag, sha, body)
}

// A self project's release names what each merged head's reader read, refuses
// a head that merged unread, and holds a root change for the person.
func TestReleaseNamesItsReads(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.0")
	n := &notes{Local: m.C.Effects.Forge.(*core.Local), got: map[string]string{}}
	m.C.Effects.Forge = n
	m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true})
	merged := func(pr int, sha, mergeSHA string, root ...string) {
		m.C.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": pr, "sha": sha, "merge_sha": mergeSHA, "root": root})
	}
	land := func(sha, run string) {
		m.C.J.MustAppend("effect.verdict", core.Verdict{Project: "hive", SHA: sha, Run: run, Verdict: "land"})
	}

	merged(1, "a1", "m1")
	m.AfterKept("hive", "m1")
	if len(n.got) != 0 {
		t.Fatalf("released a head nobody read: %v", n.got)
	}
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	if !strings.Contains(n.got["v0.0.1"], "#1 at a1, read by r1") {
		t.Fatalf("the release doesn't name its read: %q", n.got["v0.0.1"])
	}

	merged(2, "a2", "m2", "install/root.sh")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	asks := m.C.Person.Open()
	if len(n.got) != 1 || len(asks) != 1 || asks[0].Kind != "release" {
		t.Fatalf("a root change released without the person: %v %+v", n.got, asks)
	}
	asks[0].Answer = "release"
	m.OnPerson(asks[0])
	if body := n.got["v0.0.2"]; !strings.Contains(body, "#2 at a2") || strings.Contains(body, "#1 ") {
		t.Fatalf("the second release should name only #2: %q", body)
	}
}
