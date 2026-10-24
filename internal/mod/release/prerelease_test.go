package release

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// preSite is v0.1.12 as the latest release and v0.1.13 to v0.1.15 as
// pre-releases with no ci or update run at all: v0.1.13 and v0.1.14 have
// their binaries attached, v0.1.15 not yet. Adding to the map it returns
// attaches a release's binaries.
func preSite(t *testing.T, m *Module) map[string]map[string]string {
	assets := map[string]map[string]string{"v0.1.12": build("v12"), "v0.1.13": build("v13"), "v0.1.14": build("v14")}
	serveAll(t, "v0.1.12", assets, false)
	f := m.C.Effects.Forge.(*core.Local)
	for _, tag := range []string{"v0.1.12", "v0.1.13", "v0.1.14", "v0.1.15"} {
		f.CreateRelease("me/hive", tag, "sha-"+tag, "")
	}
	f.PublishRelease("me/hive", "v0.1.12")
	return assets
}

// The Hive that releases the Hive follows pre-releases by default: it takes
// the newest whose binaries are attached, without waiting for its ci or
// update test, and waits out a merge; once a newer one's binaries are
// attached it takes that too.
func TestFollowsEachPreReleaseOnceItsBinariesAreAttached(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	assets := preSite(t, m)
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if m.Channel() != Prerelease {
		t.Fatalf("the Hive that releases the Hive follows %q", m.Channel())
	}
	merging := true
	m.PauseMerges = func(time.Duration) (func(), bool) { return func() {}, !merging }
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "a merge has run past") {
		t.Fatalf("updated mid-merge: %q", msg)
	}
	if newest, behind := m.Lag(); newest != "v0.1.15" || behind != 3 {
		t.Fatalf("on v0.1.12 the dashboard shows newest %q, %d behind; want v0.1.15, 3", newest, behind)
	}
	merging = false
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.14\n") || string(raw) != "v14" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
	core.Version = "v0.1.14"
	msg, err = m.AutoUpdate()
	if newest, behind := m.Lag(); newest != "v0.1.15" || behind != 1 {
		t.Fatalf("on v0.1.14: newest %q, %d behind; want v0.1.15, 1", newest, behind)
	}
	if err != nil || !strings.HasPrefix(msg, "already on v0.1.14; v0.1.15: waiting for its binaries") {
		t.Fatalf("before v0.1.15's binaries are attached: %q, %v", msg, err)
	}
	if s := m.Skipped(); len(s) != 1 || !strings.HasPrefix(s[0], "v0.1.15: waiting for its binaries") {
		t.Fatalf("the dashboard's skips: %q", s)
	}
	assets["v0.1.15"] = build("v15")
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.15\n") {
		t.Fatalf("once v0.1.15's binaries are attached: %q, %v", msg, err)
	}
}

// A pre-release that rolled back here, after failed starts or because it
// didn't run, is skipped from then on, and the next older one is taken.
func TestRolledBackPreReleaseIsSkipped(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	assets := preSite(t, m)
	assets["v0.1.15"] = build("v15")
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	tryBinary = func(_, tag string) error {
		if tag == "v0.1.15" {
			return errors.New("exit status 2")
		}
		return nil
	}
	if msg, err := m.AutoUpdate(); err == nil || !strings.Contains(err.Error(), "v0.1.15 doesn't run here") {
		t.Fatalf("a binary that doesn't run: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "old" {
		t.Fatalf("kept binary %q", raw)
	}
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.14\n") || string(raw) != "v14" {
		t.Fatalf("after v0.1.15 failed: %q, %v, binary %q", msg, err, raw)
	}
	if s := m.Skipped(); len(s) != 1 || !strings.Contains(s[0], "v0.1.15: skipped, it rolled back") {
		t.Fatalf("the dashboard's skips: %q", s)
	}

	// A failed start rolls back in Boot; the old daemon's Report journals it.
	m2, exe2 := releaseAt(t, "v0.1.14", "me/hive")
	m2.C.Effects.Forge.(*core.Local).CreateRelease("me/hive", "v0.1.15", "sha-v0.1.15", "")
	cfg = m2.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m2.C.WS.SaveConfig(cfg)
	writePending(m2.C.WS, pending{From: "v0.1.14", To: "v0.1.15", Paths: []string{exe2}, RolledBack: "v0.1.15 failed to start 2 times after the update"})
	m2.Report()
	msg, err = m2.AutoUpdate()
	if raw, _ := os.ReadFile(exe2); err != nil || !strings.HasPrefix(msg, "already on v0.1.14; v0.1.15: skipped") || string(raw) != "old" {
		t.Fatalf("retried a release that rolled back: %q, %v, binary %q", msg, err, raw)
	}
}

// Every other install follows the latest release by default; a person can
// choose either channel.
func TestPreReleasesOffByDefault(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	preSite(t, m)
	if _, err := m.C.Projects.Add(core.Project{Name: "fork", Repo: "you/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if m.Channel() != Latest {
		t.Fatalf("a fresh install follows %q", m.Channel())
	}
	if msg, err := m.AutoUpdate(); err != nil || msg != "already on v0.1.12" {
		t.Fatalf("off by default: %q, %v", msg, err)
	}
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.14\n") {
		t.Fatalf("chosen: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "v14" {
		t.Fatalf("binary %q", raw)
	}
	m2, _ := releaseAt(t, "v0.1.12", "me/hive")
	m2.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true})
	cfg = m2.C.WS.LoadConfig()
	cfg.Channel = Latest
	m2.C.WS.SaveConfig(cfg)
	if m2.Channel() != Latest {
		t.Fatalf("the person chose latest, it follows %q", m2.Channel())
	}
}

// A channel config.json can't name is refused.
func TestUnknownChannelRefused(t *testing.T) {
	c := core.DefaultConfig()
	c.Channel = "nightly"
	if c.Check() == nil {
		t.Fatal("channel nightly accepted")
	}
}

// Only the newest pre-releases' checksums are read in one look, and the
// waits say so.
func TestOlderPreReleasesWaitTheirTurn(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.12", "me/hive")
	preSite(t, m)
	f := m.C.Effects.Forge.(*core.Local)
	for _, tag := range []string{"v0.1.16", "v0.1.17"} {
		f.CreateRelease("me/hive", tag, "sha-"+tag, "")
	}
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.AutoUpdate(); err != nil || !strings.Contains(msg, "older pre-releases aren't read") || strings.Contains(msg, "updated") {
		t.Fatalf("read past the newest three: %q, %v", msg, err)
	}
}

// Back on the latest channel, "already on" no longer names the pre-releases'
// waits.
func TestLatestChannelForgetsTheWaits(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.12", "me/hive")
	preSite(t, m)
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	tryBinary = func(string, string) error { return errors.New("exit status 2") }
	for _, tag := range []string{"v0.1.14", "v0.1.13"} {
		if _, err := m.AutoUpdate(); err == nil || !strings.Contains(err.Error(), tag+" doesn't run here") {
			t.Fatalf("installing %s: %v", tag, err)
		}
	}
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "v0.1.13: skipped") {
		t.Fatalf("on pre-release: %q", msg)
	}
	cfg.Channel = Latest
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.AutoUpdate(); err != nil || msg != "already on v0.1.12" {
		t.Fatalf("on latest: %q, %v", msg, err)
	}
}

// What follow finds goes in the journal when it changes, and a failed
// install raises one alert for its pre-release, not one every look.
func TestFollowRecordsWhatChanged(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.12", "me/hive")
	count := func(kind string) (n int) {
		m.C.J.Replay(func(e core.Event) {
			if e.Kind == kind {
				n++
			}
		})
		return n
	}
	m.followed1("already on v0.1.12; v0.1.13: waiting for ci", nil)
	m.followed1("already on v0.1.12; v0.1.13: waiting for ci", nil)
	m.followed1(busy, nil)
	if n := count("release.follow"); n != 1 {
		t.Fatalf("the same wait journalled %d times", n)
	}
	m.tried = "v0.1.13"
	m.followed1("", errors.New("hive-linux-amd64: checksum mismatch; not installing"))
	m.followed1("", errors.New("hive-linux-amd64: checksum mismatch; not installing (again)"))
	if n, a := count("release.follow"), count("alert.raised"); n != 2 || a != 1 {
		t.Fatalf("a failing pre-release: %d journalled, %d alerts; want 2, 1", n, a)
	}
	m.tried = "v0.1.14"
	m.followed1("", errors.New("download failed"))
	if a := count("alert.raised"); a != 2 {
		t.Fatalf("a newer failing pre-release raised %d alerts in all, want 2", a)
	}
}

// follow waits while this version's own update hasn't settled.
func TestFollowWaitsForTheUpdateToSettle(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.13", "me/hive")
	cfg := m.C.WS.LoadConfig()
	cfg.Channel, cfg.AutoUpdate = Prerelease, true
	m.C.WS.SaveConfig(cfg)
	writePending(m.C.WS, pending{From: "v0.1.12", To: "v0.1.13", Paths: []string{"x"}, Starts: 1})
	m.follow()
	time.Sleep(50 * time.Millisecond)
	if !m.updating.TryLock() {
		t.Fatal("follow updated over an update that hasn't settled")
	}
	m.updating.Unlock()
	m.C.J.Replay(func(e core.Event) {
		if e.Kind == "release.follow" {
			t.Fatalf("follow ran before the update settled: %s", e.Data)
		}
	})
}

// Without a GitHub login the channel can't list the pre-releases, and says so.
func TestPreReleasesWithoutALoginSaySo(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.12", "me/hive")
	preSite(t, m)
	m.C.Effects.Forge = noLogin{m.C.Effects.Forge}
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "already on v0.1.12; following pre-releases reads them through the GitHub login") {
		t.Fatalf("without a login: %q, %v", msg, err)
	}
}

type noLogin struct{ core.Forge }

func (noLogin) Releases(string) ([]core.Release, error) { return nil, errors.New("gh: not logged in") }
