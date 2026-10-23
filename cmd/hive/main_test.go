package main

import "testing"

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
