package improve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// scored is project p on the rig, scored by ./score.sh reading n.txt (5),
// with its start measured. merge puts files on main and measures the merge.
// merge measures it as task as, when given, or as an unclear task.
func scored(t *testing.T) (m *Module, merge func(files map[string]string, as ...work.Task) Measurement) {
	t.Helper()
	m = rig(t)
	m.askJudge = func(core.Project, work.Task, Measurement, string) (core.Judgement, bool) {
		return core.Judgement{Verdict: core.GateRollback, Model: "stand-in", Summary: "the fall is real"}, true
	}
	forge := m.C.Effects.Forge.(*core.Local)
	if _, err := forge.EnsureRepo("me/p", "", false); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := core.Git(src, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			t.Fatal(out, err)
		}
	}
	put := func(files map[string]string) {
		for f, c := range files {
			os.MkdirAll(filepath.Dir(filepath.Join(src, f)), 0o755)
			os.WriteFile(filepath.Join(src, f), []byte(c), 0o644)
		}
		git("add", "-A")
		git("commit", "-qm", "change")
		git("push", "-q", forge.RepoPath("me/p"), "HEAD:main")
	}
	git("init", "-q", "-b", "main")
	put(map[string]string{
		".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher"}}`,
		"score.sh":           "cat n.txt\n",
		"n.txt":              "5\n",
	})
	p, err := m.C.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	if err != nil {
		t.Fatal(err)
	}
	m.baseline(p)
	if b, ok := m.Best("p"); !ok || b != 5 {
		t.Fatalf("start: best %v %v, want 5", b, ok)
	}
	n := 0
	merge = func(files map[string]string, as ...work.Task) Measurement {
		t.Helper()
		put(files)
		n++
		sha, _ := core.Git(src, "rev-parse", "HEAD")
		task := work.Task{ID: fmt.Sprint("t", n)}
		if len(as) > 0 {
			task = as[0]
		}
		task.Project, task.MergeSHA = "p", strings.TrimSpace(sha)
		m.afterMerge(task)
		m.judges.Wait() // a fall in the score goes to the judge first
		ms := m.Scores("p")
		return ms[len(ms)-1]
	}
	return m, merge
}

// A lower score from the same score is worse, and is not kept.
func TestLowerScoreIsNotKept(t *testing.T) {
	m, merge := scored(t)
	if ms := merge(map[string]string{"n.txt": "3\n"}); ms.Kept {
		t.Fatalf("a fall from 5 to 3 was kept: %+v", ms)
	}
	if b, _ := m.Best("p"); b != 5 {
		t.Fatalf("best %v, want 5", b)
	}
}

// A known fix wins over a score dip (issue #568): it is kept, its score is
// the baseline from there, and a brief looks into the dip instead of a
// revert. A change that isn't clearly a fix is taken as a gain, and a dip
// reverts it.
func TestFixWinsOverAScoreDip(t *testing.T) {
	m, merge := scored(t)
	reverts := func() int {
		return len(m.Work.List(func(x work.Task) bool { return x.Kind == "revert" }))
	}
	looks := func() []work.Task {
		return m.Work.List(func(x work.Task) bool { return strings.HasPrefix(x.Title, "Look into the score dip") })
	}
	fix := work.Task{ID: "fix-1", Title: "Fix the broken merge path", Aim: work.AimFix, AimWhy: "TestMerge fails: the merge never lands"}
	ms := merge(map[string]string{"n.txt": "3\n"}, fix)
	if !ms.Kept || ms.Best == nil || *ms.Best != 3 || !strings.Contains(ms.Note, "kept as a fix") {
		t.Fatalf("a fix whose score dipped from 5 to 3 wasn't kept at 3: %+v", ms)
	}
	if reverts() != 0 {
		t.Fatalf("a fix was reverted for a dip alone")
	}
	l := looks()
	if len(l) != 1 || !strings.Contains(l[0].Known, "TestMerge fails") || !strings.Contains(l[0].Done, "git revert "+ms.SHA) || !strings.Contains(l[0].Done, "--aim fix") {
		t.Fatalf("want one brief to look into the dip, naming the fix and how to revert it: %+v", l)
	}
	if l[0].Fix() {
		t.Fatal("the look into a dip names no failure, so it is taken as a gain")
	}
	p, _ := m.C.Projects.Get("p")
	m.lookIntoDip(p, work.Task{ID: "fix-1", Title: fix.Title, MergeSHA: ms.SHA}, "again", 5)
	if len(looks()) != 1 {
		t.Fatal("the same dip was looked into twice")
	}

	if ms := merge(map[string]string{"n.txt": "1\n"}, work.Task{ID: "u-1", Title: "Tidy the merge loop"}); ms.Kept {
		t.Fatalf("an unclear change whose score dipped was kept: %+v", ms)
	}
	if reverts() != 1 {
		t.Fatalf("%d reverts: an unclear change, taken as a gain, is reverted on a dip", reverts())
	}
	if b, _ := m.Best("p"); b != 3 {
		t.Fatalf("best %v, want the fix's 3", b)
	}
}

// A dip while an earlier one is still looked into files no second look:
// the open one takes it as context (three of four looks on 2026-10-27
// re-found one cause). A dip gone from main by the time it is read, main past
// the merge scoring back within tolerance, files none either.
func TestNoSecondLookIntoAKnownOrGoneDip(t *testing.T) {
	m, merge := scored(t)
	looks := func() []work.Task {
		return m.Work.List(func(x work.Task) bool { return strings.HasPrefix(x.Title, dipLook) })
	}
	fix := func(id string) work.Task {
		return work.Task{ID: id, Title: "Fix " + id, Aim: work.AimFix, AimWhy: id + " fails"}
	}
	first := merge(map[string]string{"n.txt": "3\n"}, fix("fix-1"))
	if len(looks()) != 1 {
		t.Fatalf("want one look into the first dip: %+v", looks())
	}
	second := merge(map[string]string{"n.txt": "1\n"}, fix("fix-2"))
	l := looks()
	if len(l) != 1 || !strings.Contains(l[0].Known, "dipped again after "+core.Short(second.SHA)) || !strings.Contains(second.Note, "takes this one too") {
		t.Fatalf("a dip while the first look is open should join it, not file a second: %+v\n%s", l, second.Note)
	}

	// Past its worker (in review), the look takes nothing it would leave
	// unread: the next dip gets a look of its own.
	reviewing := l[0]
	reviewing.Stage = work.StageReviewing
	m.C.J.MustAppend("task.saved", reviewing)
	third := merge(map[string]string{"n.txt": "0\n"}, fix("fix-4"))
	if l := looks(); len(l) != 2 || strings.Contains(reviewing.Known, core.Short(third.SHA)) {
		t.Fatalf("a dip while the open look is in review should get its own look: %+v\n%s", l, third.Note)
	}
	if tk, _ := m.Work.Get(reviewing.ID); strings.Contains(tk.Known, core.Short(third.SHA)) {
		t.Fatalf("a look in review took a dip no one will read: %s", tk.Known)
	}

	// The looks end; main recovers past a merge whose dip is read late.
	for _, o := range looks() {
		m.Work.Stop(o.ID, "done")
	}
	merge(map[string]string{"n.txt": "5\n"})
	p, _ := m.C.Projects.Get("p")
	fix3 := fix("fix-3")
	fix3.MergeSHA = first.SHA
	if got := m.lookIntoDip(p, fix3, "late", 3); !strings.Contains(got, "gone from main") || len(looks()) != 2 {
		t.Fatalf("a dip main has since left behind filed a look: %q %+v", got, looks())
	}
	// Still there at main's head: a new look.
	m.lookIntoDip(p, fix3, "late", 9)
	if len(looks()) != 3 {
		t.Fatalf("a dip still on main got no look: %+v", looks())
	}
}

// A merge that changes the score's own file is not compared with the old
// baseline, but it is still judged (#2090): its parent is scored under the
// merge's score beside it. Equal there, it is kept and the new number is the
// baseline; a fall under the new score goes to the judge; a parent that
// gives no number under it lets the merge start a new baseline.
func TestChangedScoreIsJudgedAgainstItsParent(t *testing.T) {
	m, merge := scored(t)
	ms := merge(map[string]string{"score.sh": "expr $(cat n.txt) - 2\n"})
	if !ms.Kept || ms.Best == nil || *ms.Best != 3 || ms.Against == nil || *ms.Against != 3 || !strings.Contains(ms.Note, "under this merge's score") {
		t.Fatalf("a changed score that reads its parent the same should be kept at 3: %+v", ms)
	}
	if m.Scores("p")[0].Version == ms.Version {
		t.Fatal("the score's version did not change with its file")
	}
	if ms := merge(map[string]string{"n.txt": "12\n"}); !ms.Kept || *ms.Best != 10 {
		t.Fatalf("a change outside the score kept the new baseline: %+v", ms)
	}
	// A new score and a fall in the code it reads in one merge: 1 under
	// the new score, against its parent's 10 under it too.
	if ms := merge(map[string]string{"score.sh": "expr $(cat n.txt) - 1\n", "n.txt": "2\n"}); ms.Kept || ms.Against == nil || *ms.Against != 11 || !strings.Contains(ms.Note, "the score changed, and under the new one it fell to 1") {
		t.Fatalf("a fall under a changed score was not judged: %+v", ms)
	}
	if refs, _ := core.Git(m.C.WS.Project("p"), "for-each-ref", "refs/hive/scored/"); refs != "" {
		t.Fatalf("the scratch commits' refs were left: %s", refs)
	}
}

// A changed score its parent can't run starts a new baseline.
func TestChangedScoreParentCantScore(t *testing.T) {
	_, merge := scored(t)
	ms := merge(map[string]string{"score.sh": "cat m.txt\n", "m.txt": "2\n"})
	if !ms.Kept || *ms.Best != 2 || !strings.Contains(ms.Note, "new baseline") {
		t.Fatalf("a changed score the parent can't run should start a new baseline at 2: %+v", ms)
	}
}

// A start recorded before measurements carried a version still counts as the
// baseline: a lower score on the next merge is reverted, not a new start.
func TestUnversionedStartIsStillTheBaseline(t *testing.T) {
	m, merge := scored(t)
	m.mu.Lock()
	for i := range m.scores["p"] {
		m.scores["p"][i].Version = ""
	}
	m.mu.Unlock()
	if ms := merge(map[string]string{"n.txt": "3\n"}); ms.Kept {
		t.Fatalf("a fall from 5 to 3 after an unversioned start was kept: %+v", ms)
	}
	if b, _ := m.Best("p"); b != 5 {
		t.Fatalf("best %v, want 5", b)
	}
}

// A score set with no evaluator paths raises an alert; listing them in a
// change to the score raises none.
func TestScoreWithNoPathsWarns(t *testing.T) {
	m, merge := scored(t)
	alerts := func() (n int) {
		for _, e := range m.C.J.Since(0) {
			var a struct{ Kind string }
			if e.Kind == "alert.raised" && e.Decode(&a) == nil && a.Kind == "score-paths" {
				n++
			}
		}
		return n
	}
	if alerts() != 1 {
		t.Fatalf("a score with no paths raised %d alerts, want 1", alerts())
	}
	merge(map[string]string{".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher", "paths": ["n.txt"]}}`})
	if alerts() != 1 {
		t.Fatalf("a score with paths listed raised an alert")
	}
}

// Behind, improve measures the newest merge first; an older one waiting with
// it is then covered by that measurement, not measured on its own.
func TestBehindMeasuresTheNewestMain(t *testing.T) {
	m, merge := scored(t)
	old := merge(map[string]string{"a.txt": "a\n"}).SHA
	m.mu.Lock()
	m.scores["p"] = m.scores["p"][:1] // as if the merge at old were still waiting
	m.mu.Unlock()
	newest := merge(map[string]string{"n.txt": "6\n"}, work.Task{ID: "t2"}).SHA
	m.mu.Lock()
	m.scores["p"] = m.scores["p"][:1]
	m.mu.Unlock()
	batch := m.newestFirst([]work.Task{{ID: "t1", Project: "p", MergeSHA: old}, {ID: "t2", Project: "p", MergeSHA: newest}})
	if batch[0].MergeSHA != newest || batch[1].MergeSHA != old {
		t.Fatalf("the newest merge must come first: %+v", batch)
	}
	for _, t := range batch {
		m.afterMerge(t)
	}
	if ms := m.Scores("p"); len(ms) != 2 || ms[1].SHA != newest || !m.measured("p", old) {
		t.Fatalf("the newest is measured and the older covered by it: %+v", ms)
	}
}

// A score that moves with the machine's load is judged against the merge's
// parent scored beside it in one sitting (issue #1865): here every reading
// is one point slower than the last, as a machine getting busier. A merge
// equal to its parent, read far under a best taken under a lighter load, is
// kept, a fix files no look into a dip and nothing is reverted; a real fall,
// read under the same drifting load, still goes to the judge.
func TestFallIsJudgedAgainstItsParentInOneSitting(t *testing.T) {
	m, merge := scored(t)
	load := filepath.Join(t.TempDir(), "load")
	os.WriteFile(load, []byte("0\n"), 0o644)
	drift := fmt.Sprintf("c=$(cat %q); echo $((c+1)) > %q; expr $(cat n.txt) - $c\n", load, load)
	if ms := merge(map[string]string{"score.sh": drift, "n.txt": "20\n",
		".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher", "tolerance": 3}}`}); !ms.Kept || *ms.Best != 20 {
		t.Fatalf("the new score's start: %+v", ms)
	}
	parent := m.Scores("p")[len(m.Scores("p"))-1].SHA
	os.WriteFile(load, []byte("10\n"), 0o644) // the machine got busier
	fix := work.Task{ID: "fix-1", Title: "Fix the merge", Aim: work.AimFix, AimWhy: "TestMerge fails"}
	ms := merge(map[string]string{"other.txt": "x\n"}, fix)
	// Readings: merge 10, parent 9, parent 8, merge 7.
	if !ms.Kept || *ms.Best != 10 || ms.Against == nil || *ms.Against != 9 || ms.AgainstSHA != parent || !strings.Contains(ms.Note, "load") {
		t.Fatalf("a fall that was the load alone: %+v", ms)
	}
	if n := len(m.Work.List(func(x work.Task) bool { return strings.HasPrefix(x.Title, dipLook) || x.Kind == "revert" })); n != 0 {
		t.Fatalf("%d looks or reverts for a merge equal to its parent", n)
	}
	parent = ms.SHA
	// Readings: merge 15-14, parent 20-15, parent 20-16, merge 15-17.
	ms = merge(map[string]string{"n.txt": "15\n"})
	if ms.Kept || ms.AgainstSHA != parent || !strings.Contains(ms.Note, "fell to 1 from its parent "+core.Short(parent)+"'s 5") {
		t.Fatalf("a real fall under the same load, against its parent: %+v", ms)
	}
}

// A fix's dip is measured once more before it becomes the baseline (issues
// #1788, #1865), beside its parent: a second reading back within tolerance
// was no dip, so the best stands and no brief is filed; one still under it
// makes the better of the two readings the baseline.
func TestFixDipIsMeasuredAgain(t *testing.T) {
	m, merge := scored(t)
	looks := func() int {
		return len(m.Work.List(func(x work.Task) bool { return strings.HasPrefix(x.Title, "Look into the score dip") }))
	}
	readings := filepath.Join(t.TempDir(), "readings")
	// score.sh prints the first line of the readings file and drops it.
	score := fmt.Sprintf("f=%q; head -n1 \"$f\"; tail -n +2 \"$f\" > \"$f.x\"; mv \"$f.x\" \"$f\"\n", readings)
	os.WriteFile(readings, []byte("1\n5\n"), 0o644)
	fix := work.Task{ID: "fix-1", Title: "Fix the merge", Aim: work.AimFix, AimWhy: "TestMerge fails"}
	ms := merge(map[string]string{"score.sh": score, "n.txt": "x\n"}, fix)
	// The score's file changed, so that merge starts a new baseline at 1;
	// the next ones are measured against it.
	if !ms.Kept || *ms.Best != 1 {
		t.Fatalf("%+v", ms)
	}
	os.WriteFile(readings, []byte("9\n"), 0o644)
	if ms := merge(map[string]string{"a.txt": "a\n"}); !ms.Kept || *ms.Best != 9 {
		t.Fatalf("want best 9: %+v", ms)
	}
	// Readings in turn: the merge, its parent twice, the merge again. The
	// first dips, the second is back at 9.
	os.WriteFile(readings, []byte("2\n9\n9\n9\n"), 0o644)
	fix.ID = "fix-2"
	ms = merge(map[string]string{"b.txt": "b\n"}, fix)
	if !ms.Kept || *ms.Best != 9 || !strings.Contains(ms.Note, "one sitting") || looks() != 0 {
		t.Fatalf("a dip gone on the second reading should keep the best 9 and file nothing: %+v, %d looks", ms, looks())
	}
	// Both readings dip: the better one is the baseline, and a brief looks.
	os.WriteFile(readings, []byte("2\n9\n9\n4\n"), 0o644)
	fix.ID = "fix-3"
	ms = merge(map[string]string{"c.txt": "c\n"}, fix)
	if !ms.Kept || *ms.Best != 4 || !strings.Contains(ms.Note, "kept as a fix") || looks() != 1 {
		t.Fatalf("want the fix kept at its better reading 4 with one brief: %+v, %d looks", ms, looks())
	}
	if b, _ := m.Best("p"); b != 4 {
		t.Fatalf("best %v, want 4", b)
	}
}
