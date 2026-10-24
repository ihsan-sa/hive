package stats

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// TestFiguresFromAFixtureJournal checks every figure against a journal whose
// answers are worked out by hand: a lead's resumed session counts only what
// each turn added, a person's own message and a merge saved twice count
// nothing, and a release journaled twice is one.
func TestFiguresFromAFixtureJournal(t *testing.T) {
	raw, err := os.ReadFile("testdata/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(path, raw, 0o644)
	j, err := core.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	tl := NewTally()
	tl.Loc = time.UTC
	j.Replay(tl.Apply)
	j.Subscribe(tl.Apply)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	v := tl.View(now)

	today := Spend{Total: 4.5, Projects: map[string]float64{"": 3, "hive": 1.5}, Roles: map[string]float64{"lead": 3, "worker": 1.5}}
	if !reflect.DeepEqual(v.Today, today) {
		t.Errorf("today %+v, want %+v", v.Today, today)
	}
	if !reflect.DeepEqual(v.Month, today) {
		t.Errorf("month %+v, want %+v (September's turn is last month)", v.Month, today)
	}
	if want := (Activity{Messages: 2, Turns: 1, Finished: 1, Merged: 2, Reverted: 1}); v.Hour != want {
		t.Errorf("hour %+v, want %+v", v.Hour, want)
	}
	if want := (Activity{Messages: 2, Turns: 2, Started: 1, Finished: 1, Merged: 2, Reverted: 1}); v.Day != want {
		t.Errorf("day %+v, want %+v", v.Day, want)
	}
	all := AllTime{
		Since: time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC), Runs: map[string]int{"lead": 1, "worker": 1},
		AgentHours: 4, Turns: 3, Spend: 6.5,
		Tokens: harness.Tokens{Input: 310, Output: 135, CacheRead: 6000, CacheWrite: 310},
		Models: map[string]Model{
			"opus":   {Turns: 2, Spend: 5, Tokens: harness.Tokens{Input: 110, Output: 55, CacheRead: 1000, CacheWrite: 10}},
			"sonnet": {Turns: 1, Spend: 1.5, Tokens: harness.Tokens{Input: 200, Output: 80, CacheRead: 5000, CacheWrite: 300}},
		},
		Merged: 2, Reverted: 1, Releases: 1,
	}
	if !reflect.DeepEqual(v.All, all) {
		t.Errorf("all time\n got %+v\nwant %+v", v.All, all)
	}

	// A day on, the recent figures have aged out and today has spent nothing.
	later := tl.View(now.Add(25 * time.Hour))
	if later.Day != (Activity{}) || later.Today.Total != 0 || later.Month.Total != 4.5 {
		t.Errorf("a day later: day %+v today %v month %v", later.Day, later.Today.Total, later.Month.Total)
	}

	// The tally follows the journal as it's appended, without a rescan.
	j.MustAppend("run.created", core.RunSpec{ID: "r1", Role: "reviewer", Project: "hive", Harness: "claude"})
	if v := tl.View(time.Now()); v.All.Runs["reviewer"] != 1 || v.Hour.Started != 1 {
		t.Errorf("an appended run isn't counted: %+v, hour %+v", v.All.Runs, v.Hour)
	}
}

// A view is a copy: changing it leaves the tally alone.
func TestViewIsACopy(t *testing.T) {
	tl := NewTally()
	at := time.Now()
	tl.Apply(core.Event{Seq: 1, At: at, Kind: "run.created", Data: []byte(`{"id":"a","role":"worker"}`)})
	v := tl.View(at)
	v.All.Runs["worker"] = 99
	if tl.View(at).All.Runs["worker"] != 1 {
		t.Fatal("the view shares the tally's map")
	}
}
