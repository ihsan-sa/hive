package release

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// Train is the daily release train (issue #1836): the Hive runs one release
// and stays on it; once a day the newest pre-release whose checks passed here
// runs as a canary, a child Hive on real work beside the running release (the
// recursive gate, ties allowed: it must break nothing), and only a canary
// that keeps it moves the Hive. It is the default on the Hive that releases
// the Hive, so the engine isn't rebuilt under the work running on it.
const Train = "train"

// trainEvery is how often the Hive moves: once a day, counted from the last
// canary that kept its candidate, so one update a day at most. A rollback
// moved nothing, so it doesn't spend the day either.
var trainEvery = 24 * time.Hour

// canaryRetry is how long the train waits after a canary that moved nothing:
// one that couldn't run tries again, and one that rolled back gives the next
// newer safe pre-release its canary, an hour on.
var canaryRetry = time.Hour

// canaryTimeout bounds one canary run, as GateTimeout bounds a gate.
var canaryTimeout = 2 * time.Hour

// CanaryJob is one canary run: Head, the candidate's commit, against Base,
// the running release's, both in the self project's clone.
type CanaryJob struct {
	Project core.Project
	Tag     string
	Head    string
	Base    string
	Out     string
	Timeout time.Duration
}

// canary is one canary verdict as the journal holds it (release.canary).
type canary struct {
	Tag     string    `json:"tag"`
	From    string    `json:"from"`
	Head    string    `json:"head"`
	Base    string    `json:"base"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason"`
	Report  string    `json:"report,omitempty"`
	At      time.Time `json:"at"`
}

// canaries are this Hive's canary verdicts, oldest first.
func (m *Module) canaries() []canary {
	var out []canary
	m.C.J.Replay(func(e core.Event) {
		var c canary
		if e.Kind == "release.canary" && e.Decode(&c) == nil {
			out = append(out, c)
		}
	})
	return out
}

// train picks what the train moves to, given the newest pre-release safe
// here (from the checks): the newest one a canary kept, newer than this
// release, or "". When none is kept and the day's canary is due, it starts one
// on safe, in the background. why says what the train waits for.
func (m *Module) train(repo, safe string) (tag string, why []string) {
	cs := m.canaries()
	var last time.Time // the last keep, which spent a day
	var rolled time.Time // the last rollback, which spent an hour
	kept := map[string]bool{}
	for _, c := range cs {
		switch c.Outcome {
		case core.GateKeep:
			last = c.At
			kept[c.Tag] = true
		case core.GateRollback:
			rolled = c.At
		}
	}
	for t := range kept {
		if modules.Newer(core.Version, t) && (tag == "" || modules.Newer(tag, t)) && hasBinary(repo, t) == nil {
			tag = t
		}
	}
	if tag != "" || safe == "" {
		return tag, nil
	}
	m.pmu.Lock()
	running := m.canarying
	m.pmu.Unlock()
	if running != "" {
		return "", []string{"the canary is running on " + running}
	}
	for i := len(cs) - 1; i >= 0; i-- {
		if cs[i].Tag != safe || cs[i].From != core.Version {
			continue
		}
		switch c := cs[i]; {
		case c.Outcome == core.GateRollback:
			return "", []string{safe + ": its canary rolled it back (" + core.Clip(c.Reason, 200) + "); a newer pre-release rides the next train"}
		case c.Outcome == core.GateCantRun && time.Since(c.At) < canaryRetry:
			return "", []string{safe + ": its canary couldn't run (" + core.Clip(c.Reason, 200) + "); it tries again after " + c.At.Add(canaryRetry).UTC().Format("15:04Z")}
		}
		break
	}
	if next := last.Add(trainEvery); time.Now().Before(next) {
		return "", []string{safe + ": safe here; the next train leaves after " + next.UTC().Format("2006-01-02 15:04Z")}
	}
	if next := rolled.Add(canaryRetry); time.Now().Before(next) {
		return "", []string{safe + ": safe here; the last canary rolled back, so its canary runs after " + next.UTC().Format("2006-01-02 15:04Z")}
	}
	if err := m.startCanary(repo, safe); err != nil {
		return "", []string{safe + ": its canary can't start: " + err.Error()}
	}
	return "", []string{safe + ": its canary started against " + core.Version}
}

// startCanary runs the canary on tag against this release in the background
// and journals its verdict.
func (m *Module) startCanary(repo, tag string) error {
	if m.Canary == nil {
		return fmt.Errorf("this daemon has no canary runner")
	}
	var self core.Project
	for _, p := range m.C.Projects.List() {
		if p.Self && strings.EqualFold(p.Repo, repo) {
			self = p
		}
	}
	if self.Name == "" {
		return fmt.Errorf("no project here is %s itself, whose clone holds both commits", repo)
	}
	head, err := m.tagCommit(repo, tag)
	if err != nil {
		return fmt.Errorf("can't read %s's commit: %v", tag, err)
	}
	base, err := m.tagCommit(repo, core.Version)
	if err != nil {
		return fmt.Errorf("can't read %s's commit: %v", core.Version, err)
	}
	m.pmu.Lock()
	if m.canarying != "" {
		m.pmu.Unlock()
		return fmt.Errorf("the canary is running on %s", m.canarying)
	}
	m.canarying = tag
	m.pmu.Unlock()
	from := core.Version
	// Under the project's canary/, not recursive/, whose folders the work
	// module tidies when no gate of its own runs there.
	job := CanaryJob{Project: self, Tag: tag, Head: head, Base: base, Timeout: canaryTimeout,
		Out: filepath.Join(m.C.WS.Gates(), self.Name, "canary", tag+"-"+time.Now().UTC().Format("20060102-150405"))}
	m.C.J.MustAppend("release.canary_started", map[string]string{"tag": tag, "from": from, "head": head, "base": base, "out": job.Out})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), job.Timeout+15*time.Minute)
		defer cancel()
		g := m.Canary(ctx, job)
		c := canary{Tag: tag, From: from, Head: head, Base: base, Outcome: g.Outcome, Reason: g.Reason, Report: g.Report, At: time.Now().UTC()}
		if c.Outcome == "" {
			c.Outcome = core.GateCantRun
		}
		m.C.J.MustAppend("release.canary", c)
		if c.Outcome == core.GateRollback {
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "ref": "release:canary@" + tag,
				"text": "The canary rolled back " + tag + " against " + from + ", so the Hive stays on " + from + ": " + core.Clip(c.Reason, 300)})
		}
		m.pmu.Lock()
		m.canarying = ""
		m.followed = time.Time{} // a keep installs at the next tick, not the next look
		m.pmu.Unlock()
	}()
	return nil
}
