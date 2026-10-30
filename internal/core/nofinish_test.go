package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A worker whose turns keep ending in prose is nudged twice; when the turns
// answering its nudges make no commit either, it ends blocked and frees its
// slot. One that keeps committing, or whose commits can't be read, stays
// quiet for the lead; touching the HEAD log isn't a commit.
func TestAWorkerThatNeverFinishesEndsBlocked(t *testing.T) {
	for _, tc := range []struct {
		name  string
		turns string // each turn: . ends in prose, c commits too, p is parked on an error
		nogit bool
		want  string
	}{
		{"no commits", "...", false, StatusBlocked},
		{"commits", ".cc", false, StatusQuiet},
		{"no git", "...", true, StatusQuiet},
		{"a parked turn isn't one without finishing", "..cp.", false, StatusQuiet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testHive(t)
			co := t.TempDir()
			git := filepath.Join(co, ".git")
			log := filepath.Join(git, "logs", "HEAD")
			ref := filepath.Join(git, "refs", "heads", "main")
			if !tc.nogit {
				os.MkdirAll(filepath.Dir(log), 0o755)
				os.MkdirAll(filepath.Dir(ref), 0o755)
				os.WriteFile(filepath.Join(git, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
				os.WriteFile(ref, []byte(strings.Repeat("a", 40)+"\n"), 0o644)
				os.WriteFile(log, []byte("0 1 commit\n"), 0o644)
			}
			c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Checkout: co})
			var got Run
			for i, kind := range tc.turns {
				turn := i + 1
				if kind == 'c' {
					os.WriteFile(ref, []byte(strings.Repeat(string(rune('a'+turn)), 40)+"\n"), 0o644)
				}
				if !tc.nogit { // a touched HEAD log, without a commit, changes nothing
					f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
					f.WriteString("1 2 touch\n")
					f.Close()
				}
				r, _ := c.Runs.Get("w1")
				c.J.MustAppend("run.delivered", runNote{ID: "w1", N: len(r.Pending)}) // its nudge went into this turn
				c.J.MustAppend("run.turn_started", turnStarted{ID: "w1", Turn: turn})
				out := c.WS.TurnOut("w1", turn)
				os.MkdirAll(filepath.Dir(out), 0o755)
				line := `{"type":"result","subtype":"success","result":"All done.","session_id":"s1"}`
				if kind == 'p' {
					line = `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom","session_id":"s1"}`
				}
				os.WriteFile(out, []byte(line+"\n"), 0o600)
				r, _ = c.Runs.Get("w1")
				c.Runs.endTurn(r)
				got, _ = c.Runs.Get("w1")
				if turn < 3 && got.Status != StatusIdle {
					t.Fatalf("turn %d: %q (%q), want nudged", turn, got.Status, got.Reason)
				}
			}
			switch {
			case got.Status != tc.want:
				t.Fatalf("after its nudges: %q (%q), want %s", got.Status, got.Reason, tc.want)
			case tc.want == StatusBlocked && (!strings.HasPrefix(got.Reason, NoFinish) || !got.Terminal()):
				t.Fatalf("blocked as %q, terminal %v", got.Reason, got.Terminal())
			}
		})
	}
}

// An agent ends a quiet run blocked only while it is still quiet and no turn
// of it is starting; a message after doesn't wake it, as one does a run
// blocked on its own.
func TestEndQuietOnlyWhileQuiet(t *testing.T) {
	c, _ := testHive(t)
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Checkout: t.TempDir()})
	c.Runs.SetStatus("w1", StatusIdle, "nudged")
	if err := c.Runs.EndQuiet("w1", StoppedQuiet+"lead: x"); err == nil {
		t.Fatal("ended a run that isn't quiet")
	}
	c.Runs.SetStatus("w1", StatusQuiet, "keeps ending turns")
	c.Runs.mu.Lock()
	c.Runs.starting = map[string]bool{"w1": true}
	c.Runs.mu.Unlock()
	if err := c.Runs.EndQuiet("w1", StoppedQuiet+"lead: x"); err == nil {
		t.Fatal("ended a run whose turn is starting")
	}
	c.Runs.mu.Lock()
	delete(c.Runs.starting, "w1")
	c.Runs.mu.Unlock()
	if err := c.Runs.EndQuiet("w1", StoppedQuiet+"lead: x"); err != nil {
		t.Fatal(err)
	}
	c.J.MustAppend("run.queued", runNote{ID: "w1", Text: "late message"})
	c.Runs.wake("w1")
	if r, _ := c.Runs.Get("w1"); r.Status != StatusBlocked || !strings.HasPrefix(r.Reason, StoppedQuiet) {
		t.Fatalf("after a message: %q (%q), want it still ended", r.Status, r.Reason)
	}
}

// headMark reads the commit HEAD points at, as git does, from a loose ref,
// packed refs and a worktree, without running git in the checkout.
func TestHeadMarkReadsTheCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	co := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(co, "init", "-q", "-b", "main")
	run(co, "commit", "-q", "--allow-empty", "-m", "one")
	if got, want := headMark(co), run(co, "rev-parse", "HEAD"); got != want {
		t.Fatalf("loose ref: %q, want %q", got, want)
	}
	run(co, "pack-refs", "--all")
	if got, want := headMark(co), run(co, "rev-parse", "HEAD"); got != want {
		t.Fatalf("packed ref: %q, want %q", got, want)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	run(co, "worktree", "add", "-q", "-b", "side", wt)
	run(wt, "commit", "-q", "--allow-empty", "-m", "two")
	if got, want := headMark(wt), run(wt, "rev-parse", "HEAD"); got != want || got == headMark(co) {
		t.Fatalf("worktree: %q, want %q", got, want)
	}
}

// A worker whose nudges don't get it to finish on a stop of its route that
// isn't the last moves on to the next stop a run has finished on, with
// nudges of its own there, rather than going quiet; on the last it goes
// quiet as before. Deck's fix stalled three times on its free models and
// never reached its route's claude:opus.
func TestAWorkerThatDoesntFinishMovesAlongItsRoute(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{
		"free": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen"},
		"mid":  {Endpoint: "http://127.0.0.1:11434/v1", Model: "gpt-oss"},
	}
	c.WS.SaveConfig(cfg)
	opus := Pick{Harness: "fake", Model: "opus"}
	c.J.MustAppend("run.created", RunSpec{ID: "trial", Role: "worker", Harness: "fake", Model: "opus"})
	c.J.MustAppend("run.finish", map[string]any{"id": "trial", "finish": Finish{Status: "done"}})
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "open", Model: "free", Checkout: t.TempDir(),
		Route: []Pick{{Harness: "open", Model: "free"}, {Harness: "open", Model: "mid"}, opus}})
	turn := 0
	prose := func(on Pick) Run {
		turn++
		r, _ := c.Runs.Get("w1")
		c.J.MustAppend("run.delivered", runNote{ID: "w1", N: len(r.Pending)})
		c.J.MustAppend("run.turn_started", turnStarted{ID: "w1", Turn: turn, Harness: on.Harness, Model: on.Model})
		out := c.WS.TurnOut("w1", turn)
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte(`{"type":"result","subtype":"success","result":"All done.","session_id":"s1"}`+"\n"), 0o600)
		r, _ = c.Runs.Get("w1")
		c.Runs.endTurn(r)
		got, _ := c.Runs.Get("w1")
		return got
	}
	free := Pick{Harness: "open", Model: "free"}
	prose(free)
	prose(free)
	got := prose(free)
	if got.Status != StatusParked || got.Reason != "open:free keeps ending turns without finishing; moving to fake:opus" {
		t.Fatalf("after its nudges on a stop that isn't the last: %q (%q)", got.Status, got.Reason)
	}
	if p, at := c.Runs.next(got); !p.Same(opus) || at.After(time.Now()) {
		t.Fatalf("next after stalling: %v at %v, want fake:opus (past the stop no run has finished on)", p, at)
	}
	if got.Nudges != 0 || !slices.Equal(got.Stalled, []string{"open:free"}) {
		t.Fatalf("the next stop gets its own nudges, and the stop passed is kept: nudges %d, stalled %q", got.Nudges, got.Stalled)
	}
	for i := 0; i < 2; i++ {
		if got = prose(opus); got.Status != StatusIdle {
			t.Fatalf("a nudge on the next stop: %q (%q)", got.Status, got.Reason)
		}
	}
	if got = prose(opus); got.Status != StatusQuiet {
		t.Fatalf("on the route's last stop: %q (%q), want quiet", got.Status, got.Reason)
	}
	// A run with no route stays where it is, as before.
	c.J.MustAppend("run.created", RunSpec{ID: "w2", Role: "worker", Harness: "fake", Model: "opus", Checkout: t.TempDir()})
	if r, _ := c.Runs.Get("w2"); func() bool { _, _, ok := c.Runs.onward(r, opus); return ok }() {
		t.Fatal("a run with no route moved on")
	}
}

// A worker whose turn ended before its harness started because the harness's
// program isn't on this machine ends blocked, saying so, instead of parking
// and retrying a start that can't work; another interrupted turn still parks.
func TestAWorkerWhoseHarnessIsntInstalledEndsBlocked(t *testing.T) {
	for _, tc := range []struct{ err, want string }{
		{`sandbox: tool "opencode" not found on this machine`, StatusBlocked},
		{"killed", StatusParked},
	} {
		c, _ := testHive(t)
		c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "opencode", Checkout: t.TempDir()})
		c.J.MustAppend("run.turn_started", turnStarted{ID: "w1", Turn: 1})
		out := c.WS.TurnOut("w1", 1)
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, nil, 0o600)
		os.WriteFile(out+".err", []byte(tc.err+"\n"), 0o600)
		r, _ := c.Runs.Get("w1")
		c.Runs.endTurn(r)
		got, _ := c.Runs.Get("w1")
		if got.Status != tc.want || tc.want == StatusBlocked && !strings.HasPrefix(got.Reason, CantStartHere) {
			t.Fatalf("%q: %s (%q), want %s", tc.err, got.Status, got.Reason, tc.want)
		}
	}
}
