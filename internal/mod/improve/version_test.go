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
func scored(t *testing.T) (m *Module, merge func(files map[string]string) Measurement) {
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
	merge = func(files map[string]string) Measurement {
		t.Helper()
		put(files)
		n++
		sha, _ := core.Git(src, "rev-parse", "HEAD")
		m.afterMerge(work.Task{ID: fmt.Sprint("t", n), Project: "p", MergeSHA: strings.TrimSpace(sha)})
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
