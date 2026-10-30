package board

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

func testBoard(t *testing.T, specs ...core.RunSpec) (*core.Core, *Module) {
	t.Helper()
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	for _, s := range specs {
		s.Harness = "fake"
		c.J.MustAppend("run.created", s)
	}
	return c, Of(c)
}

func run(c *core.Core, id string) core.Run {
	r, _ := c.Runs.Get(id)
	return r
}

func pending(c *core.Core, id string) []string { return run(c, id).Pending }

func call(t *testing.T, c *core.Core, from, op string, args any) any {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := c.Runs.Ops().Call(run(c, from), op, raw)
	if err != nil {
		t.Fatalf("%s %s: %v", from, op, err)
	}
	return out
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A message is a post on the board: the one it names is woken with where to
// read it, not what it says, and finds the text on the board; its answer in
// the thread wakes the sender the same way.
func TestAMentionOnlyWakes(t *testing.T) {
	c, _ := testBoard(t, core.RunSpec{ID: "w1", Role: "worker", Project: "p"}, core.RunSpec{ID: "w2", Role: "worker", Project: "p"})
	out := call(t, c, "w1", "message", map[string]string{"to": "w2", "text": "I take the flaky gate test\nthe secret detail"})
	thread := out.(map[string]string)["thread"]
	got := pending(c, "w2")
	if len(got) != 1 || !strings.Contains(got[0], "board --thread "+thread) || strings.Contains(got[0], "secret detail") {
		t.Fatalf("w2's wake = %q, want a pointer to thread %s without the text", got, thread)
	}
	read := asJSON(call(t, c, "w2", "board", map[string]string{"thread": thread}))
	if !strings.Contains(read, "the secret detail") {
		t.Fatalf("the thread on the board lacks the message: %s", read)
	}
	call(t, c, "w2", "post", map[string]string{"thread": thread, "to": "w1", "text": "fine, I take the docs"})
	if got := pending(c, "w1"); len(got) != 1 || !strings.Contains(got[0], thread) || !strings.Contains(got[0], "flaky gate test") {
		t.Fatalf("w1's wake = %q, want thread %s and what it is about", got, thread)
	}
}

// An agent adds itself to a thread: its new posts show on its board, and
// only a post naming it wakes it. A post naming nobody is on the board too.
func TestJoinShowsWithoutWaking(t *testing.T) {
	c, _ := testBoard(t, core.RunSpec{ID: "w1", Role: "worker", Project: "p"}, core.RunSpec{ID: "w2", Role: "worker", Project: "p"}, core.RunSpec{ID: "w3", Role: "worker", Project: "p"})
	out := call(t, c, "w1", "post", map[string]string{"text": "disk is full again"})
	thread := out.(map[string]string)["thread"]
	call(t, c, "w3", "join", map[string]string{"thread": thread})
	call(t, c, "w2", "post", map[string]string{"thread": thread, "text": "it is the check homes"})
	if len(pending(c, "w3")) != 0 || len(pending(c, "w1")) != 0 {
		t.Fatal("a post naming nobody woke someone")
	}
	mine := asJSON(call(t, c, "w3", "board", map[string]string{}))
	if !strings.Contains(mine, "check homes") {
		t.Fatalf("w3's board lacks its thread's new post: %s", mine)
	}
	if again := asJSON(call(t, c, "w3", "board", map[string]string{})); strings.Contains(again, `"new_for_you":[{`) {
		t.Fatalf("w3's board shows the same post as new twice: %s", again)
	}
	if found := asJSON(call(t, c, "w2", "board", map[string]string{"query": "DISK full"})); !strings.Contains(found, "disk is full again") {
		t.Fatalf("a search missed the post: %s", found)
	}
	call(t, c, "w2", "post", map[string]string{"thread": thread, "text": "@w3 can you look?"})
	if len(pending(c, "w3")) != 1 {
		t.Fatal("an @mention didn't wake w3")
	}
}

// A run with no parent messaging "parent" fails, rather than reaching
// whichever run Find("") happens to match.
func TestToParentWithNoParentFails(t *testing.T) {
	c, m := testBoard(t, core.RunSpec{ID: "other", Role: "worker"})
	_, err := m.Deliver(core.Run{RunSpec: core.RunSpec{ID: "w1"}}, "parent", "hi")
	if err == nil || !strings.Contains(err.Error(), "no parent") {
		t.Fatalf("parent with no Parent: %v, want a no-parent error", err)
	}
	if len(pending(c, "other")) != 0 {
		t.Fatal("another run got it")
	}
}

// A worker's "lead" and "parent" reach its own project's lead, never the
// newest run whose id starts "lead" (another project's, issue #2040); a
// project's lead's "lead" is the Hive's, and so is a worker's on a project
// with no lead yet.
func TestToLeadStaysOnTheProject(t *testing.T) {
	c, m := testBoard(t,
		core.RunSpec{ID: "lead-main-1", Role: "lead", Name: "lead", Persistent: true},
		core.RunSpec{ID: "lead-project-hive-1", Role: "lead", Name: "lead:hive", Project: "hive", Persistent: true},
		core.RunSpec{ID: "lead-project-hifi-1", Role: "lead", Name: "lead:hifi", Project: "hifi", Persistent: true})
	for _, tc := range []struct {
		from     core.Run
		to, want string
	}{
		{core.Run{RunSpec: core.RunSpec{ID: "w1", Role: "worker", Project: "hive", Task: "t1"}}, "lead", "lead-project-hive-1"},
		{core.Run{RunSpec: core.RunSpec{ID: "w1", Role: "worker", Project: "hive", Task: "t1"}}, "parent", "lead-project-hive-1"},
		{core.Run{RunSpec: core.RunSpec{ID: "w2", Role: "worker", Project: "pll", Task: "t2"}}, "lead", "lead-main-1"},
		{core.Run{RunSpec: core.RunSpec{ID: "lead-project-hive-1", Role: "lead", Project: "hive"}}, "lead", "lead-main-1"},
	} {
		before := len(pending(c, tc.want))
		if _, err := m.Deliver(tc.from, tc.to, "hi"); err != nil {
			t.Fatalf("%s to %s: %v", tc.from.ID, tc.to, err)
		}
		if len(pending(c, tc.want)) != before+1 {
			t.Errorf("%s's message to %s didn't reach %s", tc.from.ID, tc.to, tc.want)
		}
	}
	if len(pending(c, "lead-project-hifi-1")) != 0 {
		t.Error("another project's lead got a message")
	}
}

// A hidden project's agents reach no other project's, and no other
// project's reach them or read their threads; its own lead still does.
func TestHiddenProjectsAreWalledOff(t *testing.T) {
	c, m := testBoard(t)
	for _, p := range []core.Project{{Name: "open", Repo: "me/open"}, {Name: "secret", Repo: "me/secret", Isolated: true}} {
		if _, err := c.Projects.Add(p); err != nil {
			t.Fatal(err)
		}
		c.J.MustAppend("run.created", core.RunSpec{ID: p.Name + "-run", Role: "worker", Project: p.Name, Isolated: p.Isolated, Harness: "fake"})
	}
	open, secret := run(c, "open-run"), run(c, "secret-run")
	sweep := core.Run{RunSpec: core.RunSpec{ID: "sweep", Role: "sweep"}}
	if _, err := m.Deliver(sweep, "secret-run", "x"); err == nil {
		t.Error("a sweep messaged an isolated project's agent")
	}
	if _, err := m.Deliver(secret, "open-run", "x"); err == nil {
		t.Error("an isolated agent messaged another project's agent")
	}
	lead := core.Run{RunSpec: core.RunSpec{ID: "secret-lead", Role: "lead", Project: "secret", Isolated: true}}
	if _, err := m.Deliver(lead, "secret-run", "x"); err != nil {
		t.Errorf("an isolated project's lead can't message its agent: %v", err)
	}
	if _, err := m.Deliver(sweep, "open-run", "x"); err != nil {
		t.Errorf("a sweep can't message a normal agent: %v", err)
	}
	out, _ := m.Post(secret, "", nil, "the secret plan")
	thread := out.(map[string]string)["thread"]
	if read := asJSON(m.Read(open, Query{Query: "secret plan"})); strings.Contains(read, "secret plan") {
		t.Fatalf("another project's agent read a hidden project's post: %s", read)
	}
	if _, err := m.Post(open, thread, nil, "me too"); err == nil {
		t.Error("another project's agent posted in a hidden project's thread")
	}
}

// The board is the journal's: a restart reads every post back, those from
// before the board (bare msg.sent events) included.
func TestBoardOutlivesARestart(t *testing.T) {
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	c, err := core.Open(ws, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"w1", "w2"} {
		c.J.MustAppend("run.created", core.RunSpec{ID: id, Role: "worker", Project: "p", Harness: "fake"})
	}
	c.J.MustAppend("msg.sent", map[string]string{"from": "w1", "to": "w2", "text": "an old message"})
	Of(c).Deliver(run(c, "w1"), "w2", "a new one")
	c.Close()
	c, err = core.Open(ws, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := asJSON(Of(c).Read(run(c, "w2"), Query{Query: "one"}))
	if !strings.Contains(got, "a new one") {
		t.Fatalf("a restart lost a post: %s", got)
	}
	if got := asJSON(Of(c).Read(run(c, "w2"), Query{Query: "old message"})); !strings.Contains(got, "an old message") {
		t.Fatalf("a message from before the board isn't on it: %s", got)
	}
}
