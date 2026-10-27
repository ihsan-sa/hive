package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The task probe: one real task from hive-eval (its tasks/dev folders: a
// repository at a pinned commit, an issue, and hidden tests no agent sees),
// done by each side's Hive on the real model, the way a person gives it work,
// and graded on what landed on its forge. The other probes run the fake
// harness, so they show the Hive's plumbing; this one shows whether a change
// helps a real model get real work merged. The task is the gate's (Config.Task,
// chosen by the daemon), never the candidate's, and its hidden tests stay
// outside every sandbox but the sealed one that grades.

// Task is one hive-eval task of kind "issue" (heval/tasks.py): task.json and
// hidden/, the hidden test files laid out as in the repository.
type Task struct {
	Dir        string `json:"-"`
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Repo       string `json:"repo"`
	Base       string `json:"base"`
	Title      string `json:"title"`
	Issue      string `json:"issue"`
	Test       string `json:"test"`
	HiddenTest string `json:"hidden_test"`
	Timeout    int    `json:"timeout"` // seconds the hidden tests may take
}

// LoadTask reads a task folder, refusing one the gate can't run or grade.
func LoadTask(dir string) (*Task, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "task.json"))
	if err != nil {
		return nil, err
	}
	t := &Task{}
	if err := json.Unmarshal(raw, t); err != nil {
		return nil, fmt.Errorf("%s: %v", dir, err)
	}
	t.Dir, _ = filepath.Abs(dir)
	if t.Kind != "" && t.Kind != "issue" {
		return nil, fmt.Errorf("%s: the gate runs only a single issue, not a %s", dir, t.Kind)
	}
	var missing []string
	for k, v := range map[string]string{"id": t.ID, "repo": t.Repo, "base": t.Base, "title": t.Title, "issue": t.Issue, "test": t.Test, "hidden_test": t.HiddenTest} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s: task.json lacks %s", dir, strings.Join(missing, ", "))
	}
	if fi, err := os.Stat(filepath.Join(t.Dir, "hidden")); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s: no hidden/ folder of tests to grade with", dir)
	}
	if t.Timeout <= 0 {
		t.Timeout = 600
	}
	return t, nil
}

// Prompt is what the side is told, word for word what every hive-eval arm is
// told (heval/tasks.py prompt).
func (t *Task) Prompt() string {
	return fmt.Sprintf("# %s\n\n%s\n\nThe project's tests run with `%s`. Make the change in this repository, with tests, and commit it.", t.Title, strings.TrimSpace(t.Issue), t.Test)
}

// taskProject is the sub-Hive's project: a neutral name, as hive-eval's.
const taskProject = "proj"

// taskFiles are the Hive files the seed's second commit adds (heval/arms.py),
// so the side's first act isn't a setup task.
var taskFiles = map[string]string{
	".hive/goals.md":   "# Goals\n\nKeep this project working and fix what is asked, with tests.\n",
	".hive/lessons.md": "# Lessons\n\nWhat was tried and why it didn't stay. Read before working here.\n",
}

// DefaultTaskWall is how long a side has for the task: hive-eval's dev pilot
// landed each one in 2 to 3 minutes on the frozen Hive.
const DefaultTaskWall = 20 * time.Minute

// taskRun is what the probe prepares once per gate: the task and its seed.
type taskRun struct {
	task *Task
	seed string // a repository with the task's tree at base and the Hive files: two commits, no upstream history
	err  error
}

// prepareTask clones the task's repository, here and outside every sandbox,
// and makes the seed both sides start from. The clone holds the upstream fix
// in its later history, so it goes once the seed is made.
func prepareTask(cfg *Config) *taskRun {
	if cfg.task != nil {
		return cfg.task
	}
	r := &taskRun{}
	cfg.task = r
	if r.task, r.err = LoadTask(cfg.Task); r.err != nil {
		return r
	}
	dir := filepath.Join(cfg.Out, "task")
	up, seed := filepath.Join(dir, "upstream"), filepath.Join(dir, "seed")
	os.RemoveAll(dir)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		r.err = err
		return r
	}
	defer os.RemoveAll(up)
	id := []string{"-c", "user.name=hive gate", "-c", "user.email=gate@hive.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}
	steps := []struct {
		dir  string
		args []string
	}{
		{dir, []string{"clone", "-q", "--no-checkout", r.task.Repo, up}},
		{seed, []string{"init", "-q", "-b", "main"}},
	}
	for _, s := range steps {
		if out, err := gitRun(s.dir, "git", s.args...); err != nil {
			r.err = fmt.Errorf("git %s: %v: %s", s.args[0], err, tail(out, 300))
			return r
		}
	}
	// The tree at base alone: no remote, no later commit to find the fix in.
	arch := exec.Command("git", "archive", "--format=tar", r.task.Base)
	arch.Dir, arch.Env = up, append(cleanEnv(), "GIT_CONFIG_GLOBAL=/dev/null")
	untar := exec.Command("tar", "-x", "-C", seed)
	var err error
	if untar.Stdin, err = arch.StdoutPipe(); err != nil {
		r.err = err
		return r
	}
	if err = untar.Start(); err == nil {
		err = arch.Run()
		if e := untar.Wait(); err == nil {
			err = e
		}
	}
	if err != nil {
		r.err = fmt.Errorf("the tree at %s: %v", short(r.task.Base), err)
		return r
	}
	commit := func(msg string) error {
		for _, a := range [][]string{{"add", "-A"}, append(append([]string{}, id...), "commit", "-q", "-m", msg)} {
			if out, err := gitRun(seed, "git", a...); err != nil {
				return fmt.Errorf("git %s: %v: %s", a[0], err, tail(out, 300))
			}
		}
		return nil
	}
	if r.err = commit("Initial import"); r.err != nil {
		return r
	}
	proj, _ := json.MarshalIndent(map[string]string{"test": r.task.Test}, "", "  ")
	files := map[string]string{".hive/project.json": string(proj) + "\n"}
	for p, s := range taskFiles {
		files[p] = s
	}
	for p, s := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(seed, p)), 0o755)
		if r.err = os.WriteFile(filepath.Join(seed, p), []byte(s), 0o644); r.err != nil {
			return r
		}
	}
	r.err = commit("Set up the Hive files")
	r.seed = seed
	return r
}

// TaskSpec is what the driver inside a side's sandbox (`hive gate-task`) is
// handed: the side's binary, a fresh workspace and local forge for it, and
// the task's words.
type TaskSpec struct {
	Bin     string  `json:"bin"`
	WS      string  `json:"ws"`
	Forge   string  `json:"forge"`
	Project string  `json:"project"`
	Prompt  string  `json:"prompt"`
	WallS   float64 `json:"wall_s"`
	Out     string  `json:"out"` // where the driver writes its TaskDrive
}

// TaskDrive is what the driver saw: why it stopped and what the side said
// of itself. It is the side's own word, so only shown; what is judged is
// measured from outside, on its forge.
type TaskDrive struct {
	Outcome string  `json:"outcome"` // "landed", "gave up", "waiting on the person", "out of time after …", or what went wrong
	Cost    float64 `json:"cost_usd_reported"`
	Runs    int     `json:"runs"`
	Tasks   int     `json:"tasks"`
}

// taskConfig is the sub-Hive's settings (heval/arms.py hive_config): the real
// harness, no self-improvement or updates, one worker, check and gate at a
// time, and autonomy 1, so a step past an agent's reach waits on a person who
// never answers and nothing a warden approves runs. Its agents run bare: the
// side's sandbox is the boundary, and no sandbox starts inside another.
func taskConfig(model string) map[string]any {
	h := taskHarness
	sched := map[string]string{}
	for _, s := range []string{"improve.sweep", "improve.stepback", "kb.file", "modules.update", "hive.update", "report.send", "hive.clean"} {
		sched[s] = "off"
	}
	return map[string]any{
		"harness": h, "model": model, "lead_model": model, "reviewer_model": model,
		"auto_update": false, "autonomy": 1, "sandbox": "bare", "bare_accepted": time.Now().UTC().Format(time.RFC3339),
		"mem_floor_mb": 0, "mem_pressure_max": 0, "cpu_pressure_max": 0, "load_max": 0, "plateau_merges": 0,
		"max_workers": 1, "max_checks": 1, "max_gates": 1, "ceilings": true, "schedules": sched,
	}
}

// claudeTool finds Claude Code on this machine, for the side to run: the
// program on PATH, resolved, and the folder to show the side read-only (the
// resolved file's own, unless that would show the home or a login, then the
// file alone).
func claudeTool() (bin, show string, err error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", "", errors.New("no claude on this machine")
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		return "", "", err
	}
	dir := filepath.Dir(p)
	home, _ := os.UserHomeDir()
	if dir == home || dir == "/" {
		return p, p, nil
	}
	for _, n := range []string{".ssh", ".claude", ".config", ".git-credentials", ".credentials.json"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			return p, p, nil
		}
	}
	return p, dir, nil
}

// taskHarness is the sides' harness for the task: the real one, but for this
// package's own test of the plumbing, which runs the fake.
var taskHarness = "claude"

// claudeAccess reads the machine's Claude login (stubbed in tests).
var claudeAccess = func() (string, time.Time, error) { return core.ClaudeAccess("") }

// probeTask has the side's Hive do the gate's real task: a fresh workspace
// with the real harness, the task's seed as its project on a local forge, the
// task given in the lead's chat; then, once it lands, gives up or runs out of
// time, the hidden tests run on the side's main in a sealed sandbox. Its
// pass says the task was run and graded; whether the hidden tests passed is
// the judged metric, so a task both Hives fail is a tie, not "not measured".
func probeTask(cfg *Config, c *Child) Result {
	if cfg.Task == "" {
		return Result{Pass: true, Detail: "no task given: not run"}
	}
	if c.Eval == nil {
		return Result{Pass: true, Detail: "prebuilt binary: no task run"}
	}
	r := prepareTask(cfg)
	if r.err != nil {
		// The gate's own step (the task's folder, its clone): neither side's doing.
		return Result{Pass: true, Detail: "the task couldn't be prepared, so it isn't judged: " + tail(r.err.Error(), 300)}
	}
	wall := cfg.TaskWall
	if wall <= 0 {
		wall = DefaultTaskWall
	}
	claude, show, tok, exp := "", "", "", time.Time{}
	if taskHarness == "claude" {
		var err error
		if claude, show, err = claudeTool(); err != nil {
			return Result{Pass: true, Detail: err.Error() + ": the task isn't run"}
		}
		tok, exp, err = claudeAccess()
		if err != nil || time.Until(exp) < wall+10*time.Minute {
			// Both sides meet the same login; one that can't last a run judges neither.
			return Result{Pass: true, Detail: "no Claude login that outlasts the task here: it isn't run"}
		}
	}
	model := cfg.TaskModel
	if model == "" {
		model = "opus"
	}
	root := filepath.Join(c.Root, "task")
	os.RemoveAll(root)
	spec := TaskSpec{Bin: c.Bin, WS: filepath.Join(root, "ws"), Forge: filepath.Join(root, "forge"), Project: taskProject,
		Prompt: r.task.Prompt(), WallS: wall.Seconds(), Out: filepath.Join(root, "drive.json")}
	login, tools := filepath.Join(root, "login"), filepath.Join(root, "tools")
	for _, d := range []string{filepath.Join(spec.WS, ".hive", "private"), filepath.Join(spec.Forge, "repos"), login, tools} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Result{Detail: err.Error()}
		}
	}
	defer os.RemoveAll(login) // the login goes with the run
	conf, _ := json.MarshalIndent(taskConfig(model), "", "  ")
	creds, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": tok, "expiresAt": exp.UnixMilli()}})
	if tok == "" {
		creds = []byte("{}")
	}
	specRaw, _ := json.Marshal(spec)
	bare := filepath.Join(spec.Forge, "repos", "me__"+taskProject+".git")
	for _, w := range []struct {
		path string
		raw  []byte
		mode os.FileMode
	}{
		{filepath.Join(spec.WS, ".hive", "config.json"), conf, 0o644},
		// A handle list, since the scan on every release and export fails without one.
		{filepath.Join(spec.WS, ".hive", "private", "scan-handles.txt"), []byte("nobody-in-this-gate\n"), 0o600},
		{filepath.Join(login, ".credentials.json"), creds, 0o600},
		{filepath.Join(root, "spec.json"), specRaw, 0o644},
	} {
		if err := os.WriteFile(w.path, w.raw, w.mode); err != nil {
			return Result{Detail: err.Error()}
		}
	}
	if claude != "" {
		if err := os.Symlink(claude, filepath.Join(tools, "claude")); err != nil {
			return Result{Detail: err.Error()}
		}
	}
	for _, a := range [][]string{{"init", "-q", "--bare", "-b", "main", bare}, {"-C", r.seed, "push", "-q", bare, "HEAD:main"}} {
		if out, err := gitRun(root, "git", a...); err != nil {
			return Result{Detail: "seeding its forge: " + tail(out, 300)}
		}
	}
	seedSHA, _ := gitRun(r.seed, "git", "rev-parse", "HEAD")

	// The driver is the gate's own binary, run in the side's sandbox, where
	// the side's daemon listens on that sandbox's own loopback.
	saved := *c
	defer func() { c.RO, c.Extra = saved.RO, saved.Extra }()
	path := ""
	for _, kv := range c.Env() {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	c.RO = append(append([]string{}, c.RO...), show)
	c.Extra = append(append([]string{}, c.Extra...), "PATH="+tools+string(os.PathListSeparator)+path, "CLAUDE_CONFIG_DIR="+login)
	if tok != "" {
		c.Extra = append(c.Extra, "CLAUDE_CODE_OAUTH_TOKEN="+tok)
	}
	t0 := time.Now()
	out, err := c.Exec(wall+10*time.Minute, root, c.fwd(), "gate-task", filepath.Join(root, "spec.json"))
	took := time.Since(t0).Seconds()
	var d TaskDrive
	if raw, e := os.ReadFile(spec.Out); e == nil {
		json.Unmarshal(raw, &d)
	}
	if d.Outcome == "" {
		return Result{Detail: fmt.Sprintf("the task's driver left no outcome (%v): %s", err, tail(out, 300))}
	}
	if strings.HasPrefix(d.Outcome, "error: ") {
		return Result{Detail: tail(d.Outcome, 300)}
	}
	passed, why := 0.0, ""
	switch main, ok, said := gradeTask(cfg, c, r.task, bare); {
	case main == "":
		why = "its main couldn't be read: " + said
	case main == strings.TrimSpace(seedSHA):
		why = "nothing landed on its main"
	case ok:
		passed, why = 1, "the hidden tests pass on its main"
	default:
		why = "the hidden tests fail on its main: " + said
	}
	// Judged: whether the hidden tests pass on what landed. Its time and what
	// it says it spent are shown: a real model's time varies too much for one
	// run a side to judge, and its cost is its own word.
	return Result{Pass: true, Detail: fmt.Sprintf("%s: %s; %s", r.task.ID, d.Outcome, why), Metrics: []Metric{
		{Name: "hidden_tests_passed", Value: passed, Higher: true},
		{Name: "task_seconds", Value: took, Info: true},
		{Name: "task_cost_usd_reported", Value: d.Cost, Info: true},
	}}
}

// gradeTask clones the side's main afresh (through git's transport, so
// nothing of the side's repository config applies here), lays the hidden
// tests over it and runs the task's hidden test command in a sealed sandbox of
// its own: no network, nothing of either Hive. The side never sees the hidden
// tests. It returns the commit it graded.
func gradeTask(cfg *Config, side *Child, t *Task, bare string) (main string, ok bool, said string) {
	g := &Child{Name: "grade-" + side.Name, Root: filepath.Join(cfg.Out, "grade-"+side.Name), Sandbox: side.Sandbox, Go: side.Go}
	os.RemoveAll(g.Root)
	if err := g.Prepare(); err != nil {
		return "", false, err.Error()
	}
	tree := filepath.Join(g.Root, "tree")
	if out, err := gitRun(g.Root, "git", "clone", "-q", "--no-local", "--branch", "main", bare, tree); err != nil {
		return "", false, "clone: " + tail(out, 200)
	}
	head, _ := gitRun(tree, "git", "rev-parse", "HEAD")
	main = strings.TrimSpace(head)
	hidden := filepath.Join(t.Dir, "hidden")
	err := filepath.WalkDir(hidden, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(hidden, p)
		dst := filepath.Join(tree, rel)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		return copyFile(p, dst)
	})
	if err != nil {
		return main, false, "laying the hidden tests over it: " + err.Error()
	}
	g.Extra = []string{"GOFLAGS=", "GOPROXY=off"}
	out, err := g.Exec(time.Duration(t.Timeout)*time.Second, tree, "sh", "-c", t.HiddenTest)
	if err != nil {
		return main, false, tail(out, 300)
	}
	return main, true, ""
}
