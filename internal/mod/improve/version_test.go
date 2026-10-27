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
// baseline: it starts a new one, so a lower number there isn't a revert, and a
// higher one isn't a gain.
func TestChangedScoreStartsANewBaseline(t *testing.T) {
	m, merge := scored(t)
	ms := merge(map[string]string{"score.sh": "echo 3\n"})
	if !ms.Kept || ms.Best == nil || *ms.Best != 3 || !strings.Contains(ms.Note, "new baseline") {
		t.Fatalf("a changed score should start a new baseline at 3: %+v", ms)
	}
	if ms := merge(map[string]string{"n.txt": "9\n"}); !ms.Kept || *ms.Best != 3 {
		t.Fatalf("a change outside the score kept the new baseline: %+v", ms)
	}
	if m.Scores("p")[0].Version == ms.Version {
		t.Fatal("the score's version did not change with its file")
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

// A score that moves with the machine's load is judged against main measured
// in the same moment (issue #1343): a merge that reads under a best taken
// under a lighter load is kept when main where the best was read scores the
// same now, and the baseline moves to that; a fall under the same load still
// goes to the judge, measured against where the best was read, so falls kept
// one by one can't walk the baseline down.
func TestFallIsJudgedAgainstMainMeasuredNow(t *testing.T) {
	m, merge := scored(t)
	load := filepath.Join(t.TempDir(), "load")
	os.WriteFile(load, []byte("0\n"), 0o644)
	if ms := merge(map[string]string{"score.sh": "expr $(cat n.txt) - $(cat " + load + ")\n", "n.txt": "10\n",
		".hive/project.json": `{"test": "true", "score": {"command": "sh ./score.sh", "direction": "higher", "tolerance": 3}}`}); !ms.Kept || *ms.Best != 10 {
		t.Fatalf("the new score's start: %+v", ms)
	}
	at := m.Scores("p")[len(m.Scores("p"))-1].SHA
	os.WriteFile(load, []byte("4\n"), 0o644) // the machine got busier
	ms := merge(map[string]string{"other.txt": "x\n"})
	if !ms.Kept || ms.Best == nil || *ms.Best != 6 || ms.Against == nil || *ms.Against != 6 || ms.AgainstSHA != at || !strings.Contains(ms.Note, "load") {
		t.Fatalf("a fall that was the load alone: %+v", ms)
	}
	second := ms.SHA // it scored the new baseline itself
	if ms := merge(map[string]string{"other.txt": "y\n", "n.txt": "9\n"}); !ms.Kept || *ms.Best != 6 || ms.Against != nil {
		t.Fatalf("a reading within the tolerance of the new baseline: %+v", ms)
	}
	ms = merge(map[string]string{"n.txt": "6\n"})
	if ms.Kept || ms.AgainstSHA != second || !strings.Contains(ms.Note, "fell to 2 from 6") {
		t.Fatalf("a real fall under the same load, against where the best was read: %+v", ms)
	}
}
