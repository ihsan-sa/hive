package harness

import (
	"testing"
	"time"
)

// The person asked what allowed_warning means: the status reads in plain
// words, and every window carries its reset, the binding one included when
// the harness sent it only at the top level.
func TestPlanReadsInPlainWords(t *testing.T) {
	now := time.Date(2026, 10, 24, 2, 9, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour)
	p := RateLimit{Status: "allowed_warning", Window: "seven_day", Utilization: 0.89, ResetsAt: reset,
		Windows: map[string]Window{"five_hour": {Utilization: 0.38, ResetsAt: now.Add(time.Hour)}}}
	if got := p.Standing(now); got != "near its limit" {
		t.Errorf("allowed_warning reads %q", got)
	}
	if got := p.Standing(reset); got != "no reading since its window reset" {
		t.Errorf("a warning past its reset reads %q", got)
	}
	ws := p.WindowUses(now)
	if len(ws) != 2 || ws[0].Name != "5-hour" || ws[1].Name != "weekly" || ws[1].Used != 0.89 || !ws[1].ResetsAt.Equal(reset) || ws[0].Stale {
		t.Fatalf("windows: %+v", ws)
	}
	if ws := p.WindowUses(now.Add(2 * time.Hour)); !ws[0].Stale || ws[1].Stale {
		t.Errorf("only the 5-hour window has reset: %+v", ws)
	}
	for in, want := range map[time.Duration]string{3*time.Hour + 31*time.Minute: "3h31m", time.Hour + 5*time.Minute: "1h05m", 12 * time.Minute: "12m",
		24 * time.Hour: "1d0h", 30*time.Hour + 5*time.Minute: "1d6h", 52 * time.Hour: "2d4h", 10 * time.Second: "under a minute", -10 * time.Second: "under a minute",
		-90 * time.Minute: "1h30m ago"} {
		if got := Until(now.Add(in), now); got != want {
			t.Errorf("Until %v = %q, want %q", in, got, want)
		}
	}
}

// A status the Hive doesn't know shows as itself, no status is no reading,
// and a binding window sent only at the top level still lists with its reset.
func TestPlanStandingEdges(t *testing.T) {
	now := time.Date(2026, 10, 24, 2, 9, 0, 0, time.UTC)
	for status, want := range map[string]string{"throttled": "limit status unknown (throttled)", "": "no reading yet", "rejected": "at its limit"} {
		if got := (RateLimit{Status: status}).Standing(now); got != want {
			t.Errorf("status %q reads %q, want %q", status, got, want)
		}
	}
	for status, want := range map[string]string{"rejected": LevelBad, "allowed_warning": LevelWarn, "allowed": LevelOK, "": LevelOK} {
		if got := (RateLimit{Status: status, ResetsAt: now.Add(time.Hour)}).Level(now); got != want {
			t.Errorf("status %q is level %q, want %q", status, got, want)
		}
		if got := (RateLimit{Status: status, ResetsAt: now}).Level(now); got != LevelOK {
			t.Errorf("status %q past its reset is level %q", status, got)
		}
	}
	ws := RateLimit{Status: "allowed", Window: "five_hour", Utilization: 0.4, ResetsAt: now.Add(time.Hour)}.WindowUses(now)
	if len(ws) != 1 || ws[0].Name != "5-hour" || ws[0].Used != 0.4 || !ws[0].ResetsAt.Equal(now.Add(time.Hour)) {
		t.Errorf("a top-level window alone: %+v", ws)
	}
}

// Codex names a window it has no word for by its length; those read with
// their underscores spaced.
func TestCodexWindowNamesReadPlainly(t *testing.T) {
	for minutes, want := range map[int]string{300: "5-hour", 7 * 24 * 60: "weekly", 3 * 24 * 60: "3 day", 120: "2 hour", 90: "90 minute", 0: "primary"} {
		if got := WindowWords(windowName(minutes, 0)); got != want {
			t.Errorf("a %d-minute window reads %q, want %q", minutes, got, want)
		}
	}
}
