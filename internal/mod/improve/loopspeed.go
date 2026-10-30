package improve

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// loopWindow is how far back the digest's loop speed looks.
const loopWindow = 7 * 24 * time.Hour

// loopTask is one task's way through the loop, from the journal: when it was
// filed, when it first reached merging (its tests and read passed, so only
// the gate, the forge's checks and the merge itself were left) and when it
// merged.
type loopTask struct {
	project              string
	filed, ready, merged time.Time
}

// applyLoop notes t's way through the loop as saved at at. The caller holds m.mu.
func (m *Module) applyLoop(t work.Task, at time.Time) {
	if t.ID == "" {
		return
	}
	lt := m.loops[t.ID]
	if lt == nil {
		lt = &loopTask{project: t.Project}
		m.loops[t.ID] = lt
	}
	if lt.filed.IsZero() {
		lt.filed = t.Created
	}
	switch {
	case t.Stage == work.StageMerging && lt.ready.IsZero():
		lt.ready = at
	case (t.Stage == work.StageMerged || t.Stage == work.StageDone) && t.MergeSHA != "" && lt.merged.IsZero():
		lt.merged = at
	}
}

// LoopSpeed says how fast project's real loop ran over the loopWindow before
// now, from the journal: filed to merged and ready to merged (median and p90),
// and merges each UTC day. It is "" when nothing merged in that time.
func (m *Module) LoopSpeed(project string, now time.Time) string {
	from := now.Add(-loopWindow)
	var filed, ready []time.Duration
	perDay := map[string]int{}
	m.mu.Lock()
	for _, lt := range m.loops {
		if lt.project != project || lt.merged.IsZero() || lt.merged.Before(from) || lt.merged.After(now) {
			continue
		}
		perDay[lt.merged.UTC().Format("01-02")]++
		if !lt.filed.IsZero() && !lt.filed.After(lt.merged) {
			filed = append(filed, lt.merged.Sub(lt.filed))
		}
		if !lt.ready.IsZero() && !lt.ready.After(lt.merged) {
			ready = append(ready, lt.merged.Sub(lt.ready))
		}
	}
	m.mu.Unlock()
	n := 0
	for _, c := range perDay {
		n += c
	}
	if n == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Loop speed, the last %d days from the journal (%d merged):\n", int(loopWindow.Hours()/24), n)
	if len(filed) > 0 {
		fmt.Fprintf(&b, "- filed to merged: median %s, p90 %s (%d tasks)\n", hours(quantile(filed, 0.5)), hours(quantile(filed, 0.9)), len(filed))
	}
	if len(ready) > 0 {
		fmt.Fprintf(&b, "- waiting to merge, from first reaching merging (tests and read passed) to merged: median %s, p90 %s (%d tasks)\n", hours(quantile(ready, 0.5)), hours(quantile(ready, 0.9)), len(ready))
	}
	var days []string
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(now); d = d.Add(24 * time.Hour) {
		k := d.Format("01-02")
		days = append(days, fmt.Sprintf("%s %d", k, perDay[k]))
	}
	fmt.Fprintf(&b, "- merges a day (UTC): %.1f on average; %s\n", float64(n)/(loopWindow.Hours()/24), strings.Join(days, ", "))
	return b.String()
}

// quantile is the nearest-rank q quantile of ds, which must not be empty.
func quantile(ds []time.Duration, q float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	i := int(math.Ceil(float64(len(s))*q)) - 1
	return s[max(0, min(i, len(s)-1))]
}

// hours writes d in hours, or minutes under one.
func hours(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	return fmt.Sprintf("%.1fh", d.Hours())
}
