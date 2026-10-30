package improve

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// ScoreJob is the forge's job that scores main: on every push to main it
// runs the project's own score command and ends with the line ScoreLine
// reads (.github/workflows/ci.yml). A project whose CI tests on the forge
// (forge_tests) and has such a job is scored there, not on this machine:
// the score is speed, and here worker turns load the machine to 100-338 on
// 20 cores, so a reading took 30 minutes and gave no number, or one 2-3
// times too low (issue #2172).
const ScoreJob = "score"

// ScoreLine is the job's last line of its own: "hive-score: <number>". The
// script's own text, which a job log echoes, holds no number there.
var ScoreLine = regexp.MustCompile(`(?m)^hive-score: (-?[0-9]+(?:\.[0-9]+)?)\s*$`)

// forgeScore reads sha's score from the forge's ScoreJob, as forgeTest reads
// its tests: ok is a number, recorded as sha's score check (Where "forge");
// wait is the job still going or not yet listed while the merge is younger
// than forgeWait. Anything else (no such job on this commit, a red, a log
// with no number, a forge that can't be read) gives neither and the score
// runs here, as it did before.
func (m *Module) forgeScore(p core.Project, sha string) (c core.Check, ok, wait bool) {
	key := "score:" + p.Name + "@" + sha
	none := func() (core.Check, bool, bool) {
		m.mu.Lock()
		delete(m.forgeAt, key)
		m.mu.Unlock()
		return core.Check{}, false, false
	}
	logs, can := m.C.Effects.Forge.(core.CheckLogs)
	if !can {
		return none()
	}
	runs, err := m.C.Effects.Forge.Checks(p.Repo, sha)
	if err != nil {
		return none()
	}
	var job *core.CheckRun
	for i := range runs {
		if runs[i].Name == ScoreJob {
			job = &runs[i]
		}
	}
	if job == nil || job.State == core.CheckPending {
		m.mu.Lock()
		first, seen := m.forgeAt[key]
		if !seen {
			if m.forgeAt == nil {
				m.forgeAt = map[string]time.Time{}
			}
			first = time.Now()
			if at, ok := m.merged[sha]; ok && at.Before(first) {
				first = at
			}
			m.forgeAt[key] = first
		}
		m.mu.Unlock()
		// A commit whose workflow has no score job lists every other check
		// and never this one: once the rest are done, it isn't coming.
		if time.Since(first) < forgeWait && (job != nil || !settled(runs)) {
			return core.Check{}, false, true
		}
		return none()
	}
	if job.State != core.CheckPass {
		return none()
	}
	log, err := logs.CheckLog(p.Repo, *job)
	if err != nil {
		return none()
	}
	v, found := scoreLine(log)
	if !found {
		return none()
	}
	m.mu.Lock()
	delete(m.forgeAt, key)
	m.mu.Unlock()
	c = core.Check{Project: p.Name, SHA: sha, Kind: "score", OK: true, Score: &v, Where: "forge", At: time.Now().UTC(),
		Output: "the forge's " + ScoreJob + " job scored this commit: " + strconv.FormatFloat(v, 'g', -1, 64)}
	m.C.J.MustAppend("effect.checked", c)
	return c, true, false
}

// settled says every listed check is done: none pending, and at least one.
func settled(runs []core.CheckRun) bool {
	for _, r := range runs {
		if r.State == core.CheckPending {
			return false
		}
	}
	return len(runs) > 0
}

// scoreLine is the last ScoreLine number in a job's log.
func scoreLine(log string) (float64, bool) {
	all := ScoreLine.FindAllStringSubmatch(log, -1)
	if len(all) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(all[len(all)-1][1], 64)
	return v, err == nil
}

// forgeScoreAt is sha's forge score without waiting: the one recorded, or
// the forge's answer now. A parent merged before the job existed has none.
func (m *Module) forgeScoreAt(p core.Project, sha string) (float64, bool) {
	if c, ok := m.C.Effects.GetCheck(p.Name, sha, "score"); ok && c.Where == "forge" && c.OK && c.Score != nil {
		return *c.Score, true
	}
	if c, ok, _ := m.forgeScore(p, sha); ok {
		return *c.Score, true
	}
	return 0, false
}

// forgePaired is paired for a merge scored on the forge: its parent's forge
// reading, taken on a runner as idle as the merge's, stands for the parent,
// and nothing is scored here. False when the parent has none, or the merge
// changed the score (its parent would need scoring under the new one, which
// only a run here can do): the merge then starts a new baseline.
func (m *Module) forgePaired(p core.Project, sha string, first float64) (parent string, mine, theirs float64, readings string, ok bool) {
	out, err := core.Git(m.C.WS.Project(p.Name), "rev-parse", "-q", "--verify", sha+"^")
	parent = strings.TrimSpace(out)
	cfg := m.C.Effects.Config(p, sha)
	if err != nil || parent == "" || cfg.Score == nil || m.version(p, parent, m.C.Effects.Config(p, parent)) != m.version(p, sha, cfg) {
		return "", 0, 0, "", false
	}
	theirs, ok = m.forgeScoreAt(p, parent)
	if !ok {
		return "", 0, 0, "", false
	}
	return parent, first, theirs, "merge " + strconv.FormatFloat(first, 'g', -1, 64) + ", parent " + strconv.FormatFloat(theirs, 'g', -1, 64) + ", both on the forge", true
}

// ForgeVersion marks a score read on the forge: a runner's number isn't
// this machine's, so the first one starts a new baseline rather than be
// compared with a best read here.
const ForgeVersion = "+forge"

// tipAhead says main's tip head is past sha and is itself a merge waiting
// to be measured, which will cover sha: a commit pushed to main by hand has
// no measure coming, so a merge under it is still measured itself.
func (m *Module) tipAhead(p core.Project, sha, head string) bool {
	if head == "" || head == sha || m.measured(p.Name, head) || !m.after(p, head, sha) {
		return false
	}
	return len(m.Work.List(func(t work.Task) bool {
		return t.Project == p.Name && (t.MergeSHA == head || slices.ContainsFunc(t.Banks, func(b work.Bank) bool { return b.MergeSHA == head }))
	})) > 0
}
