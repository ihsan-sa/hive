package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintedCommand(t *testing.T) {
	for text, want := range map[string]string{
		`hive agent finish --status done --account "gpt-oss-120b" --tested "go test ./... passed; it's done" --unsure ""`: "finish|done|gpt-oss-120b|go test ./... passed; it's done",
		"All done.\n```bash\n$ hive agent finish --status blocked \\\n  --account 'no network'\n```":                      "finish|blocked|no network|",
		"Run `hive agent finish --status=done --account=\"x y\"` next":                                                    "",
		"`hive agent finish --status=done --account=\"x y\"`":                                                             "finish|done|x y|",
		"hive agent progress --text \"half way\"":                                                                         "progress|||",
		"Done.": "",
		"":      "",
	} {
		op, args, ok := printedCommand(text)
		got := ""
		if ok {
			got = strings.Join([]string{op, args["status"], args["account"], args["tested"]}, "|")
		}
		if got != want {
			t.Errorf("printedCommand(%q) = %q, want %q", text, got, want)
		}
	}
}

// endTurnWith ends turn 1 of a fresh worker whose answer was text.
func endTurnWith(t *testing.T, text string) Run {
	t.Helper()
	c, _ := testHive(t)
	c.J.MustAppend("run.created", RunSpec{ID: "w1", Role: "worker", Harness: "open"})
	r, _ := c.Runs.Get("w1")
	r.Turn = 1
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	line, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "result": text, "session_id": "s1"})
	os.WriteFile(out, append(line, '\n'), 0o600)
	c.Runs.endTurn(r)
	got, _ := c.Runs.Get("w1")
	return got
}

// A worker that answers with its finish command instead of running it is
// finished with the flags it wrote (w-261024-173554-7272934441's turns).
func TestPrintedFinishIsRun(t *testing.T) {
	got := endTurnWith(t, `hive agent finish --status done --account "made the LRU cache" --tested "go test ./... passed" --unsure ""`)
	if got.Status != StatusDone || got.Finish == nil || got.Finish.Account != "made the LRU cache" || got.Finish.Tested != "go test ./... passed" {
		t.Fatalf("status %q, finish %+v; want done with the printed account", got.Status, got.Finish)
	}
	got = endTurnWith(t, "hive agent finish --status blocked --account \"the tests need a network\"")
	if got.Status != StatusBlocked || got.Reason != "the tests need a network" {
		t.Fatalf("status %q, reason %q; want blocked with the printed account", got.Status, got.Reason)
	}
}

// A printed command that can't be run as written, or one that isn't a
// finish, gets a nudge that says it was printed, not run, and why.
func TestPrintedCommandNudge(t *testing.T) {
	got := endTurnWith(t, `hive agent finish --status finished --account "x"`)
	if got.Status != StatusIdle || got.Finish != nil || len(got.Pending) != 1 {
		t.Fatalf("status %q, finish %+v, pending %q", got.Status, got.Finish, got.Pending)
	}
	if p := got.Pending[0]; !strings.Contains(p, "printed, not run") || !strings.Contains(p, `status must be "done" or "blocked"`) {
		t.Fatalf("nudge %q", p)
	}
	got = endTurnWith(t, `hive agent message --to lead --text "done"`)
	if p := got.Pending; len(p) != 1 || !strings.Contains(p[0], "`hive agent message ...`") || !strings.Contains(p[0], "tool call") {
		t.Fatalf("nudge %q", p)
	}
	got = endTurnWith(t, "Done.")
	if p := got.Pending; len(p) != 1 || !strings.Contains(p[0], "writing it in your answer does nothing") {
		t.Fatalf("nudge %q", p)
	}
}
