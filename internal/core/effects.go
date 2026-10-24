package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
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
//   - changes to protected paths (goals, the score and the files it names,
//     the project's settings) are the person's to merge;
//   - a change to a high-class path needs two separate land reads of its
//     exact commit, and so does every change to a self project (one read).
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
	lands     map[string][]string // project@sha -> reviewer runs that said land, since the last fix
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
	Log     string    `json:"log,omitempty"` // the full output on the host, when it ran
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
	Advice   []string  `json:"advice,omitempty"` // with a land: worth doing later, not a blocker
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
		checks: map[string]Check{}, verdicts: map[string]Verdict{}, lands: map[string][]string{}, hostMade: map[string]HostCommit{}, approvals: map[string]bool{}}
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
			if k := v.Project + "@" + v.SHA; v.Verdict == "land" && !slices.Contains(e.lands[k], v.Run) {
				e.lands[k] = append(e.lands[k], v.Run)
			} else if v.Verdict != "land" {
				delete(e.lands, k)
			}
		case "effect.hostcommit":
			var h HostCommit
			ev.Decode(&h)
			e.hostMade[h.Project+"@"+h.SHA] = h
		case "effect.approved":
			var a struct{ Kind, Project, SHA string }
			ev.Decode(&a)
			if a.Kind == "" {
				a.Kind = "merge"
			}
			e.approvals[a.Kind+":"+a.Project+"@"+a.SHA] = true
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
	if _, err := GitEnvT(5*time.Minute, dir, ghEnv(p.Repo), "fetch", "-q", "--prune", "origin"); err != nil {
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

// ScoreVersion names the score at sha: its command and the content of its
// evaluator paths. Scores of two versions are never compared.
func (e *Effects) ScoreVersion(p Project, sha string, s Score) string {
	tree := ""
	if paths := s.Evaluator(); len(paths) > 0 {
		tree, _ = Git(e.ws.Project(p.Name), append([]string{"ls-tree", sha, "--"}, paths...)...)
	}
	sum := sha256.Sum256([]byte(s.Command + "\x00" + tree))
	return hex.EncodeToString(sum[:6])
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

// ErrNotDescendant means the run's newest commit doesn't build on its base,
// as when a worker rewinds past a commit already merged.
var ErrNotDescendant = errors.New("does not descend from its base")

// Extract takes a run's commits out of its checkout. The bundle is made from
// inside the sandbox (the checkout's git metadata is the agent's to shape, so
// host git never runs in it), then fetched into the host clone with object
// checking, and the result must descend from base.
func (e *Effects) Extract(p Project, r Run, base string) (string, error) {
	return e.ExtractInto(e.ws.Project(p.Name), p.Name, r, base)
}

// Snapshot commits whatever a run left in its checkout, from inside the
// sandbox with no network, and then takes it out as Extract does.
func (e *Effects) Snapshot(p Project, r Run, base string) (string, error) {
	sb, err := e.sandbox()
	if err != nil {
		return "", err
	}
	work := sandbox.Mount{Host: r.Checkout, Inside: "/work", RW: true}
	git := "git -c safe.directory='*' -c core.hooksPath=/dev/null -c user.name=Hive -c user.email=hive@localhost -C " + shq(sandbox.InsidePath(sb, work))
	script := git + " add -A && { " + git + " diff --cached --quiet || " + git + " commit -q --no-verify -m 'what the run left'; }"
	if code, out, err := e.runSandboxed(sb, r.Project, r.ID+"-snapshot", []sandbox.Mount{work}, script, false, 5*time.Minute); err != nil || code != 0 {
		return "", fmt.Errorf("could not commit what the run left: %v %s", err, Clip(out, 300))
	}
	return e.Extract(p, r, base)
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
	code, logText, err := e.runSandboxed(sb, r.Project, r.ID+"-extract", []sandbox.Mount{work, outM}, script, false, 5*time.Minute)
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
			return "", fmt.Errorf("commit %s does not descend from main at %s; refusing it: %w", short(sha), short(base), ErrNotDescendant)
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
	if _, err := GitEnvT(5*time.Minute, e.ws.Project(p.Name), ghEnv(p.Repo), "push", "-q", "-f", "origin", sha+":refs/heads/"+branch); err != nil {
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
	id := "gate-" + short(sha) + "-" + kind + "-" + NewID()[14:]
	c.Log = filepath.Join(e.ws.Hive(), "checks", p.Name, id+".log")
	code, out, err := e.runSandboxed(sb, p.Name, id, []sandbox.Mount{{Host: dir, Inside: "/work", RW: true}}, guardProgram(command), true, 30*time.Minute)
	c.Output = out
	if err != nil {
		c.Output = err.Error() + "\n" + out
		return record()
	}
	c.OK = code == 0
	if kind == "score" && c.OK {
		v, perr := scoreOf(out)
		if perr != nil {
			c.OK, c.Output = false, "score command printed no number at the end or on one line of its own: "+Clip(out, 500)
		} else {
			c.Score = &v
		}
	}
	return record()
}

// scoreOf reads the score from a score command's output: the last word of
// the last line ("score: 42"), or failing that the one line that is a number
// alone, so a bench can print its result and then per-run detail. Two such
// lines are ambiguous and give no score, and NaN or Inf is never a score.
func scoreOf(out string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if f := strings.Fields(lines[len(lines)-1]); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[len(f)-1], 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, nil
		}
	}
	var alone []float64
	for _, l := range lines {
		if v, err := strconv.ParseFloat(strings.TrimSpace(l), 64); err == nil {
			alone = append(alone, v)
		}
	}
	if len(alone) == 1 && !math.IsNaN(alone[0]) && !math.IsInf(alone[0], 0) {
		return alone[0], nil
	}
	return 0, errors.New("no number")
}

var (
	lookupProgram = regexp.MustCompile(`^(\s*([A-Za-z_]\w*)=\$\(([^()]*)\)\s*;)\s*"?\$(?:\{([A-Za-z_]\w*)\}|([A-Za-z_]\w*))(?:["\s;]|$)`)
	lookedUp      = regexp.MustCompile(`command -v\s+([\w.+-]+)`)
)

// guardProgram stops a command whose program is a variable set by a lookup
// (PY=$(command -v python || command -v python3); $PY -m ...) when the lookup
// finds nothing, so the shell never runs the arguments as the program. Only the
// first statement's assignment followed by a bare $VAR or ${VAR} as the program
// is recognised: ${VAR:-default} has its own fallback, and a later use (after a
// cd, say) is not guarded.
func guardProgram(command string) string {
	s := lookupProgram.FindStringSubmatch(command)
	if s == nil || s[2] != s[4]+s[5] {
		return command
	}
	var names []string
	for _, n := range lookedUp.FindAllStringSubmatch(s[3], -1) {
		names = append(names, n[1])
	}
	missing := strings.Join(names, " or ")
	if missing == "" {
		missing = "the program in " + s[2]
	}
	return s[1] + ` [ -n "$` + s[2] + `" ] || { echo "hive: missing program: ` + missing + `" >&2; exit 127; };` + command[len(s[1]):]
}

// cleanCheckout clones the host clone fresh and checks out sha.
func (e *Effects) cleanCheckout(p Project, sha, kind string) (string, error) {
	dir := filepath.Join(e.ws.Gates(), p.Name, fmt.Sprintf("%s-%s-%s", short(sha), kind, NewID()[14:]))
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

// runSandboxed runs a shell command in the sandbox and returns its exit code
// and output. Its log goes in the project's own folder, which other runs
// can't see when the project is isolated.
func (e *Effects) runSandboxed(sb sandbox.Sandbox, project, id string, mounts []sandbox.Mount, script string, network bool, timeout time.Duration) (int, string, error) {
	logDir := filepath.Join(e.ws.Hive(), "checks", project)
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, id+".log")
	os.Remove(logPath)
	home, _ := os.MkdirTemp("", "hive-check-home-")
	defer os.RemoveAll(home)
	homeM := sandbox.Mount{Host: home, Inside: "/hive/home", RW: true}
	mounts = append(mounts, homeM, sandbox.Mount{Host: e.ws.Tools(), Inside: sandbox.ToolsInside})
	argv := []string{"/bin/sh", "-c", script}
	env := sandbox.InsideEnv("/hive/home", network)
	if !sb.Isolated() {
		env = nil
		argv = hostShell(script)
	}
	spec := sandbox.Spec{ID: id, Argv: argv, Env: env, Cwd: sandbox.InsidePath(sb, mounts[0]), Mounts: mounts, Tools: e.tools(),
		Stdout: logPath, Network: network, Allow: e.ws.LoadConfig().Egress.For("check"), Local: e.ws.LoadConfig().Egress.LocalAddrs(), UID: os.Getuid(), GID: os.Getgid()}
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

// Reads returns the reviewer runs that said land on exactly sha since its last fix.
func (e *Effects) Reads(project, sha string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.lands[project+"@"+sha])
}

// Approve records the approval of a commit (the person's, or a warden's
// when autonomy lets it decide): kind "merge" for a head
// that changes protected paths, "release" for a merge held before release.
// The kinds stay apart, since a fast-forwarding forge merges a head as itself.
func (e *Effects) Approve(kind, project, sha string) {
	e.j.MustAppend("effect.approved", map[string]string{"kind": kind, "project": project, "sha": sha})
}

// Approved reports whether the person approved sha for kind.
func (e *Effects) Approved(kind, project, sha string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.approvals[kind+":"+project+"@"+sha]
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
	High      []string `json:"high,omitempty"` // files in a high or root class
	Root      []string `json:"root,omitempty"` // files that run with elevated rights
	Diff      string   `json:"-"`
	// ProtectedDiff is the diff of the protected files alone, shown to the person who approves it.
	ProtectedDiff string `json:"-"`
}

var docExt = []string{".md", ".txt", ".rst", ".adoc"}

// docImage are the other types that count as docs, and only under docs/:
// a script there is code like anywhere else.
var docImage = []string{".png", ".jpg", ".jpeg", ".gif", ".webp"}

// Classify measures a change against main. Small means docs only and few
// lines, judged by file type: a script under docs/ is code.
func (e *Effects) Classify(p Project, mainSHA, sha string) (Change, error) {
	dir := e.ws.Project(p.Name)
	base, err := Git(dir, "merge-base", mainSHA, sha)
	if err != nil {
		return Change{}, err
	}
	c := Change{Base: base}
	// No renames: a move out of a protected path must show as its deletion.
	// Unquoted and NUL-split, so a name git would quote (é, a tab, a quote)
	// is read as the path it is and can't slip past a guard.
	stat, err := Git(dir, "-c", "core.quotePath=false", "diff", "--numstat", "-z", "--no-renames", base, sha)
	if err != nil {
		return c, err
	}
	cfg := e.Config(p, mainSHA).Guarded(p)
	protected := append([]string{".hive/project.json", ".hive/goals.md", "docs/REVIEW.md"}, cfg.Protected...)
	if cfg.Score != nil {
		protected = append(protected, cfg.Score.Evaluator()...)
	}
	docsOnly := true
	for _, l := range strings.Split(stat, "\x00") {
		f := strings.SplitN(l, "\t", 3)
		if len(f) < 3 || f[2] == "" {
			continue
		}
		add, _ := strconv.Atoi(f[0])
		del, _ := strconv.Atoi(f[1])
		file := f[2]
		c.Files = append(c.Files, file)
		c.Lines += add + del
		if under(file, protected) {
			c.Protected = append(c.Protected, file)
		}
		if under(file, cfg.Root) {
			c.Root = append(c.Root, file)
		}
		if under(file, cfg.High) || under(file, cfg.Root) {
			c.High = append(c.High, file)
		}
		ext := strings.ToLower(filepath.Ext(file))
		if !slices.Contains(docExt, ext) && file != ".hive/lessons.md" &&
			!(strings.HasPrefix(file, "docs/") && slices.Contains(docImage, ext)) {
			docsOnly = false
		}
	}
	c.Small = docsOnly && len(c.Files) > 0 && c.Lines <= cfg.SmallMax && len(c.Protected) == 0 && len(c.High) == 0 && !p.Self
	c.Diff, _ = Git(dir, "diff", base, sha)
	if len(c.Protected) > 0 {
		c.ProtectedDiff, _ = Git(dir, append([]string{"--literal-pathspecs", "diff", "--no-renames", base, sha, "--"}, c.Protected...)...)
	}
	return c, nil
}

// under reports whether file is one of paths or inside one of them.
func under(file string, paths []string) bool {
	for _, pp := range paths {
		if file == pp || strings.HasPrefix(file, strings.TrimSuffix(pp, "/")+"/") {
			return true
		}
	}
	return false
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
	if len(ch.Protected) > 0 && !e.Approved("merge", p.Name, m.SHA) {
		return "", &NeedsPerson{Reason: "it changes " + strings.Join(ch.Protected, ", ") + ", which needs an approval first"}
	}
	if !ch.Small && !e.readOK(p, ch, m.SHA) {
		return "", fmt.Errorf("refusing to merge %s: no land verdict bound to that exact commit", short(m.SHA))
	}
	merged, err := e.Forge.MergePR(p.Repo, m.PR, m.SHA, m.Subject)
	if err != nil {
		return "", err
	}
	e.j.MustAppend("effect.merged", map[string]any{"project": p.Name, "pr": m.PR, "sha": m.SHA, "merge_sha": merged, "root": ch.Root})
	return merged, nil
}

// NeedsPerson means only the person can take this step.
type NeedsPerson struct{ Reason string }

func (n *NeedsPerson) Error() string { return "needs the person: " + n.Reason }

func (e *Effects) readOK(p Project, ch Change, sha string) bool {
	project := p.Name
	if p.Self || len(ch.High) > 0 {
		// A release names the read of each merged head, so no verdict carries
		// over from another commit; a high class needs a second, separate read.
		need := 1
		if len(ch.High) > 0 {
			need = 2
		}
		return len(e.Reads(project, sha)) >= need
	}
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

// Reviewed reports whether sha, classified as ch, has the reads it needs to merge.
func (e *Effects) Reviewed(p Project, ch Change, sha string) bool { return e.readOK(p, ch, sha) }

// UpdateBranch merges main into the reviewed commit in a clean host checkout.
// The result is host-made, so the verdict on the reviewed commit carries, but
// it is tested again before it can merge. A base names the commit the merge
// is taken from instead of the common ancestor: the branch's own commit that
// main already holds as a squashed checkpoint, so only what came after it is
// laid onto main.
func (e *Effects) UpdateBranch(p Project, sha, mainSHA, base string) (string, error) {
	dir, err := e.cleanCheckout(p, sha, "update")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if _, err := Git(dir, "fetch", "-q", "origin", mainSHA); err != nil {
		return "", err
	}
	if base != "" {
		// Exit status 1 is a conflict; anything else (such as a git older
		// than 2.40, without --merge-base) is an error, not the worker's to fix.
		tree, err := Git(dir, "merge-tree", "--write-tree", "--merge-base="+base, sha, mainSHA)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", ErrConflict
		}
		if err != nil {
			return "", fmt.Errorf("git merge-tree --merge-base (needs git 2.40 or later): %w", err)
		}
		c, err := Git(dir, "commit-tree", tree, "-p", sha, "-p", mainSHA, "-m", "Merge main into this branch")
		if err == nil {
			_, err = Git(dir, "reset", "-q", "--hard", c)
		}
		if err != nil {
			return "", err
		}
	} else if _, err := Git(dir, "merge", "-q", "--no-edit", "-m", "Merge main into this branch", mainSHA); err != nil {
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
	dir := filepath.Join(e.ws.Gates(), p.Name, "setup-"+NewID()[14:])
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
	if out, _ := GitEnvT(2*time.Minute, dir, ghEnv(p.Repo), "ls-remote", "origin", "refs/heads/"+p.Branch); out != "" {
		return errors.New("repository is not empty; changes go through a pull request")
	}
	_, err := GitEnvT(5*time.Minute, dir, ghEnv(p.Repo), "push", "-q", "origin", sha+":refs/heads/"+p.Branch)
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
// It is written under a temp name and renamed, so a fetch running now sees the old or the new file whole.
func (e *Effects) MainBundle(p Project, path string) error {
	defer e.lock(p.Name)()
	tmp := path + ".tmp"
	if _, err := Git(e.ws.Project(p.Name), "bundle", "create", tmp, p.Branch); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
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
