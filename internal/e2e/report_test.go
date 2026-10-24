package e2e

import (
	"fmt"
	"os"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/report/contracttest"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Sharing failures, against a local endpoint that holds every request to the
// contract: while sharing is off a blocked task sends nothing; once it's on,
// the next blocked task goes out as one failure report plus a usage report,
// each with a local copy, the earlier one stays home, and a second send with
// nothing new sends nothing.
func TestReportSharing(t *testing.T) {
	t.Parallel()
	srv := contracttest.New(t)
	batches := func() int { raw, _ := srv.Got(); return len(raw) }

	h := start(t)
	cfg := h.ws.LoadConfig()
	cfg.ShareURL = srv.URL
	// Only the sends the test posts run: a scheduled one landing between a
	// send's cursor and its post would pass its wait early. (Every 6h, not at
	// start, it can't fire here today; off says so rather than relying on it.)
	cfg.Schedules["report.send"] = "off"
	h.ws.SaveConfig(cfg)
	h.seed("me/rep", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "rep", "repo": "me/rep"}, nil)
	// A send waits for its job's next system.ran event rather than comparing
	// times: Windows' wall clock ticks coarsely, so a send that returns at
	// once (as it does while sharing is off) can journal at the very instant
	// the test read as "before", and Last.After(before) never comes true.
	// It reads on from a cursor, since /api/events without one holds only
	// the newest 2000 events.
	events := func(after int64) []core.Event {
		var evs []core.Event
		h.c.Do("GET", fmt.Sprintf("/api/events?after=%d", after), nil, &evs)
		return evs
	}
	ranSince := func(after int64) bool {
		for _, e := range events(after) {
			var d struct{ Job string }
			if e.Kind == "system.ran" && e.Decode(&d) == nil && d.Job == "report.send" {
				return true
			}
		}
		return false
	}
	send := func() {
		t.Helper()
		var cursor int64
		if evs := events(0); len(evs) > 0 {
			cursor = evs[len(evs)-1].Seq
		}
		h.post("/api/job", map[string]string{"name": "report.send"}, nil)
		h.wait("report.send to run", minute, func(st api.State) bool {
			for _, j := range st.Jobs {
				if j.Name == "report.send" && !j.Running {
					return ranSince(cursor)
				}
			}
			return false
		})
	}

	h.post("/api/ask", map[string]string{"project": "rep", "title": "rename the widget", "known": "FAKE:block"}, nil)
	h.wait("the first task to block", 2*minute, taskStage("rep", "rename the widget", work.StageBlocked))
	send()
	if n := batches(); n != 0 || len(report.Sent(h.ws)) != 0 {
		t.Fatalf("sharing is off, yet %d reports went out", n)
	}

	cfg = h.ws.LoadConfig()
	cfg.SetShare(true)
	h.ws.SaveConfig(cfg)
	h.post("/api/ask", map[string]string{"project": "rep", "title": "port the parser", "known": "FAKE:block"}, nil)
	h.wait("the second task to block", 2*minute, taskStage("rep", "port the parser", work.StageBlocked))
	send()
	raw, got := srv.Got()
	blocked, usage := 0, 0
	for _, r := range got {
		if r.Install != h.ws.LoadConfig().InstallID {
			t.Fatalf("install %q", r.Install)
		}
		if r.Type == "failure" && r.Kind == "task-blocked" {
			blocked++
		}
		if r.Type == "usage" && r.Usage["runs"] > 0 && r.Usage["turns"] > 0 && r.Message != "" {
			usage++
		}
	}
	if blocked != 1 || usage != 1 || got[len(got)-1].Type != "usage" {
		t.Fatalf("want one task-blocked failure and the usage report last: %+v", got)
	}
	sent := report.Sent(h.ws)
	if len(sent) != len(raw) {
		t.Fatalf("%d local copies for %d reports", len(sent), len(raw))
	}
	for i, name := range sent {
		if b, _ := os.ReadFile(name); string(b) != string(raw[i]) {
			t.Fatalf("local copy %s doesn't match what was sent", name)
		}
	}
	n := len(raw)
	send()
	if batches() != n {
		t.Fatalf("nothing new happened, yet %d more reports went out", batches()-n)
	}
}
