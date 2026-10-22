package release

import (
	"path/filepath"
	"strings"
	"sync"
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
	got   map[string]string
	calls int
}

func (n *notes) CreateRelease(repo, tag, sha, body string) error {
	n.got[tag] = body
	n.calls++
	return n.Local.CreateRelease(repo, tag, sha, body)
}

// selfHive is a self project "hive" whose releases' notes are kept, with
// helpers to journal a merge and a reviewer's land on a head.
func selfHive(t *testing.T) (*Module, *notes, func(int, string, string, ...string), func(string, string)) {
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
	return m, n, merged, land
}

// A self project's release names what each merged head's reader read, waits
// on a head that merged unread, and holds a root change for the person.
func TestReleaseNamesItsReads(t *testing.T) {
	m, n, merged, land := selfHive(t)
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

// An answer to an older release ask, given after a newer release was cut,
// never releases the older merge under a newer tag.
func TestStaleReleaseAnswer(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(2, "a2", "m2", "install/root.sh")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	merged(3, "a3", "m3", "install/root.sh")
	land("a3", "r3")
	m.AfterKept("hive", "m3")
	asks := m.C.Person.Open()
	if len(asks) != 1 || asks[0].Ref != "hive@m3" {
		t.Fatalf("the newer ask should supersede the older one: %+v", asks)
	}
	b := asks[0]
	b.Answer = "release"
	m.OnPerson(b)
	if body := n.got["v0.0.1"]; !strings.Contains(body, "#2 at a2") || !strings.Contains(body, "#3 at a3") {
		t.Fatalf("the release at m3 should name #2 and #3: %q", body)
	}
	m.OnPerson(core.Ask{Kind: "release", Ref: "hive@m2", Project: "hive", Answer: "release"})
	if len(n.got) != 1 {
		t.Fatalf("a stale answer cut another release: %v", n.got)
	}
}

// The person's release answer stands for the unread head its ask named.
func TestPersonReleasesUnreadHead(t *testing.T) {
	m, n, merged, _ := selfHive(t)
	merged(1, "a1", "m1")
	m.AfterKept("hive", "m1")
	asks := m.C.Person.Open()
	if len(n.got) != 0 || len(asks) != 1 || !strings.Contains(asks[0].Title, "#1 at a1 merged without a read") {
		t.Fatalf("an unread head should wait for the person: %v %+v", n.got, asks)
	}
	asks[0].Answer = "release"
	m.OnPerson(asks[0])
	if body := n.got["v0.0.1"]; !strings.Contains(body, "#1 at a1, not read") || !strings.Contains(body, "The person released it") {
		t.Fatalf("the release should say #1 went out unread on the person's word: %q", body)
	}
}

// A release lists only the merges up to the one it is cut at, so the next
// release lists the later merge once.
func TestReleaseStopsAtItsMerge(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	merged(2, "a2", "m2")
	land("a1", "r1")
	land("a2", "r2")
	m.AfterKept("hive", "m1")
	m.AfterKept("hive", "m2")
	if one, two := n.got["v0.0.1"], n.got["v0.0.2"]; strings.Contains(one, "#2 ") || !strings.Contains(two, "#2 at a2") || strings.Contains(two, "#1 ") {
		t.Fatalf("each release should list its own merges: %q / %q", one, two)
	}
}

// A merge approval of a head never stands for a release of the same commit,
// as it would on a forge that fast-forwards.
func TestMergeApprovalIsNotARelease(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "m1", "m1", "install/root.sh")
	land("m1", "r1")
	m.C.Effects.Approve("merge", "hive", "m1")
	m.AfterKept("hive", "m1")
	if len(n.got) != 0 {
		t.Fatalf("a merge approval released a root change: %v", n.got)
	}
}

// Two callers keeping the same merge at once (the person's answer and the
// improve tick) cut one release, not two for the same tag.
func TestConcurrentKeepsCutOneRelease(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); m.AfterKept("hive", "m1") }()
	}
	wg.Wait()
	if n.calls != 1 {
		t.Fatalf("%d releases cut for one merge", n.calls)
	}
}
