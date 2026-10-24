package e2e

// What a person does in the first hour, each as a live probe with real agents
// (HIVE_E2E_HARNESS=claude) and, where the plumbing can be scripted, a fake
// twin that CI runs on every push. tests/live.sh runs the whole matrix.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

func live(t *testing.T) {
	if os.Getenv("HIVE_E2E_HARNESS") == "" {
		t.Skip("set HIVE_E2E_HARNESS=claude to run real agents")
	}
}

// patience is how long one step may take: real agents are slow.
func patience(t *testing.T) time.Duration {
	if os.Getenv("HIVE_E2E_HARNESS") != "" {
		return 45 * minute
	}
	return 3 * minute
}

// onMain is true once path on repo's main contains text.
func (h *hive) onMain(repo, path, text string) func(api.State) bool {
	return func(api.State) bool {
		out, err := core.Git(h.forge.RepoPath(repo), "show", "main:"+path)
		return err == nil && strings.Contains(out, text)
	}
}

// landed finds a merged task in project that isn't a revert.
func landed(st api.State, project string) (work.Task, bool) {
	for _, p := range st.Projects {
		for _, t := range p.Tasks {
			if p.Name == project && t.Stage == work.StageDone && t.MergeSHA != "" && !strings.HasPrefix(t.Title, "Revert") {
				return t, true
			}
		}
	}
	return work.Task{}, false
}

func (h *hive) chatExists(id string) func(api.State) bool {
	return func(st api.State) bool {
		for _, c := range st.Chats {
			if c.ID == id {
				return true
			}
		}
		return false
	}
}

// TestHarnesses: claude, codex and fake are accepted; an unknown name is
// refused with a clear message when set and when the Hive starts, never at
// the first agent. (Codex became a harness in #30, after the brief said v1 refuses it.)
func TestHarnesses(t *testing.T) {
	t.Parallel()
	h := start(t)
	cfg := h.ws.LoadConfig()
	refused := map[string]string{"nope": `unknown harness "nope"`}
	for _, name := range []string{"claude", "codex", "fake", "nope"} {
		c := cfg
		c.Harness = name
		err := h.c.Do("POST", "/api/config", c, nil)
		if want, bad := refused[name]; bad && (err == nil || !strings.Contains(err.Error(), want)) || !bad && err != nil {
			t.Errorf("setting harness %s: %v", name, err)
		}
	}
	c := cfg
	c.Projects = map[string]core.ProjectSettings{"x": {Harness: "nope"}}
	if err := h.c.Do("POST", "/api/config", c, nil); err == nil || !strings.Contains(err.Error(), "project x") {
		t.Errorf("a project's unknown harness was accepted: %v", err)
	}
	h.post("/api/config", cfg, nil)

	for name, want := range refused {
		ws := core.Workspace{Root: filepath.Join(t.TempDir(), "hive")}
		if err := ws.Init(); err != nil {
			t.Fatal(err)
		}
		c := ws.LoadConfig()
		c.Harness = name
		ws.SaveConfig(c)
		cmd := exec.Command(bin, "daemon")
		cmd.Env = append(os.Environ(), "HIVE_HOME="+ws.Root, "HIVE_FORGE=local:"+t.TempDir())
		done := make(chan struct{})
		var out []byte
		var err error
		go func() { out, err = cmd.CombinedOutput(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
			t.Fatalf("the Hive started with harness %s", name)
		}
		if err == nil || !strings.Contains(string(out), want) {
			t.Errorf("starting with harness %s: %v %s", name, err, out)
		}
	}
}

// TestLiveAskLands: a real lead briefs a change in the demo project, a real
// worker commits it in its sandbox, the tests pass, a real reviewer reads it
// and it merges. Its fake twin is TestAskLandsAndProjects.
func TestLiveAskLands(t *testing.T) {
	live(t)
	h := start(t)
	h.post("/api/project", map[string]any{"demo": true}, nil)
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "In textkit, add a function capitalize_words(s) that capitalizes the first letter of each word, with a test."}, nil)
	st := h.wait("the ask to merge", patience(t), func(st api.State) bool { _, ok := landed(st, "textkit"); return ok })
	tk, _ := landed(st, "textkit")
	repo := h.forge.RepoPath("local/textkit")
	if out, _ := core.Git(repo, "grep", "-l", "capitalize_words", "main"); !strings.Contains(out, "test") {
		t.Errorf("no test of capitalize_words on main: %q", out)
	}
	// The forge squash-merges as "Hive", so the worker's author is on the PR's own commits.
	if out, _ := core.Git(repo, "log", "--format=%an", "main.."+tk.PR.HeadSHA); !strings.Contains(out, "Hive worker") {
		t.Errorf("no worker commit on the PR: %q", out)
	}
	reviewed := false
	for _, c := range h.forge.Comments("local/textkit", tk.PR.Number) {
		reviewed = reviewed || strings.Contains(c, "Review of")
	}
	if !reviewed {
		t.Errorf("no review on the PR")
	}
	h.wait("the merge to be measured", 5*minute, func(st api.State) bool {
		for _, pv := range st.Projects {
			for _, s := range pv.Scores {
				if s.SHA == tk.MergeSHA && s.TestsOK {
					return true
				}
			}
		}
		return false
	})
}

// A person asks the lead for a new module; it is built in a project of its
// own, installed on a warden's approval, granted to one project, and an
// agent there uses it. A newer commit of its source then reaches it through
// modules.update (the source has no release tags, so it follows main).
func TestModuleLifecycle(t *testing.T) { t.Parallel(); moduleLifecycle(t, false) }

func TestLiveModuleLifecycle(t *testing.T) { live(t); moduleLifecycle(t, true) }

func moduleLifecycle(t *testing.T, real bool) {
	h := start(t)
	h.seed("me/app", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "app", "repo": "me/app"}, nil)
	if real {
		h.post("/api/chat", map[string]string{"chat": "lead", "text": "Create a project called greet that is a new Hive module named greet: a module.json at the repository root with name \"greet\" and a one-line description, and a skill file skill.md that says: the codeword is PLUM42."}, nil)
		h.wait("project greet", patience(t), h.chatExists("project:greet"))
	} else {
		h.post("/api/chat", map[string]string{"chat": "lead", "text": "create project greet to hold a new module"}, nil)
		h.wait("project greet", patience(t), h.chatExists("project:greet"))
		h.post("/api/chat", map[string]string{"chat": "project:greet", "text": "build the module FAKE:skillmodule greet PLUM42"}, nil)
	}
	h.wait("the module to be built", patience(t), h.onMain("local/greet", "skill.md", "PLUM42"))

	h.post("/api/chat", map[string]string{"chat": "lead", "text": "install module " + h.forge.RepoPath("local/greet")}, nil)
	module := func(st api.State) (api.ModuleView, bool) {
		for _, m := range st.Modules {
			if m.Name == "greet" {
				return m, true
			}
		}
		return api.ModuleView{}, false
	}
	// At autonomy 2 a warden decides the install and the person hears after.
	// Its release is recorded after the clone, so a state read in between
	// shows it with none, and the update below would seem done at once.
	st := h.wait("a warden to install greet", patience(t), func(st api.State) bool { m, ok := module(st); return ok && m.Release != "" })
	h.wait("the person to be told after", minute, toldAbout("Install module"))
	first, _ := module(st)
	h.post("/api/module/access", map[string]any{"module": "greet", "project": "*", "allow": false}, nil)
	h.post("/api/module/access", map[string]any{"module": "greet", "project": "app", "allow": true}, nil)

	use := "FAKE:usemodule greet"
	if real {
		use = "Read the skill of the installed Hive module greet and write the codeword it gives to USED.md at the root of the repository."
	}
	h.post("/api/ask", map[string]string{"project": "app", "title": use}, nil)
	st = h.wait("the module's codeword on app's main", patience(t), h.onMain("me/app", "USED.md", "PLUM42"))
	dir := modules.NameFromURL(h.forge.RepoPath("local/greet")) // its folder in .modules
	for _, r := range st.Runs {
		if sys, _ := os.ReadFile(filepath.Join(h.ws.RunRO(r.ID), "system.md")); r.Role == "worker" && r.Project == "app" && !strings.Contains(string(sys), "`"+dir+"`") {
			t.Errorf("the worker's system prompt doesn't offer %s:\n%s", dir, sys)
		}
	}

	h.seed("local/greet", map[string]string{"skill.md": "The codeword is FIG77.\n"})
	h.post("/api/job", map[string]string{"name": "modules.update"}, nil)
	h.wait("greet to update to the newer commit", minute, func(st api.State) bool {
		m, ok := module(st)
		return ok && m.Release != first.Release
	})
	if skill, _ := os.ReadFile(filepath.Join(h.ws.Modules(), dir, "skill.md")); !strings.Contains(string(skill), "FIG77") {
		t.Errorf("the updated module's skill: %q", skill)
	}
}

// Self-improvement: `hive improve` sweeps the records, a change it files
// raises the score and is kept, and a change that lowers it is reverted.
func TestImproveSweep(t *testing.T) { t.Parallel(); improveSweep(t, false) }

func TestLiveImproveSweep(t *testing.T) { live(t); improveSweep(t, true) }

func improveSweep(t *testing.T, real bool) {
	h := start(t)
	files := map[string]string{
		".hive/project.json": `{"test": "test -f score.txt", "score": {"command": "cat score.txt", "direction": "higher", "tolerance": 0}}`,
		".hive/goals.md":     "# Goals\n\nKeep the score high.\n",
		"score.txt":          "5\n",
	}
	lower := map[string]string{"title": "lower the score", "known": "FAKE:lowerscore"}
	if real {
		files = map[string]string{
			".hive/project.json": `{"test": "test -s FACTS.md", "score": {"command": "grep -c . FACTS.md", "direction": "higher", "tolerance": 0}}`,
			".hive/goals.md":     "# Goals\n\nFACTS.md is a list of true facts about honey bees, one per line. The score is how many it holds; raise it with more true facts.\n",
			"FACTS.md":           "Honey bees live in colonies.\nA colony has one queen.\nWorker bees are female.\n",
		}
		// The losing half is the person's own ask, so a real worker and reviewer
		// should carry it through; the Hive, not the agents, must revert it.
		lower = map[string]string{"title": "Cut FACTS.md down to its first line only", "known": "The person wants a short file: keep the first line of FACTS.md and delete the rest. Nothing else changes."}
	}
	h.seed("me/bees", files)
	h.post("/api/project", map[string]any{"name": "bees", "repo": "me/bees"}, nil)
	best := func(st api.State) float64 {
		if len(st.Projects) == 1 && st.Projects[0].Best != nil {
			return *st.Projects[0].Best
		}
		return 0
	}
	st := h.wait("the baseline", 2*minute, func(st api.State) bool { return best(st) > 0 })
	base := best(st)

	cmd := exec.Command(bin, "improve")
	cmd.Env = append(os.Environ(), "HIVE_HOME="+h.ws.Root)
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "sweep started") {
		t.Fatalf("hive improve: %v %s", err, out)
	}
	h.wait("the sweep to file an ask", patience(t), func(st api.State) bool {
		return len(st.Sweeps) > 0 && st.Sweeps[0].Status == core.StatusDone && len(st.Sweeps[0].Asks) > 0
	})
	h.wait("the sweep's change to raise the score and be kept", patience(t), func(st api.State) bool { return best(st) > base })

	h.post("/api/ask", map[string]string{"project": "bees", "title": lower["title"], "known": lower["known"]}, nil)
	st = h.wait("the score-lowering change to be reverted", patience(t), taskStage("bees", "Revert: "+lower["title"], work.StageDone))
	if rv, _ := task(st, "bees", "Revert: "+lower["title"]); !strings.Contains(rv.Lesson, "fell to") {
		t.Errorf("revert reason: %q", rv.Lesson)
	}
}

// A control center left open while the Hive restarts finds the new daemon by
// itself: its client re-reads daemon.json for the new port and token, within
// a few of its 1.5s polls, whether the old daemon stopped cleanly or crashed.
func TestControlCenterFollowsARestart(t *testing.T) {
	t.Parallel()
	h := start(t)
	cc := h.c // the control center's client, dialled once at launch
	for i, stop := range []func(){
		func() { h.post("/api/shutdown", nil, nil); time.Sleep(time.Second) },
		h.kill,
	} {
		stop()
		if _, err := cc.State(); err == nil {
			t.Fatalf("restart %d: the old daemon still answers", i+1)
		}
		h.launch()
		start := time.Now()
		var err error
		for time.Since(start) < 5*time.Second {
			var st api.State
			if st, err = cc.State(); err == nil && st.Version == core.Version {
				break
			}
			time.Sleep(1500 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("restart %d: the open client still can't reach the Hive after %v: %v", i+1, time.Since(start), err)
		}
		var evs []core.Event
		cc.Do("GET", "/api/events", nil, &evs)
		n := 0
		for _, e := range evs {
			if e.Kind == "hive.started" {
				n++
			}
		}
		if n != i+2 {
			t.Fatalf("restart %d: the client reached a daemon that saw %d starts", i+1, n)
		}
	}
}
