package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report/contracttest"
)

// commas writes n as the question does, 4000 as 4,000.
func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// The opt-in question names every key a sent report holds, with the caps the
// code clips to, and nothing the code doesn't send: a field added, dropped or
// recapped without the question (or the other way round) fails here.
func TestQuestionSaysWhatIsSent(t *testing.T) {
	says := map[string]string{
		"install":                "a random install id",
		"version":                "the Hive version",
		"type":                   "failure report",
		"os":                     "the OS",
		"kind":                   "what failed",
		"at":                     "when",
		"message":                "why (up to " + commas(maxText) + " characters)",
		"log":                    "the agent reply or the last lines of the task log (up to " + commas(maxExcerpt) + " characters)",
		"usage":                  "A usage report",
		"usage.runs":             "runs",
		"usage.turns":            "turns",
		"usage.merges":           "merges",
		"usage.reverts":          "reverts",
		"usage.cost_usd":         "cost",
		"usage.failures_dropped": "dropped reports",
	}
	for key, phrase := range says {
		if !strings.Contains(Question, phrase) {
			t.Errorf("the question doesn't say %q, for %s", phrase, key)
		}
	}
	if !strings.Contains(Question, "harness name") {
		t.Error("the usage report's message names the harness, and the question doesn't say so")
	}
	if i := strings.IndexAny(Question, "'\"`$%\\"); i >= 0 {
		t.Errorf("the installers quote the question, and it has %q", Question[i])
	}

	// Every kind of failure, each text past its cap, and more than one run
	// sends, so the usage report counts what it dropped.
	e := contracttest.New(t)
	c := sharing(t, e.URL, maxPerRun)
	long := strings.Repeat("é", 3*maxExcerpt)
	c.J.MustAppend("hive.started", map[string]any{})
	c.J.MustAppend("run.created", map[string]any{"id": "w1", "role": "worker"})
	c.J.MustAppend("run.turn_ended", map[string]any{"id": "w1", "status": "parked", "reason": long, "result": map[string]any{"text": long, "is_error": true, "cost_usd": 0.5}})
	c.J.MustAppend("task.saved", map[string]any{"id": "t1", "stage": "merged"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t2", "kind": "revert", "stage": "merged"})
	c.J.MustAppend("task.saved", map[string]any{"id": "t3", "stage": "blocked", "reason": "stuck", "log": []map[string]string{{"text": long}}})
	c.J.MustAppend("alert.raised", map[string]string{"kind": "quiet", "text": "x"})
	c.J.MustAppend("hive.started", map[string]any{})
	(&Module{C: c}).Send()
	raw, _ := e.Got()
	seen := map[string]bool{}
	longest := map[string]int{}
	for _, b := range raw {
		var r map[string]any
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		for k, v := range r {
			seen[k] = true
			if s, ok := v.(string); ok {
				longest[k] = max(longest[k], utf8.RuneCountInString(s))
			}
			if u, ok := v.(map[string]any); ok {
				for uk := range u {
					seen[k+"."+uk] = true
				}
			}
		}
	}
	for k := range seen {
		if _, ok := says[k]; !ok {
			t.Errorf("reports send %q, and the question doesn't say so", k)
		}
	}
	for k := range says {
		if !seen[k] {
			t.Errorf("the question says reports hold %q, and none sent it", k)
		}
	}
	if longest["message"] != maxText || longest["log"] != maxExcerpt {
		t.Errorf("message clipped to %d and log to %d characters; the question says %d and %d", longest["message"], longest["log"], maxText, maxExcerpt)
	}
}
