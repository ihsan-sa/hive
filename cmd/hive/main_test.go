package main

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
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
