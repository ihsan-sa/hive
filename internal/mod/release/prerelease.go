package release

import (
	"errors"
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

// runners names each system the gates test in their job names, as their
// matrix does: "test (ubuntu-latest)", "update (ubuntu-latest, previous)".
// GitHub's ubuntu and windows runners are x64 and its macos runner arm64, so
// a system they don't run on (linux/arm64, darwin/amd64, freebsd) has no
// checks of its own and doesn't follow pre-releases. Linux only for now
// (issue #564): add "darwin/arm64": "macos" and "windows/amd64": "windows"
// back with the workflows' matrices.
var runners = map[string]string{"linux/amd64": "ubuntu"}

// runner is this system's runner, "" for one the gates don't run on.
func runner() string { return runners[runtime.GOOS+"/"+runtime.GOARCH] }

// ownJobs are, for each gate, the jobs that test one system: ci's test job and
// the update test's legs. TestOwnJobsAreTheWorkflowsJobs reads the workflows.
var ownJobs = map[string]string{"ci": "test", "update": "update"}

// alsoOwn are jobs with no matrix that test one system: ci's end to end under
// bubblewrap is Linux's, the sandbox a Linux Hive runs its agents in.
var alsoOwn = map[string]map[string]string{"ubuntu": {"ci": "sandboxed-e2e"}}

// own says whether a gate's job tests the system named by runner r.
func own(gate, job, r string) bool {
	return strings.HasPrefix(job, ownJobs[gate]+" ("+r) || alsoOwn[r][gate] != "" && job == alsoOwn[r][gate]
}

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
	// A pin auto-update was on before is looked at too: AutoUpdate lifts one
	// that an update forward left.
	if cfg := m.C.WS.LoadConfig(); !(cfg.AutoUpdate && cfg.ReleasePin == "" || cfg.PinnedAuto && cfg.ReleasePin == core.Version) || m.Channel() != Prerelease {
		return
	}
	// An update not yet settled waits: installing over it would leave a
	// failed restart nothing settled to roll back to.
	if p, ok := readPending(m.C.WS); ok && p.To == core.Version && p.RolledBack == "" {
		return
	}
	go func() {
		msg, err := m.AutoUpdate()
		var l *limited
		if errors.As(err, &l) { // the next look waits for the login's quota
			m.pmu.Lock()
			if at := l.until.Add(-followEvery); at.After(m.followed) {
				m.followed = at
			}
			m.pmu.Unlock()
		}
		m.followed1(msg, err)
	}()
}

// limited is a look for a newer release that GitHub's rate limit stopped:
// not "nothing newer", and no fault, so it says when the next look is.
type limited struct {
	until time.Time
	text  string
}

func (l *limited) Error() string {
	return "GitHub's rate limit on the login stopped the look for a newer release; it looks again after " +
		l.until.UTC().Format("15:04Z") + " (" + l.text + ")"
}

// limit is err as limited when it is GitHub's rate limit, else nil. The look
// again waits for the login's quota to reset when the hour's quota is spent
// and the forge can say when; a secondary limit waits only what GitHub asked.
func (m *Module) limit(repo string, err error) *limited {
	var rl *core.RateLimited
	if !errors.As(err, &rl) {
		return nil
	}
	until := rl.Until
	if r, ok := m.C.Effects.Forge.(core.APIReader); ok {
		if at, e := r.RateReset(repo); e == nil && at.After(until) {
			until = at
		}
	}
	return &limited{until, core.Clip(rl.Text, 200)}
}

// followed1 records what one follow did when it differs from the last: in
// the journal, as the scheduled job's result is, and an error as an alert,
// once for each pre-release it was installing.
func (m *Module) followed1(msg string, err error) {
	if msg == busy {
		return
	}
	key := msg
	var l *limited
	if errors.As(err, &l) { // said once while it lasts, and no alert: nothing failed
		msg, key, err = l.Error(), "limited", nil
	}
	if err != nil {
		msg = "failed: " + err.Error()
		m.pmu.Lock()
		key = "failed@" + m.tried
		m.pmu.Unlock()
	}
	m.pmu.Lock()
	same := key == m.said1
	m.said1 = key
	m.pmu.Unlock()
	if same {
		return
	}
	m.C.J.MustAppend("release.follow", map[string]string{"result": core.Clip(msg, 600)})
	if err != nil {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "update", "ref": "release:follow",
			"text": "Following pre-releases, the update failed: " + core.Clip(err.Error(), 300) + ". It tries again every " + followEvery.String() + "."})
	}
}

// look is the most pre-releases one look reads the gates of, newest first;
// one already known red here, or rolled back here, costs no read and isn't
// counted.
const look = 3

// prerelease is the newest pre-release of repo newer than after whose checks
// for this system passed, or "" with what each newer one waits for. It reads
// the gates through the forge, on the person's login, as Publish does.
func (m *Module) prerelease(repo, after string) (tag string, why []string, err error) {
	if runner() == "" {
		return "", nil, fmt.Errorf("the checks don't run on %s/%s, so no pre-release is known safe here", runtime.GOOS, runtime.GOARCH)
	}
	f := m.C.Effects.Forge
	rels, err := f.Releases(repo)
	if l := m.limit(repo, err); l != nil {
		return "", nil, l
	}
	if err != nil {
		return "", nil, fmt.Errorf("following pre-releases reads them through the GitHub login, which failed: %w", err)
	}
	var tags, ahead []string // tags are the candidates; ahead counts the lag from this one
	for _, r := range rels {
		if r.Prerelease && modules.Newer(after, r.Tag) {
			tags = append(tags, r.Tag)
		}
		if modules.Newer(core.Version, r.Tag) {
			ahead = append(ahead, r.Tag)
		}
	}
	slices.SortFunc(tags, func(a, b string) int {
		if modules.Newer(a, b) {
			return 1
		}
		return -1
	})
	m.pmu.Lock()
	m.ahead = ahead
	for k := range m.red { // a tag no longer newer than this one is never read again
		if r, t, _ := strings.Cut(k, "@"); r == repo && !slices.Contains(tags, t) {
			delete(m.red, k)
		}
	}
	for k := range m.shas {
		if r, t, _ := strings.Cut(k, "@"); r == repo && !slices.Contains(tags, t) {
			delete(m.shas, k)
		}
	}
	m.pmu.Unlock()
	read, back := 0, m.rolledBackFrom()
	for i, t := range tags {
		if back[t] {
			why = append(why, t+": rolled back here after it failed to start")
			continue
		}
		if !m.knownRed(repo, t) {
			if read == look {
				why = append(why, fmt.Sprintf("%d older pre-releases aren't read until one of these %d is safe here", len(tags)-i, look))
				break
			}
			read++
		}
		state, w, err := m.ownGates(repo, t)
		if err != nil {
			return "", why, err
		}
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
// and why. A red verdict is kept for recheckRed. A read GitHub's rate limit
// stopped ends the look, as limited.
func (m *Module) ownGates(repo, tag string) (state, why string, err error) {
	key := repo + "@" + tag
	m.pmu.Lock()
	red, ok := m.red[key]
	m.pmu.Unlock()
	if ok && time.Since(red.at) < recheckRed {
		return "failed", red.why, nil
	}
	state, why, err = m.readOwnGates(repo, tag)
	if state == "failed" {
		m.pmu.Lock()
		if m.red == nil {
			m.red = map[string]verdict{}
		}
		m.red[key] = verdict{time.Now(), why}
		m.pmu.Unlock()
	}
	return state, why, err
}

type verdict struct {
	at  time.Time
	why string
}

// knownRed says a red verdict on tag is still kept.
func (m *Module) knownRed(repo, tag string) bool {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	red, ok := m.red[repo+"@"+tag]
	return ok && time.Since(red.at) < recheckRed
}

// tagCommit is the commit tag points at, read once: the release refuses a
// tag that moves, so a pre-release's commit never changes.
func (m *Module) tagCommit(repo, tag string) (string, error) {
	key := repo + "@" + tag
	m.pmu.Lock()
	sha := m.shas[key]
	m.pmu.Unlock()
	if sha != "" {
		return sha, nil
	}
	sha, err := m.C.Effects.Forge.TagCommit(repo, tag)
	if err == nil && sha != "" {
		m.pmu.Lock()
		if m.shas == nil {
			m.shas = map[string]string{}
		}
		m.shas[key] = sha
		m.pmu.Unlock()
	}
	return sha, err
}

func (m *Module) readOwnGates(repo, tag string) (state, why string, err error) {
	f := m.C.Effects.Forge
	sha, err := m.tagCommit(repo, tag)
	if l := m.limit(repo, err); l != nil {
		return "", "", l
	}
	if err != nil {
		return "waiting", "can't read its commit (" + core.Clip(err.Error(), 200) + ")", nil
	}
	runs, err := f.ReleaseRuns(repo, sha)
	if l := m.limit(repo, err); l != nil {
		return "", "", l
	}
	if err != nil {
		return "waiting", "can't read its runs (" + core.Clip(err.Error(), 200) + ")", nil
	}
	runner := runner()
	var bad, wait []string
	for _, g := range Gates {
		i := slices.IndexFunc(runs, func(r core.WorkflowRun) bool { return r.Name == g })
		if i < 0 {
			wait = append(wait, g+" (no run yet)")
			continue
		}
		jobs, err := f.RunJobs(repo, runs[i].ID)
		if l := m.limit(repo, err); l != nil {
			return "", "", l
		}
		if err != nil {
			wait = append(wait, g+" (can't read its jobs)")
			continue
		}
		n := 0
		for _, j := range jobs {
			if !own(g, j.Name, runner) {
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
		return "failed", strings.Join(bad, ", "), nil
	case len(wait) > 0:
		return "waiting", "waiting for " + strings.Join(wait, ", "), nil
	}
	return "passed", "", nil
}
