package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
	"github.com/ihsan-sa/hive-standalone-build/internal/softlayer"
)

// softTask is the default real task: an open fix from this Hive's own queue
// (task 261024-192651-ed793376e5, issue #460), small enough for one worker,
// and one the standard prompts would get as a brief. The first default (the
// gate's unequal TMPDIRs) landed as 4425272 before the arms ran on real agents.
const softTask = "An approved warden step can say it needs longer than 10 minutes. internal/mod/risk/risk.go:41: ExecTimeout = 10m applies to every approved step, and a host gate baseline (6 reps, 1-2h) was killed by it and reported only 'signal: killed'. Done: an approved script may carry a line '# hive-timeout: <Go duration>'; the line is weighed and pinned with the script's text, and capped at 4h. Without it a step still gets ExecTimeout (10m). A step that times out reports 'timed out after X' instead of a bare 'signal: killed'. docs/risk.md says so. Tests: the timeout line's parsing and a script that outlives the default."

// TestSoftLayer is the soft-layer experiment (docs/soft-layer.md): for each
// arm (HIVE_SOFT=all, or a comma list of softlayer.Variants), a sub-Hive in
// the state of this one (its source as the project, with its goals and
// lessons, and HIVE_SOFT_KB's notes as its knowledge base), its agents module
// replaced by the arm's prompts, given one real task in the lead's chat.
// Each is then observed from its journal: who ran, who talked to whom, what
// they asked for, and whether the task landed. It judges nothing and fails
// only if an arm can't be set up; the table is the result.
//
// Real agents (HIVE_E2E_HARNESS=claude) spend plan usage. The arms run side by
// side, so the day's weather hits them all. Unless HIVE_SOFT_WARDENS=1, a
// sub-Hive asks the person (this test, which never answers) before anything
// outside its project, so no warden's script ever runs on this machine for an
// arm whose warden was told only "You are a warden."
//
//	HIVE_SOFT=all HIVE_E2E_HARNESS=claude HIVE_SOFT_OUT=/tmp/soft go test ./internal/e2e -run TestSoftLayer -timeout 3h -v
func TestSoftLayer(t *testing.T) {
	list := os.Getenv("HIVE_SOFT")
	if list == "" {
		t.Skip("set HIVE_SOFT=all (or a comma list of arms) to run the soft-layer experiment")
	}
	arms, err := softlayer.Pick(list)
	if err != nil {
		t.Fatal(err)
	}
	essay, err := os.ReadFile(filepath.Join("..", "..", "docs", "the-hive-essay.md"))
	if err != nil {
		t.Fatal(err)
	}
	task := envOr("HIVE_SOFT_TASK", softTask)
	if os.Getenv("HIVE_SOFT_STATE") == "demo" {
		task = benchAsks[0]
	}
	wait := 15 * minute
	if os.Getenv("HIVE_E2E_HARNESS") != "" {
		wait = 90 * minute
	}
	if d, err := time.ParseDuration(os.Getenv("HIVE_SOFT_WAIT")); err == nil {
		wait = d
	}
	out := os.Getenv("HIVE_SOFT_OUT")
	if out == "" {
		out = t.TempDir()
	}
	// One arm at a time by default, two at most: seven real sub-Hives side by
	// side took the machine to a load of 600 and its memory ceiling
	// (2026-10-25). An arm's daemon and turns are stopped when its subtest
	// ends, before the next arm starts.
	par := 1
	if n, err := strconv.Atoi(os.Getenv("HIVE_SOFT_PARALLEL")); err == nil {
		par = min(max(n, 1), 2)
	}
	slots := make(chan struct{}, par)
	reps := make([]softlayer.Report, len(arms))
	t.Run("arms", func(t *testing.T) {
		for i, arm := range arms {
			t.Run(arm.Name, func(t *testing.T) {
				if par > 1 {
					t.Parallel()
				}
				slots <- struct{}{}
				t.Cleanup(func() { <-slots }) // first registered, so it runs after the arm's Hive is stopped
				reps[i] = softArm(t, arm, string(essay), task, wait, filepath.Join(out, arm.Name))
			})
		}
	})
	table := fmt.Sprintf("Task: %s\n\nHarness: %s; each arm waited up to %s.\n\n%s", task, envOr("HIVE_E2E_HARNESS", "fake"), wait, softlayer.Table(reps))
	if err := os.WriteFile(filepath.Join(out, "table.md"), []byte(table), 0o644); err != nil {
		t.Errorf("keeping the table: %v", err)
	}
	t.Logf("soft layer, in %s:\n%s", out, table)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// softArm runs one arm and keeps its journal, chats and report in keep.
func softArm(t *testing.T, arm softlayer.Variant, essay, task string, wait time.Duration, keep string) softlayer.Report {
	h := startWith(t, bin, func(c *core.Config) {
		if os.Getenv("HIVE_SOFT_WARDENS") != "1" {
			c.Autonomy = 1
		}
		c.MaxWorkers, c.MaxChecks = 1, 1 // one worker and one check at a time
	})
	if err := arm.Install(h.ws.Modules(), essay); err != nil {
		t.Fatal(err)
	}
	if kb := os.Getenv("HIVE_SOFT_KB"); kb != "" {
		copyNotes(t, kb, h.ws.KB())
	}
	project, ask := "hive", "In hive: "+task
	if os.Getenv("HIVE_SOFT_STATE") == "demo" { // a quick look at the pipeline, on the demo project
		var p core.Project
		h.post("/api/project", map[string]any{"demo": true}, &p)
		project, ask = p.Name, task
	} else {
		seedSource(t, h, "me/hive")
		h.post("/api/project", map[string]any{"name": "hive", "repo": "me/hive"}, nil)
	}
	began := time.Now().UTC()
	h.post("/api/chat", map[string]string{"chat": "lead", "text": ask}, nil)

	outcome := ""
	for deadline := time.Now().Add(wait); outcome == ""; time.Sleep(10 * time.Second) {
		standInCI(h, "me/"+project)
		// A sub-Hive whose daemon stops answering is a result too: its
		// journal and daemon log are still kept below, not lost to t.Fatal.
		st, err := h.c.State()
		if err != nil {
			outcome = "the sub-Hive's daemon stopped answering after " + time.Since(began).Round(time.Second).String() + ": " + err.Error()
			break
		}
		if tk, ok := askLanded(st, project); ok {
			outcome = "landed " + shortSHA(tk.MergeSHA) + " after " + time.Since(began).Round(time.Second).String()
		} else if time.Now().After(deadline) {
			outcome = "not landed: " + stages(st, project)
		}
	}
	h.quiesce()

	f, err := os.Open(h.ws.Events())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := softlayer.Observe(f, began, time.Time{})
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	rep.Variant, rep.Outcome = arm.Name, outcome
	if !armTookEffect(h, arm, essay) {
		t.Errorf("%s: the sub-Hive's agents didn't start from the arm's prompts", arm.Name)
	}
	// What the arm leaves is the experiment's output: a write that fails is
	// said, not dropped, but doesn't stop the other arms' results.
	kept := func(what string, err error) {
		if err != nil {
			t.Errorf("%s: keeping %s: %v", arm.Name, what, err)
		}
	}
	kept(keep, os.MkdirAll(keep, 0o755))
	raw, _ := json.MarshalIndent(rep, "", "  ")
	kept("report.json", os.WriteFile(filepath.Join(keep, "report.json"), raw, 0o644))
	j, err := os.ReadFile(h.ws.Events())
	if err == nil {
		err = os.WriteFile(filepath.Join(keep, "events.jsonl"), j, 0o644)
	}
	kept("events.jsonl", err)
	if log, err := os.ReadFile(filepath.Join(h.ws.Hive(), "daemon-test.log")); err == nil {
		kept("daemon.log", os.WriteFile(filepath.Join(keep, "daemon.log"), log, 0o644))
	}
	for what, from := range map[string]string{"chats": h.ws.Chats(), "runs": h.ws.Runs()} {
		if out, err := exec.Command("cp", "-r", from, filepath.Join(keep, what)).CombinedOutput(); err != nil {
			kept(what, fmt.Errorf("%v: %s", err, out))
		}
	}
	if diff, err := core.Git(h.forge.RepoPath("me/"+project), "log", "--stat", "--format=%h %s", "main"); err == nil {
		kept("main.log", os.WriteFile(filepath.Join(keep, "main.log"), []byte(diff), 0o644))
	} else {
		t.Logf("%s: no main.log: %v", arm.Name, err)
	}
	t.Logf("%s: %s", arm.Name, outcome)
	return rep
}

// armTookEffect: the system prompt of every run the sub-Hive made starts
// with the arm's orientation.
func armTookEffect(h *hive, arm softlayer.Variant, essay string) bool {
	files, err := arm.Build(essay)
	if err != nil {
		return false
	}
	raw, _ := os.ReadFile(h.ws.Events())
	runs := 0
	for _, line := range strings.Split(string(raw), "\n") {
		var e struct {
			Kind string
			Data struct{ System string }
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Kind != "run.created" {
			continue
		}
		runs++
		if !strings.HasPrefix(e.Data.System, files["orientation"]) {
			return false
		}
	}
	return runs > 0
}

// standInCI reports the checks main's tests/LANDING-security.toml requires as
// passed on each open PR's head: a local forge has no CI, so without this a
// sub-Hive of the Hive's own source waits on them forever. The sub-Hive's own
// tests, reads and score still decide; only the forge's run of the same tests
// (and the sandboxed e2e) is stood in for.
func standInCI(h *hive, repo string) {
	raw, err := core.Git(h.forge.RepoPath(repo), "show", "main:tests/LANDING-security.toml")
	if err != nil {
		return
	}
	var runs []core.CheckRun
	for _, n := range core.ParseList(raw, "required_checks") {
		runs = append(runs, core.CheckRun{Name: n, State: core.CheckPass, Detail: "stood in by the soft-layer experiment: a local forge has no CI"})
	}
	for _, pr := range h.forge.PRs(repo) {
		if pr.State != "OPEN" || len(runs) == 0 {
			continue
		}
		if got, _ := h.forge.Checks(repo, pr.HeadSHA); len(got) == 0 {
			h.forge.SetChecks(repo, pr.HeadSHA, runs...)
		}
	}
}

// askLanded finds the asked task merged: one the lead filed from its chat.
// Another merge (a fix the project's lead filed for a red main, say) is
// counted in the report's merges but doesn't end the arm: in the first real
// run (2026-10-25) both arms "landed" that way while the ask still waited.
func askLanded(st api.State, project string) (work.Task, bool) {
	for _, p := range st.Projects {
		for _, t := range p.Tasks {
			if p.Name == project && t.Chat == "lead" && t.Stage == work.StageDone && t.MergeSHA != "" && !strings.HasPrefix(t.Title, "Revert") {
				return t, true
			}
		}
	}
	return work.Task{}, false
}

func shortSHA(s string) string { return s[:min(len(s), 9)] }

// stages says where each of project's tasks stands, for an arm that didn't land.
func stages(st api.State, project string) string {
	var out []string
	for _, p := range st.Projects {
		if p.Name != project {
			continue
		}
		for _, tk := range p.Tasks {
			out = append(out, string(tk.Stage))
		}
	}
	if len(out) == 0 {
		return "no task was filed"
	}
	return "tasks " + strings.Join(out, ", ")
}

// seedSource pushes this checkout's committed tree, the Hive's own source with
// its .hive goals and lessons, to repo as one commit on main.
func seedSource(t *testing.T, h *hive, repo string) {
	t.Helper()
	if _, err := h.forge.EnsureRepo(repo, "", false); err != nil {
		t.Fatal(err)
	}
	head, err := core.Git(filepath.Join("..", ".."), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if _, err := core.Git("", "clone", "-q", h.forge.RepoPath(repo), tmp); err != nil {
		t.Fatal(err)
	}
	tar := filepath.Join(t.TempDir(), "src.tar")
	if _, err := core.Git(filepath.Join("..", ".."), "archive", "-o", tar, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("tar", "-xf", tar, "-C", tmp).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if err := throttleChecks(filepath.Join(tmp, ".hive", "project.json")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "The Hive at " + shortSHA(strings.TrimSpace(head))}, {"push", "-q", "origin", "HEAD:main"}} {
		if _, err := core.Git(tmp, args...); err != nil {
			t.Fatal(err)
		}
	}
}

// goThrottle caps the Go toolchain's parallelism in a sub-Hive's checks: a
// check's sandbox gets a fixed environment, so the cap goes in the command.
// It keeps the GOFLAGS the check already has (the shared module cache's
// -mod=readonly).
const goThrottle = `export GOMAXPROCS=4 GOFLAGS="${GOFLAGS:+$GOFLAGS }-p=2"; `

// throttleChecks puts goThrottle in front of the clone's test and score
// commands, so the sub-Hive's own checks and score run on a few cores.
func throttleChecks(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if c, ok := p["test"].(string); ok {
		p["test"] = goThrottle + c
	}
	if sc, ok := p["score"].(map[string]any); ok {
		if c, ok := sc["command"].(string); ok {
			sc["command"] = goThrottle + c
		}
	}
	out, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// copyNotes copies a knowledge base's notes (its top-level .md files and
// practice/), not its record of every event, which is this Hive's history.
func copyNotes(t *testing.T, from, to string) {
	t.Helper()
	for _, sub := range []string{"", "practice"} {
		files, _ := filepath.Glob(filepath.Join(from, sub, "*.md"))
		if err := os.MkdirAll(filepath.Join(to, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err == nil {
				err = os.WriteFile(filepath.Join(to, sub, filepath.Base(f)), raw, 0o644)
			}
			if err != nil {
				t.Fatalf("copying the knowledge base's notes: %v", err)
			}
		}
	}
}
