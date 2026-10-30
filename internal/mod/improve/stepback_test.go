package improve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// rig is an improve module on a Hive with no sandbox and the fake harness,
// with the Hive's own source "hive" seeded on the local forge.
func rig(t *testing.T) *Module {
	t.Helper()
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0 // a loaded machine mustn't hold the queue
	cfg.IOPressureMax, cfg.IOFullMax = 0, 0
	ws.SaveConfig(cfg)
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	if _, err := forge.EnsureRepo("me/hive", "", false); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "README.md"), []byte("# hive\n"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "seed"}, {"push", "-q", forge.RepoPath("me/hive"), "HEAD:main"}} {
		if out, err := core.Git(src, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	c, err := core.Open(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Projects.Add(core.Project{Name: "hive", Repo: "me/hive", Self: true}); err != nil {
		t.Fatal(err)
	}
	a := &agents.Agents{C: c, Mod: modules.New(ws, c.J)}
	ch := chat.New(c, a)
	w := work.New(c, a, ch)
	// Before the journal closes and the folders go: a worker step a test's
	// last Tick started may still be cloning or saving (seen on macOS ci).
	t.Cleanup(w.Wait)
	return New(c, a, w, ch)
}

// said records a run on project whose one turn said text and left notes.
func said(t *testing.T, m *Module, id, project, text string) core.Run {
	t.Helper()
	m.C.J.MustAppend("run.created", core.RunSpec{ID: id, Role: "worker", Project: project, Harness: "fake"})
	m.C.J.MustAppend("run.turn_started", map[string]any{"id": id, "turn": 1})
	out := m.C.WS.TurnOut(id, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}})
	os.WriteFile(out, append(line, '\n'), 0o644)
	os.MkdirAll(m.C.WS.RunBox(id), 0o755)
	os.WriteFile(filepath.Join(m.C.WS.RunBox(id), "notes.md"), []byte(text+" notes"), 0o644)
	r, _ := m.C.Runs.Get(id)
	return r
}

// won is a step-back on project p whose setup scored 9 against the Hive's 5,
// after one Hive run, with the fault it filed.
func won(t *testing.T, m *Module, isolated bool) (core.Run, string) {
	t.Helper()
	p, err := m.C.Projects.Add(core.Project{Name: "p", Repo: "me/p", Isolated: isolated})
	if err != nil {
		t.Fatal(err)
	}
	said(t, m, "w1", "p", "SECRET-HIVE ran make")
	exp := said(t, m, "stepback-1-exp", "p", "SECRET-SETUP ran make")
	nine, five := 9.0, 5.0
	s := StepBack{ID: "stepback-1", Run: "stepback-1", Project: "p", Score: &nine, Control: &five, Main: "abc1234", Best: 3, Setup: &Setup{Why: "SECRET-WHY", Asks: []string{"SECRET-ASK"}}}
	return exp, m.fileFault(s, p, exp, "", core.Score{Direction: "SECRET-DIR"})
}

// A fault files the scores, the count of asks and the run ids against the
// Hive's own source; the setup and the transcripts reach only its worker, as a
// file on the host, and the worker is told to quote none of it.
func TestFaultKeepsTranscriptsOffTheIssue(t *testing.T) {
	m := rig(t)
	exp, id := won(t, m, false)
	tasks := m.Work.List(func(t work.Task) bool { return t.ID == id })
	if len(tasks) != 1 || tasks[0].Project != "hive" {
		t.Fatalf("no task filed against the Hive's source: %q %+v", id, tasks)
	}
	task := tasks[0]
	if issue := task.Title + task.Done + task.Known; strings.Contains(issue, "SECRET") || !strings.Contains(issue, "9 against 5") ||
		!strings.Contains(issue, exp.ID) || !strings.Contains(issue, "w1") || !strings.Contains(issue, "1 ask") ||
		!strings.Contains(issue, "higher is better") || !strings.Contains(issue, "Quote nothing") {
		t.Fatalf("the issue must carry the scores, the count of asks and the run ids, and no setup or transcript:\n%s", issue)
	}
	ro := filepath.Join(m.C.WS.RunRO("w-"+id), "transcripts.md")
	// Two minutes: a step that failed once (a slow runner's git) is retried 30s later.
	for end := time.Now().Add(2 * time.Minute); ; time.Sleep(50 * time.Millisecond) {
		m.Work.Tick()
		if got, err := os.ReadFile(ro); err == nil {
			if !strings.Contains(string(got), "SECRET-SETUP") || !strings.Contains(string(got), "SECRET-HIVE") ||
				!strings.Contains(string(got), "SECRET-WHY") || !strings.Contains(string(got), "SECRET-ASK") {
				t.Fatalf("the worker's transcripts lack a run:\n%s", got)
			}
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the worker never got the transcripts: %+v", m.Work.List(func(t work.Task) bool { return t.ID == id }))
		}
	}
}

// A step-back on the Hive itself hands its worker the setup's commits as a
// patch that applies, and asks for them on main first.
func TestSelfFaultHandsOverTheCommits(t *testing.T) {
	m := rig(t)
	p, _ := m.C.Projects.Get("hive")
	from, err := m.C.Effects.Sync(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := m.C.WS.Project("hive")
	os.WriteFile(filepath.Join(dir, "win.txt"), []byte("the winning change\n"), 0o644)
	for _, args := range [][]string{{"add", "win.txt"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "the setup's win"}} {
		if out, err := core.Git(dir, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	sha, _ := core.Git(dir, "rev-parse", "HEAD")
	core.Git(dir, "reset", "-q", "--hard", from)
	exp := said(t, m, "stepback-2-exp", "hive", "ran make")
	nine, five := 9.0, 5.0
	s := StepBack{ID: "stepback-2", Run: "stepback-2", Project: "hive", From: from, Score: &nine, Control: &five, Main: from, Best: 5, Setup: &Setup{Asks: []string{"win"}}}
	id := m.fileFault(s, p, exp, sha, core.Score{})
	task := m.Work.List(func(t work.Task) bool { return t.ID == id })[0]
	if !strings.Contains(task.Known, "1 commit(s)") || !strings.Contains(task.Known, "git am -3") || !strings.Contains(task.Done, "forward-ported onto main") {
		t.Fatalf("the fault must point its worker at the commits that won:\n%s\n%s", task.Known, task.Done)
	}
	patch := task.Inputs["commits.patch"]
	if !strings.Contains(patch, "the setup's win") || !strings.Contains(patch, "+the winning change") {
		t.Fatalf("the patch lacks the setup's commit:\n%s", patch)
	}
	pf := filepath.Join(t.TempDir(), "commits.patch")
	os.WriteFile(pf, []byte(patch), 0o644)
	if out, err := core.Git(dir, "-c", "user.name=t", "-c", "user.email=t@t", "am", "-q", "-3", pf); err != nil {
		t.Fatalf("the patch does not apply: %v %s", out, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "win.txt")); string(got) != "the winning change\n" {
		t.Fatalf("applied, the patch left %q", got)
	}
}

// An isolated project's fault asks the person and files nothing, so its runs
// stay inside it.
func TestIsolatedFaultAsksThePerson(t *testing.T) {
	m := rig(t)
	_, id := won(t, m, true)
	if tasks := m.Work.List(nil); len(tasks) != 0 {
		t.Fatalf("an isolated project's fault was filed: %+v", tasks)
	}
	var ask core.Ask
	for _, a := range m.C.Person.Open() {
		if a.ID == id {
			ask = a
		}
	}
	if !strings.Contains(ask.Title, "9 against 5") || !strings.Contains(ask.Detail, "SECRET-ASK") ||
		strings.Contains(ask.Detail, "SECRET-SETUP") || strings.Contains(ask.Detail, "SECRET-HIVE") {
		t.Fatalf("the person must get the scores and the setup, and no transcript: %+v", ask)
	}
}

// One plateau starts one step-back: a merge whose task was filed before the
// last step-back began was in its budget, even when it is measured after.
func TestPlateauSkipsMergesFiledBeforeTheStepBack(t *testing.T) {
	m := rig(t)
	five := 5.0
	file := func(id string) {
		m.C.J.MustAppend("task.saved", work.Task{ID: id, Project: "p", Created: time.Now().UTC()})
	}
	merged := func(task string) {
		m.record(Measurement{Project: "p", Task: task, Kept: true, Score: &five, Best: &five})
	}
	merged("")
	file("a")
	file("b")
	merged("a")
	if n := m.plateau("p"); n != 1 {
		t.Fatalf("one merge on a flat score is a plateau of 1, got %d", n)
	}
	m.saveStepBack(StepBack{ID: "stepback-1", Project: "p", Status: "none", Started: time.Now().UTC()})
	merged("b")
	if n := m.plateau("p"); n != 0 {
		t.Fatalf("a merge filed before the step-back began counted toward a new plateau: %d", n)
	}
	file("c")
	merged("c")
	if n := m.plateau("p"); n != 1 {
		t.Fatalf("a merge filed after the step-back must count: %d", n)
	}
}

// An isolated project's step-back reads a digest of that project alone, and
// no other digest reads it.
func TestDigestOfOneProject(t *testing.T) {
	m := rig(t)
	for _, name := range []string{"p", "q"} {
		if _, err := m.C.Projects.Add(core.Project{Name: name, Repo: "me/" + name, Isolated: name == "p"}); err != nil {
			t.Fatal(err)
		}
		said(t, m, "w-"+name, name, "SAID-"+name)
		m.C.J.MustAppend("run.turn_ended", map[string]any{"id": "w-" + name, "turn": 1, "status": core.StatusBlocked, "reason": "BLOCKED-" + name})
		m.Chat.EnsureProject(name)
		m.Chat.Post("project:"+name, "", "", "CHAT-"+name)
	}
	all, one := m.Digest(0, ""), m.Digest(0, "p")
	for _, s := range []string{"## Project q", "SAID-q notes", "BLOCKED-q", "CHAT-q"} {
		if !strings.Contains(all, s) || strings.Contains(one, s) {
			t.Fatalf("%q must be in the shared digest only:\nall:\n%s\none:\n%s", s, all, one)
		}
	}
	for _, s := range []string{"## Project p", "SAID-p notes", "BLOCKED-p", "CHAT-p"} {
		if !strings.Contains(one, s) || strings.Contains(all, s) {
			t.Fatalf("%q must be in the isolated project's own digest only:\nall:\n%s\none:\n%s", s, all, one)
		}
	}
}

// A step-back saved before it had a budget fails rather than run uncapped.
func TestExperimentNeedsABudget(t *testing.T) {
	m := rig(t)
	m.C.Projects.Add(core.Project{Name: "p", Repo: "me/p"})
	m.experiment(StepBack{ID: "stepback-1", Run: "stepback-1", Project: "p", Status: "thinking"}, Setup{Asks: []string{"go"}})
	sbs := m.StepBacks("p")
	if len(sbs) != 1 || sbs[0].Status != "failed" || !strings.Contains(sbs[0].Note, "budget") {
		t.Fatalf("a step-back with no budget must fail: %+v", sbs)
	}
	if _, ok := m.C.Runs.Get("stepback-1-exp"); ok {
		t.Fatal("the experiment started with no budget")
	}
}

// The step-back scores its experiment beside main, not against the best it
// stored when it began: a setup that beats the stale best but not main as
// it stands loses, and one that beats main wins.
func TestStepBackComparesWithMainScoredBesideIt(t *testing.T) {
	m, merge := scored(t)
	from := m.Scores("p")[0].SHA
	merge(map[string]string{"n.txt": "8\n"})
	p, _ := m.C.Projects.Get("p")
	for i, c := range []struct {
		n, status string
	}{{"7", "lost"}, {"12", "won"}} {
		id := fmt.Sprintf("stepback-%d-exp", i)
		dir, err := m.C.Effects.NewCheckout(p, id, from)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "n.txt"), []byte(c.n+"\n"), 0o644)
		m.C.J.MustAppend("run.created", core.RunSpec{ID: id, Role: "worker", Project: "p", Harness: "fake", Checkout: dir})
		r, _ := m.C.Runs.Get(id)
		sb := StepBack{ID: fmt.Sprintf("stepback-%d", i), Run: fmt.Sprintf("stepback-%d", i), Exp: id, Project: "p", From: from, Best: 5,
			Status: "scoring", Setup: &Setup{Asks: []string{"go"}}}
		m.score(sb, r)
		var got StepBack
		for _, x := range m.StepBacks("p") { // StepBacks keeps no order
			if x.ID == sb.ID {
				got = x
			}
		}
		if got.Status != c.status || got.Control == nil || *got.Control != 8 || !strings.Contains(got.Note, "main's 8") {
			t.Fatalf("a setup scoring %s against main's 8 (stale best 5) must have %s: %+v", c.n, c.status, got)
		}
	}
}

// While main's score is one improve hasn't measured, a step-back judges
// only: a setup from the start couldn't be compared with main.
func TestStepBackWaitsForTheNewScore(t *testing.T) {
	m, _ := scored(t)
	forge := m.C.Effects.Forge.(*core.Local)
	src := t.TempDir()
	for _, args := range [][]string{{"clone", "-q", forge.RepoPath("me/p"), src}} {
		if out, err := core.Git("", args...); err != nil {
			t.Fatal(out, err)
		}
	}
	os.WriteFile(filepath.Join(src, "score.sh"), []byte("echo 100\n"), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "new score"}, {"push", "-q", "origin", "HEAD:main"}} {
		if out, err := core.Git(src, args...); err != nil {
			t.Fatal(out, err)
		}
	}
	said(t, m, "w1", "p", "spent")
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": "w1", "turn": 1, "status": core.StatusDone, "result": map[string]any{"cost_usd": 1.0}})
	p, _ := m.C.Projects.Get("p")
	m.StartStepBack(p, "test")
	sbs := m.StepBacks("p")
	if len(sbs) != 1 || sbs[0].Status != "thinking" || !sbs[0].NoScore {
		t.Fatalf("a step-back across a score change must judge only: %+v", sbs)
	}
	if r, _ := m.C.Runs.Get(sbs[0].Run); strings.Contains(r.Brief, "experiment.json") {
		t.Fatalf("a step-back across a score change was asked for an experiment:\n%s", r.Brief)
	}
}

// A project with no score to beat still gets the step-back's judgement: it
// runs with no experiment asked of it, and what it judged is kept on the
// record and said in the project's chat.
func TestStepBackJudgesWithoutAScore(t *testing.T) {
	m := rig(t)
	p, _ := m.C.Projects.Get("hive")
	said(t, m, "w1", "hive", "ran make")
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": "w1", "turn": 1, "status": core.StatusDone, "result": map[string]any{"cost_usd": 2.5}})
	if err := m.StartStepBack(p, "it is the regular step-back"); err != nil {
		t.Fatalf("a project with no score must still be judged: %v", err)
	}
	sbs := m.StepBacks("hive")
	if len(sbs) != 1 || sbs[0].Status != "thinking" || !sbs[0].NoScore || sbs[0].From == "" {
		t.Fatalf("want one judging step-back from main: %+v", sbs)
	}
	s := sbs[0]
	r, ok := m.C.Runs.Get(s.Run)
	if !ok || !strings.Contains(r.Brief, "judgement.md") || !strings.Contains(r.Brief, "goals") || strings.Contains(r.Brief, "experiment.json") {
		t.Fatalf("the brief must ask for a judgement and no experiment: %+v", r.RunSpec)
	}
	os.MkdirAll(m.C.WS.RunBox(s.Run), 0o755)
	os.WriteFile(filepath.Join(m.C.WS.RunBox(s.Run), "judgement.md"), []byte("JUDGED: the milestone has no number"), 0o644)
	os.WriteFile(filepath.Join(m.C.WS.RunBox(s.Run), "experiment.json"), []byte(`{"asks":["go"]}`), 0o644)
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": s.Run, "turn": 1, "status": core.StatusDone})
	m.advance(s)
	got := m.StepBacks("hive")[0]
	if got.Status != "judged" || got.Exp != "" || !strings.Contains(got.Report, "JUDGED") {
		t.Fatalf("a step-back with no score must end judged, its judgement kept, and run no experiment: %+v", got)
	}
	posted := false
	for _, msg := range m.Chat.Messages("project:hive") {
		posted = posted || strings.Contains(msg.Text, "JUDGED")
	}
	if !posted {
		t.Fatal("the judgement was not said in the project's chat")
	}
}

// A step-back with nothing spent still judges goals no step-back has judged
// as they stand, with no experiment; once judged, nothing new is spent and
// nothing changed, it does not run. The schedule starts one on goals alone.
func TestStepBackJudgesUnjudgedGoalsWithNothingSpent(t *testing.T) {
	m := rig(t)
	p, _ := m.C.Projects.Get("hive")
	if got := m.RotateStepBacks(); !strings.Contains(got, "hive") {
		t.Fatalf("goals never judged must get a step-back on the schedule: %q", got)
	}
	sbs := m.StepBacks("hive")
	if len(sbs) != 1 || sbs[0].Status != "thinking" || !sbs[0].Unspent || sbs[0].Goals == "" || !strings.Contains(sbs[0].Why, "goals") {
		t.Fatalf("want one judging step-back with nothing spent: %+v", sbs)
	}
	s := sbs[0]
	r, _ := m.C.Runs.Get(s.Run)
	if !strings.Contains(r.Brief, "judgement.md") || strings.Contains(r.Brief, "experiment.json") {
		t.Fatalf("the brief must ask for a judgement and no experiment: %s", r.Brief)
	}
	os.MkdirAll(m.C.WS.RunBox(s.Run), 0o755)
	os.WriteFile(filepath.Join(m.C.WS.RunBox(s.Run), "judgement.md"), []byte("JUDGED: the goals hold"), 0o644)
	m.C.J.MustAppend("run.turn_ended", map[string]any{"id": s.Run, "turn": 1, "status": core.StatusDone})
	m.advance(s)
	if got := m.StepBacks("hive")[0]; got.Status != "judged" || got.Exp != "" || !strings.Contains(got.Note, "nothing spent") {
		t.Fatalf("a step-back with nothing spent must end judged with no experiment: %+v", got)
	}
	if got := m.RotateStepBacks(); strings.Contains(got, "hive") {
		t.Fatalf("goals judged as they stand, with nothing merged, need no new step-back: %q", got)
	}
	if err := m.StartStepBack(p, "its best score has not moved"); err != nil {
		t.Fatal(err)
	}
	sbs = m.StepBacks("hive")
	var last StepBack
	for _, x := range sbs {
		if x.ID != s.ID {
			last = x
		}
	}
	if len(sbs) != 2 || last.Status != "none" || !strings.Contains(last.Note, "spent nothing") {
		t.Fatalf("with nothing spent and its goals judged, a step-back must not run: %+v", sbs)
	}
}
