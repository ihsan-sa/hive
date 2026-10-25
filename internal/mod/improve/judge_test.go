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
	m.judging["earlier"] = "p"
	m.mu.Unlock()
	if ms := merge(map[string]string{"n.txt": "4\n"}); ms.Task != "" {
		t.Fatalf("measured while an earlier merge was with its judge: %+v", ms)
	}
}
