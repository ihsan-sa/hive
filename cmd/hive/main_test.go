package main

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

func TestKBArgsTakeLimitAnywhere(t *testing.T) {
	for _, args := range [][]string{
		{"login", "--limit", "all"},
		{"--limit", "all", "login"},
	} {
		if q, l := kbArgs(args); q != "login" || l != "all" {
			t.Errorf("kbArgs(%q) = %q, %q; want login, all", args, q, l)
		}
	}
}

func TestReleaseArgRefusesWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		args []string
		tag  string
		ok   bool
	}{
		{nil, "", true},
		{[]string{"--release", "v0.1.2"}, "v0.1.2", true},
		{[]string{"--release=v0.1.2"}, "v0.1.2", true},
		{[]string{"--release"}, "", false},
		{[]string{"--release="}, "", false},
		{[]string{"v0.1.2"}, "", false},
		{[]string{"--relase", "v0.1.2"}, "", false},
	} {
		if tag, ok := releaseArg(c.args); tag != c.tag || ok != c.ok {
			t.Errorf("releaseArg(%q) = %q, %v; want %q, %v", c.args, tag, ok, c.tag, c.ok)
		}
	}
}

// approve and refuse never guess on an ask with one option (a "told" seen),
// and say what an answer means where its word doesn't.
func TestApproveRefuseNeverGuess(t *testing.T) {
	if _, err := approveOption("refuse", core.Ask{ID: "t1", Options: []string{"seen"}}); err == nil {
		t.Fatal("refuse answered a one-option ask")
	}
	if _, err := approveOption("approve", core.Ask{ID: "q1"}); err == nil {
		t.Fatal("approve answered an ask with no options")
	}
	if o, _ := approveOption("approve", core.Ask{Options: []string{"done", "declined"}}); o != "done" || meaning(o) == "" {
		t.Fatalf("approve picked %q, meaning %q", o, meaning(o))
	}
	if o, _ := approveOption("refuse", core.Ask{Options: []string{"run", "declined"}}); o != "declined" {
		t.Fatalf("refuse picked %q", o)
	}
}

// The owner read "resets Sat 01:40" at 02:09Z as a reset already past; it was
// 05:40Z printed in EDT with no zone. The line now says how far off it is.
func TestPlanLineSaysWhenTheResetIs(t *testing.T) {
	reset := time.Date(2026, 10, 24, 5, 40, 0, 0, time.UTC)
	now := time.Date(2026, 10, 24, 2, 9, 0, 0, time.UTC)
	p := core.Plan{Harness: "claude", Login: "claude", At: now.Add(-time.Minute), Limit: harness.RateLimit{Status: "allowed", ResetsAt: reset, Window: "five_hour", Utilization: 0.28,
		Windows: map[string]harness.Window{"five_hour": {Utilization: 0.28, ResetsAt: reset}, "seven_day": {Utilization: 0.36}}}}
	got := planLine(p, now)
	for _, want := range []string{"28% of the five_hour window", "resets in 3h31m0s", "seven_day 36%"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if got := planLine(p, reset.Add(time.Minute)); !strings.Contains(got, "window reset at") || !strings.Contains(got, "no reading since") {
		t.Errorf("a reading past its reset should say so: %q", got)
	}
}

// approve and refuse pick the first and last option; an ask with none is an
// error pointing at hive answer, not a panic.
func TestApproveOption(t *testing.T) {
	a := core.Ask{ID: "a1", Options: []string{"merge", "leave open"}}
	if o, err := approveOption("approve", a); err != nil || o != "merge" {
		t.Errorf("approve: %q, %v", o, err)
	}
	if o, err := approveOption("refuse", a); err != nil || o != "leave open" {
		t.Errorf("refuse: %q, %v", o, err)
	}
	for _, cmd := range []string{"approve", "refuse"} {
		if o, err := approveOption(cmd, core.Ask{ID: "q1"}); err == nil {
			t.Errorf("%s on an ask with no options: %q, want an error", cmd, o)
		}
	}
}
