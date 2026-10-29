package improve

import (
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// forgeWait bounds how long a merge's measure waits for the forge's run of
// its project's forge_tests on the merge; forgePoll is how often it looks.
var forgeWait, forgePoll = 45 * time.Minute, time.Minute

// forgeTest reads main's tests at sha from the forge, for a project whose CI
// tests every pull request and push (ForgeTests): that CI runs the merge
// commit too, so running the same tests here again only loads this machine
// (issue #2087). ok is a pass of every named check there, recorded as sha's
// test check so the rest read it as they read a run here. wait is a check
// still going or not yet reported: the measure comes back later and holds
// nothing meanwhile. Anything else gives neither and the tests run here, as
// they did before: a red or a check that couldn't start there (a red here is
// retried and its failing tests named), a forge that can't be read, or no
// answer forgeWait after the measure first looked.
func (m *Module) forgeTest(p core.Project, sha string, names []string) (c core.Check, ok, wait bool) {
	if len(names) == 0 {
		return core.Check{}, false, false
	}
	runs, err := m.C.Effects.Forge.Checks(p.Repo, sha)
	if err != nil {
		return core.Check{}, false, false
	}
	pending := false
	for _, n := range names {
		seen := false
		for _, r := range runs {
			if r.Name != n {
				continue
			}
			seen = true
			switch r.State {
			case core.CheckPass:
			case core.CheckPending:
				pending = true
			default:
				return core.Check{}, false, false
			}
		}
		pending = pending || !seen
	}
	key := p.Name + "@" + sha
	m.mu.Lock()
	first, seen := m.forgeAt[key]
	switch {
	case pending && !seen:
		if m.forgeAt == nil {
			m.forgeAt = map[string]time.Time{}
		}
		m.forgeAt[key], first = time.Now(), time.Now()
		fallthrough
	case pending && time.Since(first) < forgeWait:
		m.mu.Unlock()
		return core.Check{}, false, true
	}
	delete(m.forgeAt, key)
	m.mu.Unlock()
	if pending {
		return core.Check{}, false, false
	}
	c = core.Check{Project: p.Name, SHA: sha, Kind: "test", OK: true, Where: "forge", At: time.Now().UTC(),
		Output: "the forge's checks passed on this commit: " + strings.Join(names, ", ")}
	m.C.J.MustAppend("effect.checked", c) // after m.mu: the journal's apply takes it
	return c, true, false
}
