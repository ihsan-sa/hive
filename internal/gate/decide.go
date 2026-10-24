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
)

// Metric is one number a probe measured. Higher says which way is better.
type Metric struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Higher bool    `json:"higher"`
	Info   bool    `json:"info,omitempty"` // shown, not judged: the side's own word, not measured from outside it
}

// Result is one run of one probe on one side.
type Result struct {
	Probe   string   `json:"probe"`
	Rep     int      `json:"rep"`
	Pass    bool     `json:"pass"`
	Detail  string   `json:"detail,omitempty"`
	Seconds float64  `json:"seconds"`
	Metrics []Metric `json:"metrics,omitempty"`
}

// Options tune how much better counts as better.
type Options struct {
	Floor    float64 // a metric must move by at least this share of the control's mean (default 2%)
	AllowTie bool    // keep a candidate that regresses nothing and gains nothing
}

// Verdict is the gate's decision and every reason behind it.
type Verdict struct {
	Keep        bool     `json:"keep"`
	Reason      string   `json:"reason"`
	Regressions []string `json:"regressions,omitempty"`
	Gains       []string `json:"gains,omitempty"`
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
	type tally struct{ pass, n int }
	count := func(rs []Result) (map[string]*tally, map[key][]float64, map[key]bool) {
		t, m, hi := map[string]*tally{}, map[key][]float64{}, map[key]bool{}
		for _, r := range rs {
			if t[r.Probe] == nil {
				t[r.Probe] = &tally{}
			}
			t[r.Probe].n++
			if !r.Pass {
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
			}
		}
		return t, m, hi
	}
	ct, cm, chi := count(cand)
	kt, km, _ := count(ctrl)
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
			if cr < kr {
				v.Regressions = append(v.Regressions, fmt.Sprintf("%s passes %d/%d, the control %d/%d", p, c.pass, c.n, k.pass, k.n))
			} else if cr > kr {
				v.Gains = append(v.Gains, fmt.Sprintf("%s passes %d/%d, the control %d/%d", p, c.pass, c.n, k.pass, k.n))
			}
		}
	}
	var mk []key
	for k := range cm {
		if _, ok := km[k]; ok {
			mk = append(mk, k)
		}
	}
	sort.Slice(mk, func(i, j int) bool { return mk[i].probe+mk[i].metric < mk[j].probe+mk[j].metric })
	for _, k := range mk {
		a, b := cm[k], km[k]
		diff := mean(a) - mean(b)
		if !chi[k] {
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
	switch {
	case len(v.Regressions) > 0:
		v.Reason = "worse than the current Hive: " + v.Regressions[0]
	case len(v.Gains) > 0:
		v.Keep, v.Reason = true, "better than the current Hive with no regression: "+v.Gains[0]
	case o.AllowTie:
		v.Keep, v.Reason = true, "no regression, no gain; ties are allowed for this change"
	default:
		v.Reason = "not better than the current Hive: nothing moved beyond the noise"
	}
	return v
}
