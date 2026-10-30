package release

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// gateSite holds me/hive's pre-releases on the local forge: each tag at a
// commit named for it, and the conclusion of each job of its gate runs, ""
// while it runs.
type gateSite struct {
	f    *core.Local
	runs int64
}

// setJobs gives tag's release runs at its commit these jobs, by gate.
func (g *gateSite) setJobs(tag string, jobs map[string]map[string]string) {
	var runs []core.WorkflowRun
	for _, gate := range Gates {
		if _, ok := jobs[gate]; !ok {
			continue
		}
		g.runs++
		runs = append(runs, core.WorkflowRun{ID: g.runs, Name: gate, Status: "in_progress"})
		var js []core.WorkflowJob
		for name, c := range jobs[gate] {
			st := "completed"
			if c == "" {
				st = "in_progress"
			}
			js = append(js, core.WorkflowJob{Name: name, Status: st, Conclusion: c, URL: "https://example.com/" + name})
		}
		g.f.SetRunJobs("me/hive", g.runs, js...)
	}
	g.f.SetReleaseRuns("me/hive", "sha-"+tag, runs...)
}

// gatesFor are a release's gate jobs, this system's concluding here and the
// others' there.
func gatesFor(here, there string) map[string]map[string]string {
	own, other, e2e := runner(), "plan9", there
	if own == "ubuntu" {
		e2e = here
	}
	return map[string]map[string]string{
		"ci": {"test (" + own + "-latest)": here, "test (" + other + "-latest)": there, "sandboxed-e2e": e2e},
		"update": {"update (" + own + "-latest, previous)": here, "update (" + own + "-latest, v0.1.2)": here,
			"update (" + other + "-latest, previous)": there},
	}
}

// preSite is v0.1.12 as the latest release and v0.1.13 to v0.1.15 as
// pre-releases: v0.1.13's checks passed here though red elsewhere, v0.1.14's
// failed here, and v0.1.15's still run here.
func preSite(t *testing.T, m *Module) *gateSite {
	if runner() == "" {
		t.Skip("the checks don't run on " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	serveAll(t, "v0.1.12", map[string]map[string]string{"v0.1.12": build("v12"), "v0.1.13": build("v13"),
		"v0.1.14": build("v14"), "v0.1.15": build("v15")}, false)
	g := &gateSite{f: m.C.Effects.Forge.(*core.Local)}
	for _, tag := range []string{"v0.1.12", "v0.1.13", "v0.1.14", "v0.1.15"} {
		g.f.CreateRelease("me/hive", tag, "sha-"+tag, "")
	}
	g.f.PublishRelease("me/hive", "v0.1.12")
	g.setJobs("v0.1.13", gatesFor("success", "failure"))
	red := gatesFor("failure", "success")
	red["update"]["update ("+runner()+"-latest, v0.1.2)"] = "success" // one red leg is enough
	g.setJobs("v0.1.14", red)
	g.setJobs("v0.1.15", gatesFor("", "success"))
	return g
}

// A Hive following pre-releases (the Hive that releases the Hive did by
// default before main) takes
// the newest whose checks for its own system passed, passing over one red
// here and one still running here, and waits out a merge; once the newer one
// passes here it takes that too.
func TestFollowsTheNewestPreReleaseSafeHere(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if m.Channel() != Main {
		t.Fatalf("the Hive that releases the Hive follows %q, not main", m.Channel())
	}
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
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
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") || string(raw) != "v13" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
	core.Version = "v0.1.13"
	msg, err = m.AutoUpdate()
	if newest, behind := m.Lag(); newest != "v0.1.15" || behind != 2 {
		t.Fatalf("on v0.1.13: newest %q, %d behind; want v0.1.15, 2", newest, behind)
	}
	if err != nil || !strings.HasPrefix(msg, "already on v0.1.13") || !strings.Contains(msg, "v0.1.15: waiting for") || !strings.Contains(msg, "v0.1.14: ") || !strings.Contains(msg, "test ("+runner()+"-latest) failure") {
		t.Fatalf("with nothing safe newer: %q, %v", msg, err)
	}
	g.setJobs("v0.1.15", gatesFor("success", ""))
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.15\n") {
		t.Fatalf("once v0.1.15 passed here: %q, %v", msg, err)
	}
}

// A pre-release with no runs yet, or with no job for this system, is not safe.
func TestPreReleaseWithoutItsChecksWaits(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	g.setJobs("v0.1.13", map[string]map[string]string{"ci": gatesFor("success", "success")["ci"]})
	g.setJobs("v0.1.15", map[string]map[string]string{"ci": {"test (plan9-latest)": "success"}, "update": {"update (plan9-latest, previous)": "success"}})
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "already on v0.1.12") || string(raw) != "old" {
		t.Fatalf("took an unchecked pre-release: %q, %v, binary %q", msg, err, raw)
	}
	for _, want := range []string{"v0.1.13: waiting for update (no run yet)", "v0.1.15: waiting for ci (no " + runner() + " job yet)"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("%q doesn't say %q", msg, want)
		}
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
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") {
		t.Fatalf("chosen: %q, %v", msg, err)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "v13" {
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

// Only the newest pre-releases are read in one look, and the waits say so.
func TestOlderPreReleasesWaitTheirTurn(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	g.f.CreateRelease("me/hive", "v0.1.16", "sha-v0.1.16", "")
	g.setJobs("v0.1.16", gatesFor("", ""))
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.AutoUpdate(); err != nil || !strings.Contains(msg, "1 older pre-releases aren't read") || strings.Contains(msg, "updated") {
		t.Fatalf("read past the newest three: %q, %v", msg, err)
	}
	// v0.1.14, now known red, costs no read and doesn't count, so the next
	// look reaches v0.1.13, safe here.
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") {
		t.Fatalf("with v0.1.14 known red: %q, %v", msg, err)
	}
}

// A pre-release rolled back here is passed over for an older one safe here.
func TestRolledBackPreReleasePassedOver(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	g.setJobs("v0.1.15", gatesFor("success", ""))
	m.C.J.MustAppend("hive.rolledback", map[string]string{"from": "v0.1.15", "to": "v0.1.12", "why": "failed to start"})
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") {
		t.Fatalf("past the rolled-back v0.1.15: %q, %v", msg, err)
	}
}

// Back on the latest channel, "already on" no longer names the pre-releases'
// waits.
func TestLatestChannelForgetsTheWaits(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	g.setJobs("v0.1.13", gatesFor("", ""))
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "waiting for") {
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

// follow holds an update while main's measure runs a check here, which the
// restart would kill, and for measureHold at most.
func TestFollowWaitsForMainsMeasure(t *testing.T) {
	m, _ := releaseAt(t, "v0.1.13", "me/hive")
	cfg := m.C.WS.LoadConfig()
	cfg.Channel, cfg.AutoUpdate = Prerelease, true
	m.C.WS.SaveConfig(cfg)
	m.C.Runs.YieldFor("measure p@abc", "p main at abc is being measured")
	m.follow()
	time.Sleep(50 * time.Millisecond)
	if !m.updating.TryLock() {
		t.Fatal("follow updated while main's measure ran here")
	}
	m.updating.Unlock()
	if why := m.measureHold(); !strings.Contains(why, "p main at abc") {
		t.Fatalf("held for %q", why)
	}
	m.pmu.Lock()
	m.heldSince = time.Now().Add(-measureHold)
	m.pmu.Unlock()
	if why := m.measureHold(); why != "" {
		t.Fatalf("held past measureHold: %q", why)
	}
	m.C.Runs.YieldFor("measure p@abc", "")
	if why := m.measureHold(); why != "" {
		t.Fatalf("held with no measure: %q", why)
	}
}

// The jobs the channel reads are named as ci.yml and update.yml name them: a
// rename would leave the channel waiting without a word.
func TestOwnJobsAreTheWorkflowsJobs(t *testing.T) {
	for gate, job := range ownJobs {
		raw, err := os.ReadFile("../../../.github/workflows/" + gate + ".yml")
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		i := slices.Index(lines, "  "+job+":")
		if i < 0 {
			t.Fatalf("%s.yml has no job %s", gate, job)
		}
		matrix := ""
		for _, l := range lines[i+1:] {
			if len(l) > 2 && l[2] != ' ' && l[0] == ' ' || !strings.HasPrefix(l, " ") && l != "" {
				break // the next job, or past jobs
			}
			if strings.HasPrefix(l, "    name:") {
				t.Fatalf("%s.yml names its %s job, so it isn't %q", gate, job, job+" (<runner>...)")
			}
			if strings.HasPrefix(l, "      matrix:") {
				matrix = "next"
			} else if matrix == "next" {
				matrix = strings.TrimSpace(l)
			}
		}
		if !strings.HasPrefix(matrix, "os:") {
			t.Fatalf("%s.yml's %s matrix doesn't lead with os: %q", gate, job, matrix)
		}
		for _, r := range runners {
			if !strings.Contains(matrix, r+"-latest") {
				t.Fatalf("%s.yml's %s matrix has no %s-latest: %q", gate, job, r, matrix)
			}
		}
	}
	for r, gates := range alsoOwn {
		for gate, job := range gates {
			raw, err := os.ReadFile("../../../.github/workflows/" + gate + ".yml")
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(raw), "\n")
			i := slices.Index(lines, "  "+job+":")
			if i < 0 {
				t.Fatalf("%s.yml has no job %s", gate, job)
			}
			runs := ""
			for _, l := range lines[i+1:] {
				if !strings.HasPrefix(l, "    ") && l != "" {
					break
				}
				if strings.HasPrefix(l, "    name:") {
					t.Fatalf("%s.yml names its %s job, so it isn't %q", gate, job, job)
				}
				if v, ok := strings.CutPrefix(l, "    runs-on:"); ok {
					runs = strings.TrimSpace(v)
				}
			}
			if runs != r+"-latest" {
				t.Fatalf("%s.yml's %s runs on %q, not %s-latest", gate, job, runs, r)
			}
		}
	}
}

// Without a GitHub login the channel can't read the gates, and says so.
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

// loginForge is the local forge read through a login: its API answers notes,
// and GitHub's rate limit stops the reads named in limit until reset.
type loginForge struct {
	*core.Local
	notes string
	limit map[string]bool // "releases", "runs", "jobs"
	reset time.Time
	gets  []string
}

func (f *loginForge) limited(what string) error {
	if f.limit[what] {
		return &core.RateLimited{Until: time.Now().Add(time.Minute), Text: "HTTP 403: API rate limit exceeded for user ID 1."}
	}
	return nil
}

func (f *loginForge) Releases(repo string) ([]core.Release, error) {
	if err := f.limited("releases"); err != nil {
		return nil, err
	}
	return f.Local.Releases(repo)
}

func (f *loginForge) ReleaseRuns(repo, sha string) ([]core.WorkflowRun, error) {
	if err := f.limited("runs"); err != nil {
		return nil, err
	}
	return f.Local.ReleaseRuns(repo, sha)
}

func (f *loginForge) RunJobs(repo string, run int64) ([]core.WorkflowJob, error) {
	if err := f.limited("jobs"); err != nil {
		return nil, err
	}
	return f.Local.RunJobs(repo, run)
}

func (f *loginForge) APIGet(repo, path string) (string, error) {
	f.gets = append(f.gets, path)
	return f.notes, nil
}

func (f *loginForge) RateReset(string) (time.Time, error) { return f.reset, nil }

// A look GitHub's rate limit stops, at any of its reads, says so and when it
// looks again (the login's reset), instead of "already on" as if nothing
// were newer; follow waits for the reset, and says so once, with no alert.
func TestRateLimitedLookSaysSoAndWaitsForTheReset(t *testing.T) {
	for _, what := range []string{"releases", "runs", "jobs"} {
		t.Run(what, func(t *testing.T) {
			m, exe := releaseAt(t, "v0.1.12", "me/hive")
			preSite(t, m)
			cfg := m.C.WS.LoadConfig()
			cfg.Channel, cfg.AutoUpdate = Prerelease, true
			m.C.WS.SaveConfig(cfg)
			m.PauseMerges = func(time.Duration) (func(), bool) { return func() {}, true }
			reset := time.Now().Add(40 * time.Minute).Truncate(time.Second)
			f := &loginForge{Local: m.C.Effects.Forge.(*core.Local), limit: map[string]bool{what: true}, reset: reset}
			m.C.Effects.Forge = f
			msg, err := m.AutoUpdate()
			if raw, _ := os.ReadFile(exe); err == nil || strings.HasPrefix(msg, "already on") || string(raw) != "old" ||
				!strings.Contains(err.Error(), "rate limit") || !strings.Contains(err.Error(), reset.UTC().Format("15:04Z")) {
				t.Fatalf("rate-limited look: %q, %v, binary %q", msg, err, raw)
			}
			m.follow()
			// The follow has ended once it journals what it did: it moves
			// followed before it journals, so waiting on followed alone
			// raced the journal on a slow machine.
			journalled := func() bool {
				n := 0
				m.C.J.Replay(func(e core.Event) {
					if e.Kind == "release.follow" {
						n++
					}
				})
				return n > 0
			}
			for deadline := time.Now().Add(time.Minute); !journalled(); time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the follow journalled nothing in a minute")
				}
			}
			m.pmu.Lock()
			at := m.followed
			m.pmu.Unlock()
			if !at.Equal(reset.Add(-followEvery)) {
				t.Fatalf("the next look is due at %v, want the reset %v", at.Add(followEvery), reset)
			}
			count := func() (follows, alerts int) {
				m.C.J.Replay(func(e core.Event) {
					switch e.Kind {
					case "release.follow":
						follows++
						if !strings.Contains(string(e.Data), "rate limit") {
							t.Errorf("journalled %s", e.Data)
						}
					case "alert.raised":
						alerts++
					}
				})
				return
			}
			// The follow's goroutine sets the next look before it journals,
			// so wait for its event, not only for the time.
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if n, _ := count(); n > 0 {
					break
				}
			}
			m.followed1(m.AutoUpdate())
			follows, alerts := count()
			if follows != 1 || alerts != 0 {
				t.Fatalf("a rate limit journalled %d times with %d alerts, want once and none", follows, alerts)
			}
			f.limit = nil // after the reset, the look goes on and takes v0.1.13
			if msg, err := m.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") {
				t.Fatalf("after the reset: %q, %v", msg, err)
			}
		})
	}
}

// A newer release in hand is still installed when the look for pre-releases
// was rate-limited: the limit only stops a look short.
func TestRateLimitedLookStillTakesANewerLatest(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.11", "me/hive")
	preSite(t, m)
	cfg := m.C.WS.LoadConfig()
	cfg.Channel = Prerelease
	m.C.WS.SaveConfig(cfg)
	m.C.Effects.Forge = &loginForge{Local: m.C.Effects.Forge.(*core.Local), limit: map[string]bool{"releases": true}}
	msg, err := m.Update("")
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.12\n") || string(raw) != "v12" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
}

// Release notes are read on the forge's login, never the anonymous API.
func TestNotesReadOnTheLogin(t *testing.T) {
	serve(t, "v9.9.9", build("new"), false)
	releases = "" // the anonymous API has none
	m, _ := releaseAt(t, "v9.9.8", "me/hive")
	f := &loginForge{Local: m.C.Effects.Forge.(*core.Local), notes: releaseList}
	m.C.Effects.Forge = f
	msg, err := m.Update("")
	if err != nil || !strings.Contains(msg, "Faster merges.") || !slices.Contains(f.gets, "repos/me/hive/releases?per_page=100") {
		t.Fatalf("update: %q, %v, reads %q", msg, err, f.gets)
	}
}

// Following pre-releases, --release to one whose checks still run here (a
// hand update past a slow follow) moves the Hive forward and leaves
// auto-update on; one older than the newest safe here, past the latest
// release though it is, is a step back: from a step-back pin it stays pinned
// through the restart, and auto-update doesn't move past it (issue #1795).
func TestPreReleaseStepBackStaysPinned(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.13", "me/hive")
	g := preSite(t, m)
	cfg := m.C.WS.LoadConfig()
	cfg.Channel, cfg.AutoUpdate = Prerelease, true
	m.C.WS.SaveConfig(cfg)
	msg, err := m.Update("v0.1.15")
	if cfg := m.C.WS.LoadConfig(); err != nil || !strings.HasPrefix(msg, "updated to v0.1.15\n") || cfg.ReleasePin != "" || !cfg.AutoUpdate {
		t.Fatalf("past a slow follow: %q, %v; pin %q, auto_update %v", msg, err, cfg.ReleasePin, cfg.AutoUpdate)
	}

	m, exe = releaseAt(t, "v0.1.12", "me/hive")
	g = preSite(t, m)
	g.setJobs("v0.1.15", gatesFor("success", "success"))
	cfg = m.C.WS.LoadConfig()
	cfg.Channel, cfg.AutoUpdate, cfg.PinnedAuto, cfg.ReleasePin = Prerelease, false, true, "v0.1.12"
	m.C.WS.SaveConfig(cfg)
	if msg, err := m.Update("v0.1.13"); err != nil || !strings.Contains(msg, "updated to v0.1.13, pinned") {
		t.Fatalf("bisecting forward: %q, %v", msg, err)
	}
	core.Version = "v0.1.13" // the restart
	restarted := &Module{C: m.C}
	if msg, err := restarted.AutoUpdate(); err != nil || !strings.HasPrefix(msg, "pinned to v0.1.13") {
		t.Fatalf("auto-update after the restart: %q, %v", msg, err)
	}
	restarted.follow()
	time.Sleep(50 * time.Millisecond)
	if cfg := m.C.WS.LoadConfig(); cfg.ReleasePin != "v0.1.13" || cfg.AutoUpdate {
		t.Fatalf("pin %q, auto_update %v", cfg.ReleasePin, cfg.AutoUpdate)
	}
	if raw, _ := os.ReadFile(exe); string(raw) != "v13" {
		t.Fatalf("binary %q", raw)
	}
}
