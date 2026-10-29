package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/calls"
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
// canary that kept its candidate, so one update a day at most
// by default (release.train_every). A rollback
// moved nothing, so it doesn't spend the day (issue #1922).
var trainEvery = 24 * time.Hour

// canaryRetry is how long the train waits after a canary that moved nothing:
// one that couldn't run tries again, and after one that measured a rollback
// the next newer safe pre-release gets its canary, an hour on.
var canaryRetry = time.Hour

// canaryTimeout bounds one canary run, as GateTimeout bounds a gate.
var canaryTimeout = 2 * time.Hour

// The train's cadence is a call, not a rule (#1940): an agent that sees a
// safe release held back by the day, or a canary that keeps failing on its
// own setup, changes it at once with `hive agent call`.
var (
	callTrainEvery    = calls.Duration("release.train_every", "how long after a canary that kept its candidate the next canary leaves, so how often the Hive moves to a new release at most; 0 runs a canary as soon as a newer pre-release is safe here", &trainEvery)
	callCanaryRetry   = calls.Duration("release.canary_retry", "how long the train waits after a canary that moved nothing (one that couldn't run, or a measured rollback) before the next canary; 0 goes at the next look", &canaryRetry)
	callCanaryTimeout = calls.Duration("release.canary_timeout", "how long one canary run may take before it is stopped", &canaryTimeout).AtLeast(10 * time.Minute)
)

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
	Tag     string `json:"tag"`
	From    string `json:"from"`
	Head    string `json:"head"`
	Base    string `json:"base"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
	Report  string `json:"report,omitempty"`
	// Measured says the verdict compared both sides on the work. A rollback
	// that didn't (the candidate failed its own build or tests in the
	// canary, though its checks passed here) failed on the canary's own
	// environment, so it turns back that tag but doesn't spend the hour a
	// measured rollback does.
	Measured bool      `json:"measured,omitempty"`
	At       time.Time `json:"at"`
}

// spends says whether a verdict spends the train's day: only a keep, which
// moved the Hive.
func (c canary) spends() bool {
	return c.Outcome == core.GateKeep
}

// canaries are this Hive's canary verdicts, oldest first.
func (m *Module) canaries() []canary {
	cs, _ := m.trainRecord()
	return cs
}

// trainRecord is this Hive's canary verdicts, oldest first, and when an
// agent last asked for the train again (release.train_again): verdicts
// before then neither spend the day nor turn their tag back.
func (m *Module) trainRecord() (cs []canary, again time.Time) {
	m.C.J.Replay(func(e core.Event) {
		var c canary
		switch {
		case e.Kind == "release.canary" && e.Decode(&c) == nil:
			cs = append(cs, c)
		case e.Kind == "release.train_again":
			again = e.At
		}
	})
	return cs, again
}

// train picks what the train moves to, given the newest pre-release safe
// here (from the checks): the newest one a canary kept, newer than this
// release, or "". When none is kept and the day's canary is due, it starts one
// on safe, in the background. why says what the train waits for.
func (m *Module) train(repo, safe string) (tag string, why []string) {
	cs, again := m.trainRecord()
	var last time.Time   // the last verdict that spent a day
	var rolled time.Time // the last measured rollback, which spent an hour
	kept := map[string]bool{}
	for _, c := range cs {
		if c.spends() && c.At.After(again) {
			last = c.At
		}
		if c.Outcome == core.GateRollback && c.Measured && c.At.After(again) {
			rolled = c.At
		}
		if c.Outcome == core.GateKeep {
			kept[c.Tag] = true
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
		if !cs[i].At.After(again) {
			break
		}
		switch c := cs[i]; {
		case c.Outcome == core.GateRollback && !c.Measured:
			return "", []string{safe + ": its canary failed it before measuring it (" + core.Clip(c.Reason, 200) + "), though its checks passed here: the canary's own environment, so it didn't spend the day; a newer pre-release rides the train as soon as it is safe here, and `hive agent train` runs this one again"}
		case c.Outcome == core.GateRollback:
			return "", []string{safe + ": its canary rolled it back (" + core.Clip(c.Reason, 200) + "); a newer pre-release rides the train " + callCanaryRetry.Of(m.C.J).String() + " after it"}
		case c.Outcome == core.GateCantRun && time.Since(c.At) < callCanaryRetry.Of(m.C.J):
			return "", []string{safe + ": its canary couldn't run (" + core.Clip(c.Reason, 200) + "); it tries again after " + c.At.Add(callCanaryRetry.Of(m.C.J)).UTC().Format("15:04Z")}
		}
		break
	}
	if next := last.Add(callTrainEvery.Of(m.C.J)); time.Now().Before(next) {
		return "", []string{safe + ": safe here; the next train leaves after " + next.UTC().Format("2006-01-02 15:04Z")}
	}
	if next := rolled.Add(callCanaryRetry.Of(m.C.J)); time.Now().Before(next) {
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
	job := CanaryJob{Project: self, Tag: tag, Head: head, Base: base, Timeout: callCanaryTimeout.Of(m.C.J),
		Out: filepath.Join(m.C.WS.Gates(), self.Name, "canary", tag+"-"+time.Now().UTC().Format("20060102-150405"))}
	m.C.J.MustAppend("release.canary_started", map[string]string{"tag": tag, "from": from, "head": head, "base": base, "out": job.Out})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), job.Timeout+15*time.Minute)
		defer cancel()
		g := m.Canary(ctx, job)
		c := canary{Tag: tag, From: from, Head: head, Base: base, Outcome: g.Outcome, Reason: g.Reason, Report: g.Report, Measured: g.Measured, At: time.Now().UTC()}
		if c.Outcome == "" {
			c.Outcome = core.GateCantRun
		}
		m.C.J.MustAppend("release.canary", c)
		if c.Outcome == core.GateRollback {
			text := "The canary rolled back " + tag + " against " + from + ", so the Hive stays on " + from + ": " + core.Clip(c.Reason, 300)
			if !c.Measured {
				text += ". It failed before it was measured, though its checks passed here: the canary's own environment, so the day isn't spent; a newer pre-release rides as soon as it is safe, and `hive agent train` runs this one again"
			}
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "release", "ref": "release:canary@" + tag, "text": text})
		}
		m.pmu.Lock()
		m.canarying = ""
		m.followed = time.Time{} // a keep installs at the next tick, not the next look
		m.pmu.Unlock()
	}()
	return nil
}

// opTrain is `hive agent train`: any agent runs the train again, at the next
// look, whatever the day: the newest pre-release safe here rides as a
// canary, even one an earlier canary turned back (its environment fixed, a
// flake), and the verdicts before it don't count against the day.
func (m *Module) opTrain(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Why string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Why) == "" {
		return nil, errors.New("say why the train runs again: --why \"...\"")
	}
	if ch := m.Channel(); ch != Train {
		return nil, fmt.Errorf("this Hive doesn't ride the train: it follows %q", ch)
	}
	m.pmu.Lock()
	defer m.pmu.Unlock()
	if m.canarying != "" {
		return nil, fmt.Errorf("the canary is running on %s", m.canarying)
	}
	m.C.J.MustAppend("release.train_again", map[string]string{"by": r.ID, "why": a.Why})
	m.followed = time.Time{} // the next tick looks
	return map[string]string{"note": "the train runs again at the next look: the newest pre-release safe here rides it as a canary against " + core.Version + ", even one an earlier canary turned back"}, nil
}
