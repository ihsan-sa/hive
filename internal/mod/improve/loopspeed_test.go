package improve

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// The digest says how fast the real loop ran, from the tasks' saves: filed
// to merged and waiting to merge, median and p90, and merges each day.
func TestLoopSpeed(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Open(core.Workspace{Root: filepath.Join(dir, "hive")}, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Projects.Add(core.Project{Name: "p", Repo: "me/p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Projects.Add(core.Project{Name: "r", Repo: "me/r"}); err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c}
	ch := chat.New(c, a)
	m := New(c, a, work.New(c, a, ch), ch)
	now := time.Date(2026, 10, 29, 20, 0, 0, 0, time.UTC)
	if s := m.LoopSpeed("p", now); s != "" {
		t.Fatalf("nothing merged, yet: %q", s)
	}
	// Ten tasks filed i+1 hours before they merged, each ready an hour
	// before; one more merged outside the window, one never merged, one
	// in another project.
	save := func(id, project string, filed, ready, merged time.Time) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.applyLoop(work.Task{ID: id, Project: project, Created: filed, Stage: work.StageWorking}, filed)
		m.applyLoop(work.Task{ID: id, Project: project, Created: filed, Stage: work.StageMerging}, ready)
		m.applyLoop(work.Task{ID: id, Project: project, Created: filed, Stage: work.StageLanding}, ready.Add(time.Minute)) // a conflict sent it back
		m.applyLoop(work.Task{ID: id, Project: project, Created: filed, Stage: work.StageMerging}, ready.Add(2*time.Minute))
		if !merged.IsZero() {
			m.applyLoop(work.Task{ID: id, Project: project, Created: filed, Stage: work.StageMerged, MergeSHA: "m" + id}, merged)
			m.applyLoop(work.Task{ID: id, Project: project, Created: filed, Stage: work.StageDone, MergeSHA: "m" + id}, merged.Add(time.Minute))
		}
	}
	for i := range 10 {
		merged := now.Add(-time.Duration(i) * time.Hour)
		save(string(rune('a'+i)), "p", merged.Add(-time.Duration(i+1)*time.Hour), merged.Add(-time.Hour), merged)
	}
	save("old", "p", now.Add(-10*24*time.Hour), now.Add(-9*24*time.Hour), now.Add(-8*24*time.Hour))
	save("open", "p", now.Add(-time.Hour), now.Add(-time.Minute), time.Time{})
	save("other", "q", now.Add(-50*time.Hour), now.Add(-time.Hour), now)
	s := m.LoopSpeed("p", now)
	for _, want := range []string{
		"(10 merged)",
		"filed to merged: median 5.0h, p90 9.0h (10 tasks)",
		"to merged: median 1.0h, p90 1.0h (10 tasks)",
		"1.4 on average; 10-22 0,",
		"10-29 10\n",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("loop speed lacks %q:\n%s", want, s)
		}
	}
	c.J.MustAppend("task.saved", work.Task{ID: "live", Project: "r", Created: time.Now().UTC().Add(-3 * time.Hour), Stage: work.StageMerged, MergeSHA: "ml"})
	if d := m.Digest(0, ""); !strings.Contains(d, "## Project r (me/r)") || !strings.Contains(d, "Loop speed, the last 7 days from the journal (1 merged):\n- filed to merged: median 3.0h") {
		t.Fatalf("the digest lacks the loop speed:\n%s", d)
	}
}
