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
	reply, err := lead(prompt, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(reply, "Hello. I'm the lead."); n != 2 {
		t.Fatalf("answered %d of 2 posts: %q", n, reply)
	}
}

// A sandboxed worker can't see a path outside its checkout. With nothing
// saying it is only hidden, it reads it as gone and finishes, as Claude did;
// the worker's system prompt (orientation and role) must make it file the deletion as a T3 step instead.
func TestHiddenOutsidePathIsFiled(t *testing.T) {
	var doc []byte
	for _, f := range []string{"orientation.md", "worker.md"} {
		b, err := os.ReadFile("../../modules/agents/" + f)
		if err != nil {
			t.Fatal(err)
		}
		doc = append(doc, b...)
	}
	type op struct {
		name string
		args map[string]string
	}
	var ops []op
	call = func(name string, args any, _ time.Duration) (json.RawMessage, error) {
		ops = append(ops, op{name, args.(map[string]string)})
		return nil, nil
	}
	defer func() { call = agentcli.Call }()
	hidden := filepath.Join(t.TempDir(), "my fixture")
	brief := "# Brief: Delete it: FAKE:rmoutside " + hidden[:len(hidden)/2] + "\n\n## The ask\n\nDelete it: FAKE:rmoutside " + hidden + "\n\n## What done looks like\n\n(none)\n"
	for _, c := range []struct{ system, want, steps string }{
		{"", "finish", ""},
		{string(doc), "warden", "rm -rf " + hidden},
	} {
		ops = nil
		worker(brief, "go", c.system)
		if len(ops) != 1 || ops[0].name != c.want || ops[0].args["steps"] != c.steps || (c.want == "warden" && ops[0].args["tier"] != "T3") {
			t.Errorf("want %s %q, got %v", c.want, c.steps, ops)
		}
	}
}
