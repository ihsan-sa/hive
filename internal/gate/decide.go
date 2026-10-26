// Package gate decides whether a candidate Hive replaces the current one. It
// builds the candidate's exact head, installs it as a child Hive in a fresh
// sandbox with its own state and a fresh org test repository, runs the child
// on real work, runs the current Hive the same way as the control, and keeps
// the change only if the candidate does better. See docs/gate.md.
package gate

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Metric is one number a probe measured. Higher says which way is better.
type Metric struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Higher bool    `json:"higher"`
	Info   bool    `json:"info,omitempty"` // shown, not judged: the side's own word, not measured from outside it
	// Given names metrics of the same probe that must be equal on both sides
	// for this one to be judged: time counts only between sides that did the
	// same work, so doing less never wins it.
	Given []string `json:"given,omitempty"`
}

// Result is one run of one probe on one side.
type Result struct {
	Probe   string   `json:"probe"`
	Rep     int      `json:"rep"`
	Pass    bool     `json:"pass"`
	Detail  string   `json:"detail,omitempty"`
	Seconds float64  `json:"seconds"`
	Metrics []Metric `json:"metrics,omitempty"`
	Failed  []string `json:"failed,omitempty"` // the tests that failed in it, by name, for a probe that runs tests
}

// Options tune how much better counts as better.
type Options struct {
	Floor    float64 // a metric must move by at least this share of the control's mean (default 2%)
	AllowTie bool    // keep a candidate that regresses nothing and gains nothing
	// Fixed are the candidate's own tests that pass on it and fail on the
	// control (findFixes): each is a gain.
	Fixed []string
}

// Verdict is the gate's decision and every reason behind it.
type Verdict struct {
	Keep        bool     `json:"keep"`
	Reason      string   `json:"reason"`
	Regressions []string `json:"regressions,omitempty"`
	Gains       []string `json:"gains,omitempty"`
	Unmeasured  []string `json:"unmeasured,omitempty"` // probes that failed on both sides: never measured
	// Differs names probes that pass as often on both sides but fail
	// different tests: not judged, shown so a new failure isn't hidden by a tie.
	Differs []string `json:"differs,omitempty"`
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func sd(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m, s := mean(v), 0.0
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

type key struct{ probe, metric string }

// Decide compares the candidate's results with the control's, probe by probe
// and metric by metric. A probe the candidate passes less often, or a metric
// that is worse beyond the noise, is a regression; the mirror is a gain. The
// noise is the larger of the floor and twice the standard error of the
// difference of the two means, so repeated runs tighten it.
func Decide(cand, ctrl []Result, o Options) Verdict {
	if o.Floor == 0 {
		o.Floor = 0.02
	}
	type tally struct {
		pass, n int
		failed  map[string]int // test name: the runs it failed in
	}
	given := map[key][]string{}
	count := func(rs []Result) (map[string]*tally, map[key][]float64, map[key]bool) {
		t, m, hi := map[string]*tally{}, map[key][]float64{}, map[key]bool{}
		for _, r := range rs {
			if t[r.Probe] == nil {
				t[r.Probe] = &tally{}
			}
			t[r.Probe].n++
			if !r.Pass {
				for _, f := range r.Failed {
					if t[r.Probe].failed == nil {
						t[r.Probe].failed = map[string]int{}
					}
					t[r.Probe].failed[f]++
				}
				continue
			}
			t[r.Probe].pass++
			for _, x := range r.Metrics {
				if x.Info {
					continue
				}
				k := key{r.Probe, x.Name}
				m[k] = append(m[k], x.Value)
				hi[k] = x.Higher
				if len(x.Given) > 0 {
					given[k] = x.Given
				}
			}
		}
		return t, m, hi
	}
	ct, cm, chi := count(cand)
	kt, km, khi := count(ctrl)
	var v Verdict
	probes := map[string]bool{}
	for p := range ct {
		probes[p] = true
	}
	for p := range kt {
		probes[p] = true
	}
	names := make([]string, 0, len(probes))
	for p := range probes {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		c, k := ct[p], kt[p]
		switch {
		case c == nil:
			v.Regressions = append(v.Regressions, fmt.Sprintf("%s: the candidate did not run it", p))
		case k == nil:
			// Nothing to compare against; a candidate-only probe is not a gain.
		default:
			cr, kr := float64(c.pass)/float64(c.n), float64(k.pass)/float64(k.n)
			if c.pass == 0 && k.pass == 0 {
				// Broken on both sides: that path was never measured, so it
				// cannot count as even.
				// Naming each side's failing tests shows whether they are the
				// same ones (a stale base or the environment) or new.
				v.Unmeasured = append(v.Unmeasured, fmt.Sprintf("%s failed on both sides (0/%d, the control 0/%d)", p, c.n, k.n)+failing(c.failed, c.n, k.failed, k.n))
			} else if cr != kr && c.pass > 0 && k.pass > 0 && math.Abs(cr-kr) <= 1/float64(min(c.n, k.n))+1e-9 {
				// One run apart with the probe passing on both sides: a single
				// flaky run decides nothing (2/3 against 3/3 is noise at n=3).
				// Shown, not judged, like a tie failing different tests.
				v.Differs = append(v.Differs, fmt.Sprintf("%s passes %d/%d, the control %d/%d, within one run's noise", p, c.pass, c.n, k.pass, k.n)+failing(c.failed, c.n, k.failed, k.n))
			} else if cr < kr {
				v.Regressions = append(v.Regressions, fmt.Sprintf("%s passes %d/%d, the control %d/%d", p, c.pass, c.n, k.pass, k.n)+failing(c.failed, c.n, k.failed, k.n))
			} else if cr > kr {
				v.Gains = append(v.Gains, fmt.Sprintf("%s passes %d/%d, the control %d/%d", p, c.pass, c.n, k.pass, k.n)+failing(c.failed, c.n, k.failed, k.n))
			} else if !sameTests(c.failed, k.failed) {
				v.Differs = append(v.Differs, fmt.Sprintf("%s passes %d/%d, the control %d/%d, failing different tests", p, c.pass, c.n, k.pass, k.n)+failing(c.failed, c.n, k.failed, k.n))
			}
		}
	}
	// A judged metric the control reports and a passing candidate leaves out
	// is a regression, so a probe can't hide a worse number by dropping it.
	var gone []string
	for k := range km {
		if _, ok := cm[k]; !ok && ct[k.probe] != nil && ct[k.probe].pass > 0 {
			gone = append(gone, fmt.Sprintf("%s %s: the control reports it, the candidate does not", k.probe, k.metric))
		}
	}
	sort.Strings(gone)
	v.Regressions = append(v.Regressions, gone...)
	var mk []key
	for k := range cm {
		if _, ok := km[k]; !ok {
			continue
		}
		if chi[k] != khi[k] {
			// The direction is the control's to say; sides that disagree on
			// it can't be compared.
			v.Regressions = append(v.Regressions, fmt.Sprintf("%s %s: the candidate says %s is better, the control does not", k.probe, k.metric, map[bool]string{true: "higher", false: "lower"}[chi[k]]))
			continue
		}
		mk = append(mk, k)
	}
	sort.Slice(mk, func(i, j int) bool { return mk[i].probe+mk[i].metric < mk[j].probe+mk[j].metric })
	for _, k := range mk {
		a, b := cm[k], km[k]
		unequal := ""
		for _, g := range given[k] {
			gk := key{k.probe, g}
			if len(cm[gk]) == 0 || len(km[gk]) == 0 || mean(cm[gk]) != mean(km[gk]) {
				unequal = g
				break
			}
		}
		if unequal != "" {
			continue // judged only between sides that did the same work
		}
		diff := mean(a) - mean(b)
		if !khi[k] {
			diff = -diff
		}
		margin := math.Max(o.Floor*math.Abs(mean(b)), 2*math.Sqrt(sd(a)*sd(a)/float64(len(a))+sd(b)*sd(b)/float64(len(b))))
		text := fmt.Sprintf("%s %s: candidate %.4g, control %.4g (noise ±%.3g, n=%d/%d)", k.probe, k.metric, mean(a), mean(b), margin, len(a), len(b))
		switch {
		case diff > margin:
			v.Gains = append(v.Gains, text)
		case diff < -margin:
			v.Regressions = append(v.Regressions, text)
		}
	}
	v.Gains = append(v.Gains, o.Fixed...)
	switch {
	case len(v.Regressions) > 0:
		v.Reason = "worse than the current Hive: " + v.Regressions[0]
	case len(v.Unmeasured) > 0:
		// Not a keep, and not the candidate's failure either: the gate
		// could not measure this path (Run reports it as could not run).
		v.Reason = "not measured: " + v.Unmeasured[0]
	case len(v.Gains) > 0:
		v.Keep, v.Reason = true, "better than the current Hive with no regression: "+v.Gains[0]
	case o.AllowTie:
		v.Keep, v.Reason = true, "no regression, no gain; ties are allowed for this change"
	default:
		v.Reason = "not better than the current Hive: nothing moved beyond the noise"
	}
	return v
}

// failing names the tests that failed in each side's runs of a probe, most
// often first, with how many of its runs each failed in:
// " (failing: TestA 3/3, TestB 1/3; the control failing: TestB 2/3)".
func failing(c map[string]int, cn int, k map[string]int, kn int) string {
	var parts []string
	if t := tests(c, cn); t != "" {
		parts = append(parts, "failing: "+t)
	}
	if t := tests(k, kn); t != "" {
		parts = append(parts, "the control failing: "+t)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// sameTests says whether two sides failed the same tests, however often.
func sameTests(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for t := range a {
		if b[t] == 0 {
			return false
		}
	}
	return true
}

func tests(f map[string]int, n int) string {
	if len(f) == 0 {
		return ""
	}
	names := make([]string, 0, len(f))
	for t := range f {
		names = append(names, t)
	}
	sort.Slice(names, func(i, j int) bool {
		if f[names[i]] != f[names[j]] {
			return f[names[i]] > f[names[j]]
		}
		return names[i] < names[j]
	})
	more := ""
	if len(names) > 5 {
		names, more = names[:5], fmt.Sprintf(" and %d more", len(names)-5)
	}
	for i, t := range names {
		names[i] = fmt.Sprintf("%s %d/%d", t, f[t], n)
	}
	return strings.Join(names, ", ") + more
}
