package fakeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/agentcli"
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

// A sandboxed worker can't see a path outside its checkout. With nothing
// saying it is only hidden, it reads it as gone and finishes, as Claude did;
// the worker's role doc must make it file the deletion as a T3 step instead.
func TestHiddenOutsidePathIsFiled(t *testing.T) {
	doc, err := os.ReadFile("../../modules/agents/worker.md")
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	call = func(op string, args any, _ time.Duration) (json.RawMessage, error) {
		raw, _ := json.Marshal(args)
		ops = append(ops, op+" "+string(raw))
		return nil, nil
	}
	defer func() { call = agentcli.Call }()
	hidden := filepath.Join(t.TempDir(), "fixture")
	for _, c := range []struct{ system, want string }{
		{"", "finish "},
		{string(doc), `warden {"steps":"rm -rf ` + hidden + `","tier":"T3"`},
	} {
		ops = nil
		worker("Delete it: FAKE:rmoutside "+hidden, "go", c.system)
		if len(ops) != 1 || !strings.HasPrefix(ops[0], c.want) {
			t.Errorf("want %s, got %q", c.want, ops)
		}
	}
}
