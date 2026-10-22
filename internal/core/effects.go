package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// Effects is the one part of the Hive that holds credentials: the forge login
// (through gh), the harness logins, and anything else with reach. Agents ask
// it for actions; it checks who is asking and what they are allowed, then
// acts. Its guarantees are what make the gate safe:
//
//   - commits are taken out of a checkout from inside the sandbox and must
//     descend from the main commit the checkout started at;
//   - only hive/* branches are ever pushed; main changes only by merge;
//   - checks run on the exact commit in a clean checkout the agent can't touch;
//   - a merge is pinned to the commit that passed and was read;
//   - changes to protected paths (goals, the score, the project's settings)
//     are the person's to merge.
type Effects struct {
	ws      Workspace
	j       *Journal
	Forge   Forge
	sandbox func() (sandbox.Sandbox, error)
	tools   func() []string
	runs    *Runs

	locks sync.Map

	mu        sync.Mutex
	checks    map[string]Check
	verdicts  map[string]Verdict
	hostMade  map[string]HostCommit
	approvals map[string]bool
}

// Check is a host-run test or score on one commit.
type Check struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`
	Kind    string    `json:"kind"` // test | score
	OK      bool      `json:"ok"`
	Score   *float64  `json:"score,omitempty"`
	Output  string    `json:"output"`
	At      time.Time `json:"at"`
}

// Verdict is a reviewer's read of one commit.
type Verdict struct {
	Project  string    `json:"project"`
	SHA      string    `json:"sha"`
	Run      string    `json:"run"`
	Verdict  string    `json:"verdict"` // land | fix
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings,omitempty"`
}

// Finding is one thing the reviewer wants fixed.
type Finding struct {
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	Issue string `json:"issue"`
}

// HostCommit is a commit the effects process made itself.
type HostCommit struct {
	Project  string `json:"project"`
	SHA      string `json:"sha"`
	Kind     string `json:"kind"`               // update | revert | setup
	Reviewed string `json:"reviewed,omitempty"` // for updates: the commit whose verdict carries
	Main     string `json:"main,omitempty"`
}

// NewEffects builds the effects process and replays its records.
func NewEffects(ws Workspace, j *Journal, forge Forge, sb func() (sandbox.Sandbox, error), tools func() []string, runs *Runs) *Effects {
	e := &Effects{ws: ws, j: j, Forge: forge, sandbox: sb, tools: tools, runs: runs,
		checks: map[string]Check{}, verdicts: map[string]Verdict{}, hostMade: map[string]HostCommit{}, approvals: map[string]bool{}}
	apply := func(ev Event) {
		e.mu.Lock()
		defer e.mu.Unlock()
		switch ev.Kind {
		case "effect.checked":
			var c Check
			ev.Decode(&c)
			e.checks[c.Project+"@"+c.SHA+"/"+c.Kind] = c
		case "effect.verdict":
			var v Verdict
			ev.Decode(&v)
			e.verdicts[v.Project+"@"+v.SHA] = v
		case "effect.hostcommit":
			var h HostCommit
			ev.Decode(&h)
			e.hostMade[h.Project+"@"+h.SHA] = h
		case "effect.approved":
			var a struct{ Project, SHA string }
			ev.Decode(&a)
			e.approvals[a.Project+"@"+a.SHA] = true
		}
	}
	j.Replay(apply)
	j.Subscribe(apply)
	return e
}

func (e *Effects) lock(project string) func() {
	m, _ := e.locks.LoadOrStore(project, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Clone makes the host's clone of a project if it isn't there yet.
func (e *Effects) Clone(p Project) error {
	defer e.lock(p.Name)()
	dir := e.ws.Project(p.Name)
	if Exists(filepath.Join(dir, ".git")) {
		return nil
	}
	if err := os.MkdirAll(e.ws.Projects(), 0o755); err != nil {
		return err
	}
	if err := e.Forge.Clone(p.Repo, dir); err != nil {
		return err
	}
	excl := filepath.Join(dir, ".git", "info", "exclude")
	os.MkdirAll(filepath.Dir(excl), 0o755)
	f, err := os.OpenFile(excl, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.WriteString("\n.checkouts/\n")
		f.Close()
	}
	Git(dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	return nil
}

// Sync fetches the project and moves the host clone to the tip of main.
// It returns main's commit, or "" for an empty repository.
func (e *Effects) Sync(p Project) (string, error) {
	if err := e.Clone(p); err != nil {
		return "", err
	}
	defer e.lock(p.Name)()
	dir := e.ws.Project(p.Name)
	if _, err := GitT(5*time.Minute, dir, "fetch", "-q", "--prune", "origin"); err != nil {
		return "", err
	}
	sha, err := Git(dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+p.Branch)
	if err != nil || sha == "" {
		return "", nil
	}
	if _, err := Git(dir, "checkout", "-q", "-f", "-B", p.Branch, sha); err != nil {
		return "", err
	}
	return sha, nil
}

// Config reads .hive/project.json at a commit (defaults if absent).
func (e *Effects) Config(p Project, sha string) ProjectConfig {
	raw, _ := Git(e.ws.Project(p.Name), "show", sha+":.hive/project.json")
	return ParseProjectConfig(raw)
}

// ReadFile reads a file at a commit in the host clone.
func (e *Effects) ReadFile(p Project, sha, path string) string {
	raw, _ := Git(e.ws.Project(p.Name), "show", sha+":"+path)
	return raw
}

// NewCheckout makes an agent's own checkout on its own branch at base.
func (e *Effects) NewCheckout(p Project, id, base string) (string, error) {
	dir := filepath.Join(e.ws.Checkouts(p.Name), id)
	if Exists(filepath.Join(dir, ".git")) {
		return dir, nil
	}
	unlock := e.lock(p.Name)
	_, err := GitT(10*time.Minute, "", "clone", "-q", "--no-local", "--no-hardlinks", e.ws.Project(p.Name), dir)
	unlock()
	if err != nil {
		return "", err
	}
	origin := "/hive/projects/" + p.Name
	if sb, err := e.sandbox(); err == nil && !sb.Isolated() {
		origin = e.ws.Project(p.Name)
	}
	if base != "" {
		if _, err := Git(dir, "cat-file", "-e", base+"^{commit}"); err != nil {
			unlock := e.lock(p.Name)
			_, err = Git(dir, "fetch", "-q", "origin", base)
			unlock()
			if err != nil {
				return "", err
			}
		}
		if _, err := Git(dir, "checkout", "-q", "-B", "hive/"+id, base); err != nil {
			return "", err
		}
	} else {
		Git(dir, "checkout", "-q", "--orphan", "hive/"+id)
	}
	Git(dir, "remote", "set-url", "origin", origin)
	Git(dir, "config", "user.name", "Hive")
	Git(dir, "config", "user.email", "hive@localhost")
	Git(dir, "config", "core.autocrlf", "false")
	Git(dir, "config", "core.filemode", "false")
	return dir, nil
}

// ErrNoChanges means the checkout has no commits past its base.
var ErrNoChanges = errors.New("no new commits")

// Extract takes a run's commits out of its checkout. The bundle is made from
// inside the sandbox (the checkout's git metadata is the agent's to shape, so
// host git never runs in it), then fetched into the host clone with object
// checking, and the result must descend from base.
func (e *Effects) Extract(p Project, r Run, base string) (string, error) {
	return e.ExtractInto(e.ws.Project(p.Name), p.Name, r, base)
}

// ExtractInto is Extract into any host-owned repository (the knowledge base uses it).
func (e *Effects) ExtractInto(dir, lockKey string, r Run, base string) (string, error) {
	sb, err := e.sandbox()
	if err != nil {
		return "", err
	}
	out := filepath.Join(e.ws.RunHost(r.ID), "out")
	os.RemoveAll(out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	work := sandbox.Mount{Host: r.Checkout, Inside: "/work"}
	outM := sandbox.Mount{Host: out, Inside: "/hive/out", RW: true}
	bundle := sandbox.InsidePath(sb, outM) + "/run.bundle"
	rangeSpec := "HEAD"
	if base != "" {
		rangeSpec = base + "..HEAD"
	}
	script := fmt.Sprintf("git -c safe.directory='*' -C %s bundle create %s %s", shq(sandbox.InsidePath(sb, work)), shq(bundle), rangeSpec)
	code, logText, err := e.runSandboxed(sb, r.ID+"-extract", []sandbox.Mount{work, outM}, script, false, 5*time.Minute)
	if err != nil {
		return "", err
	}
	if code != 0 {
		if strings.Contains(logText, "empty bundle") || strings.Contains(logText, "Refusing to create") {
			return "", ErrNoChanges
		}
		return "", fmt.Errorf("could not take commits out: %s", Clip(logText, 300))
	}
	hostBundle := filepath.Join(out, "run.bundle")
	if fi, err := os.Lstat(hostBundle); err != nil || !fi.Mode().IsRegular() {
		return "", errors.New("no bundle produced")
	}
	defer e.lock(lockKey)()
	if _, err := Git(dir, "bundle", "verify", "-q", hostBundle); err != nil {
		return "", fmt.Errorf("bundle failed verification: %v", err)
	}
	ref := "refs/hive/runs/" + r.ID
	if _, err := GitT(5*time.Minute, dir, "fetch", "-q", "--no-tags", hostBundle, "+HEAD:"+ref); err != nil {
		return "", err
	}
	sha, err := Git(dir, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	if base != "" {
		if sha == base {
			return "", ErrNoChanges
		}
		if _, err := Git(dir, "merge-base", "--is-ancestor", base, sha); err != nil {
			return "", fmt.Errorf("commit %s does not descend from main at %s; refusing it", short(sha), short(base))
		}
	}
	return sha, nil
}

// Push publishes sha to a hive/* branch. Nothing else is ever pushed.
func (e *Effects) Push(p Project, branch, sha string) error {
	if !strings.HasPrefix(branch, "hive/") {
		return fmt.Errorf("the effects process only pushes hive/* branches, not %q", branch)
	}
	defer e.lock(p.Name)()
	if _, err := GitT(5*time.Minute, e.ws.Project(p.Name), "push", "-q", "-f", "origin", sha+":refs/heads/"+branch); err != nil {
		return err
	}
	e.j.MustAppend("effect.pushed", map[string]string{"project": p.Name, "branch": branch, "sha": sha})
	return nil
}

// RunCheck runs the project's test or score command on exactly sha in a clean
// checkout inside the sandbox. The command comes from main's settings, so a
// change can't rewrite the check it is judged by.
func (e *Effects) RunCheck(p Project, sha, kind, command string) Check {
	c := Check{Project: p.Name, SHA: sha, Kind: kind, At: time.Now().UTC()}
	record := func() Check {
		c.Output = Clip(c.Output, 4000)
		e.j.MustAppend("effect.checked", c)
		return c
	}
	if strings.TrimSpace(command) == "" {
		c.OK, c.Output = !e.hadCommand(p.Name, kind), "no "+kind+" command configured"
		if !c.OK {
			c.Output = "the " + kind + " command was removed from main's settings; the person restores it"
		}
		return record()
	}
	sb, err := e.sandbox()
	if err != nil {
		c.Output = err.Error()
		return c // not recorded: no sandbox is not a verdict on the commit
	}
	dir, err := e.cleanCheckout(p, sha, kind)
	if err != nil {
		c.Output = "could not make a clean checkout: " + err.Error()
		return c
	}
	defer os.RemoveAll(dir)
	code, out, err := e.runSandboxed(sb, "gate-"+short(sha)+"-"+kind+"-"+NewID()[14:], []sandbox.Mount{{Host: dir, Inside: "/work", RW: true}}, command, true, 30*time.Minute)
	c.Output = out
	if err != nil {
		c.Output = err.Error() + "\n" + out
		return record()
	}
	c.OK = code == 0
	if kind == "score" && c.OK {
		v, perr := lastNumber(out)
		if perr != nil {
			c.OK, c.Output = false, "score command did not end with a number: "+Clip(out, 500)
		} else {
			c.Score = &v
		}
	}
	return record()
}

func lastNumber(out string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		f := strings.Fields(l)
		return strconv.ParseFloat(f[len(f)-1], 64)
	}
	return 0, errors.New("no output")
}

// cleanCheckout clones the host clone fresh and checks out sha.
func (e *Effects) cleanCheckout(p Project, sha, kind string) (string, error) {
	dir := filepath.Join(e.ws.Gates(), fmt.Sprintf("%s-%s-%s-%s", p.Name, short(sha), kind, NewID()[14:]))
	unlock := e.lock(p.Name)
	_, err := GitT(10*time.Minute, "", "clone", "-q", "--no-local", "--no-hardlinks", "--no-checkout", e.ws.Project(p.Name), dir)
	if err == nil {
		_, err = GitT(5*time.Minute, dir, "fetch", "-q", "origin", sha)
	}
	unlock()
	if err == nil {
		_, err = Git(dir, "checkout", "-q", "--detach", sha)
	}
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// runSandboxed runs a shell command in the sandbox and returns its exit code and output.
func (e *Effects) runSandboxed(sb sandbox.Sandbox, id string, mounts []sandbox.Mount, script string, network bool, timeout time.Duration) (int, string, error) {
	logDir := filepath.Join(e.ws.Hive(), "checks")
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, id+".log")
	os.Remove(logPath)
	home, _ := os.MkdirTemp("", "hive-check-home-")
	defer os.RemoveAll(home)
	homeM := sandbox.Mount{Host: home, Inside: "/hive/home", RW: true}
	mounts = append(mounts, homeM)
	argv := []string{"/bin/sh", "-c", script}
	env := sandbox.InsideEnv("/hive/home", network)
	if !sb.Isolated() {
		env = nil
		argv = hostShell(script)
	}
	spec := sandbox.Spec{ID: id, Argv: argv, Env: env, Cwd: sandbox.InsidePath(sb, mounts[0]), Mounts: mounts, Tools: e.tools(),
		Stdout: logPath, Network: network, UID: os.Getuid(), GID: os.Getgid()}
	code, err := sb.Run(spec, timeout)
	raw, _ := os.ReadFile(logPath)
	errRaw, _ := os.ReadFile(logPath + ".err")
	out := string(raw)
	if len(errRaw) > 0 {
		out += "\n" + string(errRaw)
	}
	if wsl, _ := os.ReadFile(logPath + ".wsl"); len(wsl) > 0 {
		out += "\n" + string(wsl)
	}
	return code, out, err
}

func hostShell(script string) []string {
	if runtime.GOOS == "windows" {
		return []string{WindowsShell(), "-c", script}
	}
	return []string{"/bin/sh", "-c", script}
}

// WindowsShell finds a POSIX sh on Windows: on PATH, or the one Git for Windows ships.
func WindowsShell() string {
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	if g, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(g)) // ...\Git\cmd\git.exe -> ...\Git
		for _, c := range []string{`bin\sh.exe`, `usr\bin\sh.exe`} {
			if p := filepath.Join(root, c); Exists(p) {
				return p
			}
		}
	}
	return "sh"
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// hadCommand reports whether a check of this kind ever ran a real command for
// the project, so a command that later vanishes reads as red, not as none.
func (e *Effects) hadCommand(project, kind string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.checks {
		if c.Project == project && c.Kind == kind && c.Output != "no "+kind+" command configured" {
			return true
		}
	}
	return false
}

// GetCheck returns the recorded check for a commit.
func (e *Effects) GetCheck(project, sha, kind string) (Check, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.checks[project+"@"+sha+"/"+kind]
	return c, ok
}

// RecordVerdict accepts a verdict only from a read-only reviewer run the Hive
// started on exactly that commit.
func (e *Effects) RecordVerdict(v Verdict) error {
	r, ok := e.runs.Get(v.Run)
	if !ok || r.Role != "reviewer" || !r.ReadOnly || r.Inputs["sha"] != v.SHA || r.Project != v.Project {
		return fmt.Errorf("verdict from %s is not bound to commit %s", v.Run, short(v.SHA))
	}
	if v.Verdict != "land" && v.Verdict != "fix" {
		return fmt.Errorf("verdict must be land or fix")
	}
	e.j.MustAppend("effect.verdict", v)
	return nil
}

// GetVerdict returns the verdict on a commit.
func (e *Effects) GetVerdict(project, sha string) (Verdict, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.verdicts[project+"@"+sha]
	return v, ok
}

// Approve records the person's approval of a commit (protected paths).
func (e *Effects) Approve(project, sha string) {
	e.j.MustAppend("effect.approved", map[string]string{"project": project, "sha": sha})
}

// Approved reports whether the person approved sha.
func (e *Effects) Approved(project, sha string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.approvals[project+"@"+sha]
}

// HostMade returns a host-authored commit record.
func (e *Effects) HostMade(project, sha string) (HostCommit, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.hostMade[project+"@"+sha]
	return h, ok
}

// Change describes a commit relative to main.
type Change struct {
	Base      string   `json:"base"`
	Files     []string `json:"files"`
	Lines     int      `json:"lines"`
	Small     bool     `json:"small"`
	Protected []string `json:"protected,omitempty"`
	Diff      string   `json:"-"`
}

var docExt = []string{".md", ".txt", ".rst", ".adoc"}

// Classify measures a change against main. Small means docs only and few lines.
func (e *Effects) Classify(p Project, mainSHA, sha string) (Change, error) {
	dir := e.ws.Project(p.Name)
	base, err := Git(dir, "merge-base", mainSHA, sha)
	if err != nil {
		return Change{}, err
	}
	c := Change{Base: base}
	// No renames: a move out of a protected path must show as its deletion.
	stat, err := Git(dir, "diff", "--numstat", "--no-renames", base, sha)
	if err != nil {
		return c, err
	}
	cfg := e.Config(p, mainSHA)
	protected := append([]string{".hive/project.json", ".hive/goals.md"}, cfg.Protected...)
	docsOnly := true
	for _, l := range strings.Split(stat, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		add, _ := strconv.Atoi(f[0])
		del, _ := strconv.Atoi(f[1])
		file := strings.Join(f[2:], " ")
		c.Files = append(c.Files, file)
		c.Lines += add + del
		for _, pp := range protected {
			if file == pp || strings.HasPrefix(file, strings.TrimSuffix(pp, "/")+"/") {
				c.Protected = append(c.Protected, file)
			}
		}
		isDoc := strings.HasPrefix(file, "docs/") || file == ".hive/lessons.md"
		for _, x := range docExt {
			if strings.HasSuffix(strings.ToLower(file), x) {
				isDoc = true
			}
		}
		if !isDoc {
			docsOnly = false
		}
	}
	c.Small = docsOnly && len(c.Files) > 0 && c.Lines <= cfg.SmallMax && len(c.Protected) == 0
	c.Diff, _ = Git(dir, "diff", base, sha)
	return c, nil
}

// MergeRequest is everything the gate needs to merge a pull request.
type MergeRequest struct {
	Project Project
	PR      int
	SHA     string
	Main    string
	Subject string
}

// Merge merges the PR only at sha, and only when the host's own records show
// that sha passed its tests and was read (or needed no read).
func (e *Effects) Merge(m MergeRequest) (string, error) {
	p := m.Project
	if c, ok := e.GetCheck(p.Name, m.SHA, "test"); !ok || !c.OK {
		return "", fmt.Errorf("refusing to merge %s: no passing test run recorded for that exact commit", short(m.SHA))
	}
	ch, err := e.Classify(p, m.Main, m.SHA)
	if err != nil {
		return "", err
	}
	if len(ch.Protected) > 0 && !e.Approved(p.Name, m.SHA) {
		return "", &NeedsPerson{Reason: "it changes " + strings.Join(ch.Protected, ", ") + ", which the person merges"}
	}
	if !ch.Small && !e.readOK(p.Name, m.SHA) {
		return "", fmt.Errorf("refusing to merge %s: no land verdict bound to that exact commit", short(m.SHA))
	}
	merged, err := e.Forge.MergePR(p.Repo, m.PR, m.SHA, m.Subject)
	if err != nil {
		return "", err
	}
	e.j.MustAppend("effect.merged", map[string]any{"project": p.Name, "pr": m.PR, "sha": m.SHA, "merge_sha": merged})
	return merged, nil
}

// NeedsPerson means only the person can take this step.
type NeedsPerson struct{ Reason string }

func (n *NeedsPerson) Error() string { return "needs the person: " + n.Reason }

func (e *Effects) readOK(project, sha string) bool {
	if v, ok := e.GetVerdict(project, sha); ok {
		return v.Verdict == "land"
	}
	h, ok := e.HostMade(project, sha)
	if !ok {
		return false
	}
	switch h.Kind {
	case "revert", "setup":
		return true
	case "update":
		v, ok := e.GetVerdict(project, h.Reviewed)
		return ok && v.Verdict == "land"
	}
	return false
}

// Reviewed reports whether sha has the read it needs to merge.
func (e *Effects) Reviewed(project, sha string) bool { return e.readOK(project, sha) }

// UpdateBranch merges main into the reviewed commit in a clean host checkout.
// The result is host-made, so the verdict on the reviewed commit carries, but
// it is tested again before it can merge.
func (e *Effects) UpdateBranch(p Project, sha, mainSHA string) (string, error) {
	dir, err := e.cleanCheckout(p, sha, "update")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := Git(dir, "fetch", "-q", "origin", mainSHA); err != nil {
		return "", err
	}
	if _, err := Git(dir, "merge", "-q", "--no-edit", "-m", "Merge main into this branch", mainSHA); err != nil {
		Git(dir, "merge", "--abort")
		return "", ErrConflict
	}
	return e.hostCommit(p, dir, HostCommit{Project: p.Name, Kind: "update", Reviewed: sha, Main: mainSHA})
}

// ErrConflict means a branch can't be brought up to date without the worker.
var ErrConflict = errors.New("conflicts with main")

// Revert makes a commit on top of main that reverts mergeSHA and records the
// lesson where the next worker reads it.
func (e *Effects) Revert(p Project, mainSHA, mergeSHA, lesson string) (string, error) {
	dir, err := e.cleanCheckout(p, mainSHA, "revert")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := Git(dir, "revert", "--no-edit", mergeSHA); err != nil {
		Git(dir, "revert", "--abort")
		return "", fmt.Errorf("revert of %s does not apply cleanly: %v", short(mergeSHA), err)
	}
	if err := appendLine(filepath.Join(dir, ".hive", "lessons.md"), "# Lessons\n\nWhat was tried and why it didn't stay. Read before working here.\n\n", lesson); err != nil {
		return "", err
	}
	Git(dir, "add", ".hive/lessons.md")
	if _, err := Git(dir, "commit", "-q", "--amend", "--no-edit"); err != nil {
		return "", err
	}
	return e.hostCommit(p, dir, HostCommit{Project: p.Name, Kind: "revert", Main: mainSHA})
}

// Setup writes files as a host-made commit on top of base (or as the first
// commit of an empty repository).
func (e *Effects) Setup(p Project, base string, files map[string]string, message string) (string, error) {
	dir := filepath.Join(e.ws.Gates(), p.Name+"-setup-"+NewID()[14:])
	var err error
	if base != "" {
		dir, err = e.cleanCheckout(p, base, "setup")
	} else {
		err = os.MkdirAll(dir, 0o755)
		if err == nil {
			_, err = Git(dir, "init", "-q", "-b", p.Branch)
		}
	}
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	Git(dir, "add", "-A")
	if _, err := Git(dir, "commit", "-q", "-m", message); err != nil {
		return "", err
	}
	if base == "" {
		Git(dir, "remote", "add", "origin", e.ws.Project(p.Name))
	}
	return e.hostCommit(p, dir, HostCommit{Project: p.Name, Kind: "setup", Main: base})
}

// PushInitial publishes the first commit of an empty repository to main.
// This is the only push to a non-hive branch, and only into an empty repo.
func (e *Effects) PushInitial(p Project, sha string) error {
	h, ok := e.HostMade(p.Name, sha)
	if !ok || h.Kind != "setup" || h.Main != "" {
		return errors.New("only a host-made first commit may be pushed to an empty repository's main")
	}
	defer e.lock(p.Name)()
	dir := e.ws.Project(p.Name)
	if out, _ := Git(dir, "ls-remote", "origin", "refs/heads/"+p.Branch); out != "" {
		return errors.New("repository is not empty; changes go through a pull request")
	}
	_, err := Git(dir, "push", "-q", "origin", sha+":refs/heads/"+p.Branch)
	return err
}

// hostCommit moves a commit made in a gate clone into the host clone.
func (e *Effects) hostCommit(p Project, dir string, h HostCommit) (string, error) {
	sha, err := Git(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	unlock := e.lock(p.Name)
	_, err = Git(e.ws.Project(p.Name), "fetch", "-q", dir, "+HEAD:refs/hive/host/"+short(sha))
	unlock()
	if err != nil {
		return "", err
	}
	h.SHA = sha
	e.j.MustAppend("effect.hostcommit", h)
	return sha, nil
}

// MainBundle writes main as a bundle a worker can fetch to resolve conflicts.
func (e *Effects) MainBundle(p Project, path string) error {
	defer e.lock(p.Name)()
	_, err := Git(e.ws.Project(p.Name), "bundle", "create", path, p.Branch)
	return err
}

func appendLine(path, header, line string) error {
	os.MkdirAll(filepath.Dir(path), 0o755)
	raw, err := os.ReadFile(path)
	content := string(raw)
	if err != nil || strings.TrimSpace(content) == "" {
		content = header
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "- " + strings.TrimSpace(line) + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}
