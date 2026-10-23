package release

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// releases is the API's list of me/hive's releases; "" answers 404.
var releases string

// serve is a release site with tag as the latest release of me/hive, holding
// each asset with a checksums.txt over them (lie corrupts the sums). The repo's
// old name, old/name, redirects to it as GitHub does after a rename.
func serve(t *testing.T, tag string, assets map[string]string, lie bool) {
	serveAll(t, tag, map[string]map[string]string{tag: assets}, lie)
}

// serveAll is serve with releases, tag to assets, and latest the newest tag.
func serveAll(t *testing.T, latest string, byTag map[string]map[string]string, lie bool) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		tag, name, _ := strings.Cut(strings.TrimPrefix(p, "/me/hive/releases/download/"), "/")
		assets, known := byTag[tag]
		switch {
		case strings.HasPrefix(p, "/old/name/"):
			http.Redirect(w, r, "/me/hive/"+strings.TrimPrefix(p, "/old/name/"), http.StatusMovedPermanently)
		case p == "/me/hive/releases/latest":
			http.Redirect(w, r, "/me/hive/releases/tag/"+latest, http.StatusFound)
		case strings.HasPrefix(p, "/me/hive/releases/tag/"):
		case p == "/repos/me/hive/releases" && releases != "":
			w.Write([]byte(releases))
		case !strings.HasPrefix(p, "/me/hive/releases/download/") || !known:
			http.NotFound(w, r)
		case name == "checksums.txt":
			for name, body := range assets {
				h := sha256.Sum256([]byte(body))
				if lie {
					h[0]++
				}
				w.Write([]byte(hex.EncodeToString(h[:]) + "  " + name + "\n"))
			}
		case assets[name] != "":
			w.Write([]byte(assets[name]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	site, api, rels := Site, API, releases
	t.Cleanup(func() { Site, API, releases = site, api, rels })
	Site, API = srv.URL, srv.URL
}

// releaseAt is the release module in a build stamped version, following repo,
// whose binary is a file in a temp dir that an update replaces.
func releaseAt(t *testing.T, version, repo string) (*Module, string) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cfg := c.WS.LoadConfig()
	cfg.ReleaseRepo = repo
	if err := c.WS.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, AssetName(runtime.GOOS, runtime.GOARCH))
	os.WriteFile(exe, []byte("old"), 0o755)
	v, e := core.Version, executable
	t.Cleanup(func() { core.Version, executable = v, e })
	core.Version, executable = version, func() string { return exe }
	return &Module{C: c}, exe
}

// A source build (no release version) never replaces itself with a release.
func TestSourceBuildNeverUpdates(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "0.0.0-dev", "me/hive")
	msg, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.Contains(msg, "source build") || string(raw) != "old" {
		t.Fatalf("source build: %q, %v, binary %q", msg, err, raw)
	}
}

// An install that still follows the repo's old name finds the release
// through the rename's redirect, checks it and puts it in place.
func TestUpdateThroughRenamedRepo(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "old/name")
	msg, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v9.9.9\n") || string(raw) != "new" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
	core.Version = "v9.9.9"
	if msg, err := m.Update(""); err != nil || msg != "already on v9.9.9" {
		t.Fatalf("second update: %q, %v", msg, err)
	}
}

const releaseList = `[
 {"tag_name": "v9.9.9", "body": "Faster merges.", "html_url": "https://example.com/me/hive/releases/tag/v9.9.9"},
 {"tag_name": "v9.9.8", "body": "Fixed the lead's chat.", "html_url": "https://example.com/me/hive/releases/tag/v9.9.8"},
 {"tag_name": "v0.1.2", "body": "Already installed.", "html_url": "https://example.com/me/hive/releases/tag/v0.1.2"}
]`

// hive update names the new version's notes and its page, and journals them
// for the control center.
func TestUpdateShowsNotes(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	releases = releaseList
	m, _ := releaseAt(t, "v9.9.8", "me/hive")
	msg, err := m.Update("")
	if err != nil || !strings.Contains(msg, "Faster merges.") || !strings.Contains(msg, "releases/tag/v9.9.9") || strings.Contains(msg, "lead's chat") {
		t.Fatalf("update: %q, %v", msg, err)
	}
	var got struct{ Notes string }
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "hive.updated" {
			e.Decode(&got)
		}
	})
	if !strings.Contains(got.Notes, "Faster merges.") {
		t.Fatalf("journaled notes: %q", got.Notes)
	}
}

// An update that skips versions shows each skipped version's notes too.
func TestUpdateShowsSkippedNotes(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	releases = releaseList
	m, _ := releaseAt(t, "v0.1.2", "me/hive")
	msg, err := m.Update("")
	if err != nil || !strings.Contains(msg, "Faster merges.") || !strings.Contains(msg, "Fixed the lead's chat.") || strings.Contains(msg, "Already installed.") {
		t.Fatalf("update: %q, %v", msg, err)
	}
}

// Notes that can't be fetched still update, and link the releases page.
func TestUpdateWithoutNotesLinksThem(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	releases = ""
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	msg, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err != nil || string(raw) != "new" || !strings.HasSuffix(msg, Site+"/me/hive/releases") {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
}

// A download whose checksum doesn't match is never installed.
func TestChecksumMismatchKeepsBinary(t *testing.T) {
	serve(t, "v9.9.9", build("new"), true)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	_, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), "checksum") || string(raw) != "old" {
		t.Fatalf("mismatch: %v, binary %q", err, raw)
	}
}

// build is a release's binaries for this platform (and WSL), holding body.
func build(body string) map[string]string {
	return map[string]string{AssetName(runtime.GOOS, runtime.GOARCH): body, AssetName("linux", "amd64"): body}
}

// --release installs the tag asked for, an older one too, and pins it; an
// unknown tag or one with no binary for this platform is refused and leaves
// the binary and the pin as they were.
func TestUpdateToAChosenRelease(t *testing.T) {
	serveAll(t, "v0.1.3", map[string]map[string]string{"v0.1.3": build("new"), "v0.1.1": build("older"),
		"v0.1.0": {"hive-plan9-mips": "x"}}, false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	for tag, want := range map[string]string{"v0.0.9": "no release v0.0.9", "v0.1.0": "has no hive-"} {
		_, err := m.Update(tag)
		if raw, _ := os.ReadFile(exe); err == nil || !strings.Contains(err.Error(), want) || string(raw) != "old" {
			t.Fatalf("%s: %v, binary %q", tag, err, raw)
		}
	}
	if pin := m.C.WS.LoadConfig().ReleasePin; pin != "" {
		t.Fatalf("a refused tag pinned %q", pin)
	}
	msg, err := m.Update("v0.1.1")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.Contains(msg, "updated to v0.1.1, pinned") || string(raw) != "older" {
		t.Fatalf("older tag: %q, %v, binary %q", msg, err, raw)
	}
	if cfg := m.C.WS.LoadConfig(); cfg.ReleasePin != "v0.1.1" || cfg.AutoUpdate {
		t.Fatalf("pin %q, auto_update %v", cfg.ReleasePin, cfg.AutoUpdate)
	}
}

// Pinning turns auto_update off, since releases older than the pin read only
// that, and clearing the pin turns it back on only if it was on before.
func TestPinPausesAutoUpdate(t *testing.T) {
	serveAll(t, "v0.1.3", map[string]map[string]string{"v0.1.3": build("new"), "v0.1.1": build("older")}, false)
	for _, was := range []bool{true, false} {
		m, _ := releaseAt(t, "v0.1.2", "me/hive")
		cfg := m.C.WS.LoadConfig()
		cfg.AutoUpdate = was
		m.C.WS.SaveConfig(cfg)
		if msg, err := m.Update("v0.1.1"); err != nil || !strings.Contains(msg, "auto-update off") {
			t.Fatalf("pin: %q, %v", msg, err)
		}
		if cfg := m.C.WS.LoadConfig(); cfg.AutoUpdate {
			t.Fatalf("auto_update on while pinned (was %v)", was)
		}
		if _, err := m.Update(""); err != nil {
			t.Fatal(err)
		}
		if cfg := m.C.WS.LoadConfig(); cfg.ReleasePin != "" || cfg.AutoUpdate != was {
			t.Fatalf("cleared: pin %q, auto_update %v, want %v", cfg.ReleasePin, cfg.AutoUpdate, was)
		}
	}
}

// A pinned Hive doesn't update itself past its pin and says, once, that a
// newer release is out; `hive update` clears the pin and installs the newest.
func TestPinHoldsUntilCleared(t *testing.T) {
	serveAll(t, "v0.1.3", map[string]map[string]string{"v0.1.3": build("new")}, false)
	m, exe := releaseAt(t, "v0.1.1", "me/hive")
	cfg := m.C.WS.LoadConfig()
	cfg.AutoUpdate, cfg.PinnedAuto, cfg.ReleasePin = false, true, "v0.1.1"
	m.C.WS.SaveConfig(cfg)
	for range 2 {
		msg, err := m.AutoUpdate()
		if raw, _ := os.ReadFile(exe); err != nil || !strings.Contains(msg, "pinned to v0.1.1; v0.1.3 is out") || string(raw) != "old" {
			t.Fatalf("pinned auto-update: %q, %v, binary %q", msg, err, raw)
		}
	}
	alerts := 0
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "alert.raised" && strings.Contains(string(e.Data), "v0.1.3 is out") {
			alerts++
		}
	})
	if alerts != 1 {
		t.Fatalf("%d alerts for v0.1.3, want 1", alerts)
	}
	msg, err := m.Update("latest")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.3\n") || string(raw) != "new" {
		t.Fatalf("clearing the pin: %q, %v, binary %q", msg, err, raw)
	}
	if cfg := m.C.WS.LoadConfig(); cfg.ReleasePin != "" || !cfg.AutoUpdate {
		t.Fatalf("pin %q, auto_update %v after update", cfg.ReleasePin, cfg.AutoUpdate)
	}
}

// An automatic update waits while work is mid-merge, then goes ahead.
func TestAutoUpdateWaitsOutAMerge(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	merging := true
	m.Busy = func() bool { return merging }
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "mid-merge") {
		t.Fatalf("updated mid-merge: %q", msg)
	}
	merging = false
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v9.9.9\n") {
		t.Fatalf("after the merge: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary %q", raw)
	}
}

// A binary still called hive-v0 that replaced a v0.1.2 daemon copies itself
// to hive and leaves hive-v0 forwarding there; one called hive, or a hive-v0
// that replaced a newer daemon (installed so on purpose), has nothing to move.
func TestRenameMovesHiveV0ToHive(t *testing.T) {
	a := autostart
	t.Cleanup(func() { autostart = a })
	var started string
	autostart = func(exe, _ string) { started = exe } // never the machine's own autostart
	ext := filepath.Ext(AssetName(runtime.GOOS, "amd64"))
	dir := t.TempDir()
	old := filepath.Join(dir, "hive-v0"+ext)
	os.WriteFile(old, []byte("bin"), 0o755)
	e := executable
	t.Cleanup(func() { executable = e })
	executable = func() string { return old }
	if got := Rename(dir, "v0.1.3"); got != "" || core.Exists(filepath.Join(dir, "hive"+ext)) {
		t.Fatalf("a chosen hive-v0 renamed: %q", got)
	}
	got := Rename(dir, "v0.1.2")
	hive := filepath.Join(dir, "hive"+ext)
	if raw, _ := os.ReadFile(hive); got != hive || string(raw) != "bin" || started != hive {
		t.Fatalf("rename: %q, hive %q, autostart %q", got, raw, started)
	}
	alias := old
	if runtime.GOOS == "windows" {
		alias = filepath.Join(dir, "hive-v0.cmd")
	} else if l, _ := os.Readlink(old); l != "hive" {
		t.Fatalf("hive-v0 links to %q", l)
	}
	if !core.Exists(alias) {
		t.Fatal("no hive-v0 alias")
	}
	executable = func() string { return hive }
	if got := Rename(dir, "v0.1.2"); got != "" {
		t.Fatalf("hive renamed again: %q", got)
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
	m, _ := releaseAt(t, "v0.1.0", "me/hive")
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
	if m.C.Effects.Approved("release", "hive", "m2") {
		t.Fatal("a stale answer left an approval for m2")
	}
	if !journaled(m, "release.superseded") {
		t.Fatal("the stale answer was not recorded as superseded")
	}
}

func journaled(m *Module, kind string) (found bool) {
	m.C.J.Replay(func(e core.Event) { found = found || e.Kind == kind })
	return
}

// The Hive's own supersede is marked as the Hive's, not the person's word.
func TestSupersedeIsTheHives(t *testing.T) {
	m, _, merged, land := selfHive(t)
	merged(2, "a2", "m2", "install/root.sh")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	old := m.C.Person.Open()[0]
	merged(3, "a3", "m3", "install/root.sh")
	land("a3", "r3")
	m.AfterKept("hive", "m3")
	got, _ := m.C.Person.Get(old.ID)
	if got.Answer != "superseded" || !strings.HasPrefix(got.Note, "the Hive") {
		t.Fatalf("the supersede should say it is the Hive's: %+v", got)
	}
}

// A sha no journaled merge made covers nothing: no release, no approval.
func TestUnknownMergeRefused(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "outside")
	if len(n.got) != 0 {
		t.Fatalf("released a merge the Hive never made: %v", n.got)
	}
	m.OnPerson(core.Ask{Kind: "release", Ref: "hive@outside", Project: "hive", Answer: "release"})
	if m.C.Effects.Approved("release", "hive", "outside") {
		t.Fatal("approved a release of an unknown merge")
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

// A late answer for m2 that arrives while a release at m3 is being cut waits
// for it, then finds m2 covered: no approval is left behind for m2. The test
// holds the lock before the goroutine starts, so the answer cannot get ahead.
func TestLateAnswerWaitsForTheRelease(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(2, "a2", "m2", "install/root.sh")
	merged(3, "a3", "m3", "install/root.sh")
	land("a2", "r2")
	land("a3", "r3")
	m.C.Effects.Approve("release", "hive", "m3")
	unlock := m.lock("hive")
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.OnPerson(core.Ask{Kind: "release", Ref: "hive@m2", Project: "hive", Answer: "release"})
	}()
	m.afterKept("hive", "m3")
	unlock()
	<-done
	if n.calls != 1 || m.C.Effects.Approved("release", "hive", "m2") || !journaled(m, "release.superseded") {
		t.Fatalf("the late answer should be superseded: %d releases, m2 approved %v", n.calls, m.C.Effects.Approved("release", "hive", "m2"))
	}
}
