package gate

import (
	"strings"
	"testing"
)

// A child that prints a token, as itself or as git's Authorization value,
// leaves no trace of it in the report, which goes on a pull request that may
// be public; the commit and binary hashes stay.
func TestReportRedactsTokens(t *testing.T) {
	child, org := "github_pat_11AAAAchildchildchild", "org-secret-value"
	sha := strings.Repeat("ab", 20)
	r := Report{Head: sha, CandSHA256: sha + sha[:24], Error: "go build: echo " + child,
		Verdict:   Verdict{Reason: "the candidate failed: Authorization: Basic " + basic(child), Regressions: []string{"org: " + org}},
		Candidate: []Result{{Probe: "org", Detail: "pushed with " + org + " and ghp_abcdefghijklmnopqrstuv"}},
		Control:   []Result{{Probe: "daemon", Detail: "ok at " + sha}}}
	r.redact(org, child)
	md := r.Markdown()
	for _, s := range []string{child, org, basic(child), "ghp_abcdefghijklmnop"} {
		if strings.Contains(md+r.Error+r.Verdict.Reason, s) {
			t.Fatalf("the report still carries %q:\n%s", s, md)
		}
	}
	if r.Head != sha || !strings.Contains(r.Control[0].Detail, sha) || !strings.Contains(md, "[secret]") {
		t.Fatalf("redacted too much: %+v", r)
	}
}

// A short-test failure names the failing test even when its package isn't
// the last in the output, where the tail alone would show only "FAIL".
func TestTestFailuresNamesTheTest(t *testing.T) {
	out := "ok  \tx/a\t0.1s\n--- FAIL: TestThing (0.20s)\n    thing_test.go:12: want 1, got 2\nFAIL\nFAIL\tx/b\t0.3s\n" + strings.Repeat("ok  \tx/c\t0.1s\n", 50) + "FAIL\n"
	got := testFailures(out)
	for _, w := range []string{"--- FAIL: TestThing", "thing_test.go:12: want 1, got 2", "FAIL\tx/b"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in:\n%s", w, got)
		}
	}
	if testFailures("ok \tx/a\n") != "" {
		t.Fatal("named a failure in a passing run")
	}
}
