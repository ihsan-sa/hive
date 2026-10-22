package fakeagent

import (
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Two posts that queue during one turn reach the lead as one prompt; the
// lead must answer both, not read the second as the tail of the first.
func TestLeadAnswersEveryQueuedPost(t *testing.T) {
	prompt := "[chat c · thread t · Thu 11:00] Person: hello again" + core.PendingSep + "[chat c · thread t · Thu 11:00] Person: hello there"
	reply, err := lead(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(reply, "Hello. I'm the lead."); n != 2 {
		t.Fatalf("answered %d of 2 posts: %q", n, reply)
	}
}
