package release

import (
	"fmt"
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

// follow runs AutoUpdate every followEvery while the Hive follows pre-releases,
// so one installs within minutes of its binaries being attached.
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
	// An update not yet settled waits: installing over it would leave a
	// failed restart nothing settled to roll back to.
	if p, ok := readPending(m.C.WS); ok && p.To == core.Version && p.RolledBack == "" {
		return
	}
	go func() { m.followed1(m.AutoUpdate()) }()
}

// followed1 records what one follow did when it differs from the last: in
// the journal, as the scheduled job's result is, and an error as an alert,
// once for each pre-release it was installing.
func (m *Module) followed1(msg string, err error) {
	if msg == busy {
		return
	}
	key := msg
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

// look is the most pre-releases one look reads the checksums of, newest
// first; one rolled back here costs no read and isn't counted.
const look = 3

// prerelease is the newest pre-release of repo newer than after whose signed
// checksums list this system's binary, or "" with why each newer one was
// passed over. It doesn't wait for the release's own ci or update test: a
// failed start rolls back, and a release that rolled back here is skipped
// from then on. It lists the releases through the forge, on the person's
// login, as Publish does.
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
	read, back := 0, m.rolledBackFrom()
	for _, t := range tags {
		if back[t] {
			why = append(why, t+": skipped, it rolled back after it failed to start here")
			continue
		}
		if read == look {
			why = append(why, fmt.Sprintf("older pre-releases aren't read until one of the newest %d has its binaries", look))
			break
		}
		read++
		if e := hasBinary(repo, t); e != nil {
			why = append(why, t+": waiting for its binaries ("+core.Clip(e.Error(), 200)+")")
			continue
		}
		return t, why, nil
	}
	return "", why, nil
}
