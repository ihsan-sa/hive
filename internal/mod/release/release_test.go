package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
			w.Write(checksums(assets, lie))
		case name == SigName && signWith != nil:
			sig, err := Sign(signWith, signFor+tag, checksums(assets, lie))
			if err != nil {
				t.Error(err)
			}
			w.Write([]byte(sig + "\n"))
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

// signWith is the seed the release site signs with, none when nil; signFor
// prefixes the tag it signs for.
var signWith []byte
var signFor string

// checksums is a checksums.txt over assets, in name order.
func checksums(assets map[string]string, lie bool) []byte {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(assets)) {
		h := sha256.Sum256([]byte(assets[name]))
		if lie {
			h[0]++
		}
		b.WriteString(hex.EncodeToString(h[:]) + "  " + name + "\n")
	}
	return []byte(b.String())
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
	v, e, try := core.Version, executable, tryBinary
	t.Cleanup(func() { core.Version, executable, tryBinary = v, e, try })
	core.Version, executable = version, func() string { return exe }
	tryBinary = func(string, string) error { return nil } // the binaries are text
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

// An automatic update pauses merges for the swap: one still running past the
// wait leaves the binary alone and lets merges go on; once the running ones
// end it installs and keeps merges paused for the restart.
func TestAutoUpdateWaitsOutAMerge(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	running, paused := true, false
	var waited time.Duration
	m.PauseMerges = func(wait time.Duration) (func(), bool) {
		waited, paused = wait, true
		return func() { paused = false }, !running
	}
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "a merge has run past") || paused || waited != mergeWait {
		t.Fatalf("updated mid-merge: %q, merges paused %v, waited %v", msg, paused, waited)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" {
		t.Fatalf("binary %q while a merge ran", raw)
	}
	running = false
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v9.9.9\n") || !paused {
		t.Fatalf("after the merge: %q, %v, merges paused for the restart %v", msg, err, paused)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "new" {
		t.Fatalf("binary %q", raw)
	}
}

// Nothing newer pauses no merge: a Hive looking every few minutes doesn't
// stall its merges to find it is current.
func TestCurrentHiveDoesNotPauseMerges(t *testing.T) {
	serve(t, "v0.1.2", build("new"), false)
	m, _ := releaseAt(t, "v0.1.2", "me/hive")
	m.PauseMerges = func(time.Duration) (func(), bool) {
		t.Fatal("paused merges with nothing to install")
		return nil, false
	}
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "already on") {
		t.Fatalf("%q, %v", msg, err)
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
	// listed runs after Tags lists them, as a push before CreateRelease would.
	listed func()
	// tagErr, when set, is what TagCommit fails with.
	tagErr error
}

func (n *notes) TagCommit(repo, tag string) (string, error) {
	if n.tagErr != nil {
		return "", n.tagErr
	}
	return n.Local.TagCommit(repo, tag)
}

func (n *notes) Tags(repo string) ([]string, error) {
	tags, err := n.Local.Tags(repo)
	if n.listed != nil {
		n.listed()
	}
	return tags, err
}

func (n *notes) CreateRelease(repo, tag, sha, body string) error {
	n.got[tag] = body
	n.calls++
	return n.Local.CreateRelease(repo, tag, sha, body)
}

// selfHive is a self project "hive" whose releases' notes are kept, with
// helpers to journal a merge and a reviewer's land on a head.
func selfHive(t *testing.T) (*Module, *notes, func(int, string, string, ...string), func(string, string)) {
	m, _ := releaseAt(t, "0.0.0-dev", "me/hive") // a source build, so tags count from v0.0.0
	n := &notes{Local: m.C.Effects.Forge.(*core.Local), got: map[string]string{}}
	m.C.Effects.Forge = n
	m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true, Branch: "main"})
	// A clone whose main has no steward record, which the release reads.
	if _, err := n.Local.EnsureRepo("me/hive", "", false); err != nil {
		t.Fatal(err)
	}
	seed := t.TempDir()
	for _, args := range [][]string{{"clone", "-q", n.Local.RepoPath("me/hive"), seed},
		{"-C", seed, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "seed"}, {"-C", seed, "push", "-q", "origin", "HEAD:main"}} {
		if _, err := core.Git("", args...); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := m.C.Projects.Get("hive")
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	m.Scan = func(core.Project, string) error { return nil } // scan_test.go tests the scan
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

// seated journals a security seat's verdict on the own diff diff.
func seated(m *Module, sha, diff, seat, verdict string) {
	m.C.J.MustAppend("effect.verdict", core.Verdict{Project: "hive", SHA: sha, Run: "s-" + seat, Verdict: verdict, Diff: diff, Seat: seat})
}

// A merge to a root path whose own diff the security seat and a majority of
// the panel passed is released by the Hive itself, and the person is told
// after instead of asked.
func TestRootReleaseOnTheHivesReads(t *testing.T) {
	m, n, _, land := selfHive(t)
	m.C.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": 2, "sha": "a2", "merge_sha": "m2", "root": []string{"install/root.sh"}, "diff": "d2"})
	land("a2", "r2")
	seated(m, "a2", "d2", core.SeatSecurity, "land")
	seated(m, "a2", "d2", core.PanelSeat(0), "land")
	m.AfterKept("hive", "m2")
	if len(n.got) != 0 || len(m.C.Person.Open()) != 1 {
		t.Fatalf("one panel land of three released a root change: %v", n.got)
	}
	seated(m, "a2", "d2", core.PanelSeat(1), "land")
	m.AfterKept("hive", "m2")
	body := n.got["v0.0.1"]
	if !strings.Contains(body, "install/root.sh) passed the security seat") || strings.Contains(body, "The person released it") {
		t.Fatalf("the release should say the Hive's reads passed its root paths: %q", body)
	}
	if open := m.C.Person.Open(); len(open) != 0 {
		t.Fatalf("the earlier release ask should be answered by the release: %+v", open)
	}
	told := m.C.Person.Told(5)
	if len(told) != 1 || !strings.Contains(told[0].Title, "Released v0.0.1") || !strings.Contains(told[0].Title, "#2 (install/root.sh)") {
		t.Fatalf("the person should be told after: %+v", told)
	}
}

// A root path no security read passed (here no seat read its diff at all)
// holds the release for the person.
func TestRootPathNoSeatReadsHolds(t *testing.T) {
	m, n, _, land := selfHive(t)
	m.C.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": 4, "sha": "a4", "merge_sha": "m4", "root": []string{".github/CODEOWNERS"}, "diff": "d4"})
	land("a4", "r4")
	m.AfterKept("hive", "m4")
	asks := m.C.Person.Open()
	if len(n.got) != 0 || len(asks) != 1 || !strings.Contains(asks[0].Title, ".github/CODEOWNERS run with elevated rights and no security read passed them") {
		t.Fatalf("a root path no seat read should hold for the person: %v %+v", n.got, asks)
	}
	if len(m.C.Person.Told(5)) != 0 {
		t.Fatal("told the person about a release that didn't go out")
	}
}

// A security read that ran and didn't pass holds the release: an outside fix
// outweighs the seats' and panel's land, and a security seat's fix isn't
// outweighed by the panel's majority.
func TestRootReadThatFailedHolds(t *testing.T) {
	for name, read := range map[string]func(m *Module){
		"outside fix": func(m *Module) {
			for _, seat := range []string{core.SeatSecurity, core.PanelSeat(0), core.PanelSeat(1), core.PanelSeat(2)} {
				seated(m, "a2", "d2", seat, "land")
			}
			m.C.J.MustAppend("effect.read", core.OutsideRead{Project: "hive", SHA: "a2", Diff: "d2", By: "perms", Verdict: "fix"})
		},
		"seat fix": func(m *Module) {
			seated(m, "a2", "d2", core.SeatSecurity, "fix")
			for _, seat := range []string{core.PanelSeat(0), core.PanelSeat(1), core.PanelSeat(2)} {
				seated(m, "a2", "d2", seat, "land")
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, n, _, land := selfHive(t)
			m.C.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": 2, "sha": "a2", "merge_sha": "m2", "root": []string{"install/root.sh"}, "diff": "d2"})
			land("a2", "r2")
			read(m)
			m.AfterKept("hive", "m2")
			asks := m.C.Person.Open()
			if len(n.got) != 0 || len(asks) != 1 || !strings.Contains(asks[0].Title, "install/root.sh run with elevated rights") {
				t.Fatalf("a root path whose security read said fix released: %v %+v", n.got, asks)
			}
			if len(m.C.Person.Told(5)) != 0 {
				t.Fatal("told the person about a release that didn't go out")
			}
		})
	}
}

// The seats read a merge's whole own diff, so their land passes every root
// path in it, one on the security list and one only in the root class alike;
// an outside land on its own does too.
func TestRootReadCoversTheWholeDiff(t *testing.T) {
	m, n, _, land := selfHive(t)
	m.C.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": 2, "sha": "a2", "merge_sha": "m2", "root": []string{"install/root.sh", ".github/CODEOWNERS"}, "diff": "d2"})
	land("a2", "r2")
	for _, seat := range []string{core.SeatSecurity, core.PanelSeat(0), core.PanelSeat(1)} {
		seated(m, "a2", "d2", seat, "land")
	}
	m.C.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": 3, "sha": "a3", "merge_sha": "m3", "root": []string{".github/CODEOWNERS"}, "diff": "d3"})
	land("a3", "r3")
	m.C.J.MustAppend("effect.read", core.OutsideRead{Project: "hive", SHA: "a3", Diff: "d3", By: "architect", Verdict: "land"})
	m.AfterKept("hive", "m3")
	body := n.got["v0.0.1"]
	if !strings.Contains(body, "(install/root.sh, .github/CODEOWNERS) passed the security seat") || !strings.Contains(body, "(.github/CODEOWNERS) passed the box's architect read") {
		t.Fatalf("both merges' root paths should pass on their reads: %v", n.got)
	}
}

// A root merge recorded before effect.merged carried its diff can't be matched
// to any read, so it holds, and the ask says why.
func TestRootMergeWithNoDiffHolds(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(2, "a2", "m2", "install/root.sh")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	asks := m.C.Person.Open()
	if len(n.got) != 0 || len(asks) != 1 || !strings.Contains(asks[0].Title, "merge record names no diff") {
		t.Fatalf("a root merge with no diff should hold and say why: %v %+v", n.got, asks)
	}
}

// A cut answers only the release asks at merges it covers: one at a commit
// no release covered, or another project's, stays open.
func TestCutAnswersOnlyCoveredAsks(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.C.Person.Ask(core.Ask{Kind: "release", Ref: "hive@m9", Project: "hive", Title: "stray", Options: []string{"release", "hold"}})
	m.C.Person.Ask(core.Ask{Kind: "release", Ref: "other@m2", Project: "other", Title: "other", Options: []string{"release", "hold"}})
	m.AfterKept("hive", "m2")
	if len(n.got) != 1 {
		t.Fatalf("no release: %v", n.got)
	}
	if open := m.C.Person.Open(); len(open) != 2 {
		t.Fatalf("the cut answered asks it doesn't cover: %+v", open)
	}
}

// A release with an unread merge still waits for the person, even when every
// root path in it passed the Hive's security reads.
func TestUnreadMergeStillHeldWithRootRead(t *testing.T) {
	m, n, merged, land := selfHive(t)
	m.C.J.MustAppend("effect.merged", map[string]any{"project": "hive", "pr": 2, "sha": "a2", "merge_sha": "m2", "root": []string{"install/root.sh"}, "diff": "d2"})
	land("a2", "r2")
	for _, seat := range []string{core.SeatSecurity, core.PanelSeat(0), core.PanelSeat(1), core.PanelSeat(2)} {
		seated(m, "a2", "d2", seat, "land")
	}
	merged(3, "a3", "m3")
	m.AfterKept("hive", "m3")
	asks := m.C.Person.Open()
	if len(n.got) != 0 || len(asks) != 1 || !strings.Contains(asks[0].Title, "#3 at a3 merged without a read") || strings.Contains(asks[0].Title, "elevated") {
		t.Fatalf("an unread merge should hold the release for the person, on that alone: %v %+v", n.got, asks)
	}
	if len(m.C.Person.Told(5)) != 0 {
		t.Fatal("told the person about a release that didn't go out")
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

func journaledN(m *Module, kind string) (n int) {
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == kind {
			n++
		}
	})
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
	if got.Answer != "hold" || !strings.HasPrefix(got.Note, "the Hive") {
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

// releaseSite is a release site where each repo's latest release is tags[repo];
// a repo in bare has a release whose checksums list no binary.
func releaseSite(t *testing.T, tags map[string]string, bare ...string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for repo, tag := range tags {
			switch r.URL.Path {
			case "/" + repo + "/releases/latest":
				http.Redirect(w, r, "/"+repo+"/releases/tag/"+tag, http.StatusFound)
				return
			case "/" + repo + "/releases/tag/" + tag:
				return
			case "/" + repo + "/releases/download/" + tag + "/checksums.txt":
				if !slices.Contains(bare, repo) {
					w.Write([]byte("00  " + AssetName(runtime.GOOS, runtime.GOARCH) + "\n"))
				}
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	site := Site
	t.Cleanup(func() { Site = site })
	Site = srv.URL
}

// A release the person's Hive cut on its own fork is the next update when it
// is newer than the Hive's own releases.
func TestCheckFollowsTheFork(t *testing.T) {
	releaseSite(t, map[string]string{"me/hive": "v9.9.9", "local/hive": "v9.9.10"})
	m, _ := releaseAt(t, "v0.1.0", "me/hive")
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "local/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if repo, tag, ok, err := m.Check(); err != nil || !ok || repo != "local/hive" || tag != "v9.9.10" {
		t.Fatalf("check: %q %q %v %v", repo, tag, ok, err)
	}
}

// Only release_repo and the person's fork of it are update sources: a Hive
// project an agent pointed at another repository is never followed, and a
// release without this platform's binary is passed over.
func TestCheckFollowsOnlyTheHivesOwnRepository(t *testing.T) {
	releaseSite(t, map[string]string{"me/hive": "v9.9.9", "evil/hive": "v99.0.0", "local/hive": "v9.9.10"}, "local/hive")
	m, _ := releaseAt(t, "v0.1.0", "me/hive")
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "evil/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if repo, tag, _, err := m.Check(); err != nil || repo != "me/hive" || tag != "v9.9.9" {
		t.Fatalf("check followed %q %q (%v), want the Hive's own v9.9.9", repo, tag, err)
	}
	if _, err := m.C.Projects.Add(core.Project{Name: "hive-fork", Repo: "local/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if repo, tag, _, err := m.Check(); err != nil || tag != "v9.9.9" {
		t.Fatalf("check took the release with no binaries: %q %q %v", repo, tag, err)
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

// A release is cut as a pre-release and becomes the latest only once ci and
// update pass on its commit; a red run keeps it a pre-release and raises one
// alert, and a re-run that passes publishes it.
func TestReleasePublishesOnlyAfterItsGates(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	latest := func() core.Release {
		t.Helper()
		rel, err := n.LatestRelease("me/hive")
		if err != nil {
			t.Fatal(err)
		}
		return rel
	}
	if rel := latest(); rel.Tag != "v0.0.1" || !rel.Prerelease {
		t.Fatalf("the release wasn't cut as a pre-release: %+v", rel)
	}
	alerts := func() (k int) {
		m.C.J.Replay(func(e core.Event) {
			if e.Kind == "alert.raised" && strings.Contains(string(e.Data), "v0.0.1") {
				k++
			}
		})
		return k
	}
	run := func(name, status, conclusion string) core.WorkflowRun {
		return core.WorkflowRun{Name: name, Status: status, Conclusion: conclusion, URL: "https://ci/" + name}
	}
	steps := []struct {
		runs   []core.WorkflowRun
		says   string
		pre    bool
		alerts int
	}{
		{nil, "waiting for ci (no run yet), update (no run yet)", true, 0},
		{[]core.WorkflowRun{run("update", "in_progress", ""), run("ci", "completed", "success")}, "waiting for update", true, 0},
		{[]core.WorkflowRun{run("update", "completed", "failure"), run("ci", "completed", "success")}, "update failure (https://ci/update)", true, 1},
		{[]core.WorkflowRun{run("update", "completed", "failure"), run("ci", "completed", "success")}, "update failure", true, 1},
		// a re-run: the newest run of each workflow counts
		{[]core.WorkflowRun{run("update", "completed", "success"), run("update", "completed", "failure"), run("ci", "completed", "success")}, "published v0.0.1", false, 1},
		{nil, "nothing to publish", false, 1},
	}
	for i, s := range steps {
		n.SetReleaseRuns("me/hive", "m1", s.runs...)
		got := m.Publish()
		if !strings.Contains(got, s.says) || latest().Prerelease != s.pre || alerts() != s.alerts {
			t.Fatalf("step %d: Publish said %q; pre-release %v; %d alerts; want %q, %v, %d", i, got, latest().Prerelease, alerts(), s.says, s.pre, s.alerts)
		}
	}
	// The next release numbers on from the newest, a pre-release included.
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	if rel := latest(); rel.Tag != "v0.0.2" || !rel.Prerelease {
		t.Fatalf("the next release is %+v", rel)
	}
}

// v0.1.73 to v0.1.78: release.yml failed at its scan job, so the tags had no
// binaries and update failed waiting for them. Publish names the release run
// and its scan job, not update, and the cut stops until that run passes.
func TestFailedReleaseRunStopsTheCut(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	n.SetReleaseRuns("me/hive", "m1",
		core.WorkflowRun{ID: 7, Name: "release", Status: "completed", Conclusion: "failure", URL: "https://ci/release"},
		core.WorkflowRun{Name: "update", Status: "completed", Conclusion: "failure", URL: "https://ci/update"},
		core.WorkflowRun{Name: "ci", Status: "completed", Conclusion: "success"})
	n.SetRunJobs("me/hive", 7,
		core.WorkflowJob{Name: "scan", Status: "completed", Conclusion: "failure"},
		core.WorkflowJob{Name: "build", Status: "completed", Conclusion: "skipped"})
	alerts := func() (out []string) {
		m.C.J.Replay(func(e core.Event) {
			var a struct{ Text string }
			if e.Kind == "alert.raised" && e.Decode(&a) == nil {
				out = append(out, a.Text)
			}
		})
		return out
	}
	if got := m.Publish(); !strings.Contains(got, "release run failure at its scan job (https://ci/release)") || strings.Contains(got, "update") {
		t.Fatalf("Publish said %q", got)
	}
	a := alerts()
	if len(a) != 1 || !strings.Contains(a[0], "v0.0.1 has no binaries") || !strings.Contains(a[0], "HIVE_SCAN_HANDLES") || strings.Contains(a[0], "A re-run that passes") {
		t.Fatalf("alerts %q", a)
	}
	m.Publish()
	if len(alerts()) != 1 {
		t.Fatalf("the failure was raised again: %q", alerts())
	}

	changes := map[string][]string{"m2": {"README.md", "cmd/hive/main_test.go"}, "m3": {"internal/mod/release/release.go"}}
	m.Diff = func(_ core.Project, from, to string) ([]string, error) {
		if from != "m1" {
			t.Fatalf("diffed from %s", from)
		}
		return changes[to], nil
	}
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	if _, ok := n.got["v0.0.2"]; ok {
		t.Fatal("cut another release while the last one's release run failed")
	}
	if a := alerts(); len(a) != 2 || !strings.Contains(a[1], "no release at m2: v0.0.1 has no binaries, since its release run failure at its scan job") {
		t.Fatalf("alerts %q", a)
	}
	m.AfterKept("hive", "m2")
	held := 0
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "release.held" {
			held++
		}
	})
	if held != 1 {
		t.Fatalf("%d release.held events", held)
	}
	if a := alerts(); len(a) != 2 {
		t.Fatalf("each held merge raised the hold again: %q", a)
	}

	asks := m.C.Person.Open()
	if len(asks) != 1 || asks[0].Kind != "release" || !strings.Contains(asks[0].Title, "has no binaries") {
		t.Fatalf("the hold asks %+v", asks)
	}

	// A build failure fixed in the code: a re-run of v0.0.1 can never pass,
	// so the first commit that changes what the build reads is cut.
	n.SetRunJobs("me/hive", 7, core.WorkflowJob{Name: "build", Status: "completed", Conclusion: "failure"})
	m.AfterKept("hive", "m2")
	if _, ok := n.got["v0.0.2"]; ok {
		t.Fatal("a test-only change was cut over a failed build")
	}
	merged(3, "a3", "m3")
	land("a3", "r3")
	m.AfterKept("hive", "m3")
	if _, ok := n.got["v0.0.2"]; !ok {
		t.Fatal("the fix for a failed build was never released")
	}
}

// The person's "release" on the hold's ask cuts one though the newest
// release's run failed and nothing it reads changed.
// v0.1.111's release.failed came from a daemon that blamed update; the
// corrected alert that names the release run still goes out once.
func TestUnbuiltCorrectsAnOldUpdateBlame(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	m.C.J.MustAppend("release.failed", map[string]string{"project": "hive", "tag": "v0.0.1", "sha": "m1", "why": "update failure"})
	n.SetReleaseRuns("me/hive", "m1", core.WorkflowRun{ID: 7, Name: "release", Status: "completed", Conclusion: "failure", URL: "https://ci/release"})
	m.Publish()
	m.Publish()
	var said []string
	m.C.J.Replay(func(e core.Event) {
		var a struct{ Text string }
		if e.Kind == "alert.raised" && e.Decode(&a) == nil {
			said = append(said, a.Text)
		}
	})
	if len(said) != 1 || !strings.Contains(said[0], "v0.0.1 has no binaries") {
		t.Fatalf("alerts %q", said)
	}
}

func TestPersonReleasesPastAFailedReleaseRun(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	n.SetReleaseRuns("me/hive", "m1", core.WorkflowRun{ID: 7, Name: "release", Status: "completed", Conclusion: "cancelled"})
	m.Diff = func(core.Project, string, string) ([]string, error) { return []string{"README.md"}, nil }
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	asks := m.C.Person.Open()
	if _, ok := n.got["v0.0.2"]; ok || len(asks) != 1 {
		t.Fatalf("cut %v, asks %+v", n.got, asks)
	}
	asks[0].Answer = "release"
	m.OnPerson(asks[0])
	if _, ok := n.got["v0.0.2"]; !ok {
		t.Fatal("the person's release didn't cut")
	}
}

func TestFailedReleaseRunRerunLiftsTheHold(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	n.SetReleaseRuns("me/hive", "m1", core.WorkflowRun{ID: 7, Name: "release", Status: "completed", Conclusion: "failure"})
	m.Diff = func(core.Project, string, string) ([]string, error) { return []string{"README.md"}, nil }
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	if _, ok := n.got["v0.0.2"]; ok {
		t.Fatal("cut while the release run failed")
	}
	// The person sets the secrets and re-runs release, then update: it is
	// published and the cut goes on.
	n.SetReleaseRuns("me/hive", "m1",
		core.WorkflowRun{ID: 8, Name: "release", Status: "completed", Conclusion: "success"},
		core.WorkflowRun{Name: "update", Status: "completed", Conclusion: "success"},
		core.WorkflowRun{Name: "ci", Status: "completed", Conclusion: "success"})
	if got := m.Publish(); got != "published v0.0.1" {
		t.Fatalf("Publish said %q", got)
	}
	m.AfterKept("hive", "m2")
	if _, ok := n.got["v0.0.2"]; !ok {
		t.Fatal("the cut didn't go on once the release run passed")
	}
}

// Of the pre-releases waiting, the newest that passed is published; an older
// one still waiting stays a pre-release and is no longer checked.
func TestPublishSkipsOlderPreReleases(t *testing.T) {
	m, n, merged, land := selfHive(t)
	for i, sha := range []string{"m1", "m2"} {
		merged(i+1, "a"+sha, sha)
		land("a"+sha, "r"+sha)
		m.AfterKept("hive", sha)
	}
	green := []core.WorkflowRun{{Name: "ci", Status: "completed", Conclusion: "success"}, {Name: "update", Status: "completed", Conclusion: "success"}}
	n.SetReleaseRuns("me/hive", "m2", green...)
	if got := m.Publish(); got != "published v0.0.2" {
		t.Fatalf("Publish said %q", got)
	}
	n.SetReleaseRuns("me/hive", "m1", green...)
	if got := m.Publish(); got != "nothing to publish" {
		t.Fatalf("an older release would be published over a newer one: %q", got)
	}
	if rel, _ := n.LatestRelease("me/hive"); rel.Tag != "v0.0.2" || rel.Prerelease {
		t.Fatalf("latest is %+v", rel)
	}
}

// The person marked v0.1.3 the latest after v0.1.4 to v0.1.6 went out as
// pre-releases: the next release takes v0.1.7, not v0.1.4 again (which
// GitHub refuses as taken).
func TestReleaseNumbersPastEveryTag(t *testing.T) {
	m, n, merged, land := selfHive(t)
	for _, tag := range []string{"v0.1.4", "v0.1.6", "v0.1.5", "probe-1"} {
		n.AddRelease("me/hive", core.Release{Tag: tag, Prerelease: true})
	}
	n.AddRelease("me/hive", core.Release{Tag: "v0.1.3"})
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	if rel, _ := n.LatestRelease("me/hive"); rel.Tag != "v0.1.7" || !rel.Prerelease {
		t.Fatalf("the next release is %+v; want the pre-release v0.1.7", rel)
	}
}

func TestNextTag(t *testing.T) {
	for _, c := range []struct {
		tags []string
		want string
	}{
		{nil, "v0.0.1"},
		{[]string{"dev", "probe-3"}, "v0.0.1"},
		{[]string{"v0.1.3", "v0.1.6", "v0.1.4", "v0.1.10", "v0.1.9"}, "v0.1.11"},
		{[]string{"v0.2.0", "v0.1.9"}, "v0.2.1"},
	} {
		if got := nextTag(c.tags); got != c.want {
			t.Errorf("nextTag(%v) = %s; want %s", c.tags, got, c.want)
		}
	}
}

// A gate run that was skipped or cancelled didn't pass: the release stays a
// pre-release, as for a failure.
func TestSkippedOrCancelledGateIsNotAPass(t *testing.T) {
	m, n, _, _ := selfHive(t)
	for _, conclusion := range []string{"skipped", "cancelled", "neutral", ""} {
		n.SetReleaseRuns("me/hive", "m1",
			core.WorkflowRun{Name: "ci", Status: "completed", Conclusion: "success"},
			core.WorkflowRun{Name: "update", Status: "completed", Conclusion: conclusion, URL: "https://ci/update"})
		if state, why := m.gates("me/hive", "m1"); state != "failed" || !strings.Contains(why, "update "+conclusion) {
			t.Fatalf("update %q: %s, %s", conclusion, state, why)
		}
	}
}

// A tag pushed between the listing and CreateRelease keeps its own commit, so
// the release would build and sign that code: it is refused, never published,
// and the merges it named go out in the next release.
func TestReleaseRefusesATagAtAnotherCommit(t *testing.T) {
	m, n, merged, land := selfHive(t)
	// The push races the cut's own listing, after the one tagged reads.
	n.listed = func() { n.listed = func() { n.AddTag("me/hive", "v0.0.1", "evil") } }
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	n.listed = nil
	if !journaled(m, "release.refused") || journaled(m, "hive.released") {
		t.Fatal("a release whose tag points elsewhere was recorded as released")
	}
	if a := alertText(m); !strings.Contains(a, "release-signing") || !strings.Contains(a, "reject it") {
		t.Fatalf("the refusal doesn't say to reject the signing run: %q", a)
	}
	n.SetReleaseRuns("me/hive", "m1", core.WorkflowRun{Name: "ci", Status: "completed", Conclusion: "success"},
		core.WorkflowRun{Name: "update", Status: "completed", Conclusion: "success"})
	if got := m.Publish(); got != "nothing to publish" {
		t.Fatalf("Publish said %q", got)
	}
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	if body := n.got["v0.0.2"]; !strings.Contains(body, "#1 at a1") || !strings.Contains(body, "#2 at a2") {
		t.Fatalf("the next release should name #1 and #2: %q", body)
	}
}

// A tag moved after its release was cut is checked again before publishing.
func TestPublishRefusesAMovedTag(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	n.AddTag("me/hive", "v0.0.1", "evil")
	n.SetReleaseRuns("me/hive", "m1", core.WorkflowRun{Name: "ci", Status: "completed", Conclusion: "success"},
		core.WorkflowRun{Name: "update", Status: "completed", Conclusion: "success"})
	if got := m.Publish(); !strings.Contains(got, "refused") {
		t.Fatalf("Publish said %q", got)
	}
	if a := alertText(m); !strings.Contains(a, "reject it") {
		t.Fatalf("the refusal doesn't say to reject the signing run: %q", a)
	}
	if rel, _ := n.LatestRelease("me/hive"); !rel.Prerelease {
		t.Fatalf("a release whose tag moved was published: %+v", rel)
	}
	if got := m.Publish(); got != "nothing to publish" {
		t.Fatalf("a refused release is checked again: %q", got)
	}
}

// alertText is the last alert raised.
func alertText(m *Module) (text string) {
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "alert.raised" {
			var x struct{ Text string }
			json.Unmarshal(e.Data, &x)
			text = x.Text
		}
	})
	return
}

// A tag that can't be read after the release is cut is said, and the record
// leaves tag_sha out rather than holding an empty one; Publish waits for it.
func TestReleaseSaysAnUnreadTag(t *testing.T) {
	m, n, merged, land := selfHive(t)
	n.tagErr = errors.New("gh api: HTTP 502")
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	var rec map[string]any
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "hive.released" {
			json.Unmarshal(e.Data, &rec)
		}
	})
	if rec == nil {
		t.Fatal("no hive.released")
	}
	if _, ok := rec["tag_sha"]; ok || !strings.Contains(fmt.Sprint(rec["tag_unread"]), "502") {
		t.Fatalf("record %v", rec)
	}
	if a := alertText(m); !strings.Contains(a, "can't read its tag") {
		t.Fatalf("alert %q", a)
	}
	n.SetReleaseRuns("me/hive", "m1", core.WorkflowRun{Name: "ci", Status: "completed", Conclusion: "success"},
		core.WorkflowRun{Name: "update", Status: "completed", Conclusion: "success"})
	if got := m.Publish(); !strings.Contains(got, "can't read") {
		t.Fatalf("Publish said %q", got)
	}
	n.tagErr = nil
	if got := m.Publish(); got != "published v0.0.1" {
		t.Fatalf("Publish said %q once the tag read", got)
	}
}

// A merge that runs past the wait look after look is hung, not slow: after
// maxSkips looks the update installs anyway, each skipped look on the record.
func TestAutoUpdateGoesAheadPastAHungMerge(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, exe := releaseAt(t, "v0.1.2", "me/hive")
	m.PauseMerges = func(time.Duration) (func(), bool) { return func() {}, false }
	for i := 1; i < maxSkips; i++ {
		if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "a merge has run past") {
			t.Fatalf("look %d: %q", i, msg)
		}
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" {
		t.Fatalf("installed before %d looks: %q", maxSkips, raw)
	}
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v9.9.9\n") {
		t.Fatalf("look %d: %q, %v", maxSkips, msg, err)
	}
	if n := journaledN(m, "hive.update_held"); n != maxSkips {
		t.Fatalf("%d skipped looks on the record, want %d", n, maxSkips)
	}
}

// When the restart after an install never comes, merges go on after the
// grace, and the record says so.
func TestMissedRestartIsRecorded(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	m, _ := releaseAt(t, "v0.1.2", "me/hive")
	old := restartGrace
	restartGrace = 10 * time.Millisecond
	t.Cleanup(func() { restartGrace = old })
	resumed := make(chan struct{})
	m.PauseMerges = func(time.Duration) (func(), bool) { return func() { close(resumed) }, true }
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v9.9.9\n") {
		t.Fatalf("%q, %v", msg, err)
	}
	select {
	case <-resumed:
	case <-time.After(5 * time.Second):
		t.Fatal("merges stayed paused past the grace")
	}
	if !journaled(m, "hive.update_unrestarted") {
		t.Fatal("the missed restart isn't on the record")
	}
}

// On the latest channel, the lag counts every full release between this one
// and the newest, as following pre-releases does.
func TestLatestChannelCountsEveryReleaseBehind(t *testing.T) {
	serveAll(t, "v0.1.12", map[string]map[string]string{"v0.1.12": build("v12")}, false)
	m, _ := releaseAt(t, "v0.1.10", "me/hive")
	f := m.C.Effects.Forge.(*core.Local)
	for _, tag := range []string{"v0.1.10", "v0.1.11", "v0.1.12", "v0.1.13"} {
		f.CreateRelease("me/hive", tag, "sha-"+tag, "")
		if tag != "v0.1.13" {
			f.PublishRelease("me/hive", tag)
		}
	}
	if _, _, _, err := m.Check(); err != nil {
		t.Fatal(err)
	}
	if newest, behind := m.Lag(); newest != "v0.1.12" || behind != 2 {
		t.Fatalf("newest %q, %d behind; want v0.1.12, 2", newest, behind)
	}
}

// Following pre-releases, the lag counts from this one, so the ones before
// the latest full release count too.
func TestPreReleaseLagCountsFromThisOne(t *testing.T) {
	if runner() == "" {
		t.Skip("the checks don't run on " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	serveAll(t, "v0.1.12", map[string]map[string]string{"v0.1.12": build("v12")}, false)
	m, _ := releaseAt(t, "v0.1.10", "me/hive")
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	f := m.C.Effects.Forge.(*core.Local)
	for _, tag := range []string{"v0.1.11", "v0.1.12", "v0.1.13"} {
		f.CreateRelease("me/hive", tag, "sha-"+tag, "")
	}
	f.PublishRelease("me/hive", "v0.1.12")
	if _, _, _, err := m.Check(); err != nil {
		t.Fatal(err)
	}
	if newest, behind := m.Lag(); newest != "v0.1.13" || behind != 3 {
		t.Fatalf("newest %q, %d behind; want v0.1.13, 3", newest, behind)
	}
}

// A Hive the release repository's record doesn't name as its steward
// neither cuts a release there nor publishes one; the steward does.
func TestNonStewardDoesNotRelease(t *testing.T) {
	m, n, merged, land := selfHive(t)
	f := n.Local
	if _, err := f.EnsureRepo("me/hive", "", false); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	for _, args := range [][]string{{"clone", "-q", f.RepoPath("me/hive"), tmp}} {
		if _, err := core.Git("", args...); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(tmp, ".hive"), 0o755)
	os.WriteFile(filepath.Join(tmp, core.StewardFile), []byte(core.StewardRecord("H0")), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "steward"}, {"push", "-q", "origin", "HEAD:main"}} {
		if _, err := core.Git(tmp, args...); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := m.C.Projects.Get("hive")
	p.Branch = "main"
	sha, err := m.C.Effects.Sync(p)
	if err != nil || sha == "" {
		t.Fatalf("sync: %q %v", sha, err)
	}
	merged(1, "a1", sha)
	land("a1", "r1")
	m.AfterKept("hive", sha)
	if len(n.got) != 0 || !strings.Contains(alertText(m), "steward is the Hive H0") {
		t.Fatalf("a non-steward released: %v, alert %q", n.got, alertText(m))
	}
	id := filepath.Join(m.C.WS.Hive(), "hive-id")
	other, _ := os.ReadFile(id)
	os.WriteFile(id, []byte("H0\n"), 0o644)
	m.AfterKept("hive", sha)
	if len(n.got) != 1 {
		t.Fatalf("the steward didn't release: %v, alert %q", n.got, alertText(m))
	}
	os.WriteFile(id, other, 0o644)
	if out := m.Publish(); !strings.Contains(out, "not published") {
		t.Fatalf("a non-steward published: %s", out)
	}
}

// A kept merge whose checks failed on main is neither cut nor offered to the
// person; one whose checks still run waits, and Tick's retry releases it once
// they pass, covering the red merge before it.
func TestReleaseOnlyAtAGreenMain(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1", "install/root.sh") // would ask the person
	land("a1", "r1")
	n.SetChecks("me/hive", "m1", core.CheckRun{Name: "test", State: core.CheckFail})
	m.AfterKept("hive", "m1")
	if len(n.got) != 0 || len(m.C.Person.Open()) != 0 {
		t.Fatalf("a red main was released or offered: %v %+v", n.got, m.C.Person.Open())
	}
	if a := alertText(m); !strings.Contains(a, "no release at m1: its checks failed on main (test)") {
		t.Fatalf("alert %q", a)
	}
	m.AfterKept("hive", "m1")
	if journaledN(m, "release.ci_failed") != 1 || journaledN(m, "alert.raised") != 1 {
		t.Fatal("the red main was raised again")
	}

	merged(2, "a2", "m2")
	land("a2", "r2")
	n.SetChecks("me/hive", "m2", core.CheckRun{Name: "test", State: core.CheckPending})
	listings := 0
	n.listed = func() { listings++ }
	m.AfterKept("hive", "m2")
	m.retryWaiting()
	n.listed = nil
	if len(n.got) != 0 || len(m.C.Person.Open()) != 0 || journaledN(m, "release.ci_waiting") != 1 {
		t.Fatalf("a running main was released or offered: %v %+v", n.got, m.C.Person.Open())
	}
	if listings != 0 {
		t.Fatalf("a commit waiting on main's checks listed the tags %d times", listings)
	}

	n.SetChecks("me/hive", "m2", core.CheckRun{Name: "test", State: core.CheckPass})
	m.retryWaiting()
	asks := m.C.Person.Open()
	if len(asks) != 1 || asks[0].Ref != "hive@m2" {
		t.Fatalf("the green main isn't offered: %+v", asks)
	}
	m.retryWaiting()
	if len(m.C.Person.Open()) != 1 || journaledN(m, "release.ci_passed") != 1 {
		t.Fatal("a passed wait was tried again")
	}
	asks[0].Answer = "release"
	m.OnPerson(asks[0])
	if body := n.got["v0.0.1"]; !strings.Contains(body, "#1 at a1") || !strings.Contains(body, "#2 at a2") {
		t.Fatalf("release %q", body)
	}
}

// A kept merge whose checks failed on main is tried again on Tick, so a
// re-run that passes releases it with no later merge; a refusal before the
// checks are read is raised once however often the commit is retried.
func TestReleaseAfterAPassingReRun(t *testing.T) {
	m, n, merged, land := selfHive(t)
	merged(1, "a1", "m1")
	land("a1", "r1")
	n.SetChecks("me/hive", "m1", core.CheckRun{Name: "test", State: core.CheckFail})
	m.AfterKept("hive", "m1")
	m.retryWaiting()
	if len(n.got) != 0 || journaledN(m, "release.ci_failed") != 1 || journaledN(m, "alert.raised") != 1 {
		t.Fatalf("a red main was released or raised again: %v", n.got)
	}
	n.SetChecks("me/hive", "m1", core.CheckRun{Name: "test", State: core.CheckPass})
	m.retryWaiting()
	if len(n.got) != 1 || journaledN(m, "release.ci_passed") != 1 {
		t.Fatalf("the passing re-run wasn't released: %v", n.got)
	}

	alerts := journaledN(m, "alert.raised")
	for range 3 {
		m.AfterKept("hive", "m9")
	}
	if got := journaledN(m, "alert.raised") - alerts; got != 1 || !strings.Contains(alertText(m), "no release at m9: it is not a merge") {
		t.Fatalf("the refusal was raised %d times, want once: %q", got, alertText(m))
	}
}

// v0.1.110 was cut by a warden's step at a commit newer than the Hive's last
// merge, so no hive.released named it: a merge it already contains is not
// cut again as a higher number carrying older code, and the next merge past
// it is.
func TestNoReleaseUnderATagThatContainsIt(t *testing.T) {
	m, n, merged, land := selfHive(t)
	n.AddRelease("me/hive", core.Release{Tag: "v0.1.110", Prerelease: true})
	n.AddTag("me/hive", "v0.1.110", "newer")
	n.AddRelease("me/hive", core.Release{Tag: "v0.1.109"})
	n.AddTag("me/hive", "v0.1.109", "old")
	history := map[string][]string{"newer": {"m1", "old"}, "old": nil}
	m.Contains = func(_ core.Project, sha, at string) (bool, error) { return slices.Contains(history[at], sha), nil }
	merged(1, "a1", "m1")
	land("a1", "r1")
	m.AfterKept("hive", "m1")
	if n.calls != 0 || journaled(m, "hive.released") {
		t.Fatalf("cut a release under v0.1.110, which already has m1: %v", n.got)
	}
	if a := alertText(m); !strings.Contains(a, "v0.1.110 at newer already contains it") {
		t.Fatalf("the refusal doesn't name the tag: %q", a)
	}
	merged(2, "a2", "m2")
	land("a2", "r2")
	m.AfterKept("hive", "m2")
	if rel, _ := n.LatestRelease("me/hive"); rel.Tag != "v0.1.111" {
		t.Fatalf("the merge past v0.1.110 wasn't released: %+v", rel)
	}
}

// contains reads the project's clone: a commit is in its own history and its
// descendants', not its ancestors'.
func TestContainsReadsTheClone(t *testing.T) {
	m, _, _, _ := selfHive(t)
	dir := m.C.WS.Project("hive")
	os.MkdirAll(dir, 0o755)
	commit := func(msg string) string {
		for _, args := range [][]string{{"commit", "-q", "--allow-empty", "-m", msg}} {
			if _, err := core.Git(dir, args...); err != nil {
				t.Fatal(err)
			}
		}
		sha, _ := core.Git(dir, "rev-parse", "HEAD")
		return strings.TrimSpace(sha)
	}
	if _, err := core.Git(dir, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	core.Git(dir, "config", "user.email", "t@t")
	core.Git(dir, "config", "user.name", "t")
	old, newer := commit("old"), ""
	newer = commit("newer")
	p, _ := m.C.Projects.Get("hive")
	for _, c := range []struct {
		sha, at string
		want    bool
	}{{old, newer, true}, {newer, newer, true}, {newer, old, false}} {
		if got, err := m.contains(p, c.sha, c.at); err != nil || got != c.want {
			t.Errorf("contains(%s, %s) = %v, %v; want %v", c.sha[:7], c.at[:7], got, err, c.want)
		}
	}
	if _, err := m.contains(p, old, strings.Repeat("ab", 20)); err == nil {
		t.Error("a tag commit the clone can't fetch read as known")
	}
	if _, err := m.contains(p, old, "--upload-pack=touch x"); err == nil || !strings.Contains(err.Error(), "not a commit name") {
		t.Errorf("a tag commit shaped like an option reached git: %v", err)
	}
}
