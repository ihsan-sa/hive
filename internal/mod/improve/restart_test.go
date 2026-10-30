package improve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// events counts the journal's events of kind that say sha (and, for a
// check, are of check kind ck).
func events(m *Module, kind, sha, ck string) int {
	n := 0
	for _, e := range m.C.J.Since(0) {
		var x struct{ SHA, Kind string }
		if e.Kind == kind && e.Decode(&x) == nil && x.SHA == sha && (ck == "" || x.Kind == ck) {
			n++
		}
	}
	return n
}

// A merge whose measure a daemon restart cut short, its tests and score
// already run, concludes after the restart: Recover finds it from its task,
// notes it is resumed, and the measure goes on from the checks it had
// finished rather than running them again (hive main went 13 hours with no
// result on 2026-10-30 while the daemon restarted every 7 to 41 minutes).
func TestMeasureCutShortByARestartConcludesAfterIt(t *testing.T) {
	m, commit, _ := tested(t)
	commit(map[string]string{".hive/project.json": `{"test": "sh ./t.sh", "score": {"command": "sh ./score.sh", "direction": "higher"}}`, "score.sh": "cat n.txt\n", "n.txt": "5\n"})
	p, _ := m.C.Projects.Get("p")
	m.baseline(p)
	if b, ok := m.Best("p"); !ok || b != 5 {
		t.Fatalf("start: best %v %v, want 5", b, ok)
	}
	sha := commit(map[string]string{"n.txt": "6\n"})
	// The measure before the restart got this far.
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	if c := m.C.Effects.RunCheck(p, sha, "test", "sh ./t.sh"); !c.OK {
		t.Fatalf("test: %+v", c)
	}
	if c := m.C.Effects.RunCheck(p, sha, "score", "sh ./score.sh"); c.Score == nil || *c.Score != 6 {
		t.Fatalf("score: %+v", c)
	}
	m.C.J.MustAppend("task.saved", work.Task{ID: "t1", Project: "p", Title: "change t1", Stage: work.StageMerged, MergeSHA: sha, Updated: time.Now()})

	again := New(m.C, m.Agents, m.Work, m.Chat) // the daemon after its restart
	again.Recover()
	for deadline := time.Now().Add(30 * time.Second); !again.measured("p", sha); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the merge cut short by the restart never got a result")
		}
	}
	ms := again.Scores("p")
	last := ms[len(ms)-1]
	if last.SHA != sha || !last.Kept || last.Score == nil || *last.Score != 6 || !strings.Contains(last.Note, "recorded at this commit already") {
		t.Fatalf("measured %+v", last)
	}
	if n := events(m, "effect.checked", sha, "score"); n != 1 {
		t.Fatalf("the score ran again after the restart: %d readings", n)
	}
	if n := events(m, "effect.checked", sha, "test"); n != 1 {
		t.Fatalf("the tests ran again after the restart: %d runs", n)
	}
	if events(m, "score.resumed", sha, "") != 1 {
		t.Fatal("no note that the measure was resumed")
	}
}

// A merge whose pair a restart cut short goes on from the readings it had:
// the parent read once before the restart is not read again after it, so a
// pair longer than the time between restarts still ends in a result
// (hifi-receiver-2's cb7bbae0 started its pair over at four restarts in an
// hour on 2026-10-30).
func TestPairCutShortByARestartGoesOnFromItsReadings(t *testing.T) {
	m, commit, _ := tested(t)
	readings := filepath.Join(t.TempDir(), "readings")
	// score.sh prints the first line of the readings file and drops it.
	score := fmt.Sprintf("f=%q; head -n1 \"$f\"; tail -n +2 \"$f\" > \"$f.x\"; mv \"$f.x\" \"$f\"\n", readings)
	os.WriteFile(readings, []byte("9\n"), 0o644)
	commit(map[string]string{".hive/project.json": `{"test": "sh ./t.sh", "score": {"command": "sh ./score.sh", "direction": "higher", "tolerance": 1}}`, "score.sh": score})
	p, _ := m.C.Projects.Get("p")
	m.baseline(p)
	if b, ok := m.Best("p"); !ok || b != 9 {
		t.Fatalf("start: best %v %v, want 9", b, ok)
	}
	sha := commit(map[string]string{"a.txt": "a\n"})
	if _, err := m.C.Effects.Sync(p); err != nil {
		t.Fatal(err)
	}
	// Before the restart: the merge reads 2, its parent 9, then the
	// restart cuts the second reading of the parent (no number) short.
	os.WriteFile(readings, []byte("2\n9\n\n"), 0o644)
	m.C.Effects.RunCheck(p, sha, "test", "sh ./t.sh")
	if c := m.C.Effects.RunCheck(p, sha, "score", "sh ./score.sh"); c.Score == nil || *c.Score != 2 {
		t.Fatalf("score: %+v", c)
	}
	if _, _, _, _, ok := m.paired(p, sha, 2); ok {
		t.Fatal("the pair finished before its restart")
	}
	m.C.J.MustAppend("task.saved", work.Task{ID: "t1", Project: "p", Title: "change t1", Stage: work.StageMerged, MergeSHA: sha, Updated: time.Now()})

	// After it, only the parent's second reading and the merge's are left.
	os.WriteFile(readings, []byte("9\n9\n"), 0o644)
	again := New(m.C, m.Agents, m.Work, m.Chat)
	again.Recover()
	for deadline := time.Now().Add(30 * time.Second); !again.measured("p", sha); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the merge whose pair the restart cut short never got a result")
		}
	}
	ms := again.Scores("p")
	last := ms[len(ms)-1]
	if last.SHA != sha || !last.Kept || last.Against == nil || *last.Against != 9 || !strings.Contains(last.Note, "one sitting (merge 2, parent 9, parent 9, merge 9)") {
		t.Fatalf("measured %+v", last)
	}
	if b, _ := os.ReadFile(readings); len(b) != 0 {
		t.Fatalf("readings left %q: the parent was read again", b)
	}
	if n := events(m, "score.reading", sha, ""); n != 3 {
		t.Fatalf("%d readings recorded, want 3", n)
	}
}

// A merge never measured whose task aged past Recover's week is noted as
// given up, once however often the daemon starts.
func TestUnmeasuredMergeTooOldIsNotedAsAbandoned(t *testing.T) {
	m, commit, _ := tested(t)
	sha := commit(map[string]string{"README.md": "old\n"})
	m.C.J.MustAppend("task.saved", work.Task{ID: "t1", Project: "p", Title: "change t1", Stage: work.StageDone, MergeSHA: sha, Updated: time.Now().Add(-8 * 24 * time.Hour)})
	m.Recover()
	New(m.C, m.Agents, m.Work, m.Chat).Recover()
	if n := events(m, "score.abandoned", sha, ""); n != 1 {
		t.Fatalf("score.abandoned %d times, want once", n)
	}
}

// The wait for the forge's tests counts from the merge, which the journal
// keeps, not from when this daemon first looked: past forgeWait since the
// merge, the tests run here at once.
func TestForgeWaitCountsFromTheMerge(t *testing.T) {
	m, commit, _ := tested(t)
	p, _ := m.C.Projects.Get("p")
	sha := commit(map[string]string{"README.md": "x\n"})
	forge := m.C.Effects.Forge.(*core.Local)
	forge.SetChecks("me/p", sha, core.CheckRun{Name: "ci", State: core.CheckPending})
	if _, _, wait := m.forgeTest(p, sha, []string{"ci"}); !wait {
		t.Fatal("a merge just made should wait on the forge")
	}
	m.mu.Lock()
	delete(m.forgeAt, "p@"+sha) // a restart forgets the first look
	m.merged[sha] = time.Now().Add(-forgeWait - time.Minute)
	m.mu.Unlock()
	if _, ok, wait := m.forgeTest(p, sha, []string{"ci"}); wait || ok {
		t.Fatalf("waited past forgeWait since the merge: ok %v wait %v", ok, wait)
	}
}
