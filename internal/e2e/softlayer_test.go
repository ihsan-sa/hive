package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
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
// Real agents (HIVE_E2E_HARNESS=claude) spend plan usage. The arms run one at
// a time (HIVE_SOFT_PARALLEL=2 runs two), each sub-Hive with one worker and
// one check at once and pinned to softCPUs CPUs, and each arm's processes are
// ended before the next starts: all seven side by side, uncapped, ran ~28
// builds at once and swapped the machine (issue #771). On Linux a real run
// must be under a memory ceiling of its own (docs/soft-layer.md). Unless HIVE_SOFT_WARDENS=1, a
// sub-Hive asks the person (this test, which never answers) before anything
// outside its project, so no warden's script ever runs on this machine for an
// arm whose warden was told only "You are a warden."
//
//	systemd-run --user --scope -p MemoryHigh=2500M -p MemoryMax=3G -p MemorySwapMax=0 -p CPUWeight=20 -p IOWeight=20 \
//	  env HIVE_SOFT=standard,essay HIVE_E2E_HARNESS=claude HIVE_SOFT_OUT=/tmp/soft go test ./internal/e2e -run TestSoftLayer -timeout 6h -v
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
	if os.Getenv("HIVE_E2E_HARNESS") != "" && os.Getenv("HIVE_SOFT_UNBOUNDED") != "1" {
		if why := unbounded(); why != "" {
			t.Fatalf("a real soft-layer run needs a memory ceiling of its own, or a swapping machine takes the Hive's own runs with it (issue #771): %s. Run it under systemd-run --user --scope -p MemoryMax=3G -p MemorySwapMax=0 (docs/soft-layer.md), or set HIVE_SOFT_UNBOUNDED=1", why)
		}
	}
	at := 1
	if n, err := strconv.Atoi(os.Getenv("HIVE_SOFT_PARALLEL")); err == nil {
		at = min(max(n, 1), 2)
	}
	out := os.Getenv("HIVE_SOFT_OUT")
	if out == "" {
		out = t.TempDir()
	}
	reps := make([]softlayer.Report, len(arms))
	slots := make(chan struct{}, at)
	t.Run("arms", func(t *testing.T) {
		for i, arm := range arms {
			t.Run(arm.Name, func(t *testing.T) {
				t.Parallel()
				slots <- struct{}{}
				// Registered first, so it runs last: after the arm's daemon
				// and what it left are ended and its folder removed.
				t.Cleanup(func() { <-slots })
				reps[i] = softArm(t, arm, string(essay), task, wait, filepath.Join(out, arm.Name))
			})
		}
	})
	table := fmt.Sprintf("Task: %s\n\nHarness: %s; each arm waited up to %s, %d at a time on %d CPUs each.%s\n\n%s", task, envOr("HIVE_E2E_HARNESS", "fake"), wait, at, softCPUs, cgroupPeak(), softlayer.Table(reps))
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

// softCPUs is how many CPUs one arm's sub-Hive runs on: its agents, its
// checks' builds and their tests (go sizes -p and GOMAXPROCS to them).
const softCPUs = 4

// softArm runs one arm and keeps its journal, chats and report in keep.
func softArm(t *testing.T, arm softlayer.Variant, essay, task string, wait time.Duration, keep string) softlayer.Report {
	// The caps: one worker and one check at a time, set as the person's
	// ceilings so the pace can't raise them, and builds of two packages at
	// once where a check gets the daemon's environment (a bare sandbox).
	h := startOn(t, softCPUs, bin, func(c *core.Config) {
		if os.Getenv("HIVE_SOFT_WARDENS") != "1" {
			c.Autonomy = 1
		}
		c.MaxWorkers, c.MaxChecks, c.MaxGates, c.Ceilings = 1, 1, 1, true
	}, "GOFLAGS=-p=2", fmt.Sprintf("GOMAXPROCS=%d", softCPUs))
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
	stopWatch := watchPeaks()

	outcome := ""
	for deadline := time.Now().Add(wait); outcome == ""; time.Sleep(10 * time.Second) {
		standInCI(h, "me/"+project)
		st := h.state()
		if tk, ok := landed(st, project); ok {
			outcome = "landed " + shortSHA(tk.MergeSHA) + " after " + time.Since(began).Round(time.Second).String()
		} else if time.Now().After(deadline) {
			outcome = "not landed: " + stages(st, project)
		}
	}
	h.quiesce()
	peakMB, peakLoad := stopWatch()

	f, err := os.Open(h.ws.Events())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := softlayer.Observe(f, began, time.Time{})
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	rep.Variant, rep.Outcome, rep.PeakMB, rep.PeakLoad = arm.Name, outcome, peakMB, peakLoad
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
			Data struct{ ID string }
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Kind != "run.created" {
			continue
		}
		runs++
		// the journal names a long system prompt; the run's ro folder has it
		system, _ := os.ReadFile(filepath.Join(h.ws.RunRO(e.Data.ID), "system.md"))
		if !strings.HasPrefix(string(system), files["orientation"]) {
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
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "The Hive at " + shortSHA(strings.TrimSpace(head))}, {"push", "-q", "origin", "HEAD:main"}} {
		if _, err := core.Git(tmp, args...); err != nil {
			t.Fatal(err)
		}
	}
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

// watchPeaks samples, every two seconds until the returned func is called,
// the memory this test's processes hold (its own and every descendant's RSS,
// the daemons' orphans included, since the test adopts them) and the
// machine's 1-minute load, and that func returns the highest of each. Both
// read /proc, so elsewhere they stay 0 and the table says "-".
func watchPeaks() func() (int, float64) {
	stop, done := make(chan struct{}), make(chan struct{})
	mb, load := 0, 0.0
	go func() {
		defer close(done)
		for {
			mb, load = max(mb, int(treeRSS(os.Getpid())>>20)), max(load, load1())
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return func() (int, float64) {
		close(stop)
		<-done
		return mb, load
	}
}

// treeRSS is the resident memory of pid and all its descendants, in bytes.
func treeRSS(pid int) int64 {
	kids := map[int][]int{}
	rss := map[int]int64{}
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		p, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", d.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ... rss is field 24; comm may hold spaces.
		f := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+1:]))
		if len(f) < 22 {
			continue
		}
		pp, _ := strconv.Atoi(f[1])
		pages, _ := strconv.ParseInt(f[21], 10, 64)
		kids[pp] = append(kids[pp], p)
		rss[p] = pages * int64(os.Getpagesize())
	}
	var sum int64
	for todo := []int{pid}; len(todo) > 0; {
		p := todo[len(todo)-1]
		todo = append(todo[:len(todo)-1], kids[p]...)
		sum += rss[p]
	}
	return sum
}

// load1 is the machine's 1-minute load average, or 0 without /proc.
func load1() float64 {
	raw, _ := os.ReadFile("/proc/loadavg")
	f := strings.Fields(string(raw))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// ownCgroup is this process's cgroup v2 folder, or "".
func ownCgroup() string {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(l, "0::"); ok {
			return filepath.Join("/sys/fs/cgroup", rest)
		}
	}
	return ""
}

// softMemMax is the most a soft-layer run's memory ceiling may be: well under
// the Hive's own 8 GB share, so a run that outgrows it is ended inside its own
// cgroup and the Hive's runs never swap for it.
const softMemMax = 4 << 30

// unbounded says why this run has no memory ceiling of its own (its own
// cgroup's memory.max, at most softMemMax, so the Hive's own 8 GB unit
// doesn't count), or "" when it has one. Off Linux it can't tell, and says "".
func unbounded() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	cg := ownCgroup()
	if cg == "" {
		return "no cgroup v2 found for this process"
	}
	raw, err := os.ReadFile(filepath.Join(cg, "memory.max"))
	if err != nil {
		return "its cgroup " + cg + " has no memory.max"
	}
	v := strings.TrimSpace(string(raw))
	if n, err := strconv.ParseInt(v, 10, 64); err != nil || n > softMemMax {
		return fmt.Sprintf("its cgroup %s has memory.max %s, over %d MB", cg, v, softMemMax>>20)
	}
	return ""
}

// cgroupPeak is the most memory this run's own cgroup has held, said as a
// sentence for the table, or "" without one (memory.peak, Linux 5.19).
func cgroupPeak() string {
	cg := ownCgroup()
	if cg == "" || unbounded() != "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(cg, "memory.peak"))
	n, err2 := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || err2 != nil {
		return ""
	}
	return fmt.Sprintf(" The run's cgroup peaked at %d MB.", n>>20)
}

// TestSoftCaps: a pinned start gives the process it starts, and what that
// starts, softCPUs CPUs, and this process keeps all of its own; the sampler
// sees this process's memory and the machine's load.
func TestSoftCaps(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("pin and the samplers read Linux's /proc")
	}
	before := runtime.NumCPU()
	if before <= softCPUs {
		t.Skipf("only %d CPUs here", before)
	}
	cmd := exec.Command("/bin/sh", "-c", "grep Cpus_allowed_list /proc/self/status; /bin/sh -c 'grep Cpus_allowed_list /proc/self/status'")
	var got []byte
	err := pinned(softCPUs, func() error {
		var err error
		got, err = cmd.Output()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Cpus_allowed_list:\t0-%d", softCPUs-1)
	if lines := strings.Split(strings.TrimSpace(string(got)), "\n"); len(lines) != 2 || lines[0] != want || lines[1] != want {
		t.Errorf("a pinned child and its own child: %q, want %q twice", got, want)
	}
	threads, _ := filepath.Glob("/proc/self/task/*/status")
	for _, th := range threads {
		if raw, _ := os.ReadFile(th); strings.Contains(string(raw), want+"\n") {
			t.Errorf("this process's thread %s was left pinned", th)
		}
	}
	if mb := treeRSS(os.Getpid()) >> 20; mb <= 0 {
		t.Errorf("this test's memory read as %d MB", mb)
	}
	if mb, _ := watchPeaks()(); mb <= 0 {
		t.Errorf("the sampler's peak is %d MB", mb)
	}
}
