package improve

import (
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// A fall in the score is one thing the judge weighs: when it judges the
// change worth keeping, main keeps it, and the baseline moves to where main
// now stands, so the next merge isn't measured against a best main left.
func TestJudgeKeepsAFallInTheScore(t *testing.T) {
	m, merge := scored(t)
	var asked []string
	m.askJudge = func(_ core.Project, _ work.Task, ms Measurement, reason string) (core.Judgement, bool) {
		asked = append(asked, reason)
		return core.Judgement{Verdict: core.GateKeep, Model: "stand-in", Summary: "a test that catches a bug; the score is noise"}, true
	}
	ms := merge(map[string]string{"n.txt": "4\n"})
	if len(asked) != 1 || !strings.Contains(asked[0], "fell to 4") {
		t.Fatalf("the judge was asked %q", asked)
	}
	if !ms.Kept || ms.Best == nil || *ms.Best != 4 || !strings.Contains(ms.Note, "kept by its judge") {
		t.Fatalf("a fall the judge kept: %+v", ms)
	}
	if ms := merge(map[string]string{"other.txt": "x\n"}); !ms.Kept || len(asked) != 1 {
		t.Fatalf("the next merge at 4 was measured against the old best: %+v, asked %d", ms, len(asked))
	}
	if b, _ := m.Best("p"); b != 4 {
		t.Fatalf("best %v, want 4", b)
	}
}

// The judge weighs only the score: red tests revert as before, unasked, and
// a judge with no usable answer leaves the score to decide.
func TestJudgeWeighsOnlyTheScore(t *testing.T) {
	m, merge := scored(t)
	asked := 0
	m.askJudge = func(core.Project, work.Task, Measurement, string) (core.Judgement, bool) {
		asked++
		return core.Judgement{}, false
	}
	if ms := merge(map[string]string{".hive/project.json": `{"test": "false", "score": {"command": "sh ./score.sh", "direction": "higher"}}`}); ms.Kept || asked != 0 {
		t.Fatalf("red tests: %+v, judge asked %d times", ms, asked)
	}
	ms := merge(map[string]string{".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher"}}`, "n.txt": "2\n"})
	if ms.Kept || asked != 1 || !strings.Contains(ms.Note, "no usable answer") {
		t.Fatalf("no judgement: %+v, asked %d", ms, asked)
	}
}

// A later merge measured while the judge decided keeps its baseline: the
// judge's keep of the earlier fall doesn't lower it.
func TestJudgeKeepLeavesALaterBaseline(t *testing.T) {
	m, merge := scored(t)
	m.askJudge = func(core.Project, work.Task, Measurement, string) (core.Judgement, bool) {
		nine := 9.0
		m.record(Measurement{Project: "p", SHA: "later", Task: "t9", TestsOK: true, Score: &nine, Kept: true, Best: &nine, Version: m.Scores("p")[0].Version})
		return core.Judgement{Verdict: core.GateKeep, Model: "stand-in"}, true
	}
	merge(map[string]string{"n.txt": "4\n"})
	if b, _ := m.Best("p"); b != 9 {
		t.Fatalf("best %v, want the later merge's 9", b)
	}
}

// While one merge is with its judge, a later merge of the project waits for
// the baseline it settles instead of being measured against the old one.
func TestLaterMergeWaitsForTheJudge(t *testing.T) {
	m, merge := scored(t)
	old := judgeHold
	judgeHold = time.Hour
	t.Cleanup(func() { judgeHold = old })
	m.mu.Lock()
	m.judging["earlier"] = judgingMerge{project: "p", running: true}
	m.mu.Unlock()
	if ms := merge(map[string]string{"n.txt": "4\n"}); ms.Task != "" {
		t.Fatalf("measured while an earlier merge was with its judge: %+v", ms)
	}
}

// After a restart, a merge whose judge run is still going holds the
// project's later merges again, until that merge comes back to be measured
// and finds its judge run by id.
func TestRestartHoldsLaterMergesForTheJudge(t *testing.T) {
	m, merge := scored(t)
	old := judgeHold
	judgeHold = time.Hour
	t.Cleanup(func() { judgeHold = old })
	earlier := work.Task{ID: "t0", Project: "p", MergeSHA: "0123456789abcdef0123456789abcdef01234567"}
	m.C.J.MustAppend("run.created", core.RunSpec{ID: "j-t0-" + core.Short(earlier.MergeSHA) + "-0", Role: core.JudgeRole, Project: "p"})
	m.holdForJudge(earlier)
	if ms := merge(map[string]string{"n.txt": "4\n"}); ms.Task != "" {
		t.Fatalf("measured while a recovered merge was with its judge: %+v", ms)
	}
	m.afterMerge(earlier) // it comes back: the hold is its own to settle now
	if ms := merge(map[string]string{"n.txt": "5\n"}); ms.Task == "" {
		t.Fatal("a later merge still waits once the recovered one was handled")
	}
}

// numbers.md says how far judges' keeps have lowered the baseline since the
// score last set a best of its own.
func TestJudgedDrift(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	hist := []Measurement{
		{Kept: true, Best: f(10)},
		{Kept: true, Best: f(9), Note: judgedKeep + " (x) though it fell"},
		{Kept: false, Score: f(1)},
		{Kept: true, Best: f(9)},
		{Kept: true, Best: f(7.5), Note: judgedKeep + " (x) though it fell"},
	}
	if n, drop, before := judgedDrift(hist); n != 2 || drop != 2.5 || before != 10 {
		t.Fatalf("drift %d %g %g, want 2 keeps, 2.5 down from 10", n, drop, before)
	}
	if n, _, _ := judgedDrift(append(hist, Measurement{Kept: true, Best: f(11)})); n != 0 {
		t.Fatalf("a new best of the score's own still counts %d judged keeps", n)
	}
}

// A fall in the score of a merge that touches security-listed files reverts
// as the score says, with no judge: its read of the diff is what an
// injection there would reach.
func TestSecurityFallRevertsUnjudged(t *testing.T) {
	m, merge := scored(t)
	m.askJudge = nil
	merge(map[string]string{core.SecurityList: "paths = [\"secure/**\"]\n"})
	ms := merge(map[string]string{"n.txt": "4\n", "secure/gate.go": "package secure\n"})
	if ms.Kept || !strings.Contains(ms.Note, "security-listed") {
		t.Fatalf("a security-listed fall: %+v", ms)
	}
	if n := len(m.C.Runs.List(func(r *core.Run) bool { return r.Role == core.JudgeRole })); n != 0 {
		t.Fatalf("%d judge runs on a security-listed fall", n)
	}
}
