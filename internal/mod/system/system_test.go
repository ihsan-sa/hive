package system

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
)

func TestSchedules(t *testing.T) {
	start := time.Date(2026, 10, 22, 1, 0, 0, 0, time.Local)
	every, err := Parse("every 12h")
	if err != nil {
		t.Fatal(err)
	}
	if every.Due(start.Add(time.Hour), time.Time{}, start, false) {
		t.Fatal("due before the first interval")
	}
	if !every.Due(start.Add(13*time.Hour), time.Time{}, start, false) {
		t.Fatal("not due after the interval")
	}
	if !every.Due(start.Add(time.Minute), time.Time{}, start, true) {
		t.Fatal("at-start job not due")
	}
	daily, _ := Parse("daily 04:00")
	if daily.Due(start.Add(2*time.Hour), time.Time{}, start, false) {
		t.Fatal("daily due before its time")
	}
	if !daily.Due(start.Add(3*time.Hour+time.Minute), time.Time{}, start, false) {
		t.Fatal("daily not due after its time")
	}
	if daily.Due(start.Add(4*time.Hour), start.Add(3*time.Hour+30*time.Minute), start, false) {
		t.Fatal("daily ran twice")
	}
	if off, _ := Parse("off"); off.Due(start.Add(1000*time.Hour), time.Time{}, start, true) {
		t.Fatal("off ran")
	}
	if _, err := Parse("sometimes"); err == nil {
		t.Fatal("accepted a bad schedule")
	}
}

// A Hive left running checks for a release again each interval after its
// at-start check, with no restart.
func TestRunningHiveRechecksForUpdates(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	site, v := release.Site, core.Version
	defer func() { release.Site, core.Version = site, v }()
	release.Site, core.Version = srv.URL, "v0.1.0"
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m := New(c)
	m.Register(Job{Name: "hive.update", AtStart: true, Fn: (&release.Module{C: c}).AutoUpdate})
	now := time.Now()
	m.started, m.last["hive.update"] = now.Add(-7*time.Hour), now.Add(-6*time.Hour-time.Minute)
	m.Tick()
	for i := 0; i < 100 && m.Jobs()[0].Last.Before(now); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked for the latest release %d times, want 1", n)
	}
	m.Tick()
	time.Sleep(100 * time.Millisecond)
	if asked.Load() != 1 {
		t.Fatal("checked again before the interval")
	}
}
