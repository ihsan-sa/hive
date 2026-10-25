package core

import (
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// The person asked what allowed_warning means: the status reads in plain
// words, and every window carries its reset, the binding one included when
// the harness sent it only at the top level.
func TestPlanReadsInPlainWords(t *testing.T) {
	now := time.Date(2026, 10, 24, 2, 9, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour)
	p := Plan{Limit: harness.RateLimit{Status: "allowed_warning", Window: "seven_day", Utilization: 0.89, ResetsAt: reset,
		Windows: map[string]harness.Window{"five_hour": {Utilization: 0.38, ResetsAt: now.Add(time.Hour)}}}}
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
	for in, want := range map[[2]time.Duration]string{{3*time.Hour + 31*time.Minute}: "3h31m", {12 * time.Minute}: "12m", {52 * time.Hour}: "2d4h", {10 * time.Second}: "under a minute"} {
		if got := Until(now.Add(in[0]), now); got != want {
			t.Errorf("Until %v = %q, want %q", in[0], got, want)
		}
	}
}
