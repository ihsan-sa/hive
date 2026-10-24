package release

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// The channels auto-update follows.
const (
	Latest     = "latest"
	Prerelease = "pre-release"
)

// Channel is the releases this install follows: the config's channel, or by
// default pre-releases on the Hive that releases the Hive (its own project is
// release_repo, as on H0) and the latest release everywhere else.
func (m *Module) Channel() string {
	cfg := m.C.WS.LoadConfig()
	if cfg.Channel != "" {
		return cfg.Channel
	}
	for _, p := range m.C.Projects.List() {
		if p.Self && strings.EqualFold(p.Repo, cfg.ReleaseRepo) {
			return Prerelease
		}
	}
	return Latest
}

// followEvery is how often a Hive following pre-releases looks for one: each
// look is 1 call on the person's GitHub login, and 4 more for each newer
// pre-release, well within the login's 5,000 an hour.
const followEvery = 5 * time.Minute

// recheckRed is how long a pre-release whose own checks failed is passed over
// before they are read again, so a re-run that passes is still taken.
const recheckRed = time.Hour

// runners names this system in the gates' job names, as their matrix does:
// "test (ubuntu-latest)", "update (ubuntu-latest, previous)".
var runners = map[string]string{"linux": "ubuntu", "darwin": "macos", "windows": "windows"}

// ownJobs are, for each gate, the jobs that test one system: ci's test job and
// the update test's legs.
var ownJobs = map[string]string{"ci": "test", "update": "update"}

// follow runs AutoUpdate every followEvery while the Hive follows pre-releases,
// so one that is safe here installs within minutes, not at the next schedule.
func (m *Module) follow() {
	m.pmu.Lock()
	due := time.Since(m.followed) >= followEvery
	if due {
		m.followed = time.Now()
	}
	m.pmu.Unlock()
	if !due {
		return
	}
	if cfg := m.C.WS.LoadConfig(); !cfg.AutoUpdate || cfg.ReleasePin != "" || m.Channel() != Prerelease {
		return
	}
	go m.AutoUpdate()
}

// look is the most pre-releases one look reads the gates of, newest first.
const look = 3

// prerelease is the newest pre-release of repo newer than after whose checks
// for this system passed, or "" with what each newer one waits for. It reads
// the gates through the forge, on the person's login, as Publish does.
func (m *Module) prerelease(repo, after string) (tag string, why []string, err error) {
	f := m.C.Effects.Forge
	rels, err := f.Releases(repo)
	if err != nil {
		return "", nil, fmt.Errorf("following pre-releases reads them through the GitHub login, which failed: %w", err)
	}
	var tags []string
	for _, r := range rels {
		if r.Prerelease && modules.Newer(after, r.Tag) {
			tags = append(tags, r.Tag)
		}
	}
	slices.SortFunc(tags, func(a, b string) int {
		if modules.Newer(a, b) {
			return 1
		}
		return -1
	})
	m.pmu.Lock()
	m.pre = tags
	m.pmu.Unlock()
	for i, t := range tags {
		if i == look {
			why = append(why, fmt.Sprintf("%d older pre-releases aren't read until one of these %d is safe here", len(tags)-look, look))
			break
		}
		state, w := m.ownGates(repo, t)
		if state == "passed" {
			if e := hasBinary(repo, t); e != nil {
				why = append(why, t+": "+e.Error())
				continue
			}
			return t, why, nil
		}
		why = append(why, t+": "+w)
	}
	return "", why, nil
}

// ownGates says "passed" once every gate's jobs for this system passed in
// the release's own runs, at its tag's commit, else "failed" or "waiting",
// and why. A red verdict is kept for recheckRed.
func (m *Module) ownGates(repo, tag string) (state, why string) {
	key := repo + "@" + tag
	m.pmu.Lock()
	red, ok := m.red[key]
	m.pmu.Unlock()
	if ok && time.Since(red.at) < recheckRed {
		return "failed", red.why
	}
	state, why = m.readOwnGates(repo, tag)
	if state == "failed" {
		m.pmu.Lock()
		if m.red == nil {
			m.red = map[string]verdict{}
		}
		m.red[key] = verdict{time.Now(), why}
		m.pmu.Unlock()
	}
	return state, why
}

type verdict struct {
	at  time.Time
	why string
}

func (m *Module) readOwnGates(repo, tag string) (state, why string) {
	f := m.C.Effects.Forge
	sha, err := f.TagCommit(repo, tag)
	if err != nil {
		return "waiting", "can't read its commit (" + core.Clip(err.Error(), 200) + ")"
	}
	runs, err := f.ReleaseRuns(repo, sha)
	if err != nil {
		return "waiting", "can't read its runs (" + core.Clip(err.Error(), 200) + ")"
	}
	runner := runners[runtime.GOOS]
	var bad, wait []string
	for _, g := range Gates {
		i := slices.IndexFunc(runs, func(r core.WorkflowRun) bool { return r.Name == g })
		if i < 0 {
			wait = append(wait, g+" (no run yet)")
			continue
		}
		jobs, err := f.RunJobs(repo, runs[i].ID)
		if err != nil {
			wait = append(wait, g+" (can't read its jobs)")
			continue
		}
		prefix, n := ownJobs[g]+" ("+runner, 0
		for _, j := range jobs {
			if !strings.HasPrefix(j.Name, prefix) {
				continue
			}
			n++
			switch {
			case j.Status != "completed":
				wait = append(wait, j.Name)
			case j.Conclusion != "success":
				bad = append(bad, j.Name+" "+j.Conclusion+" ("+j.URL+")")
			}
		}
		if n == 0 {
			wait = append(wait, g+" (no "+runner+" job yet)")
		}
	}
	switch {
	case len(bad) > 0:
		return "failed", strings.Join(bad, ", ")
	case len(wait) > 0:
		return "waiting", "waiting for " + strings.Join(wait, ", ")
	}
	return "passed", ""
}
