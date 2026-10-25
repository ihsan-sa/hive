package report

import (
	"encoding/json"
	"fmt"
	"slices"
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
	// Each key a report sends, as type.key, with the sentence of the question
	// that names it (by how it starts) and the words it's named by there, so
	// a word used elsewhere in the question doesn't count.
	common := "Every report has "
	failure, usage := "Each failure report holds ", "A usage report counts "
	says := map[string][2]string{
		"failure.install":              {common, "a random install id"},
		"failure.version":              {common, "the Hive version"},
		"failure.os":                   {common, "the OS"},
		"failure.type":                 {"failures sends ", "failure and usage reports"},
		"failure.kind":                 {failure, "holds what failed,"},
		"failure.at":                   {failure, "what failed, when and why"},
		"failure.message":              {failure, "why (up to " + commas(maxText) + " characters)"},
		"failure.log":                  {failure, "the agent reply or the last lines of the task log (up to " + commas(maxExcerpt) + " characters)"},
		"usage.install":                {common, "a random install id"},
		"usage.version":                {common, "the Hive version"},
		"usage.os":                     {common, "the OS"},
		"usage.type":                   {"failures sends ", "failure and usage reports"},
		"usage.at":                     {usage, "the time span it covers"},
		"usage.message":                {usage, "with the harness name and the time span it covers"},
		"usage.usage":                  {usage, "A usage report counts"},
		"usage.usage.runs":             {usage, "counts runs,"},
		"usage.usage.turns":            {usage, " turns,"},
		"usage.usage.merges":           {usage, " merges,"},
		"usage.usage.reverts":          {usage, " reverts,"},
		"usage.usage.cost_usd":         {usage, " cost and"},
		"usage.usage.failures_dropped": {usage, "and dropped reports,"},
	}
	sentences := strings.FieldsFunc(strings.NewReplacer("? ", "?\n", ". ", ".\n").Replace(Question), func(r rune) bool { return r == '\n' })
	for key, at := range says {
		i := slices.IndexFunc(sentences, func(s string) bool { return strings.HasPrefix(s, at[0]) })
		if i < 0 {
			t.Errorf("the question has no sentence starting %q, for %s", at[0], key)
		} else if !strings.Contains(sentences[i], at[1]) {
			t.Errorf("the question's %q doesn't say %q, for %s", sentences[i], at[1], key)
		}
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
			k = fmt.Sprint(r["type"]) + "." + k
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
	if longest["failure.message"] != maxText || longest["failure.log"] != maxExcerpt {
		t.Errorf("message clipped to %d and log to %d characters; the question says %d and %d", longest["failure.message"], longest["failure.log"], maxText, maxExcerpt)
	}
}
