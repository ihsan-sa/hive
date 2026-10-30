package standup

import (
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

// A standup asks the lead's chat about the shared projects and a hidden
// project's own chat about it, with what merged, reverted and got stuck
// since the last one; the person sees one line and the lead's answer, and a
// standup with nothing new since asks no one.
func TestStandup(t *testing.T) {
	dir := t.TempDir()
	ws := core.Workspace{Root: filepath.Join(dir, "hive")}
	ws.Init()
	cfg := ws.LoadConfig()
	cfg.Sandbox, cfg.BareAccepted, cfg.Harness = "bare", time.Now().Format(time.RFC3339), "fake"
	cfg.TurnMB = 0
	ws.SaveConfig(cfg)
	c, err := core.Open(ws, &core.Local{Dir: filepath.Join(dir, "forge")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, p := range []core.Project{{Name: "p", Repo: "me/p"}, {Name: "q", Repo: "me/q"}, {Name: "secret", Repo: "me/secret", Private: true}} {
		if _, err := c.Projects.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	a := &agents.Agents{C: c, Mod: modules.New(ws, c.J)}
	ch := chat.New(c, a)
	w := work.New(c, a, ch)
	m := New(c, w, ch)
	now := time.Now()
	save := func(t work.Task) { c.J.MustAppend("task.saved", t) }
	save(work.Task{ID: "t1", Project: "p", Title: "MERGED faster checks", Stage: work.StageDone, MergeSHA: "m1", PR: core.PR{Number: 7}, Worker: "w-1", Updated: now})
	save(work.Task{ID: "t2", Project: "p", Title: "OLD merge", Stage: work.StageDone, MergeSHA: "m0", Updated: now.Add(-48 * time.Hour)})
	save(work.Task{ID: "t3", Project: "p", Title: "Revert x", Kind: "revert", Stage: work.StageDone, MergeSHA: "m3", Lesson: "SCORE fell", Updated: now})
	save(work.Task{ID: "t4", Project: "p", Title: "STUCK thing", Stage: work.StageBlocked, Reason: "NO token", Updated: now})
	save(work.Task{ID: "t5", Project: "p", Title: "busy", Stage: work.StageWorking, Updated: now})
	save(work.Task{ID: "t6", Project: "secret", Title: "HIDDEN win", Stage: work.StageDone, MergeSHA: "m6", Updated: now})

	res, err := m.Run()
	if err != nil || !strings.Contains(res, "lead: 3 tasks") || !strings.Contains(res, "project:secret: 1 tasks") {
		t.Fatalf("run: %q %v", res, err)
	}
	prompts := map[string]string{}
	for _, e := range c.J.Since(0) {
		if e.Kind == "run.created" {
			var s core.RunSpec
			e.Decode(&s)
			prompts[s.Project] = s.Brief
		}
	}
	lead, hidden := prompts[""], prompts["secret"]
	for _, want := range []string{"Module standup:", "## p (me/p)", "MERGED faster checks (task t1, PR #7, worker w-1): gain", "Reverted (1)", "SCORE fell", "STUCK thing", "blocked: NO token", "In flight now: 1 working"} {
		if !strings.Contains(lead, want) {
			t.Fatalf("the lead's standup lacks %q:\n%s", want, lead)
		}
	}
	if strings.Contains(lead, "OLD merge") || strings.Contains(lead, "## q") || strings.Contains(lead, "HIDDEN") {
		t.Fatalf("the lead's standup holds what isn't new, a quiet project or a hidden one:\n%s", lead)
	}
	if !strings.Contains(hidden, "HIDDEN win") || strings.Contains(hidden, "MERGED") {
		t.Fatalf("the hidden project's standup: %q", hidden)
	}
	msgs := ch.Messages("lead")
	if len(msgs) != 1 || msgs[0].From != "hive" || !strings.HasPrefix(msgs[0].Text, "Standup:") || !strings.Contains(lead, "thread "+msgs[0].Thread) {
		t.Fatalf("the person sees one line, in the thread the lead answers in: %+v", msgs)
	}

	if res, _ := m.Run(); res != "nothing to tell" {
		t.Fatalf("a second standup with nothing new: %q", res)
	}
}
