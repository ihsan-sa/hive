package main

import (
	"encoding/json"
	"testing"
)

// The driver stops when the side's work has landed, been given up or waits
// on the person, as hive-eval's arms do: review follow-ups and the Hive's own
// setup task don't hold it.
func TestTaskWhere(t *testing.T) {
	for _, tc := range []struct{ tasks, want string }{
		{`[]`, ""},
		{`[{"title":"Set up the Hive files","stage":"merged","merge_sha":"a"}]`, ""},
		{`[{"title":"Fix it","stage":"reviewing"}]`, ""},
		{`[{"title":"Fix it","stage":"merged","merge_sha":"a"},{"title":"Follow up the review of #1","stage":"queued"}]`, "landed"},
		{`[{"title":"Fix it","stage":"merged","merge_sha":"a"},{"title":"More","stage":"working"}]`, ""},
		{`[{"title":"Fix it","stage":"blocked"},{"title":"Again","stage":"stopped"}]`, "gave up"},
		{`[{"title":"Fix it","stage":"waiting-person"}]`, "waiting on the person"},
	} {
		var v taskView
		if err := json.Unmarshal([]byte(`{"projects":[{"name":"other","tasks":[{"title":"x","stage":"merged","merge_sha":"b"}]},{"name":"proj","tasks":`+tc.tasks+`}]}`), &v); err != nil {
			t.Fatal(err)
		}
		if got := taskWhere(v, "proj"); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.tasks, got, tc.want)
		}
	}
}
