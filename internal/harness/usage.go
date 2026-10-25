package harness

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Levels of a plan's standing, so its colour and its words agree.
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelBad  = "bad"
)

// Level is how near its limit the plan is as of now: LevelBad at it,
// LevelWarn near it, LevelOK otherwise (a reading past its reset included).
func (l RateLimit) Level(now time.Time) string {
	if r := l.ResetsAt; !r.IsZero() && !r.After(now) {
		return LevelOK
	}
	switch l.Status {
	case "rejected":
		return LevelBad
	case "allowed_warning":
		return LevelWarn
	}
	return LevelOK
}

// Standing is the plan's usage state in plain words as of now, from the status
// the harness reports for its binding window: within its limit, near it
// (allowed_warning) or at it (rejected). A reading whose window has reset
// since says so rather than passing an old warning off as current, and no
// status at all is no reading yet.
func (l RateLimit) Standing(now time.Time) string {
	if r := l.ResetsAt; !r.IsZero() && !r.After(now) {
		return StaleWords
	}
	switch l.Status {
	case "":
		return "no reading yet"
	case "allowed":
		return "within its limit"
	case "allowed_warning":
		return "near its limit"
	case "rejected":
		return "at its limit"
	}
	return "limit status unknown (" + l.Status + ")"
}

// StaleWords is how a reading whose window has reset since reads, in
// hive status and the control center alike.
const StaleWords = "no reading since its window reset"

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
// seven_day "weekly", seven_day_opus "weekly opus"; any other name (Codex's
// 3_day, primary) only has its underscores spaced.
func WindowWords(k string) string {
	switch {
	case strings.HasPrefix(k, "five_hour"):
		k = "5-hour" + strings.TrimPrefix(k, "five_hour")
	case strings.HasPrefix(k, "seven_day"):
		k = "weekly" + strings.TrimPrefix(k, "seven_day")
	}
	return strings.ReplaceAll(k, "_", " ")
}

// ResetsIn is how far off the window's reset is, as Until says it.
func (w WindowUse) ResetsIn(now time.Time) string { return Until(w.ResetsAt, now) }

// Until is how far off a future t is, to the minute: "3h31m", "1h05m", "12m",
// and from a day on "1d0h", "2d4h". A t already past reads as how long ago it
// was ("3h31m ago"), never as a time still to come.
func Until(t, now time.Time) string {
	d := t.Sub(now).Round(time.Minute)
	switch {
	case d <= -time.Minute:
		return Until(now, t) + " ago"
	case d < time.Minute:
		return "under a minute"
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd%dh", h/24, h%24)
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
