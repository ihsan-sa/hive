package work

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// A fix's check ranks first, and the rest, or a sha no task holds, not at
// all.
func TestCheckRankFixes(t *testing.T) {
	m, _, _ := helperRig(t)
	put := func(tk Task) {
		m.mu.Lock()
		m.tasks[tk.ID] = &tk
		m.mu.Unlock()
	}
	put(Task{ID: "blocker", Project: "p", SHA: "s1", Stage: StageTesting, Title: "holds"})
	put(Task{ID: "fix", Project: "p", SHA: "s2", Stage: StageTesting, Aim: AimFix})
	put(Task{ID: "gain", Project: "p", SHA: "s3", Stage: StageTesting})
	put(Task{ID: "done", Project: "p", SHA: "s4", Stage: StageDone})
	for sha, want := range map[string]int{"s1": 0, "s2": rankFix, "s3": 0, "s4": 0, "nobody": 0} {
		if got := m.checkRank("p", sha, "test"); got != want {
			t.Errorf("rank of %s: %d, want %d", sha, got, want)
		}
	}
	if got := m.checkRank("other", "s2", "test"); got != 0 {
		t.Errorf("another project's sha ranked %d", got)
	}
}

// While main is red, the fix whose brief names one of its failing tests goes
// ahead of every other fix, though behind main's own measurement; once main
// passes it is one fix among the rest (issue #1877).
func TestCheckRankRedMainFix(t *testing.T) {
	m, _, _ := helperRig(t)
	m.mu.Lock()
	m.tasks["red"] = &Task{ID: "red", Project: "p", SHA: "s1", Stage: StageTesting, Aim: AimFix, Title: "Fix the flaky example.com/x/internal/core::TestStaleModuleCacheRunsAgain"}
	m.tasks["why"] = &Task{ID: "why", Project: "p", SHA: "s2", Stage: StageTesting, Aim: AimFix, AimWhy: "TestMerge fails on main"}
	m.tasks["fix"] = &Task{ID: "fix", Project: "p", SHA: "s3", Stage: StageTesting, Aim: AimFix, Title: "TestStaleModuleCacheRunsAgainSlowly fails"}
	m.tasks["gain"] = &Task{ID: "gain", Project: "p", SHA: "s4", Stage: StageTesting, Known: "TestMerge"}
	m.mainFails["p"] = []string{"TestStaleModuleCacheRunsAgain", "TestMerge/sub_case"}
	m.mu.Unlock()
	for sha, want := range map[string]int{"s1": rankRedFix, "s2": rankRedFix, "s3": rankFix, "s4": 0} {
		if got := m.checkRank("p", sha, "test"); got != want {
			t.Errorf("while main is red, rank of %s: %d, want %d", sha, got, want)
		}
	}
	if got := m.checkRank("other", "s1", "test"); got != 0 {
		t.Errorf("another project's sha ranked %d", got)
	}
	done := m.MeasuringMain("p", "main1")
	if m.checkRank("p", "main1", "test") <= m.checkRank("p", "s1", "test") {
		t.Error("the red main's fix ranks with main's own measurement")
	}
	done()
	m.mu.Lock()
	delete(m.mainFails, "p")
	m.mu.Unlock()
	if got := m.checkRank("p", "s1", "test"); got != rankFix {
		t.Errorf("main green: the fix ranked %d", got)
	}
	if RankWhy(rankRedFix) != "fixes red main" || RankWhy(rankMain) != "measures main" || RankWhy(rankFix) != "a fix" {
		t.Error("RankWhy doesn't tell the ranks apart")
	}
}

// A red main's pytest ids name their fix by the test or the file, not by the
// directory before the first slash (hive-hwde main 5f5bd3af, issue #1989).
func TestCheckRankRedMainPytestFix(t *testing.T) {
	m, _, _ := helperRig(t)
	m.mu.Lock()
	m.tasks["fix"] = &Task{ID: "fix", Project: "p", SHA: "s1", Stage: StageTesting, Aim: AimFix, Title: "Main is green again", Known: "test_known_answers_fire_where_expected and test_parts_search_missing_httpx_names_cause fail on main 5f5bd3af"}
	m.tasks["file"] = &Task{ID: "file", Project: "p", SHA: "s2", Stage: StageTesting, Aim: AimFix, Title: "test_route_auto.py hangs on main"}
	m.tasks["word"] = &Task{ID: "word", Project: "p", SHA: "s3", Stage: StageTesting, Aim: AimFix, Title: "The tests under tests/ run faster"}
	m.tasks["param"] = &Task{ID: "param", Project: "p", SHA: "s4", Stage: StageTesting, Aim: AimFix, Title: "test_fit fails for the lid"}
	m.tasks["near"] = &Task{ID: "near", Project: "p", SHA: "s5", Stage: StageTesting, Aim: AimFix, Title: "test_parts fails"}
	m.mainFails["p"] = []string{
		"tests/test_parts.py::test_parts_search_missing_httpx_names_cause",
		"tests/test_verify.py::TestKnown::test_known_answers_fire_where_expected",
		"tests/test_route_auto.py::test_run_freerouting_silent_but_growing_ses_is_not_killed",
		"tests/test_mech.py::test_fit[lid-0.2]",
	}
	m.mu.Unlock()
	for sha, want := range map[string]int{"s1": rankRedFix, "s2": rankRedFix, "s3": rankFix, "s4": rankRedFix, "s5": rankFix} {
		if got := m.checkRank("p", sha, "test"); got != want {
			t.Errorf("rank of %s: %d, want %d", sha, got, want)
		}
	}
}

// Main's own measurement goes ahead of every task's check while it runs,
// and keeps its place once done (issue #1078).
func TestCheckRankMainFirst(t *testing.T) {
	m, _, _ := helperRig(t)
	m.mu.Lock()
	m.tasks["fix"] = &Task{ID: "fix", Project: "p", SHA: "s1", Stage: StageTesting, Aim: AimFix}
	m.mu.Unlock()
	done := m.MeasuringMain("p", "main1")
	again := m.MeasuringMain("p", "main1")
	if got := m.checkRank("p", "main1", "size"); got != rankMain || got <= m.checkRank("p", "s1", "test") {
		t.Errorf("main's measurement ranked %d, a fix's check %d", got, m.checkRank("p", "s1", "test"))
	}
	if got := m.checkRank("other", "main1", "score"); got != 0 {
		t.Errorf("another project's sha ranked %d", got)
	}
	done()
	done()
	if got := m.checkRank("p", "main1", "score"); got != rankMain {
		t.Errorf("one of two measurements done: ranked %d", got)
	}
	again()
	if got := m.checkRank("p", "main1", "score"); got != 0 {
		t.Errorf("measured: still ranked %d", got)
	}
}

// Driven through the check slots: a rank-0 check already waiting in a full
// line starts next once MeasuringMain marks its sha, ahead of one that asked
// before it (issue #1713).
func TestMeasuringMainMovesAWaiterUp(t *testing.T) {
	m, _, _ := helperRig(t)
	slots := m.C.Effects.Slots
	slots.Resize(func() int { return 1 })
	hold := slots.Acquire("p", "running", "test")
	started := make(chan string, 4)
	for _, sha := range []string{"pr", "main1"} {
		go func() {
			rel := slots.Acquire("p", sha, "score")
			started <- sha
			rel()
		}()
		for end := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			_, w := slots.State()
			if slices.ContainsFunc(w, func(u core.SlotUse) bool { return u.SHA == sha }) {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("%s never queued", sha)
			}
		}
	}
	if _, w := slots.State(); w[0].SHA != "pr" || w[1].Rank != 0 {
		t.Fatalf("before the mark, waiting %+v", w)
	}
	done := m.MeasuringMain("p", "main1")
	defer done()
	if _, w := slots.State(); w[0].SHA != "main1" || w[0].Rank != rankMain {
		t.Fatalf("after the mark, waiting %+v", w)
	}
	hold()
	if a, b := <-started, <-started; a != "main1" || b != "pr" {
		t.Fatalf("started %s then %s", a, b)
	}
}

// On a saturated disk no new worker starts while another works, but one
// does when none works, so the loop never stops; checks run one at a time.
func TestSaturatedDiskHoldsNewWorkersAndChecks(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	busy := "waiting: disk (IO pressure full avg60 62%, limit 40%)"
	was := diskBusy
	diskBusy = func(float64) string { return busy }
	defer func() { diskBusy = was }()
	a, b := file(t, m, "first", ""), file(t, m, "second", "")
	for _, tk := range []*Task{&a, &b} {
		tk.Stage = StageQueued
		m.save(tk, "")
	}
	start, wait := m.admit(m.List(nil))
	if !start[a.ID] || start[b.ID] || wait[b.ID] != busy+" for a worker" {
		t.Fatalf("with no worker working: start %v, wait %v", start, wait)
	}
	a.Stage = StageWorking
	m.save(&a, "")
	if start, wait = m.admit(m.List(nil)); start[b.ID] || wait[b.ID] == "" {
		t.Fatalf("started beside a working one: start %v, wait %v", start, wait)
	}
	if n := m.maxChecks(); n != 1 {
		t.Fatalf("%d checks at once on a saturated disk", n)
	}
	if p := m.Pace(); !strings.HasPrefix(p["checks"], "1, waiting: disk") {
		t.Fatalf("pace %v", p)
	}
	busy = ""
	if start, _ = m.admit(m.List(nil)); !start[b.ID] {
		t.Fatal("the disk eased and the brief still waits")
	}
	if n := m.maxChecks(); n != core.DefaultMaxChecks() {
		t.Fatalf("%d checks at once", n)
	}
}

// A brief the strain here would hold starts when an executor has room for
// its project's worker, one a pass, and its worker starts as an away run.
func TestExecutorRoomStartsAHeldBrief(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	wasDisk, wasRoom := diskBusy, elsewhereFor
	room := true
	diskBusy = func(float64) string { return "waiting: disk (IO pressure full avg60 62%, limit 40%)" }
	elsewhereFor = func(*core.Runs, string) bool { return room }
	defer func() { diskBusy, elsewhereFor = wasDisk, wasRoom }()
	w, a, b := file(t, m, "working", ""), file(t, m, "first", ""), file(t, m, "second", "")
	w.Stage = StageWorking
	m.save(&w, "")
	for _, tk := range []*Task{&a, &b} {
		tk.Stage = StageQueued
		m.save(tk, "")
	}
	start, wait := m.admit(m.List(nil))
	if !start[a.ID] || start[b.ID] || wait[b.ID] == "" {
		t.Fatalf("with room elsewhere: start %v, wait %v", start, wait)
	}
	m.mu.Lock()
	away := m.away[a.ID]
	m.mu.Unlock()
	if !away {
		t.Fatal("the brief started past the disk isn't marked away")
	}
	room = false
	if start, _ = m.admit(m.List(nil)); start[a.ID] || start[b.ID] {
		t.Fatalf("started with no room: %v", start)
	}
	m.mu.Lock()
	away = m.away[a.ID]
	m.mu.Unlock()
	if away {
		t.Fatal("a brief held again is still marked away")
	}
}

// A queued task isn't saved again when only the live figures in its waiting
// reason change: on 10-30 each was re-saved ~170 times an hour for a new avg60.
func TestWaitingReasonIgnoresLiveFigures(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	a := file(t, m, "first", "")
	a.Stage = StageQueued
	m.save(&a, "")
	m.waits(a, "waiting: disk (IO pressure full avg60 67%, limit 40%) for a worker")
	a, _ = m.Get(a.ID)
	at := a.Updated
	m.waits(a, "waiting: disk (IO pressure full avg60 64%, limit 40%) for a worker")
	if b, _ := m.Get(a.ID); !b.Updated.Equal(at) || !strings.Contains(b.Reason, "67%") {
		t.Fatalf("re-saved for a new figure: %q at %v, was %v", b.Reason, b.Updated, at)
	}
	m.waits(a, core.MemWait+" for a worker")
	if b, _ := m.Get(a.ID); b.Reason != core.MemWait+" for a worker" {
		t.Fatalf("a new kind of wait wasn't saved: %q", b.Reason)
	}
}
