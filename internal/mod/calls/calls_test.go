package calls

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

var (
	testEvery = 24 * time.Hour
	testTries = 3
	callEvery = Duration("test.every", "how often", &testEvery)
	callTries = Count("test.tries", "how many", &testTries).Bar()
)

func open(t *testing.T) *core.Core {
	t.Helper()
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	New(c)
	return c
}

func call(c *core.Core, r core.Run, args map[string]string) (any, error) {
	raw, _ := json.Marshal(args)
	return c.Runs.Ops().Call(r, "call", raw)
}

// A cadence that was a constant is a call: its default until an agent makes
// another, which holds at once for the whole Hive and is on the record with
// who and why; "default" puts it back.
func TestCallChangesAtOnce(t *testing.T) {
	c := open(t)
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	if got := callEvery.Of(c.J); got != 24*time.Hour {
		t.Fatalf("default: %s", got)
	}
	if _, err := call(c, lead, map[string]string{"name": "test.every", "value": "0"}); err == nil || !strings.Contains(err.Error(), "why") {
		t.Fatalf("a call without why: %v", err)
	}
	if _, err := call(c, lead, map[string]string{"name": "test.every", "value": "soon", "why": "x"}); err == nil {
		t.Fatal("a call that isn't a length of time was taken")
	}
	if _, err := call(c, lead, map[string]string{"name": "no.such", "value": "1h", "why": "x"}); err == nil {
		t.Fatal("an unknown call was taken")
	}
	if _, err := call(c, lead, map[string]string{"name": "test.every", "value": "90m", "why": "a safe release waits on the day"}); err != nil {
		t.Fatal(err)
	}
	if got := callEvery.Of(c.J); got != 90*time.Minute {
		t.Fatalf("after the call: %s", got)
	}
	if _, err := call(c, lead, map[string]string{"name": "test.every", "value": "2d", "why": "quiet"}); err != nil || callEvery.Of(c.J) != 48*time.Hour {
		t.Fatalf("days: %s %v", callEvery.Of(c.J), err)
	}
	// The test's override of the variable behind the default still holds
	// once the call goes back to default.
	testEvery = time.Minute
	t.Cleanup(func() { testEvery = 24 * time.Hour })
	if _, err := call(c, lead, map[string]string{"name": "test.every", "value": "default", "why": "back"}); err != nil || callEvery.Of(c.J) != time.Minute {
		t.Fatalf("back to default: %s %v", callEvery.Of(c.J), err)
	}
	out, err := call(c, lead, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	for _, x := range out.([]Entry) {
		if x.Name == "test.every" {
			e = x
		}
	}
	if e.Value != "1m" || e.Default != "1m" || e.Made == nil || e.Made.By != "lead-1" || e.Made.Why != "back" || e.Made.Was != "2d" {
		t.Fatalf("listed: %+v %+v", e, e.Made)
	}
}

// A restart reads the calls from the journal: they outlive the daemon.
func TestCallsOutliveARestart(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	New(c)
	if _, err := Make(c.J, core.Run{RunSpec: core.RunSpec{ID: "s-1", Role: "sweep"}}, "test.tries", "5", "flaky forge"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c2, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got := callTries.Of(c2.J); got != 5 {
		t.Fatalf("after a restart: %d", got)
	}
}

// A call that decides whether a change counts isn't a worker's to make, its
// own change being one; reviewers don't steer at all.
func TestBarCallsAreNotAWorkers(t *testing.T) {
	c := open(t)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker"}}
	if _, err := call(c, w, map[string]string{"name": "test.tries", "value": "9", "why": "mine is flaky"}); err == nil {
		t.Fatal("a worker moved a bar")
	}
	if _, err := call(c, w, map[string]string{"name": "test.every", "value": "1h", "why": "the train waits"}); err != nil {
		t.Fatalf("a worker's call on a cadence: %v", err)
	}
	if _, err := call(c, core.Run{RunSpec: core.RunSpec{ID: "r-1", Role: "reviewer"}}, map[string]string{"name": "test.every", "value": "1h", "why": "x"}); err == nil {
		t.Fatal("a reviewer made a call")
	}
	if callTries.Of(c.J) != 3 {
		t.Fatal("the refused call took effect")
	}
}

func TestDurationText(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0s", 30 * time.Second: "30s", time.Hour: "1h", 90 * time.Minute: "1h30m", 48 * time.Hour: "2d", 135 * time.Minute: "2h15m"} {
		if got := fmtDur(d); got != want {
			t.Errorf("%s: %q, want %q", d, got, want)
		}
		if back, err := parseDur(fmtDur(d)); err != nil || back != d {
			t.Errorf("%s doesn't read back: %s %v", d, back, err)
		}
	}
}
