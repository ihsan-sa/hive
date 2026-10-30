package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/stats"
	"github.com/ihsan-sa/hive-standalone-build/internal/text"
)

// ---- spend and activity ------------------------------------------------------

// glance is the feed's one line of figures: spend today and this month, and
// what happened in the last hour. Empty before the daemon sends any.
func (m *model) glance(w int) string {
	v := m.st.Stats
	if v == nil {
		return ""
	}
	h := v.Hour
	hour := []string{fmt.Sprintf("%d %s", h.Messages, plural(h.Messages, "message", "messages")), fmt.Sprintf("%d %s", h.Turns, plural(h.Turns, "turn", "turns"))}
	if h.Merged > 0 {
		hour = append(hour, fmt.Sprintf("%d merged", h.Merged))
	}
	if h.Reverted > 0 {
		hour = append(hour, stWarn.Render(fmt.Sprintf("%d reverted", h.Reverted)))
	}
	parts := []string{"today " + money(v.Today.Total), m.monthText(v.Month.Total), "last hour: " + strings.Join(hour, ", ")}
	more := "   s more"
	return clip(strings.Join(parts, " · "), max(w-len(more), 10)) + stDim.Render(more)
}

// monthText is this month's spend against monthly_usd, amber past it.
func (m *model) monthText(spent float64) string {
	limit := m.st.Config.MonthlyUSD
	if limit <= 0 {
		return "month " + money(spent)
	}
	s := fmt.Sprintf("month %s of %s", money(spent), money(limit))
	if spent > limit {
		return stWarn.Render(s)
	}
	return s
}

// activityText is a span's happenings; full spells out every count, else
// only what isn't zero (the turns always).
func activityText(a stats.Activity, full bool) string {
	var parts []string
	add := func(n int, one, many string) {
		if full || n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, one, many)))
		}
	}
	add(a.Messages, "message", "messages")
	parts = append(parts, fmt.Sprintf("%d %s", a.Turns, plural(a.Turns, "turn", "turns")))
	add(a.Started, "run started", "runs started")
	add(a.Finished, "finished", "finished")
	add(a.Merged, "merged", "merged")
	if a.Peer > 0 {
		parts = append(parts, fmt.Sprintf("%d %s between workers", a.Peer, plural(a.Peer, "message", "messages")))
	}
	if a.Helped > 0 {
		parts = append(parts, fmt.Sprintf("%d helped-by", a.Helped))
	}
	if a.Reverted > 0 {
		parts = append(parts, stWarn.Render(fmt.Sprintf("%d reverted", a.Reverted)))
	} else if full {
		parts = append(parts, "0 reverted")
	}
	return strings.Join(parts, " · ")
}

// statRows are the status tree's spend, activity and all-time rows.
func (m *model) statRows(add func(row) bool) {
	v := m.st.Stats
	if v == nil {
		return
	}
	if add(row{key: "spend", text: fmt.Sprintf("spend  today %s · %s", money(v.Today.Total), m.monthText(v.Month.Total)), expand: v.Month.Total > 0, def: true}) {
		for _, s := range []struct {
			name string
			sp   stats.Spend
		}{{"today", v.Today}, {"this month", v.Month}} {
			if s.sp.Total == 0 {
				continue
			}
			add(row{key: "spend:" + s.name, depth: 1, text: fmt.Sprintf("%-11s by project  %s", s.name, split(s.sp.Projects, "workspace"))})
			add(row{key: "spend:" + s.name + ":roles", depth: 1, text: fmt.Sprintf("%-11s by role     %s", "", split(s.sp.Roles, "other"))})
		}
	}
	waiting := waitStyle(len(m.st.Asks)).Render(fmt.Sprintf("%d waiting on you", len(m.st.Asks)))
	if add(row{key: "activity", text: fmt.Sprintf("activity  %d turns in the last hour, %d in the last day · %s", v.Hour.Turns, v.Day.Turns, waiting), expand: true, def: true}) {
		add(row{key: "activity:hour", depth: 1, text: "last hour  " + activityText(v.Hour, true)})
		add(row{key: "activity:day", depth: 1, text: "last day   " + activityText(v.Day, true)})
	}
	a := v.All
	n := 0
	for _, c := range a.Runs {
		n += c
	}
	since := ""
	if !a.Since.IsZero() {
		since = " since " + a.Since.Local().Format("Jan 2 2006")
	}
	if add(row{key: "alltime", text: fmt.Sprintf("all time%s  %d agents · %.1f agent-hours · %s", since, n, a.AgentHours, money(a.Spend)), expand: n > 0, def: true}) {
		var roles []string
		for _, r := range byCount(a.Runs) {
			roles = append(roles, fmt.Sprintf("%s %d", text.Visible(r), a.Runs[r]))
		}
		add(row{key: "all:agents", depth: 1, text: fmt.Sprintf("agents   %d: %s · %d turns", n, strings.Join(roles, " · "), a.Turns)})
		add(row{key: "all:tokens", depth: 1, text: "tokens   " + tokensText(a.Tokens)})
		for _, name := range slices.SortedFunc(maps.Keys(a.Models), func(x, y string) int {
			return cmp.Or(cmp.Compare(a.Models[y].Spend, a.Models[x].Spend), cmp.Compare(x, y))
		}) {
			mu := a.Models[name]
			add(row{key: "all:model:" + name, depth: 1, text: fmt.Sprintf("%-8s %s · %d turns · %s", text.Visible(cmp.Or(name, "default")), money(mu.Spend), mu.Turns, stDim.Render(tokensText(mu.Tokens)))})
		}
		rev := fmt.Sprintf("%d reverted", a.Reverted)
		if a.Reverted > 0 {
			rev = stWarn.Render(rev)
		}
		if a.Met+a.Peer+a.Helped > 0 {
			add(row{key: "all:peers", depth: 1, text: fmt.Sprintf("peers    %d %s met on shared files · %d of them talked · %d %s between workers · %d helped-by", a.Met, plural(a.Met, "pair", "pairs"), a.Talked, a.Peer, plural(a.Peer, "message", "messages"), a.Helped)})
		}
		add(row{key: "all:prs", depth: 1, text: fmt.Sprintf("PRs      %s · %s · %d %s cut", stGood.Render(fmt.Sprintf("%d merged", a.Merged)), rev, a.Releases, plural(a.Releases, "release", "releases"))})
	}
}

// split is a spend map, largest first: "hive $3.20 · web $1.10".
func split(m map[string]float64, blank string) string {
	var parts []string
	for _, k := range slices.SortedFunc(maps.Keys(m), func(x, y string) int { return cmp.Or(cmp.Compare(m[y], m[x]), cmp.Compare(x, y)) }) {
		parts = append(parts, text.Visible(cmp.Or(k, blank))+" "+money(m[k]))
	}
	return strings.Join(parts, " · ")
}

// byCount is a count map's keys, largest first.
func byCount(m map[string]int) []string {
	keys := sortedKeys(m)
	slices.SortStableFunc(keys, func(x, y string) int { return cmp.Compare(m[y], m[x]) })
	return keys
}

func tokensText(t harness.Tokens) string {
	return fmt.Sprintf("in %s · out %s · cache read %s · cache write %s", tokenCount(t.Input), tokenCount(t.Output), tokenCount(t.CacheRead), tokenCount(t.CacheWrite))
}

func money(f float64) string { return fmt.Sprintf("$%.2f", f) }

// tokenCount is a token count in a few characters: 950, 12.3k, 4.1M, 2.0B.
func tokenCount(n int64) string {
	switch f := float64(n); {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1e6:
		return fmt.Sprintf("%.1fk", f/1e3)
	case n < 1e9:
		return fmt.Sprintf("%.1fM", f/1e6)
	default:
		return fmt.Sprintf("%.1fB", f/1e9)
	}
}
