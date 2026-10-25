package harness

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Standing is the plan's usage state in plain words as of now, from the status
// the harness reports for its binding window: within its limit, near it
// (allowed_warning) or at it (rejected). A reading whose window has reset
// since says so rather than passing an old warning off as current.
func (l RateLimit) Standing(now time.Time) string {
	if r := l.ResetsAt; !r.IsZero() && !r.After(now) {
		return "no reading since its window reset"
	}
	switch l.Status {
	case "allowed", "":
		return "within its limit"
	case "allowed_warning":
		return "near its limit"
	case "rejected":
		return "at its limit"
	}
	return "limit status unknown (" + l.Status + ")"
}

// WindowUse is one usage window as of now.
type WindowUse struct {
	Name     string    // in plain words: "5-hour", "weekly"
	Used     float64   // the share used at the reading
	ResetsAt time.Time // zero when the harness didn't say
	Stale    bool      // the window has reset since the reading, so Used is old
}

// WindowUses lists the usage windows in order, each with when it
// resets; the binding window is among them even when the harness sent it
// only at the top level.
func (l RateLimit) WindowUses(now time.Time) []WindowUse {
	ws := maps.Clone(l.Windows)
	if ws == nil {
		ws = map[string]Window{}
	}
	if k := l.Window; k != "" {
		w, ok := ws[k]
		if !ok {
			w.Utilization = l.Utilization
		}
		if w.ResetsAt.IsZero() {
			w.ResetsAt = l.ResetsAt
		}
		ws[k] = w
	}
	var out []WindowUse
	for _, k := range slices.Sorted(maps.Keys(ws)) {
		w := ws[k]
		out = append(out, WindowUse{Name: WindowWords(k), Used: w.Utilization, ResetsAt: w.ResetsAt, Stale: !w.ResetsAt.IsZero() && !w.ResetsAt.After(now)})
	}
	return out
}

// WindowWords names a usage window in plain words: five_hour is "5-hour",
// seven_day "weekly", seven_day_opus "weekly opus".
func WindowWords(k string) string {
	switch {
	case strings.HasPrefix(k, "five_hour"):
		k = "5-hour" + strings.TrimPrefix(k, "five_hour")
	case strings.HasPrefix(k, "seven_day"):
		k = "weekly" + strings.TrimPrefix(k, "seven_day")
	}
	return strings.ReplaceAll(k, "_", " ")
}

// Until is how far off t is, to the minute: "3h31m", "12m", "2d4h".
func Until(t, now time.Time) string {
	d := t.Sub(now).Round(time.Minute)
	if d < time.Minute {
		return "under a minute"
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h >= 48:
		return fmt.Sprintf("%dd%dh", h/24, h%24)
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
