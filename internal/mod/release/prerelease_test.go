package release

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

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
	own, other := runners[runtime.GOOS], "plan9"
	return map[string]map[string]string{
		"ci": {"test (" + own + "-latest)": here, "test (" + other + "-latest)": there, "sandboxed-e2e": there},
		"update": {"update (" + own + "-latest, previous)": here, "update (" + own + "-latest, v0.1.2)": here,
			"update (" + other + "-latest, previous)": there},
	}
}

// preSite is v0.1.12 as the latest release and v0.1.13 to v0.1.15 as
// pre-releases: v0.1.13's checks passed here though red elsewhere, v0.1.14's
// failed here, and v0.1.15's still run here.
func preSite(t *testing.T, m *Module) *gateSite {
	serveAll(t, "v0.1.12", map[string]map[string]string{"v0.1.12": build("v12"), "v0.1.13": build("v13"),
		"v0.1.14": build("v14"), "v0.1.15": build("v15")}, false)
	g := &gateSite{f: m.C.Effects.Forge.(*core.Local)}
	for _, tag := range []string{"v0.1.12", "v0.1.13", "v0.1.14", "v0.1.15"} {
		g.f.CreateRelease("me/hive", tag, "sha-"+tag, "")
	}
	g.f.PublishRelease("me/hive", "v0.1.12")
	g.setJobs("v0.1.13", gatesFor("success", "failure"))
	red := gatesFor("failure", "success")
	red["update"]["update ("+runners[runtime.GOOS]+"-latest, v0.1.2)"] = "success" // one red leg is enough
	g.setJobs("v0.1.14", red)
	g.setJobs("v0.1.15", gatesFor("", "success"))
	return g
}

// The Hive that releases the Hive follows pre-releases by default: it takes
// the newest whose checks for its own system passed, passing over one red
// here and one still running here, and waits out a merge; once the newer one
// passes here it takes that too.
func TestFollowsTheNewestPreReleaseSafeHere(t *testing.T) {
	m, exe := releaseAt(t, "v0.1.12", "me/hive")
	g := preSite(t, m)
	if _, err := m.C.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	if m.Channel() != Prerelease {
		t.Fatalf("the Hive that releases the Hive follows %q", m.Channel())
	}
	merging := true
	m.Busy = func() bool { return merging }
	if msg, _ := m.AutoUpdate(); !strings.Contains(msg, "mid-merge") {
		t.Fatalf("updated mid-merge: %q", msg)
	}
	merging = false
	msg, err := m.AutoUpdate()
	if raw, _ := os.ReadFile(exe); err != nil || !strings.HasPrefix(msg, "updated to v0.1.13\n") || string(raw) != "v13" {
		t.Fatalf("update: %q, %v, binary %q", msg, err, raw)
	}
	core.Version = "v0.1.13"
	msg, err = m.AutoUpdate()
	if err != nil || !strings.HasPrefix(msg, "already on v0.1.13") || !strings.Contains(msg, "v0.1.15: waiting for") || !strings.Contains(msg, "v0.1.14: test (") {
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
	for _, want := range []string{"v0.1.13: waiting for update (no run yet)", "v0.1.15: waiting for ci (no " + runners[runtime.GOOS] + " job yet)"} {
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
