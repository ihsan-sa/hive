// Package e2e runs the real hive binary as a daemon with the scripted fake
// harness and a local forge, and checks the milestones end to end.
//
// Agents run bare here (opted in) unless HIVE_E2E_SANDBOX=auto, which uses the
// platform sandbox (Linux namespaces, or WSL on Windows). The live probes
// (HIVE_E2E_HARNESS=claude or codex) use the sandbox when it works, unless
// HIVE_E2E_SANDBOX=bare.
package e2e

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/risk"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/scan"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

var bin string

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "hive-e2e-bin-")
	if err != nil {
		panic(err)
	}
	name := "hive"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin = filepath.Join(dir, name)
	if pre := os.Getenv("HIVE_E2E_BIN"); pre != "" {
		// The gate's evaluator: this tree's tests against another tree's binary.
		bin = pre
		code := m.Run()
		os.RemoveAll(dir)
		os.Exit(code)
	}
	root, _ := exec.Command("go", "env", "GOMOD").Output()
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/hive")
	cmd.Dir = filepath.Dir(strings.TrimSpace(string(root)))
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(string(out))
	}
	if runtime.GOOS == "windows" {
		// The WSL sandbox runs the Linux build next to the Windows one.
		lin := exec.Command("go", "build", "-o", filepath.Join(dir, "hive-linux-amd64"), "./cmd/hive")
		lin.Dir = cmd.Dir
		lin.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if out, err := lin.CombinedOutput(); err != nil {
			panic(string(out))
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type hive struct {
	t     *testing.T
	ws    core.Workspace
	forge *core.Local
	cmd   *exec.Cmd
	c     *api.Client
	env   []string      // added to the daemon's environment
	exe   string        // the binary the daemon runs, when not bin
	cpus  int           // the daemon, and all it starts, run on this many CPUs (Linux; 0 is all)
	slot  int           // which group of cpus CPUs (pickCPUs)
	jobs  []func()      // end what each daemon left running (Windows)
	ended chan struct{} // closed once the daemon process last launched has exited
}

func start(t *testing.T, env ...string) *hive { return startWith(t, bin, nil, env...) }

// startWith starts exe as the daemon, after prep (if any) has changed the
// test's settings.
func startWith(t *testing.T, exe string, prep func(*core.Config), env ...string) *hive {
	t.Helper()
	return startOn(t, 0, 0, exe, prep, env...)
}

// startOn is startWith with the daemon and everything it starts pinned to
// cpus CPUs (0 is all of them), the slot-th group of them, where pin can.
func startOn(t *testing.T, cpus, slot int, exe string, prep func(*core.Config), env ...string) *hive {
	t.Helper()
	dir := t.TempDir()
	h := &hive{t: t, ws: core.Workspace{Root: filepath.Join(dir, "hive")}, forge: &core.Local{Dir: filepath.Join(dir, "forge")}, env: env, exe: exe, cpus: cpus, slot: slot}
	if err := h.ws.Init(); err != nil {
		t.Fatal(err)
	}
	// A handle list, since the scan on every release and export fails without one.
	os.WriteFile(filepath.Join(h.ws.Private(), scan.HandlesFile), []byte("nobody-in-this-test\n"), 0o600)
	cfg := h.ws.LoadConfig()
	cfg.Harness, cfg.AutoUpdate = "fake", false
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0 // a loaded CI runner mustn't hold fake turns
	if hn := os.Getenv("HIVE_E2E_HARNESS"); hn != "" {
		cfg.Harness = hn // a live probe: real agents, plan usage
		if _, err := harness.Get(hn); err != nil {
			// A module's harness (opencode): its module is a default one, which
			// the daemon writes only after it checks the harness.
			if err := modules.New(h.ws, nil).InstallDefaults(); err != nil {
				t.Fatal(err)
			}
			harness.UseModules(h.ws.Modules())
		}
	}
	if m := os.Getenv("HIVE_E2E_MODEL"); m != "" { // a harness whose default model isn't Claude's (opencode/big-pickle)
		cfg.Model, cfg.LeadModel, cfg.ReviewerModel = m, m, m
	}
	if sb := os.Getenv("HIVE_E2E_SANDBOX"); sb == "auto" || (sb == "" && cfg.Harness != "fake" && sandboxed()) {
		cfg.Sandbox = "auto" // a live probe takes the path people get
	} else {
		cfg.Sandbox, cfg.BareAccepted = "bare", time.Now().Format(time.RFC3339)
	}
	cfg.PlateauMerges = 0 // TestStepBack turns it on
	cfg.Schedules = map[string]string{"improve.sweep": "off", "improve.stepback": "off", "kb.file": "off", "modules.update": "off", "hive.update": "off"}
	if prep != nil {
		prep(&cfg)
	}
	h.ws.SaveConfig(cfg)
	// Before the launch, so a daemon that is slow to answer is ended too.
	t.Cleanup(func() {
		h.quiesce()
		// A clean shutdown first, so module programs (the kb's dream, run
		// with go run) are stopped and waited for before the folder goes.
		if h.c != nil && h.ended != nil {
			h.c.Do("POST", "/api/shutdown", nil, nil)
			select {
			case <-h.ended:
			case <-time.After(20 * time.Second):
			}
		}
		h.kill()
		for _, end := range h.jobs {
			end()
		}
		// What a daemon started outside its group (module programs, turns)
		// outlives it once killed, and a git it started (a gate's, a check's,
		// the knowledge base's) can for a moment; t.TempDir's removal fails on
		// a folder still being written, so end those and remove the Hive once
		// it goes quiet.
		settle(dir, h.ws.Root)
	})
	h.launch()
	return h
}

// sandboxed says the platform sandbox works here. On Linux it asks the built
// binary, because an in-process probe would exec this test binary; the other
// platforms' checks run system tools and are safe in-process.
func sandboxed() bool {
	if runtime.GOOS == "linux" {
		return exec.Command(bin, "sandbox-probe").Run() == nil
	}
	_, err := sandbox.Select(sandbox.Options{Mode: "auto"})
	return err == nil
}

// TestSandboxedAsksTheBinary: the live probes take the sandbox whenever the
// built binary says it works. It proves sandboxed() runs the built binary and
// not an in-process probe: that would exec this test binary, which the sandbox
// guard refuses, so sandboxed() would be false where the probe passes.
func TestSandboxedAsksTheBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("not Linux")
	}
	if sandboxed() {
		return
	}
	if exec.Command(bin, "sandbox-probe").Run() == nil {
		t.Fatal("sandboxed() is false where `hive sandbox-probe` passes")
	}
	t.Skip("the sandbox probe fails here")
}

// quiesce stops live agents and waits for their processes to end, so the
// test's folder can be removed (Windows won't delete open files).
func (h *hive) quiesce() {
	if h.c == nil {
		return
	}
	st, err := h.c.State()
	if err != nil {
		return
	}
	for _, r := range st.Runs {
		if !r.Terminal() {
			h.c.Do("POST", "/api/run/stop", map[string]string{"id": r.ID}, nil)
		}
	}
	for i := 0; i < 60; i++ {
		st, err := h.c.State()
		if err != nil {
			return
		}
		running := false
		for _, r := range st.Runs {
			running = running || r.Status == core.StatusRunning
		}
		if !running {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *hive) launch() {
	h.t.Helper()
	cmd := exec.Command(cmp.Or(h.exe, bin), "daemon")
	cmd.Env = append(append(os.Environ(), "HIVE_HOME="+h.ws.Root, "HIVE_FORGE=local:"+h.forge.Dir), h.env...)
	cmd.Dir = h.ws.Root
	log, _ := os.OpenFile(filepath.Join(h.ws.Hive(), "daemon-test.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	cmd.Stdout, cmd.Stderr = log, log
	var endGroup func()
	if group != nil {
		endGroup = group(cmd)
	}
	if err := pinned(h.cpus, h.slot, cmd.Start); err != nil {
		h.t.Fatal(err)
	}
	log.Close() // the daemon has its own handle
	h.cmd = cmd
	if endGroup != nil {
		h.jobs = append(h.jobs, endGroup)
	}
	if contain != nil {
		h.jobs = append(h.jobs, contain(h.t, cmd.Process.Pid))
	}
	ended := make(chan struct{})
	h.ended = ended
	go func() { cmd.Wait(); close(ended) }()
	// A loaded Windows runner has taken over 30 seconds to start one.
	began := time.Now()
	for time.Since(began) < 2*time.Minute {
		time.Sleep(200 * time.Millisecond)
		if c, err := api.Dial(h.ws); err == nil && ours(c.Info, cmd.Process.Pid, began.Add(-time.Second)) && c.Ping() {
			h.c = c
			if d := time.Since(began); d > 10*time.Second {
				// The daemon's own account of a slow start is its log's last lines.
				raw, _ := os.ReadFile(filepath.Join(h.ws.Hive(), "daemon-test.log"))
				h.t.Logf("the daemon took %s to start; its log ends: %s", d.Round(time.Second), raw[max(0, len(raw)-1000):])
			}
			return
		}
	}
	raw, _ := os.ReadFile(filepath.Join(h.ws.Hive(), "daemon-test.log"))
	h.t.Fatalf("daemon didn't start in %s: %s", time.Since(began).Round(time.Second), raw)
}

// ours says the daemon that answers is the one just launched. The gate runs
// the binary in a sandbox of its own (HIVE_E2E_WRAPPED), where the daemon
// sees another pid, so there it is the start time instead.
func ours(info api.DaemonInfo, pid int, launched time.Time) bool {
	if os.Getenv("HIVE_E2E_WRAPPED") != "" {
		return !info.Started.Before(launched) // a coarse clock can stamp both at one instant
	}
	return info.PID == pid
}

// binVersion is what the binary under test says its version is: the gate
// stamps the one it builds, so it need not be this tree's core.Version.
func binVersion(t *testing.T) string {
	out, err := exec.Command(bin, "--version").Output()
	f := strings.Fields(string(out))
	if err != nil || len(f) == 0 {
		t.Fatalf("%s --version: %v %s", bin, err, out)
	}
	return f[len(f)-1]
}

// kill ends the daemon abruptly, as a crash would.
func (h *hive) kill() {
	if h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Kill()
		h.exited(30 * time.Second)
		h.left(10 * time.Second)
	}
}

// left ends what the killed daemon left working in its workspace (a
// module's job, such as kb's dream run with go run, and the git it commits
// with) and waits up to d for it to go, so the test's folder isn't removed
// while they still write into it. Linux only, from /proc.
func (h *hive) left(d time.Duration) {
	if runtime.GOOS != "linux" {
		return
	}
	root := h.ws.Root + string(filepath.Separator)
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		dirs, _ := filepath.Glob("/proc/[0-9]*/cwd")
		busy := false
		for _, l := range dirs {
			cwd, err := os.Readlink(l)
			if err != nil || !strings.HasPrefix(cwd+string(filepath.Separator), root) {
				continue
			}
			busy = true
			var pid int
			if _, err := fmt.Sscanf(filepath.Base(filepath.Dir(l)), "%d", &pid); err == nil && pid != os.Getpid() {
				if p, err := os.FindProcess(pid); err == nil {
					p.Kill()
				}
			}
		}
		if !busy {
			return
		}
	}
	h.t.Logf("processes still work in %s after %v", h.ws.Root, d)
}

// exited waits up to d for the daemon last launched to exit, so a new one
// isn't refused as a second daemon on the workspace (slow on Windows).
func (h *hive) exited(d time.Duration) {
	if h.ended == nil {
		return
	}
	select {
	case <-h.ended:
	case <-time.After(d):
		h.t.Logf("the daemon (pid %d) hasn't exited after %v", h.cmd.Process.Pid, d)
	}
}

func (h *hive) post(path string, body, out any) {
	h.t.Helper()
	if err := h.c.Do("POST", path, body, out); err != nil {
		h.t.Fatalf("POST %s: %v", path, err)
	}
}

func (h *hive) state() api.State {
	st, err := h.c.State()
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

// wait polls until cond holds, failing with what it last saw.
func (h *hive) wait(what string, timeout time.Duration, cond func(api.State) bool) api.State {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	var st api.State
	for time.Now().Before(deadline) {
		var err error
		st, err = h.c.State()
		if err == nil && cond(st) {
			return st
		}
		time.Sleep(500 * time.Millisecond)
	}
	raw, _ := json.MarshalIndent(st.Projects, "", " ")
	var runs []string
	for _, r := range st.Runs {
		errOut, _ := os.ReadFile(h.ws.TurnOut(r.ID, max(r.Turn, 1)) + ".err")
		runs = append(runs, fmt.Sprintf("%s %s turn %d: %s %s", r.Role, r.ID, r.Turn, r.Status, core.Clip(r.Reason+" "+string(errOut), 300)))
	}
	var reqs []string // a step that never settles leaves its asker unwoken
	for _, r := range st.Requests {
		reqs = append(reqs, fmt.Sprintf("%s from %s: %s %s %q", r.ID, r.Origin, r.State, core.Clip(r.Outcome, 200), core.Clip(r.Want, 80)))
	}
	h.t.Fatalf("timed out waiting for %s; runs:\n%s\nrequests:\n%s\nprojects: %s\nalerts: %+v", what, strings.Join(runs, "\n"), strings.Join(reqs, "\n"), core.Clip(string(raw), 4000), st.Alerts)
	return st
}

// task finds a task by exact title, else by the first title containing it.
func task(st api.State, project, title string) (work.Task, bool) {
	var partial *work.Task
	for _, p := range st.Projects {
		if p.Name != project {
			continue
		}
		for i, t := range p.Tasks {
			if t.Title == title {
				return t, true
			}
			if partial == nil && strings.Contains(t.Title, title) {
				partial = &p.Tasks[i]
			}
		}
	}
	if partial != nil {
		return *partial, true
	}
	return work.Task{}, false
}

// logLines puts a task's log one entry to a line, so a failure's annotation
// on ci (which cuts each line short) still shows every step.
func logLines(log []work.Entry) string {
	var b strings.Builder
	for _, e := range log {
		fmt.Fprintf(&b, "\n  %s %s", e.At.Format("15:04:05.000"), core.Clip(strings.ReplaceAll(e.Text, "\n", " "), 200))
	}
	return b.String()
}

func taskStage(project, title, stage string) func(api.State) bool {
	return func(st api.State) bool {
		t, ok := task(st, project, title)
		return ok && t.Stage == stage
	}
}

func (h *hive) messages(chatID string) []chat.Message {
	var msgs []chat.Message
	h.c.Do("GET", "/api/chat?id="+chatID, nil, &msgs)
	return msgs
}

func (h *hive) seed(repo string, files map[string]string) {
	h.t.Helper()
	if _, err := h.forge.EnsureRepo(repo, "", false); err != nil {
		h.t.Fatal(err)
	}
	tmp := h.t.TempDir()
	git := func(dir string, args ...string) {
		if _, err := core.Git(dir, args...); err != nil {
			h.t.Fatal(err)
		}
	}
	git("", "clone", "-q", h.forge.RepoPath(repo), tmp)
	for p, c := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(tmp, p)), 0o755)
		os.WriteFile(filepath.Join(tmp, p), []byte(c), 0o644)
	}
	git(tmp, "add", "-A")
	git(tmp, "commit", "-q", "-m", "seed")
	git(tmp, "push", "-q", "origin", "HEAD:main")
}

const minute = time.Minute

// M2 and M5: an ask in the lead's chat becomes an issue, a worker, a PR with
// tests and a review, and a pinned merge; the chat says it merged with a link.
// Telling the lead to create a project yields a repository, a chat and a
// dashboard section, and an ask in that chat lands there.
func TestAskLandsAndProjects(t *testing.T) {
	t.Parallel()
	h := start(t)
	var p core.Project
	h.post("/api/project", map[string]any{"demo": true}, &p)
	if p.Name != "textkit" {
		t.Fatalf("demo project: %+v", p)
	}
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "hello"}, nil)
	h.wait("the lead's reply", minute, func(api.State) bool {
		for _, m := range h.messages("lead") {
			if m.From != "person" && strings.Contains(m.Text, "lead") {
				return true
			}
		}
		return false
	})
	// The lead reads the person's own pushes: its next message syncs the clone.
	h.seed("local/textkit", map[string]string{"NOTES.md": "pushed by the person\n"})
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "hello again, I pushed some notes"}, nil)
	if !core.Exists(filepath.Join(h.ws.Project("textkit"), "NOTES.md")) {
		t.Fatal("the lead's view missed the person's push")
	}
	var sent chat.Message
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "implement capitalize_words with a test"}, &sent)
	st := h.wait("the ask to merge", 3*minute, taskStage("textkit", "capitalize_words", work.StageDone))
	tk, _ := task(st, "textkit", "capitalize_words")
	if tk.Issue.Number == 0 || tk.PR.Number == 0 || tk.MergeSHA == "" {
		t.Fatalf("task: %+v", tk)
	}
	prs := h.forge.PRs("local/textkit")
	if len(prs) != 1 || prs[0].State != "MERGED" || h.forge.Issues("local/textkit") != 1 {
		t.Fatalf("forge: prs %+v issues %d", prs, h.forge.Issues("local/textkit"))
	}
	reviewed := false
	for _, c := range h.forge.Comments("local/textkit", tk.PR.Number) {
		if strings.Contains(c, "Review of") && strings.Contains(c, "land") {
			reviewed = true
		}
	}
	if !reviewed {
		t.Fatalf("no review on the PR: %v", h.forge.Comments("local/textkit", tk.PR.Number))
	}
	merged := false
	for _, m := range h.messages("lead") {
		if m.Thread == sent.Thread && strings.Contains(m.Text, "Merged:") && strings.Contains(m.Text, tk.PR.URL) {
			merged = true
		}
	}
	if !merged {
		t.Fatalf("the chat didn't say it merged with a link: %+v", h.messages("lead"))
	}
	h.wait("the merge to be measured", minute, func(st api.State) bool {
		for _, pv := range st.Projects {
			for _, s := range pv.Scores {
				if s.SHA == tk.MergeSHA && s.Kept {
					return true
				}
			}
		}
		return false
	})

	// M5: projects.
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "create project alpha for experiments"}, nil)
	h.wait("project alpha", 2*minute, func(st api.State) bool {
		for _, c := range st.Chats {
			if c.ID == "project:alpha" {
				return true
			}
		}
		return false
	})
	h.post("/api/chat", map[string]string{"chat": "project:alpha", "text": "add a first feature"}, nil)
	h.wait("alpha's ask to merge", 3*minute, taskStage("alpha", "first feature", work.StageDone))
	if prs := h.forge.PRs("local/alpha"); len(prs) != 1 || prs[0].State != "MERGED" {
		t.Fatalf("alpha prs: %+v", prs)
	}
	// A live project lead sees the person's own pushes too, and so does a live
	// isolated project's lead, which reads a checkout of its own.
	h.seed("local/alpha", map[string]string{"OUTSIDE.md": "pushed by the person\n"})
	h.post("/api/chat", map[string]string{"chat": "project:alpha", "text": "what's in the repo now?"}, nil)
	h.wait("alpha's lead to see the push", 2*minute, h.replied("project:alpha", "OUTSIDE.md"))
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "create isolated project beta for secrets"}, nil)
	h.wait("project beta", 2*minute, func(st api.State) bool {
		for _, c := range st.Chats {
			if c.ID == "project:beta" {
				return true
			}
		}
		return false
	})
	h.post("/api/chat", map[string]string{"chat": "project:beta", "text": "what's in the repo?"}, nil)
	h.wait("beta's lead to answer", 2*minute, h.replied("project:beta", "The repo has"))
	h.seed("local/beta", map[string]string{"OUTSIDE.md": "pushed by the person\n"})
	h.post("/api/chat", map[string]string{"chat": "project:beta", "text": "what's in the repo now?"}, nil)
	h.wait("beta's live lead to see the push", 2*minute, h.replied("project:beta", "OUTSIDE.md"))
}

// replied is true once someone other than the person has said text in a chat.
func (h *hive) replied(chat, text string) func(api.State) bool {
	return func(api.State) bool {
		for _, m := range h.messages(chat) {
			if m.From != "person" && strings.Contains(m.Text, text) {
				return true
			}
		}
		return false
	}
}

// A lead keeps a journal every agent can read, and its context size shows on
// its run. A lead that replaces it answers from that journal, and the person
// sees one chat.
func TestLeadJournal(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "remember the launch is on Friday"}, nil)
	st := h.wait("the lead to note it", minute, h.replied("lead", "Noted."))
	var first core.Run
	for _, r := range st.Runs {
		if r.Role == "lead" {
			first = r
		}
	}
	if first.Context == 0 {
		t.Fatalf("no context figure on the lead: %+v", first)
	}
	h.post("/api/run/stop", map[string]string{"id": first.ID}, nil)
	h.wait("the lead to stop", minute, func(st api.State) bool {
		for _, r := range st.Runs {
			if r.ID == first.ID {
				return r.Terminal()
			}
		}
		return false
	})
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "what did I ask you to remember?"}, nil)
	st = h.wait("the new lead to answer from the journal", minute, h.replied("lead", "the launch is on Friday"))
	leads := 0
	for _, r := range st.Runs {
		if r.Role == "lead" {
			leads++
		}
	}
	var thread []string
	for _, m := range h.messages("lead") {
		thread = append(thread, m.From+": "+m.Text)
	}
	if leads != 2 || len(thread) < 4 || !strings.HasPrefix(thread[0], "person: remember") || !strings.Contains(thread[len(thread)-1], "Friday") {
		t.Fatalf("%d leads; chat:\n%s", leads, strings.Join(thread, "\n"))
	}
}

// M3 and M4: failing tests never merge; a merge that lowers the score is
// reverted by a merged PR with a reason, a line lands in the lessons file, and
// the next worker's brief quotes it; a notice run files an ask with evidence.
func TestGateAndLoop(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/gate", map[string]string{
		".hive/project.json": `{"test": "test ! -f BROKEN", "score": {"command": "cat score.txt", "direction": "higher", "tolerance": 0}}`,
		".hive/goals.md":     "# Goals\n\nKeep the score high.\n",
		".hive/lessons.md":   "# Lessons\n\n",
		"score.txt":          "5\n",
	})
	h.post("/api/project", map[string]any{"name": "gate", "repo": "me/gate"}, nil)
	h.wait("the baseline", minute, func(st api.State) bool {
		return len(st.Projects) == 1 && st.Projects[0].Best != nil && *st.Projects[0].Best == 5
	})

	h.post("/api/ask", map[string]string{"project": "gate", "title": "break the build", "known": "FAKE:breaktests"}, nil)
	st := h.wait("the failing change to stop", 4*minute, taskStage("gate", "break the build", work.StageStopped))
	tk, _ := task(st, "gate", "break the build")
	pr, _ := h.forge.GetPR("me/gate", tk.PR.Number)
	if pr.State != "OPEN" || tk.Round != 3 {
		t.Fatalf("a failing change must stop after 3 rounds with its PR open: round %d, pr %+v", tk.Round, pr)
	}

	h.post("/api/ask", map[string]string{"project": "gate", "title": "lower the score", "known": "FAKE:lowerscore"}, nil)
	h.wait("the score-lowering change to merge", 3*minute, taskStage("gate", "lower the score", work.StageDone))
	st = h.wait("the revert to merge", 3*minute, taskStage("gate", "Revert: lower the score", work.StageDone))
	orig, _ := task(st, "gate", "lower the score")
	rv, _ := task(st, "gate", "Revert: lower the score")
	if !strings.Contains(rv.Lesson, "fell to 1") {
		t.Fatalf("revert reason: %q", rv.Lesson)
	}
	commented := false
	for _, c := range h.forge.Comments("me/gate", orig.PR.Number) {
		commented = commented || strings.Contains(c, "reverted")
	}
	if !commented {
		t.Fatal("the original PR has no comment saying why it was reverted")
	}
	// The task shows done once the forge merged; the clone's origin/main may
	// not have fetched that yet (seen on Windows), so fetch until it has.
	var lessons string
	h.wait("the clone to fetch the revert", minute, func(api.State) bool {
		core.Git(h.ws.Project("gate"), "fetch", "-q", "origin")
		lessons, _ = core.Git(h.ws.Project("gate"), "show", "origin/main:.hive/lessons.md")
		return strings.Contains(lessons, "was reverted")
	})
	if !strings.Contains(lessons, "was reverted") {
		t.Fatalf("lessons on main: %q", lessons)
	}
	if score, _ := core.Git(h.ws.Project("gate"), "show", "origin/main:score.txt"); strings.TrimSpace(score) != "5" {
		t.Fatalf("score after revert: %q", score)
	}
	h.post("/api/ask", map[string]string{"project": "gate", "title": "raise it next", "known": "try something else"}, nil)
	st = h.wait("the next worker", minute, func(st api.State) bool {
		t, ok := task(st, "gate", "raise it next")
		return ok && t.Worker != ""
	})
	next, _ := task(st, "gate", "raise it next")
	brief, _ := os.ReadFile(filepath.Join(h.ws.RunRO(next.Worker), "brief.md"))
	if !strings.Contains(string(brief), "was reverted") {
		t.Fatalf("the next brief doesn't quote the lesson:\n%s", brief)
	}

	var sw map[string]any
	h.post("/api/improve", map[string]string{}, &sw)
	h.wait("the sweep to file an ask", 2*minute, func(st api.State) bool {
		_, ok := task(st, "gate", "FAKE sweep")
		return ok && len(st.Sweeps) > 0 && st.Sweeps[0].Status == core.StatusDone && len(st.Sweeps[0].Asks) == 1
	})
}

// On a scored project each commit a running worker makes that beats the best
// kept score merges as a checkpoint while it keeps going: the fake worker
// commits 7, then 9, in one turn and waits on each merge before it goes on,
// and its finish lands on top of both.
func TestCheckpointsBankEachGain(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/bank", map[string]string{
		".hive/project.json": `{"test": "test ! -f BROKEN", "score": {"command": "cat score.txt", "direction": "higher", "tolerance": 0}}`,
		".hive/goals.md":     "# Goals\n\nRaise the score as far as it goes.\n",
		"score.txt":          "5\n",
	})
	h.post("/api/project", map[string]any{"name": "bank", "repo": "me/bank"}, nil)
	h.wait("the baseline", minute, func(st api.State) bool {
		return len(st.Projects) == 1 && st.Projects[0].Best != nil && *st.Projects[0].Best == 5
	})
	h.post("/api/ask", map[string]string{"project": "bank", "title": "raise the score", "known": "FAKE:checkpoints"}, nil)
	st := h.wait("both checkpoints and the finish to merge", 8*minute, taskStage("bank", "raise the score", work.StageDone))
	tk, _ := task(st, "bank", "raise the score")
	if tk.Banked != 2 {
		t.Fatalf("banked %d:%s", tk.Banked, logLines(tk.Log))
	}
	// main's score at each merge, and whether the worker had finished by then.
	var merges []string
	finished := false
	raw, _ := os.ReadFile(filepath.Join(h.ws.Hive(), "events.jsonl"))
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e core.Event
		var d struct {
			ID       string
			MergeSHA string `json:"merge_sha"`
		}
		if json.Unmarshal([]byte(l), &e) != nil || e.Decode(&d) != nil {
			continue
		}
		finished = finished || e.Kind == "run.finish" && d.ID == tk.Worker
		if e.Kind == "effect.merged" {
			// The task shows done once the forge merged; the clone may not
			// have fetched the last merge yet (seen on Windows), so fetch until it has.
			var s string
			h.wait("the clone to fetch "+d.MergeSHA, minute, func(api.State) bool {
				var err error
				if s, err = core.Git(h.ws.Project("bank"), "show", d.MergeSHA+":score.txt"); err != nil {
					core.Git(h.ws.Project("bank"), "fetch", "-q", "origin")
				}
				return err == nil
			})
			merges = append(merges, fmt.Sprintf("%s/%v", strings.TrimSpace(s), finished))
		}
	}
	if strings.Join(merges, " ") != "7/false 9/false 9/true" {
		t.Fatalf("main's score at each merge/worker finished: %v", merges)
	}
}

// A review reads the project's docs/REVIEW.md from main and is pointed at its
// goals and lessons, and advice given with a land stays on the pull request: it
// holds nothing and is filed as no new task.
func TestReviewReadsTheRules(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/rules", map[string]string{
		".hive/project.json": `{"test": "true"}`,
		".hive/goals.md":     "# Goals\n\nEvery agent sees the same thing.\n",
		"docs/REVIEW.md":     "# Review\n\nThe sandbox walls the Hive off from the host. FAKE:reviewadvice\n",
		".hive/lessons.md":   "# Lessons\n\n- a lesson from an earlier revert\n",
	})
	h.post("/api/project", map[string]any{"name": "rules", "repo": "me/rules"}, nil)
	h.post("/api/ask", map[string]string{"project": "rules", "title": "add a helper"}, nil)
	st := h.wait("the ask to merge", 3*minute, taskStage("rules", "add a helper", work.StageDone))
	tk, _ := task(st, "rules", "add a helper")
	brief, _ := os.ReadFile(filepath.Join(h.ws.RunRO(tk.Reviewer), "brief.md"))
	for _, want := range []string{"walls the Hive off from the host", "`.hive/goals.md` and `.hive/lessons.md` are in your checkout"} {
		if !strings.Contains(string(brief), want) {
			t.Fatalf("the review brief lacks %q:\n%s", want, brief)
		}
	}
	if c := strings.Join(h.forge.Comments("me/rules", tk.PR.Number), "\n"); !strings.Contains(c, "Advice: FAKE advice") {
		t.Fatalf("the advice isn't on the PR:\n%s", c)
	}
	time.Sleep(3 * time.Second) // a few more ticks, and nothing is filed
	for _, p := range h.state().Projects {
		for _, x := range p.Tasks {
			if x.From == tk.Reviewer {
				t.Fatalf("the reviewer's advice was filed as a task: %+v", x)
			}
		}
	}
}

// A person's hold on merges stops a change at the last step: it passes its
// gate and waits as a ready PR, and merges once the hold names it.
func TestHoldStopsMerges(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/held", map[string]string{
		".hive/project.json": `{"test": "true"}`,
		".hive/goals.md":     "# Goals\n",
		".hive/lessons.md":   "# Lessons\n\n",
	})
	h.post("/api/project", map[string]any{"name": "held", "repo": "me/held"}, nil)
	h.post("/api/hold", map[string]any{"project": "held", "why": "nothing merges until the next release"}, nil)
	h.post("/api/ask", map[string]string{"project": "held", "title": "add a helper"}, nil)
	held := func(st api.State) bool {
		tk, ok := task(st, "held", "add a helper")
		return ok && tk.Stage == work.StageMerging && strings.Contains(tk.Reason, "merges held")
	}
	h.wait("the ask to wait on the hold", 3*minute, held)
	time.Sleep(3 * time.Second) // a few more ticks, and it still waits
	st := h.state()
	for _, p := range st.Projects {
		if p.Name == "held" && (p.Hold == nil || !strings.Contains(p.Hold.Text(), "until the next release")) {
			t.Fatalf("the state doesn't show the hold: %+v", p.Hold)
		}
	}
	tk, _ := task(st, "held", "add a helper")
	if tk.Stage != work.StageMerging || tk.MergeSHA != "" {
		t.Fatalf("the ask moved under the hold: %s %s", tk.Stage, tk.MergeSHA)
	}
	h.post("/api/hold", map[string]any{"project": "held", "why": "only the helper may merge", "only": []int{tk.PR.Number}}, nil)
	h.wait("the named PR to merge under the hold", 3*minute, taskStage("held", "add a helper", work.StageDone))
}

// A worker isn't blamed for a failure main has too: its change waits with no
// round spent, main's failure is one alert, and once main is fixed the change
// is brought up to date and merges.
func TestMainRedDoesNotBlameWorker(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/red", map[string]string{
		".hive/project.json": `{"test": "grep -q green STATE"}`,
		".hive/goals.md":     "# Goals\n",
		".hive/lessons.md":   "# Lessons\n\n",
		"STATE":              "red\n",
	})
	h.post("/api/project", map[string]any{"name": "red", "repo": "me/red"}, nil)
	h.post("/api/ask", map[string]string{"project": "red", "title": "add a feature", "known": "a plain change"}, nil)
	st := h.wait("the change to wait on main", 4*minute, func(st api.State) bool {
		tk, ok := task(st, "red", "add a feature")
		return ok && strings.HasPrefix(tk.Reason, "waiting on main")
	})
	tk, _ := task(st, "red", "add a feature")
	alerts := 0
	for _, a := range st.Alerts {
		if a.Kind == "main-red" {
			alerts++
		}
	}
	if tk.Round != 0 || alerts != 1 {
		t.Fatalf("round %d, %d main-red alerts; want 0 and 1", tk.Round, alerts)
	}
	h.seed("me/red", map[string]string{"STATE": "green\n"})
	h.wait("the change to merge once main is green", 4*minute, taskStage("red", "add a feature", work.StageDone))
}

// M7: kill the Hive in the middle of an ask, restart, and get the same
// outcome with no duplicate issue, run or PR. Usage limits park and resume;
// a crashed turn is retried on its checkout.
func TestRecovery(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/rec", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "rec", "repo": "me/rec"}, nil)

	for _, stage := range []string{work.StageWorking, work.StageTesting, work.StageReviewing, work.StageMerging} {
		title := "survive " + stage
		h.post("/api/ask", map[string]string{"project": "rec", "title": title}, nil)
		h.wait(title+" to reach "+stage, 2*minute, func(st api.State) bool {
			t, ok := task(st, "rec", title)
			return ok && (t.Stage == stage || t.Terminal())
		})
		h.kill()
		h.launch()
		h.wait(title+" to finish after a restart", 3*minute, taskStage("rec", title, work.StageDone))
	}
	st := h.state()
	issues, prs := h.forge.Issues("me/rec"), h.forge.PRs("me/rec")
	if issues != 4 || len(prs) != 4 {
		t.Fatalf("duplicates after restarts: %d issues, %d PRs", issues, len(prs))
	}
	for _, p := range prs {
		if p.State != "MERGED" {
			t.Fatalf("pr not merged: %+v", p)
		}
	}
	workers := map[string]int{}
	for _, r := range st.Runs {
		if r.Role == "worker" {
			workers[r.Task]++
		}
	}
	for tid, n := range workers {
		if n != 1 {
			t.Fatalf("task %s has %d workers", tid, n)
		}
	}

	h.post("/api/ask", map[string]string{"project": "rec", "title": "hit the limit", "known": "FAKE:limit"}, nil)
	h.wait("the run to park on the usage limit", minute, func(st api.State) bool {
		tk, ok := task(st, "rec", "hit the limit")
		if !ok {
			return false
		}
		for _, r := range st.Runs {
			if r.ID == tk.Worker && r.Status == core.StatusParked && strings.Contains(r.Reason, "usage limit") {
				return true
			}
		}
		return false
	})
	h.wait("the limited run to resume and land", 2*minute, taskStage("rec", "hit the limit", work.StageDone))

	h.post("/api/ask", map[string]string{"project": "rec", "title": "crash once", "known": "FAKE:crash"}, nil)
	h.wait("the crashed run to be retried and land", 3*minute, taskStage("rec", "crash once", work.StageDone))
}

// toldAbout waits for a told in the feed whose title names what was decided.
func toldAbout(what string) func(api.State) bool {
	return func(st api.State) bool {
		for _, a := range st.Told {
			if strings.Contains(a.Title, what) {
				return true
			}
		}
		return false
	}
}

// The Hive notices its own stalls. A worker, a reviewer and a check that stop
// moving for longer than their stage takes in this Hive are restarted or
// handed to the lead, each with one alert that updates when it moves on; a
// worker slower than the floor but within this Hive's history is left alone.
func TestStalls(t *testing.T) {
	t.Parallel()
	h := start(t)
	// The history is built under a floor no history turn reaches, even on a
	// slow runner: one the watcher acted on isn't history, and without it
	// the limit falls to the floor and flags the slow worker.
	cfg := h.ws.LoadConfig()
	cfg.StallFloorSeconds = 120
	h.ws.SaveConfig(cfg)
	h.seed("me/stall", map[string]string{".hive/project.json": `{"test": "if [ -f STALL ]; then sleep 60; fi"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "stall", "repo": "me/stall"}, nil)
	// History: a worker here has taken about 13 seconds (11 asleep; the loop
	// ticks soon after each step, so little else), so the limit is about 39
	// without moving, well over the slow worker's 22 on a slow runner.
	for title, known := range map[string]string{"history one": "FAKE:slowworker:11", "history two": ""} {
		h.post("/api/ask", map[string]string{"project": "stall", "title": title, "known": known}, nil)
		h.wait(title+" to land", 3*minute, taskStage("stall", title, work.StageDone))
	}
	cfg = h.ws.LoadConfig()
	cfg.StallFloorSeconds = 20
	h.ws.SaveConfig(cfg)

	asks := map[string]string{"hung worker": "FAKE:stallworker", "hung reviewer": "FAKE:stallreviewer", "hung check": "FAKE:stallcheck", "slow worker": "FAKE:slowworker:22"}
	for title, known := range asks {
		h.post("/api/ask", map[string]string{"project": "stall", "title": title, "known": known}, nil)
	}
	for title := range asks {
		h.wait(title+" to land", 4*minute, taskStage("stall", title, work.StageDone))
	}
	hung := map[string]string{"hung worker": "restarted its turn", "hung reviewer": "restarted its turn", "hung check": "handed it to the lead"}
	stalls := func(st api.State) map[string][]string {
		alerts := map[string][]string{}
		for _, a := range st.Alerts {
			if a.Kind == "stall" {
				alerts[a.Ref] = append(alerts[a.Ref], a.Text)
			}
		}
		return alerts
	}
	// A task can land before the watcher's next pass says it moved on.
	st := h.wait("every stall alert to say it moved on", minute, func(st api.State) bool {
		for title := range hung {
			tk, _ := task(st, "stall", title)
			if got := stalls(st)["task:"+tk.ID]; len(got) == 0 || !strings.Contains(got[len(got)-1], "moved on") {
				return false
			}
		}
		return true
	})
	alerts := stalls(st)
	for title, did := range hung {
		tk, _ := task(st, "stall", title)
		if got := alerts["task:"+tk.ID]; len(got) != 1 || !strings.Contains(got[0], did) || !strings.Contains(got[0], "moved on") {
			t.Fatalf("%s: want one alert that says %q and that it moved on, got %q", title, did, got)
		}
		workers := 0
		for _, r := range st.Runs {
			if r.Role == "worker" && r.Task == tk.ID {
				workers++
			}
		}
		if workers != 1 {
			t.Fatalf("%s: a restart started %d workers", title, workers)
		}
	}
	if slow, _ := task(st, "stall", "slow worker"); len(alerts["task:"+slow.ID]) != 0 {
		t.Fatalf("a slow worker within this Hive's history was flagged: %q", alerts["task:"+slow.ID])
	}
	told := false
	for _, msg := range h.messages("lead") {
		told = told || strings.Contains(msg.Text, "Stuck:") && strings.Contains(msg.Text, "hung check")
	}
	if !told {
		t.Fatal("the lead was not told about the hung check")
	}
}

// A request past the sandbox goes to a warden, which runs its script, and the
// outcome reaches the agent. At autonomy 2 a warden approves a module install
// and a goals change, and the person is told after. A module updates when its
// source releases. The Hive releases itself after a kept merge.
func TestPersonModulesRelease(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/self", map[string]string{".hive/project.json": `{"test": "true", "dryrun": "true"}`, ".hive/goals.md": "# Goals\n", scan.PublicList: "**\n"})
	h.post("/api/project", map[string]any{"name": "self", "repo": "me/self", "self": true}, nil)

	h.post("/api/ask", map[string]string{"project": "self", "title": "needs a package", "known": "FAKE:needpackage"}, nil)
	h.wait("the worker to continue and land", 3*minute, taskStage("self", "needs a package", work.StageDone))
	h.wait("a release after the kept merge", 2*minute, func(api.State) bool {
		rel, err := h.forge.LatestRelease("me/self")
		return err == nil && rel.Tag == "v0.0.1"
	})

	src := filepath.Join(t.TempDir(), "tools-module")
	git := func(dir string, args ...string) {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(src, 0o755)
	git(src, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(src, "module.json"), []byte(`{"name":"tools","description":"helper scripts"}`), 0o644)
	git(src, "add", "-A")
	git(src, "commit", "-q", "-m", "v1")
	git(src, "tag", "v1.0.0")
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "install module " + src}, nil)
	module := func(st api.State) (api.ModuleView, bool) {
		for _, m := range st.Modules {
			if m.Name == "tools" {
				return m, true
			}
		}
		return api.ModuleView{}, false
	}
	h.wait("a warden to install the module at v1.0.0", 2*minute, func(st api.State) bool { m, ok := module(st); return ok && m.Release == "v1.0.0" })
	h.wait("the person to be told after", minute, toldAbout("Install module tools"))
	h.post("/api/ask", map[string]string{"project": "self", "title": "write down a goal", "known": "FAKE:goals"}, nil)
	h.wait("a warden to approve the goals change and it to merge", 3*minute, taskStage("self", "write down a goal", work.StageDone))
	h.wait("the person to be told after", minute, toldAbout("write down a goal"))
	os.WriteFile(filepath.Join(src, "tool.sh"), []byte("echo hi\n"), 0o644)
	git(src, "add", "-A")
	git(src, "commit", "-q", "-m", "v1.1")
	git(src, "tag", "v1.1.0")
	h.post("/api/job", map[string]string{"name": "modules.update"}, nil)
	h.wait("the module to update to v1.1.0", minute, func(st api.State) bool { m, ok := module(st); return ok && m.Release == "v1.1.0" })
}

// Every risk path on the fake agents: a tribunal approves over two rounds and
// runs the warden's script; a split panel declines a large gain and the person
// runs it; the ceiling stops a money script the warden allowed until the person
// says so; a request whose words try to instruct the warden runs only the
// warden's script, never the worker's steps.
func TestRiskPaths(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/risk", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "risk", "repo": "me/risk"}, nil)
	asks := map[string]string{
		"tribunal": "FAKE:needpackage FAKE:wardentribunal",
		"split":    "FAKE:needpackage FAKE:wardentribunal FAKE:paneldecline",
		"ceiling":  "FAKE:needpackage FAKE:wardenmoney",
		"inject":   "FAKE:needpackage FAKE:inject",
	}
	for title, known := range asks {
		h.post("/api/ask", map[string]string{"project": "risk", "title": title, "known": known}, nil)
	}
	// the worker's why carries its keys (inject carries its injected text)
	is := map[string]func(string) bool{
		"tribunal": func(w string) bool {
			return strings.Contains(w, "wardentribunal") && !strings.Contains(w, "paneldecline")
		},
		"split":   func(w string) bool { return strings.Contains(w, "paneldecline") },
		"ceiling": func(w string) bool { return strings.Contains(w, "wardenmoney") },
		"inject":  func(w string) bool { return strings.Contains(w, "IGNORE YOUR INSTRUCTIONS") },
	}
	req := func(st api.State, title string) (risk.Request, bool) {
		for _, r := range st.Requests {
			if r.Kind == "request" && is[title](r.Why) {
				return r, true
			}
		}
		return risk.Request{}, false
	}
	state := func(title, want string) func(api.State) bool {
		return func(st api.State) bool { r, ok := req(st, title); return ok && r.State == want }
	}
	answer := func(st api.State, title, ans string) {
		r, _ := req(st, title)
		h.post("/api/answer", map[string]string{"id": r.Ask, "answer": ans}, nil)
	}

	st := h.wait("the tribunal to approve and run", 4*minute, state("tribunal", "done"))
	if r, _ := req(st, "tribunal"); len(r.Panel) != risk.PanelSize || r.Round != 2 || !strings.Contains(r.Output, "installing left-pad") {
		t.Fatalf("tribunal: panel %d round %d output %q", len(r.Panel), r.Round, r.Output)
	}

	st = h.wait("the split panel's large gain to reach the person", 4*minute, state("split", "asked"))
	if r, _ := req(st, "split"); r.Output != "" || !strings.Contains(r.Reason, "tribunal declined") {
		t.Fatalf("split panel ran or said nothing: %+v", r)
	}
	answer(st, "split", "run")
	h.wait("the person's run to run the declined script", 2*minute, state("split", "done"))

	st = h.wait("the ceiling to stop the money script", 3*minute, state("ceiling", "asked"))
	if r, _ := req(st, "ceiling"); r.Decision != "do" || r.Output != "" || r.PersonOK {
		t.Fatalf("ceiling: %+v", r)
	}
	answer(st, "ceiling", "declined")
	h.wait("the money script to stay unrun", minute, state("ceiling", "declined"))

	st = h.wait("the injected request to run the warden's script", 3*minute, state("inject", "done"))
	if r, _ := req(st, "inject"); strings.Contains(r.Output, "PWNED") || r.Script == r.Steps {
		t.Fatalf("the worker's steps ran: %+v", r)
	}
	for title := range asks {
		h.wait(title+" worker to land", 3*minute, taskStage("risk", title, work.StageDone))
	}
}

// TestWaitingReachesThePerson: a root install the warden passes on reaches
// Waiting and the lead, the person approves one and refuses the other from a
// shell, and each worker hears the answer: the approved one lands, the
// refused one stops.
func TestWaitingReachesThePerson(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/wait", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "wait", "repo": "me/wait"}, nil)
	for _, title := range []string{"yes", "no"} {
		h.post("/api/ask", map[string]string{"project": "wait", "title": title, "known": "FAKE:needpackage FAKE:wardenperson" + title}, nil)
	}
	req := func(st api.State, title string) (risk.Request, core.Ask, bool) {
		for _, r := range st.Requests {
			if strings.Contains(r.Why, "wardenperson"+title) {
				for _, a := range st.Asks {
					if a.ID == r.Ask {
						return r, a, r.State == "asked"
					}
				}
			}
		}
		return risk.Request{}, core.Ask{}, false
	}
	st := h.wait("both installs to wait on the person", 3*minute, func(st api.State) bool {
		_, _, y := req(st, "yes")
		_, _, n := req(st, "no")
		return y && n
	})
	_, yes, _ := req(st, "yes")
	_, no, _ := req(st, "no")
	if strings.Join(yes.Options, "|") != "run|declined" || !strings.Contains(yes.Detail, "install the left-pad package") || !strings.Contains(yes.Detail, "echo apt-get install -y left-pad") {
		t.Fatalf("Waiting doesn't say what is asked: %+v", yes)
	}
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "Can I approve the worker installing it?"}, nil)
	r, _, _ := req(st, "yes")
	since, _ := json.Marshal(r.Updated)
	h.wait("the lead to see where the request stands, since when, and how to answer", 2*minute, func(st api.State) bool {
		return h.replied("lead", `"State":"asked","Warden":"`+r.Warden+`"`)(st) && h.replied("lead", `"Since":`+string(since))(st) && h.replied("lead", "approve "+yes.ID)(st)
	})
	for verb, a := range map[string]core.Ask{"approve": yes, "refuse": no} {
		cmd := exec.Command(bin, verb, a.ID)
		cmd.Env = append(os.Environ(), "HIVE_HOME="+h.ws.Root, "HIVE_FORGE=local:"+h.forge.Dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hive %s: %v %s", verb, err, out)
		}
	}
	st = h.wait("the approved install to run", 2*minute, func(st api.State) bool {
		for _, r := range st.Requests {
			if r.Ask == yes.ID {
				return r.State == "done"
			}
		}
		return false
	})
	h.wait("the approved worker to land", 3*minute, taskStage("wait", "yes", work.StageDone))
	h.wait("the refused worker to stop", 3*minute, taskStage("wait", "no", work.StageBlocked))
}

// TestClosedPRStops: a pull request the person closes on the forge while its
// task waits for them ends the task, even once its merge is approved, so the
// dashboard never lists it as merging and main never takes it.
func TestClosedPRStops(t *testing.T) {
	t.Parallel()
	// At autonomy 1 the merge waits on the person, not on a fake warden that
	// answers in a fraction of a second, so the test sees it waiting.
	h := startWith(t, bin, func(c *core.Config) { c.Autonomy = 1 })
	h.seed("me/closed", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "closed", "repo": "me/closed"}, nil)
	h.post("/api/ask", map[string]string{"project": "closed", "title": "edit the goals", "known": "FAKE:goals"}, nil)
	st := h.wait("the goals change to wait for the person", 3*minute, func(st api.State) bool {
		tk, ok := task(st, "closed", "edit the goals")
		return ok && tk.Stage == work.StagePerson && tk.PR.Number != 0
	})
	tk, _ := task(st, "closed", "edit the goals")
	if err := h.forge.ClosePR("me/closed", tk.PR.Number, "not wanted"); err != nil {
		t.Fatal(err)
	}
	for _, a := range st.Asks {
		if a.Kind == "merge" && strings.Contains(a.Title, "edit the goals") {
			h.post("/api/answer", map[string]string{"id": a.ID, "answer": "merge"}, nil)
		}
	}
	st = h.wait("the closed PR's task to stop as the person's", 2*minute, func(st api.State) bool {
		tk, ok := task(st, "closed", "edit the goals")
		return ok && tk.Stage == work.StageStopped && tk.ByPerson
	})
	for _, p := range h.forge.PRs("me/closed") {
		if p.State == "MERGED" {
			t.Fatalf("a closed PR's change merged: %+v", p)
		}
	}
	for i := 0; i < 3; i++ { // it stays stopped across ticks
		time.Sleep(2 * time.Second)
		if tk, _ := task(h.state(), "closed", "edit the goals"); tk.Stage != work.StageStopped {
			t.Fatalf("the stopped task came back as %q", tk.Stage)
		}
	}
}

// TestBlockedWorkComesBack replays the first real run: each worker's tests fail
// on a missing tool, it asks a warden and finishes blocked at once. Where the
// warden installs the tool, its answer wakes the worker and the PR merges;
// where it declines and the person installs the tool, the lead re-runs the check.
func TestBlockedWorkComesBack(t *testing.T) {
	t.Parallel()
	h := start(t)
	// the sandboxed check sees system dirs like /var, not the test's temp dir
	dir, err := os.MkdirTemp("/var/tmp", "hive-tool-")
	if err != nil {
		dir = t.TempDir()
	} else {
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	for _, name := range []string{"tool", "rerun"} {
		marker := filepath.Join(dir, name)
		// main is green; only the worker's change (under work/) needs the tool
		h.seed("me/"+name, map[string]string{".hive/project.json": `{"test": "test -f ` + filepath.ToSlash(marker) + ` || test ! -d work"}`, ".hive/goals.md": "# Goals\n"})
		h.post("/api/project", map[string]any{"name": name, "repo": "me/" + name}, nil)
		known := "FAKE:needtool " + filepath.ToSlash(marker)
		if name == "rerun" {
			known += " FAKE:wardendecline"
		}
		h.post("/api/ask", map[string]string{"project": name, "title": name, "known": known}, nil)
	}
	h.wait("the warden's answer to wake the worker and its PR to merge", 4*minute, taskStage("tool", "tool", work.StageDone))
	st := h.wait("the declined worker to stay blocked", 4*minute, func(st api.State) bool {
		t, ok := task(st, "rerun", "rerun")
		return ok && t.Stage == work.StageBlocked && strings.Contains(t.Reason, "declined") && t.PR.Number != 0
	})
	rerun, _ := task(st, "rerun", "rerun")
	os.WriteFile(filepath.Join(dir, "rerun"), nil, 0o644) // the person installs the tool
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "recheck " + rerun.ID}, nil)
	h.wait("the lead's re-run to pass and merge the PR", 4*minute, taskStage("rerun", "rerun", work.StageDone))
}

// TestStoppedWorkComesBack: a change whose tests keep failing stops after its
// fix rounds and goes to the lead, not the person; the lead sends it back with
// what remains and it merges.
func TestStoppedWorkComesBack(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/stop", map[string]string{".hive/project.json": `{"test": "test ! -f BROKEN"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "stop", "repo": "me/stop"}, nil)
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "hello"}, nil) // the lead is up to be told
	title := "break the build FAKE:leadfixes"
	h.post("/api/ask", map[string]string{"project": "stop", "title": title, "known": "FAKE:breaktests"}, nil)
	st := h.wait("the lead's call to bring it back past the fix rounds and merge it", 6*minute, taskStage("stop", title, work.StageDone))
	tk, _ := task(st, "stop", title)
	stopped := false
	for _, e := range tk.Log {
		stopped = stopped || strings.Contains(e.Text, "stopped after 3 rounds")
	}
	if !stopped {
		t.Fatalf("it never stopped after its rounds: %+v", tk.Log)
	}
	for _, msg := range h.messages("lead") {
		if strings.Contains(msg.Text, "Stopped") {
			t.Fatalf("the person was told of work the lead owns: %q", msg.Text)
		}
	}
}

// TestWardenOutlivesARestart: a warden mid-turn when the Hive restarts still
// decides, and once its process ends it leaves the live list. Here the test
// process adopts the orphaned turn and never reaps it, as a container's PID 1
// may not, so the ended turn lingers as a zombie, which is not alive.
func TestWardenOutlivesARestart(t *testing.T) {
	if adopt == nil {
		t.Skip("a child subreaper is Linux's")
	}
	adopt()
	h := start(t)
	h.seed("me/restart", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "restart", "repo": "me/restart"}, nil)
	h.post("/api/ask", map[string]string{"project": "restart", "title": "install", "known": "FAKE:needpackage FAKE:wardenslow"}, nil)
	warden := func(st api.State) (core.Run, bool) {
		for _, r := range st.Runs {
			if r.Role == "warden" {
				return r, true
			}
		}
		return core.Run{}, false
	}
	h.wait("the warden's first turn", 2*minute, func(st api.State) bool { w, ok := warden(st); return ok && w.Status == core.StatusRunning })
	h.kill()
	h.launch()
	st := h.wait("the warden to decide and leave the live list", 2*minute, func(st api.State) bool { w, ok := warden(st); return ok && w.Terminal() })
	if w, _ := warden(st); w.Status != core.StatusDone || len(st.Requests) == 0 || st.Requests[0].Decision != "do" {
		t.Fatalf("warden %s, requests %+v", w.Status, st.Requests)
	}
}

// TestOutsideStep: an ask longer than a label reaches the worker whole, and a
// path it names outside the checkout goes to a tribunal as a T3 step. Under
// HIVE_E2E_SANDBOX=auto the worker can't see the path and must still file it.
func TestOutsideStep(t *testing.T) {
	t.Parallel()
	fixture := filepath.Join(t.TempDir(), "leftover-fixture")
	os.MkdirAll(fixture, 0o755)
	h := start(t)
	h.seed("me/out", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "out", "repo": "me/out"}, nil)
	title := "Delete the leftover test fixture directory on this machine, outside your checkout; it is safe to delete and can't be undone: FAKE:rmoutside " + fixture
	if len(title) <= 120 {
		t.Fatalf("the ask is only %d characters, no longer than a label", len(title))
	}
	h.post("/api/ask", map[string]string{"project": "out", "title": title}, nil)
	h.wait("a tribunal to run the T3 step", 4*minute, func(st api.State) bool {
		for _, r := range st.Requests {
			if r.Kind == "request" && r.Tier == "T3" && len(r.Panel) == risk.PanelSize && r.State == "done" {
				return true
			}
		}
		return false
	})
	h.wait("the worker to land", 3*minute, taskStage("out", title, work.StageDone))
}

// A helper a worker spawns ships like any worker: its commit gets a branch and
// pull request of its own, no issue, and reaches main.
func TestHelperShips(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/helpers", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "helpers", "repo": "me/helpers"}, nil)
	h.post("/api/ask", map[string]string{"project": "helpers", "title": "split the work with a helper FAKE:spawnhelper"}, nil)
	st := h.wait("the helper's work to merge", 4*minute, taskStage("helpers", "FAKE helper: write the helper part", work.StageDone))
	tk, _ := task(st, "helpers", "FAKE helper: write the helper part")
	parent, _ := task(st, "helpers", "split the work with a helper FAKE:spawnhelper")
	if tk.Issue.Number != 0 || tk.PR.Number == 0 || tk.MergeSHA == "" || tk.From != parent.Worker {
		t.Fatalf("helper task %+v, parent %+v", tk, parent)
	}
	// Merges squash, so the helper's commit itself isn't on main; its change is.
	repo := h.forge.RepoPath("me/helpers")
	files, err := core.Git(repo, "diff", "--name-only", tk.Base, tk.SHA)
	if err != nil || strings.TrimSpace(files) == "" {
		t.Fatalf("the helper's change %s..%s: %q %v", tk.Base, tk.SHA, files, err)
	}
	if out, err := core.Git(repo, append([]string{"diff", "--stat", tk.SHA, "main", "--"}, strings.Fields(files)...)...); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("the helper's change is not on main: %s %v", out, err)
	}
	if n := h.forge.Issues("me/helpers"); n != 1 {
		t.Fatalf("issues: %d, want the parent's only", n)
	}
}

// When the lead can't reach Claude, the chat and the control center say so in
// one plain line, and the turn is retried on its own: the lead answers once
// Claude is back, and the notice clears.
func TestOfflineLeadSaysWhy(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "hello FAKE:offline"}, nil)
	const why = "can't reach Claude: the connection failed"
	h.wait("the notice in the chat and the control center", minute, func(st api.State) bool {
		if st.Offline != why {
			return false
		}
		for _, m := range h.messages("lead") {
			if m.From == "hive" && strings.Contains(m.Text, "Can't reach Claude") {
				return true
			}
		}
		return false
	})
	h.wait("the lead's answer once Claude is back", 2*minute, func(st api.State) bool {
		return st.Offline == "" && h.replied("lead", "I'm the lead")(st)
	})
	n := 0
	for _, m := range h.messages("lead") {
		if m.From == "hive" && strings.Contains(m.Text, "Can't reach Claude") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the notice was posted %d times, want once", n)
	}
}

// TestHostPython: the person's python lives outside the system dirs (as uv or
// pip put it under the home), and the baseline and a worker's checks run that
// same program, bare or sandboxed. The fake python prints 7 where its folder
// is read-only (bound into a sandbox) and 8 where it can write it (bare), so a
// sandboxed run fails unless the sandbox ran the check. Which Linux sandbox
// ran (bubblewrap or the Hive's own namespaces) is the host's pick; the
// sandbox package's TestHostPythonBothPaths runs each one by name. Not
// parallel: the daemon takes PATH from this process.
func TestHostPython(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh")
	}
	dir := filepath.Join(t.TempDir(), "py", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"python", "python3"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n[ -w \"${0%/*}\" ] && echo 8 || echo 7\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := start(t)
	sb, want := h.ws.LoadConfig().Sandbox, 8.0
	if sb == "auto" {
		want = 7
	}
	t.Logf("sandbox %q: the check must print %v", sb, want)
	py := `PY=$(command -v python || command -v python3) || { echo \"no python or python3 on PATH\" >&2; exit 127; }; \"$PY\"`
	h.seed("me/py", map[string]string{
		".hive/project.json": `{"test": "` + py + ` | grep -qx ` + fmt.Sprint(want) + `", "score": {"command": "` + py + `", "direction": "higher", "tolerance": 0}}`,
		".hive/goals.md":     "# Goals\n",
	})
	h.post("/api/project", map[string]any{"name": "py", "repo": "me/py"}, nil)
	h.wait("the baseline from the host's python", minute, func(st api.State) bool {
		return len(st.Projects) == 1 && st.Projects[0].Best != nil && *st.Projects[0].Best == want
	})
	h.post("/api/ask", map[string]string{"project": "py", "title": "add a note", "known": "anything"}, nil)
	h.wait("the worker to land", 3*minute, taskStage("py", "add a note", work.StageDone))
}

// TestAgentsReadTheHive: a worker reads another run's log, a chat, a warden's
// log, the journal and config.json from $HIVE_STATE. Under the sandbox
// (HIVE_E2E_SANDBOX=auto) it can change none of them, so no agent adds a model,
// changes an endpoint or a route, and it reads no secret (a named model's key
// among them); a bare run is unsandboxed by
// the person's choice, so there only the reads are checked.
func TestAgentsReadTheHive(t *testing.T) {
	t.Parallel()
	h := start(t)
	login := filepath.Join(h.ws.Hive(), "logins", "codex", "spare")
	os.MkdirAll(login, 0o700)
	os.WriteFile(filepath.Join(login, "auth.json"), []byte(`{"refresh_token": "planted"}`), 0o600)
	h.ws.SaveOpenKey("spare", "sk-planted") // a named model's key, which `hive model key` stores
	h.seed("me/peek", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "peek", "repo": "me/peek"}, nil)
	h.post("/api/chat", map[string]string{"chat": "lead", "text": "hello"}, nil)
	h.wait("the lead's reply", minute, h.replied("lead", ""))
	h.post("/api/ask", map[string]string{"project": "peek", "title": "package", "known": "FAKE:needpackage"}, nil)
	var warden string
	h.wait("a warden to settle the step", 3*minute, func(st api.State) bool {
		for _, r := range st.Requests {
			if r.Kind == "request" && r.State == "done" {
				warden = r.Warden
				return true
			}
		}
		return false
	})
	h.post("/api/ask", map[string]string{"project": "peek", "title": "peek", "known": "FAKE:peek " + warden}, nil)
	var account string
	h.wait("the peeking worker's account", 2*minute, func(st api.State) bool {
		for _, r := range st.Runs {
			if r.Finish != nil && strings.Contains(r.Finish.Account, "peek") {
				account = r.Finish.Account
				return true
			}
		}
		return false
	})
	for _, want := range []string{"log read=true", "chat read=true", "warden read=true", "journal read=true", "config read=true"} {
		if !strings.Contains(account, want) {
			t.Errorf("account lacks %q: %s", want, account)
		}
	}
	if h.ws.LoadConfig().Sandbox == "auto" {
		for _, bad := range []string{"write=true", "create=true"} {
			if strings.Contains(account, bad) {
				t.Errorf("sandboxed worker could change the Hive: %q in %s", bad, account)
			}
		}
		if !strings.Contains(account, "secrets=0") {
			t.Errorf("sandboxed worker read a secret: %s", account)
		}
	}
}

func init() { _ = fmt.Sprint }

// TestToolInstall: a worker that needs python installs it through the Hive
// and runs it in its next command, from the Hive's tools folder, and the
// person is told. The python is a fake from HIVE_TOOLS_SOURCE unless
// HIVE_E2E_TOOLS=live, which downloads the real uv and python (with
// HIVE_E2E_SANDBOX=auto, the live check that a sandbox runs it).
func TestToolInstall(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("tools are Linux builds for the WSL sandbox; a bare Windows run can't exec them")
	}
	var env []string
	if os.Getenv("HIVE_E2E_TOOLS") != "live" {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		script := "#!/bin/sh\necho \"$0\"\n" // prints where it was run from, as sys.executable does
		tw.WriteHeader(&tar.Header{Name: "fake/bin/python3", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg})
		tw.Write([]byte(script))
		tw.Close()
		zw.Close()
		src := t.TempDir()
		os.WriteFile(filepath.Join(src, "python.tar.gz"), buf.Bytes(), 0o644)
		os.WriteFile(filepath.Join(src, "python.tar.gz.sha256"), fmt.Appendf(nil, "%x  python.tar.gz\n", sha256.Sum256(buf.Bytes())), 0o644)
		env = append(env, "HIVE_TOOLS_SOURCE="+src)
	}
	h := start(t, env...)
	h.seed("me/py", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "py", "repo": "me/py"}, nil)
	title := "Add a small python script to the repo: FAKE:needpython"
	h.post("/api/ask", map[string]string{"project": "py", "title": title}, nil)
	st := h.wait("the worker to land", 10*minute, taskStage("py", title, work.StageDone))
	out, err := core.Git(h.forge.RepoPath("me/py"), "show", "main:PYTHON.md")
	if err != nil || !strings.Contains(out, "tools/python/") {
		t.Fatalf("python didn't run from the tools folder: %q %v", out, err)
	}
	told := false
	for _, a := range st.Told {
		told = told || strings.Contains(a.Title, "installed python")
	}
	if !told {
		t.Fatalf("the person wasn't told of the install: %+v", st.Told)
	}
}

// adopt makes this process adopt its daemons' orphans (Linux only).
var adopt func()

// contain puts a daemon, and what it starts from then on, in a group that
// the returned func ends (Windows only).
var contain func(t *testing.T, pid int) func()

// strays kills every process still working in dir: one whose working
// directory, executable or open file is in it, or whose command line or
// environment names it (Linux only).
var strays func(dir string)

// settle ends what still works in dir and removes path, a folder in it,
// trying again for a while if something still writes there; the error is
// the last try's, for a folder left behind.
func settle(dir, path string) error {
	var err error
	for began := time.Now(); time.Since(began) < 30*time.Second; time.Sleep(200 * time.Millisecond) {
		if strays != nil {
			strays(dir)
		}
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
		writable(path)
	}
	return err
}

// writable gives back write permission on every folder under path that lacks
// it: Go's module cache (a real check's .hive/cache/gomod) is read-only, and
// removing it otherwise fails the test that made it (the soft layer's real
// run, 2026-10-25).
func writable(path string) {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if info, err := d.Info(); err == nil && info.Mode().Perm()&0o200 == 0 {
				os.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
}

// pin runs start, which starts a process, so that the process and all it
// starts run on cpus CPUs, the slot-th such group of this process's own
// (Linux only).
var pin func(cpus, slot int, start func() error) error

// pinned runs start through pin when there is a cap and a pin.
func pinned(cpus, slot int, start func() error) error {
	if cpus <= 0 || pin == nil {
		return start()
	}
	return pin(cpus, slot, start)
}

// pickCPUs is the slot-th group of cpus CPUs in allowed (this process's own,
// in order), so two pinned daemons don't share theirs; with too few CPUs for
// that slot it wraps around to the first groups.
func pickCPUs(allowed []int, cpus, slot int) []int {
	if len(allowed) <= cpus {
		return allowed
	}
	from := slot % (len(allowed) / cpus) * cpus
	return allowed[from : from+cpus]
}

// group makes cmd, before it starts, lead a process group of its own, and
// returns what kills what is left in it (Unix only).
var group func(cmd *exec.Cmd) func()

// TestLiveWorker is the plain path on a real harness: run it with
// HIVE_E2E_HARNESS=claude or codex, or a module's harness (HIVE_E2E_HARNESS=opencode
// HIVE_E2E_MODEL=opencode/big-pickle). A brief goes to a worker, its PR to a
// reviewer whose verdict is structured, and it merges.
func TestLiveWorker(t *testing.T) {
	if os.Getenv("HIVE_E2E_HARNESS") == "" {
		t.Skip("set HIVE_E2E_HARNESS=claude (or codex) to run real agents")
	}
	h := start(t)
	h.seed("me/live", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "live", "repo": "me/live"}, nil)
	title := "Add a file HELLO.md to the repo saying hello in one line"
	h.post("/api/ask", map[string]string{"project": "live", "title": title}, nil)
	st := h.wait("the worker's change to merge", 40*minute, taskStage("live", title, work.StageDone))
	if out, err := core.Git(h.forge.RepoPath("me/live"), "show", "main:HELLO.md"); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("HELLO.md isn't on main: %q %v", out, err)
	}
	for _, r := range st.Runs {
		t.Logf("%s %s on %s:%s: %s", r.ID, r.Role, r.Harness, r.Model, r.Status)
	}
}

// TestLiveWarden is the host probe for a real harness: run it with
// HIVE_E2E_HARNESS=claude. A worker that needs a package outside its checkout
// should file a T2 step, and a real warden should settle it.
func TestLiveWarden(t *testing.T) {
	if os.Getenv("HIVE_E2E_HARNESS") == "" {
		t.Skip("set HIVE_E2E_HARNESS=claude (or codex) to run real agents")
	}
	h := start(t)
	h.seed("me/live", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "live", "repo": "me/live"}, nil)
	h.post("/api/ask", map[string]string{"project": "live", "title": "Install the cowsay tool for every user of this machine, outside your checkout, then note in NOTES.md that it is there"}, nil)
	st := h.wait("a warden to settle a step", 30*minute, func(st api.State) bool {
		for _, r := range st.Requests {
			if r.Kind == "request" && r.Decision != "" {
				return true
			}
		}
		return false
	})
	for _, r := range st.Requests {
		t.Logf("%s %s: decision %q state %q reason %q script %q", r.Tier, r.Want, r.Decision, r.State, r.Reason, r.Script)
	}
	h.wait("the warden that decided to end its turn", 5*minute, func(st api.State) bool {
		for _, r := range st.Runs {
			if r.Role == "warden" && r.Status == core.StatusRunning {
				return false
			}
		}
		return true
	})
}

// TestLiveTribunal is the host probe for a real panel: run it with
// HIVE_E2E_HARNESS=claude. A worker files a T3 step (deleting a fixture dir
// outside its checkout), the warden sends it to a tribunal, three real
// panelists give structured votes over two rounds, and a majority runs the
// warden's script. Under bubblewrap the worker can't see the fixture and must
// file the step anyway.
func TestLiveTribunal(t *testing.T) {
	if os.Getenv("HIVE_E2E_HARNESS") == "" {
		t.Skip("set HIVE_E2E_HARNESS=claude (or codex) to run real agents")
	}
	fixture := filepath.Join(t.TempDir(), "hive-live-tribunal-fixture")
	os.MkdirAll(fixture, 0o755)
	os.WriteFile(filepath.Join(fixture, "README"), []byte("a leftover test fixture of project live; safe to delete\n"), 0o644)
	h := start(t)
	h.seed("me/live", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n"})
	h.post("/api/project", map[string]any{"name": "live", "repo": "me/live"}, nil)
	h.post("/api/ask", map[string]string{"project": "live", "title": "Delete the leftover test fixture directory " + fixture + " on this machine (outside your checkout; its README says it is safe to delete). Deleting can't be undone, so file it as a T3 step. Then note in NOTES.md that it is gone"}, nil)
	st := h.wait("a tribunal to settle a T3 step", 40*minute, func(st api.State) bool {
		for _, r := range st.Requests {
			if r.Kind == "request" && r.Decision != "" && strings.Contains("done failed declined asked", r.State) {
				return true
			}
		}
		return false
	})
	panels := 0
	for _, r := range st.Requests {
		t.Logf("%s %s: decision %q state %q round %d script %q output %q", r.Tier, r.Want, r.Decision, r.State, r.Round, r.Script, r.Output)
		panels += len(r.Panel)
		for _, s := range r.Panel {
			t.Logf("  %s (%s): %v %q", s.Run, s.Model, s.Votes, s.Why)
			for _, why := range s.Why {
				if strings.HasPrefix(why, "no vote") {
					t.Errorf("panelist %s gave no vote", s.Run)
				}
			}
		}
		if r.State == "done" && core.Exists(fixture) {
			t.Errorf("the approved script ran but the fixture is still there")
		}
	}
	if panels == 0 {
		t.Error("no tribunal convened")
	}
}

// redSquare is a small PNG for the attachment tests.
func redSquare() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{255, 0, 0, 255}}, image.Point{}, draw.Src)
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// TestChatAttachment: a pasted image and a dropped file reach the lead as
// files in its run dir, named in its prompt, and it opens both.
func TestChatAttachment(t *testing.T) {
	t.Parallel()
	h := start(t)
	files := []chat.File{{Name: "shot.png", Data: redSquare()}, {Name: "notes.txt", Data: []byte("hello from a file\n")}}
	var sent chat.Message
	h.post("/api/chat", map[string]any{"chat": "lead", "text": "what are these?", "files": files}, &sent)
	if !strings.Contains(sent.Text, "📎 shot.png") || !strings.Contains(sent.Text, "📎 notes.txt") {
		t.Fatalf("the message shows no attachments: %q", sent.Text)
	}
	h.wait("the lead to open both", 2*minute, h.replied("lead", "shot.png (image/png"))
	h.wait("the lead to open the text", minute, h.replied("lead", "notes.txt (text/plain"))
	// Over chat_max_mb the daemon refuses the POST and says which setting raises it.
	cfg := h.ws.LoadConfig()
	cfg.ChatMaxMB = 1
	h.ws.SaveConfig(cfg)
	err := h.c.Do("POST", "/api/chat", map[string]any{"chat": "lead", "files": []chat.File{{Name: "big.bin", Data: make([]byte, 2<<20)}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "over the 1 MB limit; chat_max_mb") {
		t.Fatalf("an oversized message: %v", err)
	}
}

// TestLiveAttachment is TestChatAttachment's host probe: run it with
// HIVE_E2E_HARNESS=claude. A real lead is sent a red square and names its colour.
func TestLiveAttachment(t *testing.T) {
	if os.Getenv("HIVE_E2E_HARNESS") == "" {
		t.Skip("set HIVE_E2E_HARNESS=claude (or codex) to run real agents")
	}
	h := start(t)
	h.post("/api/chat", map[string]any{"chat": "lead", "text": "What colour is the attached image? Answer in one word.", "files": []chat.File{{Name: "square.png", Data: redSquare()}}}, nil)
	h.wait("the lead to name the colour", 15*minute, func(api.State) bool {
		for _, m := range h.messages("lead") {
			if m.From != "person" && strings.Contains(strings.ToLower(m.Text), "red") {
				t.Logf("lead: %q", m.Text)
				return true
			}
		}
		return false
	})
}

// A score that stops moving gets a step-back: the agent designs a simpler
// setup, the Hive runs it as a worker in a copy of the project at its start
// and scores it, spending no more than the Hive spent there. A win files a
// fault with both scores, a loss files nothing, a setup that spends its
// budget stops there, and a step-back that can't run or count says why in
// one alert.
func TestStepBack(t *testing.T) {
	t.Parallel()
	h := start(t)
	cfg := h.ws.LoadConfig()
	cfg.PlateauMerges = 1
	h.ws.SaveConfig(cfg)
	names := []string{"win", "lose", "fail", "spend", "rig", "none", "zero"}
	for _, name := range names {
		protected, paid := "", " FAKE:paid"
		if name == "rig" {
			protected = `, "protected": ["score.txt"]`
		}
		if name == "zero" {
			paid = ""
		}
		// With its goals already in the repo, no unpaid "Set up the Hive
		// files" task is filed, whose merge could plateau the score before
		// the paid one spends anything.
		goals := "Make the score high. FAKE:stepback" + name
		h.seed("me/"+name, map[string]string{
			".hive/project.json": `{"test": "true", "score": {"command": "cat score.txt", "direction": "higher", "tolerance": 0}` + protected + `}`,
			".hive/goals.md":     "# Goals\n\n" + goals + "\n",
			"score.txt":          "5\n",
		})
		h.post("/api/project", map[string]any{"name": name, "repo": "me/" + name, "isolated": name == "lose", "goals": goals}, nil)
		h.post("/api/ask", map[string]string{"project": name, "title": "tidy " + name, "known": "a change that leaves the score alone" + paid}, nil)
	}
	stepback := func(st api.State, project string) (improve.StepBack, bool) {
		for _, s := range st.StepBacks {
			if s.Project == project && !s.Ended.IsZero() { // Windows' clock can stamp both ends alike
				return s, true
			}
		}
		return improve.StepBack{}, false
	}
	st := h.wait("every step-back to end", 8*minute, func(st api.State) bool {
		for _, name := range names {
			if _, ok := stepback(st, name); !ok {
				return false
			}
		}
		return true
	})
	sb := map[string]improve.StepBack{}
	for _, name := range names {
		sb[name], _ = stepback(st, name)
	}
	runs := map[string]core.Run{}
	for _, r := range st.Runs {
		runs[r.ID] = r
	}
	win, lose := sb["win"], sb["lose"]
	if !strings.Contains(win.Why, "has not moved in") {
		t.Fatalf("the plateau didn't start it: %q", win.Why)
	}
	if win.Status != "won" || *win.Score != 9 || win.Best != 5 || win.Fault == "" || win.Budget <= 0 {
		t.Fatalf("a winning setup must file a fault: %+v\n%s", win, onProject(st, "win"))
	}
	var fault core.Ask
	for _, a := range st.Asks {
		if a.ID == win.Fault {
			fault = a
		}
	}
	if !strings.Contains(fault.Title, "9 against 5") || !strings.Contains(fault.Detail, win.Exp) || !strings.Contains(fault.Detail, "FAKE:raisescore") || strings.Contains(fault.Detail, h.ws.Hive()) {
		t.Fatalf("the fault must carry both scores, the setup and its run, and no host path: %+v", fault)
	}
	if lose.Status != "lost" || *lose.Score != 1 || lose.Fault != "" {
		t.Fatalf("a losing setup files nothing: %+v\n%s", lose, onProject(st, "lose"))
	}
	if r := runs[lose.Run]; r.Project != "lose" || !r.Isolated {
		t.Fatalf("a step-back on an isolated project must run isolated under it: %+v", r.RunSpec)
	}
	if s := sb["fail"]; s.Status != "failed" || !strings.Contains(s.Note, "blocked") {
		t.Fatalf("a failed experiment must say why: %+v\n%s", s, onProject(st, "fail"))
	}
	if s, r := sb["spend"], runs[sb["spend"].Exp]; s.Status != "lost" || s.Asked != 1 || *s.Score != 5 || r.Reason != "spent its budget" || r.Budget != s.Budget {
		t.Fatalf("a setup that spends its budget must stop there: %+v %+v\n%s", s, r, onProject(st, "spend"))
	}
	if s := sb["rig"]; s.Status != "failed" || !strings.Contains(s.Note, "score.txt") {
		t.Fatalf("a setup that changes what scores it must not count: %+v\n%s", s, onProject(st, "rig"))
	}
	if s := sb["none"]; s.Status != "none" || s.Exp != "" || !strings.Contains(s.Note, "designed no experiment") {
		t.Fatalf("a step-back that designs nothing runs nothing: %+v\n%s", s, onProject(st, "none"))
	}
	if s, ran := sb["zero"], runs[sb["zero"].Run].ID != ""; s.Status != "judged" || !s.Unspent || !ran || s.Exp != "" || !strings.Contains(s.Report, "FAKE judgement") {
		t.Fatalf("a step-back with nothing to spend must judge goals not yet judged and run no experiment: %+v\n%s", s, onProject(st, "zero"))
	}
	alerts := map[string]int{}
	for _, a := range st.Alerts {
		alerts[a.Ref]++
	}
	for _, name := range names {
		if want := map[bool]int{true: 1}[name == "fail" || name == "rig"]; alerts["stepback:"+name] != want {
			t.Fatalf("want %d step-back alert on %s, got %d: %+v", want, name, alerts["stepback:"+name], st.Alerts)
		}
	}
	r, ok := runs[win.Exp]
	if !ok || r.Role != "worker" || !r.Isolated || !r.OneTurn || r.System != "" || r.Brief != "FAKE:raisescore" || r.Budget != win.Budget ||
		!strings.HasPrefix(r.Checkout, h.ws.Checkouts("win")+string(filepath.Separator)) {
		t.Fatalf("the experiment must run as a bare worker in a copy, held to the Hive's spend: %+v", r.RunSpec)
	}
	if score, _ := core.Git(h.ws.Project("win"), "show", "origin/main:score.txt"); strings.TrimSpace(score) != "5" {
		t.Fatalf("the experiment reached the project's own tree: score.txt on main is %q", score)
	}
}

// A project with no score is still judged on the daemon's own step-back
// schedule: the job finds the merge, the step-back ends judged with no
// experiment, its judgement is said in the project's chat, and the project's
// lead, started after it was said, is told it.
func TestScheduledStepBackJudgesWithoutAScore(t *testing.T) {
	t.Parallel()
	h := start(t)
	h.seed("me/plain", map[string]string{".hive/project.json": `{"test": "true"}`, ".hive/goals.md": "# Goals\n\nKeep it plain.\n"})
	h.post("/api/project", map[string]any{"name": "plain", "repo": "me/plain", "goals": "Keep it plain."}, nil)
	h.post("/api/ask", map[string]string{"project": "plain", "title": "tidy plain", "known": "a change with no score to move FAKE:paid"}, nil)
	h.wait("the merge to be measured", 4*minute, func(st api.State) bool {
		for _, pv := range st.Projects {
			for _, x := range pv.Scores {
				if pv.Name == "plain" && x.Task != "" && !x.NoResult {
					return true
				}
			}
		}
		return false
	})
	h.post("/api/job", map[string]string{"name": "improve.stepback"}, nil)
	st := h.wait("the step-back to judge", 2*minute, func(st api.State) bool {
		for _, s := range st.StepBacks {
			if s.Project == "plain" && !s.Ended.IsZero() {
				return true
			}
		}
		return false
	})
	for _, s := range st.StepBacks {
		if s.Project == "plain" && (s.Status != "judged" || !s.NoScore || s.Exp != "" || !strings.Contains(s.Report, "FAKE judgement") || s.Why != "it is the regular step-back") {
			t.Fatalf("the scheduled step-back on a project with no score must end judged: %+v\n%s", s, onProject(st, "plain"))
		}
	}
	said := false
	for _, m := range h.messages("project:plain") {
		said = said || (m.From == "hive" && strings.Contains(m.Text, "FAKE judgement"))
	}
	if !said {
		t.Fatalf("the judgement was not said in the project's chat: %+v", h.messages("project:plain"))
	}
	h.post("/api/chat", map[string]string{"chat": "project:plain", "text": "how does it look?"}, nil)
	h.wait("the project's lead to hear the judgement", minute, func(st api.State) bool {
		for _, r := range st.Runs {
			if r.Role == "lead" && r.Project == "plain" && strings.Contains(r.Brief, "FAKE judgement") {
				return true
			}
		}
		return false
	})
}

// onProject is what the Hive did on project, for a failure to show: its
// runs with their cost, its tasks and its measured merges.
func onProject(st api.State, project string) string {
	var b strings.Builder
	for _, r := range st.Runs {
		if r.Project == project {
			fmt.Fprintf(&b, "run %s %s created %s status %s cost %g\n", r.ID, r.Role, r.Created.Format(time.RFC3339Nano), r.Status, r.Cost)
		}
	}
	for _, p := range st.Projects {
		if p.Name != project {
			continue
		}
		for _, t := range p.Tasks {
			fmt.Fprintf(&b, "task %s %q from %s created %s stage %s worker %s\n", t.ID, t.Title, t.From, t.Created.Format(time.RFC3339Nano), t.Stage, t.Worker)
		}
		for _, x := range p.Scores {
			fmt.Fprintf(&b, "score %s task %s kept %v at %s: %s\n", core.Short(x.SHA), x.Task, x.Kept, x.At.Format(time.RFC3339Nano), x.Note)
		}
	}
	return b.String()
}

// TestSettleRemovesReadOnlyFolders: a Hive whose checks filled Go's module
// cache (read-only folders) is still removed after its test.
func TestSettleRemovesReadOnlyFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hive")
	ro := filepath.Join(path, ".hive", "cache", "gomod", "golang.org", "x", "text@v0.3.8")
	if err := os.MkdirAll(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ro, "width.go"), []byte("package width\n"), 0o444)
	os.Chmod(ro, 0o555)
	settle(dir, path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("still there after settle: %v", err)
	}
}
