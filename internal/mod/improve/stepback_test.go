package improve

import (
	"encoding/json"
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
	ws.SaveConfig(cfg)
	forge := &core.Local{Dir: filepath.Join(dir, "forge")}
	if _, err := forge.EnsureRepo("me/hive", ""); err != nil {
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
	return New(c, a, work.New(c, a, ch), ch)
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
	nine := 9.0
	s := StepBack{ID: "stepback-1", Run: "stepback-1", Project: "p", Score: &nine, Best: 5, Setup: &Setup{Why: "SECRET-WHY", Asks: []string{"SECRET-ASK"}}}
	return exp, m.fileFault(s, p, exp, core.Score{Direction: "SECRET-DIR"})
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
	for end := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
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
	if !strings.Contains(ask.Title, "9 against 5") || strings.Contains(ask.Detail, "SECRET") {
		t.Fatalf("the person must get the scores and no transcript: %+v", ask)
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
