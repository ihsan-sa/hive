package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProc writes a /proc with this much available memory and pressure; the
// "full" memory line is always high, so a hold on it would show.
func fakeProc(t *testing.T, memKB, avg10, cpu10 string) string {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "pressure"), 0o755)
	os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal:       16000000 kB\nMemFree:          100000 kB\nMemAvailable:   "+memKB+" kB\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pressure", "memory"), []byte("some avg10="+avg10+" avg60=0.00 avg300=0.00 total=0\nfull avg10=99.00 avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pressure", "cpu"), []byte("some avg10="+cpu10+" avg60=0.00 avg300=0.00 total=0\n"), 0o644)
	return dir
}

func TestStrained(t *testing.T) {
	for _, c := range []struct {
		name, memKB, avg10, cpu10 string
		floor                     int
		mem, cpu                  float64
		want                      string
	}{
		{"healthy", "8000000", "1.50", "20.00", 1024, 10, 40, ""},
		{"low memory", "500000", "1.50", "20.00", 1024, 10, 40, "low on memory (488 MB free"},
		{"memory pressure", "8000000", "15.20", "20.00", 1024, 10, 40, "memory pressure (15.20%"},
		{"cpu pressure", "8000000", "1.50", "55.00", 1024, 10, 40, "cpu pressure (55.00%"},
		{"checks off", "500000", "55.20", "90.00", 0, 0, 0, ""},
	} {
		got := strained(fakeProc(t, c.memKB, c.avg10, c.cpu10), c.floor, c.mem, c.cpu)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := strained(t.TempDir(), 1024, 10, 40); got != "" {
		t.Errorf("no /proc files: %q, want no hold", got)
	}
}

// A strain hold leaves a parked run's backoff and a nudged run's count alone,
// and journals once however the live figures move.
func TestStrainHoldKeepsBackoffAndNudges(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.MemFloorMB = 1 << 30
	if Strained(cfg.MemFloorMB, 0, 0) == "" {
		t.Skip("no /proc/meminfo here")
	}
	for _, id := range []string{"parked", "nudged"} {
		c.J.MustAppend("run.created", RunSpec{ID: id, Harness: "fake"})
	}
	c.J.MustAppend("run.turn_ended", turnEnded{ID: "parked", Turn: 1, Status: StatusParked, ParkedUntil: time.Now().Add(-time.Minute)})
	c.J.MustAppend("run.turn_ended", turnEnded{ID: "nudged", Turn: 1, Status: StatusIdle, Reason: "nudged: ended a turn without finishing", Queue: []string{"finish"}})
	if p, _ := c.Runs.Get("parked"); p.Retries != 1 || p.ParkedUntil.IsZero() {
		t.Fatalf("parked run set up as %+v", p)
	}
	if n, _ := c.Runs.Get("nudged"); n.Nudges != 1 {
		t.Fatalf("nudged run set up as %+v", n)
	}
	for _, id := range []string{"parked", "nudged"} {
		before, _ := c.Runs.Get(id)
		c.Runs.launch(before, cfg)
		mid, _ := c.Runs.Get(id)
		c.Runs.launch(mid, cfg)
		after, _ := c.Runs.Get(id)
		if after.Status != before.Status || !after.ParkedUntil.Equal(before.ParkedUntil) || after.Retries != before.Retries || after.Nudges != before.Nudges {
			t.Errorf("%s: held from %+v to %+v", id, before, after)
		}
		if !strings.HasPrefix(after.Reason, "waiting: machine low on memory") {
			t.Errorf("%s: reason %q", id, after.Reason)
		}
	}
	held := 0
	c.J.Replay(func(e Event) {
		if e.Kind == "run.held" {
			held++
		}
	})
	if held != 2 {
		t.Errorf("%d run.held events for two runs held twice each, want 2", held)
	}
}
