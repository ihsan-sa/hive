package core

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path"
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
//   - a merge is pinned to the commit that passed and was read, and waits
//     for every one of the forge's own checks to pass on it;
//   - a read is of a change's own diff, so it holds for a later head only
//     while that diff is the same;
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
	sweep   func() int // sandbox.Sweep; a test points it away from the host

	locks  sync.Map
	unread sync.Map // project@main whose history MainAnswer couldn't read, journaled once

	// MergeHeld is why a pull request on a project may not merge now (the
	// person's hold on its merges, the work module), or "" if it may.
	MergeHeld func(project string, pr int) string

	// Quarantined are a project's known flaky tests (the improve module's
	// quarantine), keyed package::test, as shown by evidence from outside
	// task (none of its own branches' reds counts for it): a forge check
	// that fails only these doesn't hold task's merge. Unset, none are.
	Quarantined func(project, task string) []string

	// Slots holds checks to config's max_checks at a time, started in the order they asked.
	Slots *Slots

	mu        sync.Mutex
	checks    map[string]Check
	verdicts  map[string]Verdict
	lands     map[string][]string               // project@sha -> reviewer runs that said land, since the last fix
	diffLands map[string][]string               // project#diff -> the same, for every head with that own diff
	diffVerds map[string]Verdict                // project#diff -> the latest verdict on that own diff
	diffHeads map[string][]string               // project#diff -> the heads a verdict on that own diff was given on
	outside   map[string]map[string]OutsideRead // project#diff -> reader -> their latest read
	seats     map[string]map[string]Verdict     // project#diff -> security seat -> its latest verdict
	hostMade  map[string]HostCommit
	pushes    map[string][]string // project@branch -> each commit pushed to it, oldest first
	approvals map[string]bool
	failing   map[string]CheckFailure // repo url -> what a failed check's log says
	steps     map[string][]JobStep    // repo url -> a finished check's steps
	gates     map[string]GateResult   // project@head@main -> the recursive gate's verdict (selfgate.go)
	gateMains map[string]GateResult   // project@main -> the newest can't-run there not tied to its head (GateAtMain)
	standing  map[string]bool         // project@head@verdict's main@main -> whether that verdict still stands there (StandingGate)
	judged    map[string]Judgement    // project@sha@main -> the judge's newest call (judge.go)
	homes     map[string]bool         // folders in use that Prune leaves: running checks' homes and clean checkouts (disk.go)
	modMu     sync.RWMutex            // read-held while a check fetches into or reads the module cache; Prune empties it only under the write lock
	buildMu   sync.RWMutex            // the same for the projects' build caches
	disk      DiskUse                 // the last measurement of the Hive's disk use
	diskFull  bool                    // a check couldn't run for room, and the alert is out

	// OnDiskFull runs once when checks first can't run for room (the clean job).
	OnDiskFull func()
}

// Check is a host-run test or score on one commit.
type Check struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`
	Kind    string    `json:"kind"` // test | score
	OK      bool      `json:"ok"`
	Score   *float64  `json:"score,omitempty"`
	Output  string    `json:"output"`
	CantRun string    `json:"cant_run,omitempty"` // why the command couldn't start (a missing tool): no verdict on the commit
	Tool    string    `json:"tool,omitempty"`     // with CantRun: the tool the shell couldn't find
	Log     string    `json:"log,omitempty"`      // the full output on the host, when it ran
	Failed  []string  `json:"failed,omitempty"`   // the failing tests it names, read before the output is clipped
	Keys    []string  `json:"keys,omitempty"`     // those tests with their packages (FailedTestKeys), when each can be placed
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
	Advice   []string  `json:"advice,omitempty"` // with a land: worth doing later, not a blocker; it stays on the PR
	// Diff names the change's own diff the reviewer read (DiffID), so the read
	// carries to any head with the same own diff, and to no other.
	Diff string `json:"diff,omitempty"`
	// Seat names the Hive's own security seat that gave it (SeatSecurity, or
	// PanelSeat(i)); "" is the ordinary reviewer. The host sets it on the run,
	// so a seat's read never counts as an ordinary one, nor the other way.
	Seat string `json:"seat,omitempty"`
}

// SeatSecurity is the Hive's own security reviewer: a reviewer separate from
// the ordinary one, on another model, briefed on the sandbox, credentials and
// what runs outside it. A change on the security list needs its land.
const SeatSecurity = "security"

// PanelSeats is how many sit on the panel that also reads a change to a root
// path on the security list (what runs with elevated rights); a majority of
// their lands passes it.
const PanelSeats = 3

// PanelSeat names the panel's seat i.
func PanelSeat(i int) string { return fmt.Sprintf("panel-%d", i) }

// OutsideRead is a security read from outside the Hive (the box's perms seat
// or its architect) of one head, recorded by the person.
type OutsideRead struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`
	Diff    string    `json:"diff"` // DiffID of the head's own diff
	PR      int       `json:"pr,omitempty"`
	By      string    `json:"by"`      // perms | architect
	Verdict string    `json:"verdict"` // land | fix
	Note    string    `json:"note,omitempty"`
	At      time.Time `json:"at"`
}

// OutsideReaders are who may give an outside read.
var OutsideReaders = []string{"perms", "architect"}

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
	e := &Effects{ws: ws, j: j, Forge: forge, sandbox: sb, tools: tools, runs: runs, sweep: sandbox.Sweep,
		checks: map[string]Check{}, verdicts: map[string]Verdict{}, lands: map[string][]string{}, diffLands: map[string][]string{}, diffVerds: map[string]Verdict{}, diffHeads: map[string][]string{}, outside: map[string]map[string]OutsideRead{}, seats: map[string]map[string]Verdict{}, hostMade: map[string]HostCommit{}, pushes: map[string][]string{}, approvals: map[string]bool{}, gates: map[string]GateResult{}, judged: map[string]Judgement{}, gateMains: map[string]GateResult{}, standing: map[string]bool{}}
	e.Slots = NewSlots(func() int { return cmp.Or(ws.LoadConfig().MaxChecks, DefaultMaxChecks()) }) // the work module paces it
	apply := func(ev Event) {
		e.mu.Lock()
		defer e.mu.Unlock()
		switch ev.Kind {
		case "effect.checked":
			var c Check
			ev.Decode(&c)
			if !noVerdict(c) { // a check that couldn't start runs again next time
				e.checks[c.Project+"@"+c.SHA+"/"+c.Kind] = c
			}
		case "effect.verdict":
			var v Verdict
			ev.Decode(&v)
			if v.Seat != "" {
				k := v.Project + "#" + v.Diff
				if e.seats[k] == nil {
					e.seats[k] = map[string]Verdict{}
				}
				e.seats[k][v.Seat] = v
				return
			}
			e.verdicts[v.Project+"@"+v.SHA] = v
			if k := v.Project + "@" + v.SHA; v.Verdict == "land" && !slices.Contains(e.lands[k], v.Run) {
				e.lands[k] = append(e.lands[k], v.Run)
			} else if v.Verdict != "land" {
				delete(e.lands, k)
			}
			if v.Diff != "" {
				k := v.Project + "#" + v.Diff
				e.diffVerds[k] = v
				if !slices.Contains(e.diffHeads[k], v.SHA) {
					e.diffHeads[k] = append(e.diffHeads[k], v.SHA)
				}
				if v.Verdict == "land" && !slices.Contains(e.diffLands[k], v.Run) {
					e.diffLands[k] = append(e.diffLands[k], v.Run)
				} else if v.Verdict != "land" {
					// A fix on the diff is a fix on every head that has it,
					// so no earlier head's own lands outlive it in Reads.
					delete(e.diffLands, k)
					for _, sha := range e.diffHeads[k] {
						delete(e.lands, v.Project+"@"+sha)
					}
				}
			}
		case "effect.read":
			var o OutsideRead
			ev.Decode(&o)
			k := o.Project + "#" + o.Diff
			if e.outside[k] == nil {
				e.outside[k] = map[string]OutsideRead{}
			}
			e.outside[k][o.By] = o
		case "effect.hostcommit":
			var h HostCommit
			ev.Decode(&h)
			e.hostMade[h.Project+"@"+h.SHA] = h
		case "effect.pushed":
			var pu struct{ Project, Branch, SHA string }
			ev.Decode(&pu)
			k := pu.Project + "@" + pu.Branch
			e.pushes[k] = append(slices.DeleteFunc(e.pushes[k], func(s string) bool { return s == pu.SHA }), pu.SHA)
		case "effect.approved":
			var a struct{ Kind, Project, SHA string }
			ev.Decode(&a)
			if a.Kind == "" {
				a.Kind = "merge"
			}
			e.approvals[a.Kind+":"+a.Project+"@"+a.SHA] = true
		case "gate.judged":
			var g GateResult
			ev.Decode(&g)
			if prev, ok := e.gateMains[g.Project+"@"+g.Main]; ok && prev.MainFails && g.MainFails {
				g.Repeats = prev.Repeats
				if g.Started.IsZero() || !prev.At.After(g.Started) { // a run beside it saw the same main: no repeat
					g.Repeats++
				}
			}
			e.gates[g.Project+"@"+g.Head+"@"+g.Main] = g
			switch {
			case g.Outcome == GateCantRun && (g.Alone || len(g.Conflicts) > 0):
				// Its own cause: the others at this main go on as they were.
			case g.Outcome == GateCantRun:
				e.gateMains[g.Project+"@"+g.Main] = g
			default:
				delete(e.gateMains, g.Project+"@"+g.Main)
			}
		case "judge.decided":
			var j Judgement
			ev.Decode(&j)
			e.judged[j.Project+"@"+j.SHA+"@"+j.Main] = j
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

// MainLine is the set of commits main has been at in the host clone: its
// first-parent history, without the branches merged into it.
func (e *Effects) MainLine(p Project) map[string]bool {
	out, err := Git(e.ws.Project(p.Name), "rev-list", "--first-parent", "refs/remotes/origin/"+p.Branch)
	if err != nil {
		return nil
	}
	line := map[string]bool{}
	for _, sha := range strings.Fields(out) {
		line[sha] = true
	}
	return line
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
	if code, out, err := e.runSandboxed(sb, r.Project, r.ID+"-snapshot", []sandbox.Mount{work}, script, false, 5*time.Minute, nil, 0); err != nil || code != 0 {
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
	code, logText, err := e.runSandboxed(sb, r.Project, r.ID+"-extract", []sandbox.Mount{work, outM}, script, false, 5*time.Minute, nil, 0)
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
	defer e.Slots.Acquire(p.Name, sha, kind)()
	dir, err := e.cleanCheckout(p, sha, kind)
	if err != nil {
		c.Output = "could not make a clean checkout: " + err.Error()
		return c
	}
	defer e.dropCheckout(dir)
	id := "gate-" + short(sha) + "-" + kind + "-" + NewID()[14:]
	c.Log = filepath.Join(e.ws.Hive(), "checks", p.Name, id+".log")
	mounts := []sandbox.Mount{{Host: dir, Inside: "/work", RW: true}}
	mods := e.goModules(sb, dir)
	build := e.goBuild(sb, p, sha, dir)
	run := func(withMods bool) (int, string, error) {
		ms, env := append(slices.Clone(mounts), build.mounts...), slices.Clone(build.env)
		if withMods {
			ms, env = append(ms, mods.mounts...), append(env, mods.env...)
		}
		return e.runSandboxed(sb, p.Name, id, ms, guardProgram(command), true, 30*time.Minute, env, e.checkCacheCap())
	}
	code, out, err := run(true)
	if len(build.mounts) > 0 && code != 0 && strings.Contains(out, "GOCACHEPROG") {
		// The build cache's program failed, not the commit: run once
		// without the shared build cache.
		build.done()
		build = goMods{note: "the shared build cache failed; run again without it", done: func() {}}
		code, out, err = run(true)
	}
	mods.done()
	if mods.marker != "" && code != 0 && strings.Contains(out, staleModules) {
		// Offline, the check couldn't have a module: not the commit's fault,
		// so it runs once more fetching its own, in a checkout the first run
		// hasn't written to. If go.sum names the module the cache lacked
		// what its marker promised, and the marker goes; if not, the check
		// needs more than the cache can hold, and later checks of the same
		// go.mod and go.sum skip it.
		if cacheLacked(out, mods.sum) {
			os.Remove(mods.marker)
			mods.note = "the shared module cache lacked a module; run again without it"
		} else {
			os.WriteFile(mods.marker+ownModules, nil, 0o644)
			mods.note = "the check needs a module go.sum doesn't name; run again fetching its own"
		}
		again, cerr := e.cleanCheckout(p, sha, kind)
		if cerr != nil {
			build.done()
			c.Output = "could not make a clean checkout: " + cerr.Error()
			return c
		}
		defer e.dropCheckout(again)
		if len(build.mounts) > 0 {
			stableTimes(again) // as goBuild made the first checkout's, for the shared build cache
		}
		mounts = []sandbox.Mount{{Host: again, Inside: "/work", RW: true}}
		code, out, err = run(false)
	}
	build.done()
	for _, n := range []string{build.note, mods.note} {
		if n != "" {
			out = "hive: " + n + "\n" + out
		}
	}
	c.Output = out
	if errors.Is(err, ErrHomeFull) {
		c.CantRun = fmt.Sprintf("can't run: %v (%s)", err, Bytes(e.checkCacheCap()))
		return record()
	}
	if err != nil {
		c.Output = err.Error() + "\n" + out
		if line := DiskFull(c.Output); line != "" {
			c.CantRun = "can't run: the disk or a quota is full where checks run"
			e.diskAlert(line)
		}
		return record()
	}
	c.OK = code == 0
	if tool := MissingTool(code, out); tool != "" {
		c.CantRun, c.Tool = "can't run: "+tool+" is not installed where checks run", tool
		return record()
	}
	line := DiskFull(out)
	if line == "" && !c.OK && (DiskUse{Room: e.room()}).Low() {
		line = toolDiskFull(out)
	}
	if !c.OK && line != "" {
		c.OK, c.CantRun = false, "can't run: the disk or a quota is full where checks run"
		e.diskAlert(line)
		return record()
	}
	e.mu.Lock()
	e.diskFull = false
	e.mu.Unlock()
	if (kind == "test" || kind == "retest" || kind == "before") && !c.OK {
		c.Failed = FailedTests(out)
		if keys, ok := FailedTestKeys(out); ok {
			c.Keys = keys
		}
	}
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

// missingTool matches the shell's "not found" for a command looked up on the
// PATH: dash's "/bin/sh: 1: go: not found", bash's "bash: go: command not found",
// busybox's "sh: go: not found", zsh's "zsh:1: command not found: go" and env's
// "env: 'go': No such file or directory" ('go' in a UTF-8 locale).
var missingTool = regexp.MustCompile(`(?m)^(?:\S*sh: (?:line )?\d*:? ?([^\s/:']+): (?:command )?not found|env: ['‘]?([^\s/:'‘’]+)['’]?: No such file or directory|zsh:(?:\d+: )? ?command not found: ([^\s/:']+))\s*$`)

// MissingTool names the tool a check's command couldn't find, or "" when it
// ran. Only exit 127 with the shell's own message counts, and only for a bare
// name: a missing ./script is the commit's fault, not the machine's.
func MissingTool(code int, out string) string {
	if code != 127 {
		return ""
	}
	if m := missingTool.FindStringSubmatch(out); m != nil {
		return m[1] + m[2] + m[3]
	}
	return ""
}

// noVerdict says a recorded check couldn't start, so it isn't the commit's
// answer. A failure recorded before checks carried cant_run is read from its
// output: those kept "go: not found" as main's red result for good. Later
// records say so themselves, so a real red that merely logs a "not found" line
// stays cached, as does an old one that named its failing tests.
func noVerdict(c Check) bool {
	if c.CantRun != "" {
		return true
	}
	return !c.OK && c.At.Before(cantRunRecorded) && len(c.Failed) == 0 && missingTool.MatchString(c.Output)
}

// cantRunRecorded is when every check came to carry cant_run (a day's margin
// after it merged, for daemons that hadn't updated yet).
var cantRunRecorded = time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)

var failedTest = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)|^FAILED (\S+)`)

// FailedTests names the failing tests in a test command's output, as Go's
// test runner ("--- FAIL: TestX") and pytest ("FAILED path::test") print them.
func FailedTests(out string) []string {
	var names []string
	for _, m := range failedTest.FindAllStringSubmatch(out, -1) {
		if n := m[1] + m[2]; !slices.Contains(names, n) && len(names) < 50 {
			names = append(names, n)
		}
	}
	return names
}

var (
	// GoPkgLine and GoFailLine are go test's line closing a package's
	// output ("FAIL\tpkg\t0.1s", "FAIL pkg [build failed]") and the line
	// naming a failed test; RedKeys and improve's failures read both.
	GoPkgLine  = regexp.MustCompile(`^(ok|FAIL|\?)\s+(\S+)(\s+\[([^\]]+)\])?`)
	GoFailLine = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	redOdd     = regexp.MustCompile(`^(panic: |fatal error: |test timed out)|\[(build|setup) failed\]`)
	// A compiler's or vet's error on one line: go and go vet
	// ("a.go:3:4: msg"), javac ("A.java:3: error: msg"), tsc
	// ("a.ts(3,4): error TS2322: msg", or "a.ts:3:4 - error TS2322: msg").
	redBuild = []*regexp.Regexp{
		regexp.MustCompile(`^(?:vet: )?(\S+\.go):\d+(?::\d+)?: (.+)`),
		regexp.MustCompile(`^(\S+\.java):\d+: (error: .+)`),
		regexp.MustCompile(`^(\S+\.[cm]?[jt]sx?)\(\d+,\d+\): (error TS\d+: .+)`),
		regexp.MustCompile(`^(\S+\.[cm]?[jt]sx?):\d+:\d+ - (error TS\d+: .+)`),
	}
	// rustc says the error first and where on the next line.
	rustErr, rustAt = regexp.MustCompile(`^(error(?:\[E\d+\])?: .+)`), regexp.MustCompile(`^\s*--> (\S+\.rs):\d+:\d+`)
	// A python traceback names its frames, then the error at column 0.
	pyAt, pyErr = regexp.MustCompile(`^\s*File "([^"]+\.py)", line \d+`), regexp.MustCompile(`^([A-Za-z_][\w.]*(?:Error|Exception|Exit)\b.*)`)
)

// RedKeys names what failed in a red go test run so two runs' reds can be
// compared: each failing test with its package ("pkg TestX"), and "pkg !"
// for a package that failed in a way no test name accounts for (no test
// named, a build failure, a timeout, a panic or exit outside a test). go test
// run in a package's directory ends an ordinary failure with "FAIL" then
// "exit status 1"; only an exit with no "FAIL" just before it (os.Exit in a
// test) counts as unnamed. A build's "# pkg" header counts at column 0 only.
// Output
// with no go package lines falls back to FailedTests' names. Nil means
// nothing could be named.
func RedKeys(out string) []string {
	var keys, tests []string
	odd, pkgs, prev := false, false, ""
	add := func(k string) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if m := GoPkgLine.FindStringSubmatch(l); m != nil && strings.ContainsAny(m[2], "/.") {
			pkgs = true
			if m[1] == "FAIL" {
				for _, t := range tests {
					add(m[2] + " " + t)
				}
				if len(tests) == 0 || odd || strings.Contains(m[4], "failed") {
					add(m[2] + " !")
				}
			}
			tests, odd, prev = nil, false, ""
			continue
		}
		if m := GoFailLine.FindStringSubmatch(l); m != nil {
			tests = append(tests, m[1])
		} else if redOdd.MatchString(t) || strings.HasPrefix(l, "# ") || strings.HasPrefix(t, "exit status ") && prev != "FAIL" {
			odd = true
		}
		if t != "" {
			prev = t
		}
	}
	if !pkgs {
		return FailedTests(out)
	}
	return keys
}

// StopKeys names, for a red run RedKeys names nothing in (go vet, or a build
// that stopped the command before the tests ran), each compile or vet error
// as "file: message", its line left out so an edit above it doesn't make the
// same error look new: go, javac, tsc and rustc errors, and the error a
// python traceback ends in, by the last file it names. The rest of the
// command didn't run, so these can show a red isn't new at a merge but never
// that a change is no worse than main.
func StopKeys(out string) []string {
	var keys []string
	add := func(file, msg string) {
		if k := file + ": " + msg; !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	rust, py := "", ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, " \r")
		if m := rustAt.FindStringSubmatch(l); m != nil && rust != "" {
			add(m[1], rust)
		}
		rust = ""
		if m := rustErr.FindStringSubmatch(l); m != nil {
			rust = m[1]
		}
		if m := pyAt.FindStringSubmatch(l); m != nil {
			py = m[1]
		} else if m := pyErr.FindStringSubmatch(l); m != nil && py != "" {
			add(py, m[1])
			py = ""
		}
		for _, re := range redBuild {
			if m := re.FindStringSubmatch(l); m != nil {
				add(m[1], m[2])
				break
			}
		}
	}
	return keys
}

// FullOutput is a check's whole output: its log on the host when it can be
// read, else the clipped output it was journaled with.
func (c Check) FullOutput() string {
	if c.Log != "" {
		if b, err := SafeRead(c.Log, 8<<20); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	return c.Output
}

// NoWorse says a red run c fails only what main's red run mc fails too,
// each test by its package and name, so the red is main's and the change
// may be its fix. A failure in c that no test name accounts for (a timeout,
// a panic or exit outside a test, a build failure) counts only where main's
// run fails that package so too; one either run can't name at all can't be
// told apart. A run stopped by vet or the build (StopKeys) never ran its
// tests, so matching main's vet error never makes a change no worse.
func NoWorse(c, mc Check) bool {
	mine, mains := RedKeys(c.FullOutput()), RedKeys(mc.FullOutput())
	if len(mine) == 0 || len(mains) == 0 {
		return false
	}
	for _, k := range mine {
		if !slices.Contains(mains, k) {
			return false
		}
	}
	return true
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
	e.markLive(dir, true)
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
		e.dropCheckout(dir)
		return "", err
	}
	return dir, nil
}

// dropCheckout removes a clean checkout and its live mark. It is marked from
// before its clone, so Prune never takes a running check's tree for a crash's
// leftover: goBuild dates its folders to 2001 (stableTimes), past staleAfter
// the moment the check starts.
func (e *Effects) dropCheckout(dir string) {
	os.RemoveAll(dir)
	e.markLive(dir, false)
}

// runSandboxed runs a shell command in the sandbox and returns its exit code
// and output. Its log goes in the project's own folder, which other runs
// can't see when the project is isolated.
// The command gets its own home under the cache folder, removed after; extra
// is more environment where the sandbox has its own, and a capB over 0 ends
// the command with ErrHomeFull once its home passes that many bytes.
func (e *Effects) runSandboxed(sb sandbox.Sandbox, project, id string, mounts []sandbox.Mount, script string, network bool, timeout time.Duration, extra []string, capB int64) (int, string, error) {
	logDir := filepath.Join(e.ws.Hive(), "checks", project)
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, id+".log")
	os.Remove(logPath)
	home, done := e.newHome()
	defer done()
	homeM := sandbox.Mount{Host: home, Inside: "/hive/home", RW: true}
	mounts = append(mounts, homeM, sandbox.Mount{Host: e.ws.Tools(), Inside: sandbox.ToolsInside})
	argv := []string{"/bin/sh", "-c", script}
	env := append(checkTmp(sandbox.InsideEnv("/hive/home", network)), extra...)
	if !sb.Isolated() {
		env = nil
		argv = hostShell(script)
	}
	spec := sandbox.Spec{ID: id, Argv: argv, Env: env, Cwd: sandbox.InsidePath(sb, mounts[0]), Mounts: mounts, Tools: e.tools(),
		Stdout: logPath, StopFile: home + ".stop", Network: network, Allow: e.ws.LoadConfig().Egress.For("check"), Local: e.ws.LoadConfig().Egress.LocalAddrs(), Guard: e.ws.Guarded(), Cover: e.ws.Covered(), UID: os.Getuid(), GID: os.Getgid(), Nice: CheckNice}
	over := watchHome(home, capB)
	code, err := sb.Run(spec, timeout)
	if over() && err == nil {
		err = ErrHomeFull
	}
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
// started on exactly that commit, and binds it to the diff that run was given.
func (e *Effects) RecordVerdict(v Verdict) error {
	r, ok := e.runs.Get(v.Run)
	if !ok || r.Role != "reviewer" || !r.ReadOnly || r.Inputs["sha"] != v.SHA || r.Project != v.Project {
		return fmt.Errorf("verdict from %s is not bound to commit %s", v.Run, short(v.SHA))
	}
	if v.Verdict != "land" && v.Verdict != "fix" {
		return fmt.Errorf("verdict must be land or fix")
	}
	v.Diff = DiffID(r.Inputs["diff.patch"])
	if v.Seat = r.Inputs["seat"]; v.Seat != "" && v.Diff == "" {
		return errors.New("a security seat's read is bound to a diff")
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

// Reads returns the reviewer runs that said land since the last fix, on
// exactly sha or on a head with the same own diff (DiffID; "" for none).
func (e *Effects) Reads(project, sha, diff string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := slices.Clone(e.lands[project+"@"+sha])
	if diff != "" {
		for _, r := range e.diffLands[project+"#"+diff] {
			if !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

// verdict is the latest verdict on the own diff, else on exactly sha.
func (e *Effects) verdict(project, sha, diff string) (Verdict, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v, ok := e.diffVerds[project+"#"+diff]; ok && diff != "" {
		return v, true
	}
	v, ok := e.verdicts[project+"@"+sha]
	return v, ok
}

// RecordRead records an outside read (OutsideReaders) of a head whose own
// diff is o.Diff; it holds for every head with that own diff.
func (e *Effects) RecordRead(o OutsideRead) error {
	if !slices.Contains(OutsideReaders, o.By) {
		return fmt.Errorf("an outside read is by one of %s, not %q", strings.Join(OutsideReaders, ", "), o.By)
	}
	if o.Verdict != "land" && o.Verdict != "fix" {
		return fmt.Errorf("verdict must be land or fix")
	}
	if o.Project == "" || o.SHA == "" || o.Diff == "" {
		return errors.New("an outside read names a project, a head and its diff")
	}
	o.At = time.Now().UTC()
	e.j.MustAppend("effect.read", o)
	return nil
}

// OutsideRead is the latest outside read of the own diff: a fix from any
// reader outweighs a land from another.
func (e *Effects) OutsideRead(project, diff string) (OutsideRead, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out OutsideRead
	for _, by := range OutsideReaders {
		if o, ok := e.outside[project+"#"+diff][by]; ok && out.Verdict != "fix" {
			out = o
		}
	}
	return out, out.By != "" && diff != ""
}

// SecurityReads are the Hive's own security seats' latest verdicts on the
// own diff, by seat.
func (e *Effects) SecurityReads(project, diff string) map[string]Verdict {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.seats[project+"#"+diff])
}

// PanelTally counts the panel's lands and fixes on the own diff.
func PanelTally(reads map[string]Verdict) (lands, fixes int) {
	for i := 0; i < PanelSeats; i++ {
		switch reads[PanelSeat(i)].Verdict {
		case "land":
			lands++
		case "fix":
			fixes++
		}
	}
	return lands, fixes
}

// SecurityOK reports whether ch, a change on the security list, has the
// security read it needs: an outside read (the box's perms seat or
// architect) decides when there is one, a fix from either outweighing a
// land; otherwise the Hive's own security seat said land on its own diff,
// and for a root path a majority of the panel did too.
func (e *Effects) SecurityOK(project string, ch Change) bool {
	if len(ch.Security) == 0 {
		return true
	}
	if o, ok := e.OutsideRead(project, ch.DiffID); ok {
		return o.Verdict == "land"
	}
	if ch.DiffID == "" {
		return false
	}
	reads := e.SecurityReads(project, ch.DiffID)
	if reads[SeatSecurity].Verdict != "land" {
		return false
	}
	lands, _ := PanelTally(reads)
	return len(ch.SecurityRoot) == 0 || lands > PanelSeats/2
}

// OwnDiff is the diff from base (a change's merge base with main) to sha, as
// its reviewers read it and DiffID names it. Binary and full-index, so a
// binary file (or a script git takes for one, with a NUL byte in it) carries
// its content into the diff and DiffID, and two different blobs never read
// as the same change. It is "" when git can't tell.
func OwnDiff(dir, base, sha string) string {
	out, _ := Git(dir, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", base, sha)
	return out
}

// Between is the diff from one commit to another in p's host clone, "" when
// git can't tell (one of them isn't fetched).
func (e *Effects) Between(p Project, from, to string) string {
	return OwnDiff(e.ws.Project(p.Name), from, to)
}

// DiffID names a change's own diff (the diff from its merge base with main to
// its head) so that a head whose own diff is the same gets the same name,
// whatever main it was brought up to date with. Hunk positions and text
// files' blob ids are left out, since main moving shifts them without
// changing the change; everything else, context lines included, counts. A
// binary file keeps its index line: git may write it as a delta against the
// old blob, which only that blob id pins down.
//
// Left out on purpose: a hunk's position and function context, so the same
// added lines with the same context moved elsewhere in one file keep the
// name (docs/log.md). Changing what counts renames every diff and drops the
// reads recorded on open PRs.
func DiffID(diff string) string {
	if strings.TrimSpace(diff) == "" {
		return ""
	}
	h := sha256.New()
	lines := strings.Split(diff, "\n")
	binary := false
	for i, l := range lines {
		if strings.HasPrefix(l, "diff --git ") {
			binary = false
			for _, next := range lines[i+1:] {
				if strings.HasPrefix(next, "diff --git ") {
					break
				}
				if next == "GIT binary patch" {
					binary = true
					break
				}
			}
		}
		switch {
		case strings.HasPrefix(l, "index ") && !binary:
			continue
		case strings.HasPrefix(l, "@@"):
			l = "@@"
		}
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Approve records the approval of a commit (the person's, or a warden's
// when autonomy lets it decide): kind "merge" for a head
// that changes protected paths, "release" for a merge held before release,
// "gate" for a change to the gate landed unjudged at one main (sha is
// head@main), while main itself fails in the gate.
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

// Pushed says whether the Hive pushed sha to branch, and whether it is the
// newest commit it pushed there.
func (e *Effects) Pushed(project, branch, sha string) (pushed, last bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	all := e.pushes[project+"@"+branch]
	return slices.Contains(all, sha), len(all) > 0 && all[len(all)-1] == sha
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
	// Security are files on main's SecurityList, or in the project's root
	// class: the change merges only after a security read said land on its
	// diff (SecurityOK).
	Security []string `json:"security,omitempty"`
	// SecurityRoot are those of them that run with elevated rights (the
	// list's root entries, or the project's root class): they need the
	// panel's read as well.
	SecurityRoot []string `json:"security_root,omitempty"`
	// Unlisted are those of them the list doesn't name: in the root class only.
	Unlisted []string `json:"unlisted,omitempty"`
	Root     []string `json:"root,omitempty"`    // files that run with elevated rights
	DiffID   string   `json:"diff_id,omitempty"` // names Diff (DiffID)
	Diff     string   `json:"-"`
	// ProtectedDiff is the diff of the protected files alone, shown to the person who approves it.
	ProtectedDiff string `json:"-"`
}

// SecurityWhere says why the change's Security files need a security read:
// they are on SecurityList, in the project's root class, or some of each.
func (c Change) SecurityWhere() string {
	switch {
	case len(c.Unlisted) == 0:
		return "on " + SecurityList
	case len(c.Unlisted) == len(c.Security):
		return "in the project's root class (which runs with elevated rights)"
	}
	return "on " + SecurityList + " or in the project's root class (" + strings.Join(c.Unlisted, ", ") + ")"
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
	protected := append([]string{".hive/project.json", ".hive/goals.md", "docs/REVIEW.md", StewardFile}, cfg.Protected...)
	if cfg.Score != nil {
		protected = append(protected, cfg.Score.Evaluator()...)
	}
	list := e.ReadFile(p, mainSHA, SecurityList)
	security, root := ParseList(list, "paths"), ParseList(list, "root")
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
		// What runs with elevated rights (the list's root entries, or the
		// project's root class) needs the security read and its panel,
		// whether or not the list's paths name it.
		if elevated := matchAny(file, root) || under(file, cfg.Root); elevated || matchAny(file, security) {
			c.Security = append(c.Security, file)
			if elevated {
				c.SecurityRoot = append(c.SecurityRoot, file)
			}
			if !matchAny(file, root) && !matchAny(file, security) {
				c.Unlisted = append(c.Unlisted, file)
			}
		}
		// .gitattributes picks merge drivers: merge=union on code would
		// quietly keep both sides of every conflict in it.
		if under(file, cfg.High) || under(file, cfg.Root) || file == ".gitattributes" || strings.HasSuffix(file, "/.gitattributes") {
			c.High = append(c.High, file)
		}
		ext := strings.ToLower(filepath.Ext(file))
		if !slices.Contains(docExt, ext) && file != ".hive/lessons.md" &&
			!(strings.HasPrefix(file, "docs/") && slices.Contains(docImage, ext)) {
			docsOnly = false
		}
	}
	c.Small = docsOnly && len(c.Files) > 0 && c.Lines <= cfg.SmallMax && len(c.Protected) == 0 && len(c.High) == 0 && len(c.Security) == 0 && !p.Self
	c.Diff = OwnDiff(dir, base, sha)
	c.DiffID = DiffID(c.Diff)
	if len(c.Protected) > 0 {
		c.ProtectedDiff, _ = Git(dir, append([]string{"--literal-pathspecs", "diff", "--no-renames", base, sha, "--"}, c.Protected...)...)
	}
	return c, nil
}

// SecurityList is the project's list of paths a change to which merges only
// after a security read (SecurityOK): the Hive's own security seat, with its
// panel for the list's root entries, or an outside read from the box's perms
// seat or architect. Its required_checks are the forge's checks that must
// succeed on a head before it merges. It is read from main, so a change
// can't move its own bar.
const SecurityList = "tests/LANDING-security.toml"

// ParseList reads the quoted entries of one array in the list: TOML as the
// list writes it, `key = [ ... ]` with # comments.
func ParseList(raw, key string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(raw, "\n") {
		l = strings.TrimSpace(l)
		if !in {
			k, v, ok := strings.Cut(l, "=")
			if !ok || strings.TrimSpace(k) != key {
				continue
			}
			in, l = true, strings.TrimSpace(v)
		}
		for {
			i := strings.IndexAny(l, "\"#]")
			if i < 0 || l[i] == '#' {
				break
			}
			if l[i] == ']' {
				return out
			}
			j := strings.IndexByte(l[i+1:], '"')
			if j < 0 {
				break
			}
			out = append(out, l[i+1:i+1+j])
			l = l[i+j+2:]
		}
	}
	return out
}

// matchAny reports whether file matches one of the list's patterns: `*`
// stays inside one path segment, `**` crosses segments.
func matchAny(file string, patterns []string) bool {
	for _, pat := range patterns {
		if globMatch(strings.Split(pat, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

func globMatch(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if globMatch(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, _ := path.Match(pat[0], segs[0]); !ok {
		return false
	}
	return globMatch(pat[1:], segs[1:])
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
	Gain    bool   // the change aims at a measurable gain: on the Hive's own repository it needs the recursive gate's keep
	Task    string // the task whose PR it is: the quarantine excuses it only on evidence from outside it
	// Draft is the PR's state at the caller's last look: a draft is readied
	// once every read is checked. One made a draft after that look isn't
	// (the lead's pr_draft, say): MergePR refuses it with ErrDraft.
	Draft bool
}

// Merge merges the PR only at sha, and only when the host's own records show
// that sha passed its tests and was read (or needed no read).
func (e *Effects) Merge(m MergeRequest) (string, error) {
	p := m.Project
	if e.MergeHeld != nil {
		if why := e.MergeHeld(p.Name, m.PR); why != "" {
			return "", refuse("#%d: %s", m.PR, why)
		}
	}
	if why := e.Stewardship(p, m.Main); why != "" {
		return "", refuse("#%d: %s", m.PR, why)
	}
	ch, err := e.Classify(p, m.Main, m.SHA)
	if err != nil {
		return "", err
	}
	var flaky []string // a red local test run's known flakes, its only failures
	if c, ok := e.GetCheck(p.Name, m.SHA, "test"); !ok {
		return "", refuse("%s: no test run recorded for that exact commit", short(m.SHA))
	} else if !c.OK {
		// Red only where main's own run at m.Main is red too: no worse than
		// main, as the work module's testing stage let it on, so the fix for
		// a red main can land. Or red only in quarantined tests, which count
		// against no head, as the forge's checks below.
		if flaky = e.ExcusedLocal(p, m.Main, m.Task, ch.Files, c); len(flaky) == 0 {
			if mc, ok := e.GetCheck(p.Name, m.Main, "test"); !ok || mc.OK || !NoWorse(c, mc) {
				return "", refuse("%s: its test run is red beyond what main's own run at %s fails too (or main's has no record)", short(m.SHA), short(m.Main))
			}
		}
	}
	if len(ch.Protected) > 0 && !e.Approved("merge", p.Name, m.SHA) {
		return "", &NeedsPerson{Reason: "it changes " + strings.Join(ch.Protected, ", ") + ", which needs an approval first"}
	}
	if !ch.Small && !e.readOK(p, ch, m.SHA) {
		return "", refuse("%s: no land verdict bound to that exact commit", short(m.SHA))
	}
	if !e.SecurityOK(p.Name, ch) {
		need := "the Hive's security seat"
		if len(ch.SecurityRoot) > 0 {
			need += " and a majority of its panel"
		}
		return "", refuse("%s: it changes %s, %s, and no security read said land on its diff (%s, or an outside read)", short(m.SHA), strings.Join(ch.Security, ", "), ch.SecurityWhere(), need)
	}
	if p.Self && m.Gain {
		// Or once the person landed that exact head at that main unjudged,
		// the gate unable to judge there (the work module's "gate" ask).
		if !e.Kept(p, m.SHA, m.Main) && !e.Approved("gate", p.Name, m.SHA+"@"+m.Main) {
			return "", refuse("%s: a change to the Hive itself aimed at a gain merges only once the recursive gate, as its judge called it, kept it against main at %s, or the person landed it", short(m.SHA), short(m.Main))
		}
	}
	runs, err := e.Forge.Checks(p.Repo, m.SHA)
	if err != nil {
		return "", err
	}
	runs, req, warned := Advisory(p, runs, e.RequiredChecks(p, m.Main), e.AdvisoryChecks(p, m.Main))
	st, which := ChecksState(runs, req)
	var shared, excused []string
	var answer map[string]string // check -> the older main commit its answer came from
	if st == CheckFail {
		mainRuns, at, err := e.MainAnswer(p, m.Main, which)
		answer = at
		if err != nil {
			return "", err
		}
		runs, excused = e.Excused(p, m.Main, m.Task, ch.Files, runs, mainRuns)
		st, which, shared, _ = ChecksAgainstMain(runs, mainRuns, req, e.FailingTests(p.Repo))
	}
	var prose []string
	if st == CheckFail && ProseOnly(ch.Files) {
		st, which, prose = ChecksBesidesFailures(runs, req)
	}
	if st != CheckPass {
		return "", refuse("%s: the forge's checks are not all passing there, beyond what fails on main at %s too (%s: %s)", short(m.SHA), short(m.Main), st, CheckNames(which))
	}
	// Every read above is the Hive's own and bound to sha, and nobody outside
	// the Hive reads its PRs, so a draft it cleared is its to ready.
	if m.Draft {
		if err := e.ReadyPR(p, m.PR, m.SHA); err != nil {
			return "", err
		}
	}
	merged, err := e.Forge.MergePR(p.Repo, m.PR, m.SHA, m.Subject)
	if red := append(slices.Concat(shared, prose), warned...); err != nil && len(red) > 0 && protectionRefused.MatchString(err.Error()) {
		return "", &RefusedRed{Checks: red, Warned: warned, Err: err}
	}
	if err != nil {
		return "", err
	}
	ev := map[string]any{"project": p.Name, "pr": m.PR, "sha": m.SHA, "merge_sha": merged, "root": ch.Root, "diff": ch.DiffID}
	if len(excused) > 0 {
		ev["quarantined"] = excused // red only on known flakes
	}
	if len(flaky) > 0 {
		ev["quarantined_local"] = flaky // its own test run red only on known flakes
	}
	if len(warned) > 0 {
		ev["warned"] = warned
	}
	if len(shared) > 0 {
		// A merge past a red check names the main commits that excused it,
		// so one that relied on an older main answer can be audited.
		ev["red_on_main"] = shared
		if len(answer) > 0 {
			ev["main_answer"] = answer
		}
	}
	if len(prose) > 0 {
		ev["red_on_prose"] = prose // a change to prose alone, which can't break them
	}
	e.j.MustAppend("effect.merged", ev)
	return merged, nil
}

// ReadyPR takes the PR out of draft, only while its head is still sha; the
// journal hears only when the forge changed something.
func (e *Effects) ReadyPR(p Project, pr int, sha string) error {
	readied, err := e.Forge.ReadyPR(p.Repo, pr, sha)
	if readied {
		e.j.MustAppend("effect.readied", map[string]any{"project": p.Name, "pr": pr, "sha": sha})
	}
	return err
}

// RefusedRed is the forge refusing a merge the Hive found no worse than main:
// its branch rules want a check that is red on main too, or one main's list
// says only warns (Warned), so retrying won't help until main is fixed or the
// person relaxes the rule.
type RefusedRed struct {
	Checks []string
	Warned []string
	Err    error
}

func (r *RefusedRed) Error() string {
	why := ", red on main too"
	if len(r.Warned) > 0 {
		why = ", red here (" + strings.Join(r.Warned, ", ") + " only warn on the Hive's own PRs, per advisory_checks)"
	}
	return "the forge refused the merge: its branch rules want " + strings.Join(r.Checks, ", ") + why + " (" + Clip(r.Err.Error(), 200) + ")"
}

func (r *RefusedRed) Unwrap() error { return r.Err }

// protectionRefused is how GitHub says a branch rule refused a merge.
var protectionRefused = regexp.MustCompile(`(?i)required status check|base branch policy|protected branch|repository rule violation`)

// Refusal is Merge declining a merge one of its own conditions doesn't
// allow yet, so the task can say what holds it.
type Refusal string

func (r Refusal) Error() string { return string(r) }

func refuse(format string, a ...any) error {
	return Refusal(fmt.Sprintf("refusing to merge "+format, a...))
}

// NeedsPerson means only the person can take this step.
type NeedsPerson struct{ Reason string }

func (n *NeedsPerson) Error() string { return "needs the person: " + n.Reason }

// RequiredChecks are the forge's checks main's list says must succeed on a
// head before it merges.
func (e *Effects) RequiredChecks(p Project, mainSHA string) []string {
	return ParseList(e.ReadFile(p, mainSHA, SecurityList), "required_checks")
}

// AdvisoryChecks are the forge's checks main's list says only warn on the
// Hive's own PRs (Advisory).
func (e *Effects) AdvisoryChecks(p Project, mainSHA string) []string {
	return ParseList(e.ReadFile(p, mainSHA, SecurityList), "advisory_checks")
}

// Advisory splits off the checks that only warn: on the Hive's own project
// (Self) a check named on main's advisory_checks (a Windows job, whose red was
// mostly flakes unrelated to the change) doesn't hold the merge, red, pending
// or unable to start (the person: keep the Hive improving as fast as
// possible). A release still needs the whole ci workflow green. kept are the
// rest; warned names the red ones set aside, and required loses every
// advisory check so none is waited on.
func Advisory(p Project, runs []CheckRun, required, advisory []string) (kept []CheckRun, req, warned []string) {
	if !p.Self || len(advisory) == 0 {
		return runs, required, nil
	}
	for _, r := range runs {
		if !slices.Contains(advisory, r.Name) || r.State == CheckPass {
			kept = append(kept, r)
		} else if r.State == CheckFail && !slices.Contains(warned, r.Name) {
			warned = append(warned, r.Name)
		}
	}
	req = slices.DeleteFunc(slices.Clone(required), func(n string) bool { return slices.Contains(advisory, n) })
	return kept, req, warned
}

// ChecksState sums up the forge's checks on one head, the worst first: a
// failure, then one that can't start, then one still running, then a
// required check that hasn't succeeded (not reported yet, or neutral or
// skipped), which holds as pending. Otherwise it is a pass: with nothing
// required, no checks at all is a pass. which are the checks in that state.
func ChecksState(runs []CheckRun, required []string) (state string, which []CheckRun) {
	for _, st := range []string{CheckFail, CheckCantStart, CheckPending} {
		for _, r := range runs {
			if r.State == st {
				which = append(which, r)
			}
		}
		if len(which) > 0 {
			return st, which
		}
	}
	for _, name := range required {
		i := slices.IndexFunc(runs, func(r CheckRun) bool { return r.Name == name })
		switch {
		case i < 0:
			which = append(which, CheckRun{Name: name, State: CheckPending, Detail: "required, not reported yet"})
		case runs[i].State != CheckPass:
			which = append(which, CheckRun{Name: name, State: CheckPending, Detail: "required, but " + runs[i].State, URL: runs[i].URL})
		}
	}
	if len(which) > 0 {
		return CheckPending, which
	}
	return CheckPass, nil
}

// ChecksAgainstMain sums up a head's checks as ChecksState does, except that
// a check that fails on main's head too doesn't count against it: the change
// is no worse than main there, and holding it would keep the fix for a red
// main from ever landing. shared names those checks; with main green it is
// empty and the result is ChecksState's. With tests (the failing tests a
// check's log names) a shared check whose log names a test failing that
// main's doesn't is the change's after all, so a second red test inside a job
// main already fails still counts; byName are the shared checks whose logs
// couldn't tell, judged by the check's name alone.
func ChecksAgainstMain(runs, mainRuns []CheckRun, required []string, tests TestsOf) (state string, which []CheckRun, shared, byName []string) {
	var kept []CheckRun
	for _, r := range runs {
		// Only a check main fails outright: one of its name that passes or
		// still runs there (another attempt, a status) keeps it the change's.
		named := func(mr CheckRun) bool { return mr.Name == r.Name }
		if r.State == CheckFail && slices.ContainsFunc(mainRuns, named) && !slices.ContainsFunc(mainRuns, func(mr CheckRun) bool { return named(mr) && mr.State != CheckFail }) {
			fresh, ok := newTests(r, mainRuns, tests)
			if len(fresh) > 0 {
				r.Detail = strings.TrimPrefix(r.Detail+"; ", "; ") + "fails " + strings.Join(fresh, ", ") + ", which main's run of it doesn't"
				kept = append(kept, r)
				continue
			}
			if !slices.Contains(shared, r.Name) {
				shared = append(shared, r.Name)
				if !ok {
					byName = append(byName, r.Name)
				}
			}
			continue
		}
		kept = append(kept, r)
	}
	req := slices.DeleteFunc(slices.Clone(required), func(n string) bool { return slices.Contains(shared, n) })
	state, which = ChecksState(kept, req)
	return state, which, shared, byName
}

// JobRead reads a forge check's job: its log's failure, and its steps. A
// nil func reads nothing.
type JobRead struct {
	Failure func(CheckRun) (CheckFailure, bool)
	Steps   func(CheckRun) ([]JobStep, bool)
}

// Excuse marks as passing each failed check that failed only in quarantined
// tests (known flakes, keyed package::test), so it counts against no head;
// excused names those checks. A failure only in such tests is shown, never
// assumed: the log's failing tests, each with its package, are all
// quarantined, and it shows nothing else failing (no build or setup failure,
// panic, timeout, package red apart from them or failing test with no
// package, CheckFailure.OnlyTests); none of their packages is one the head
// changes (touched), so a change can't break a quarantined test and pass;
// exactly one of the job's steps failed; and every step it skipped was
// skipped on main's finished run of that check too, so no step the red kept
// from running goes unchecked. Anything unread excuses nothing.
func Excuse(runs, mainRuns []CheckRun, read JobRead, quarantined []string, touched func(key string) bool) (out []CheckRun, excused []string) {
	if read.Failure == nil || read.Steps == nil || len(quarantined) == 0 || touched == nil {
		return runs, nil
	}
	out = slices.Clone(runs)
	for i, r := range out {
		if r.State != CheckFail {
			continue
		}
		f, ok := read.Failure(r)
		if !ok || !f.OnlyTests || len(f.Keys) == 0 || slices.ContainsFunc(f.Keys, func(k string) bool { return !slices.Contains(quarantined, k) || touched(k) }) {
			continue
		}
		if !stepsShown(r, mainRuns, read.Steps) {
			continue
		}
		out[i].State = CheckPass
		out[i].Detail = "fails only the quarantined " + strings.Join(f.Keys, ", ")
		if !slices.Contains(excused, r.Name) {
			excused = append(excused, r.Name)
		}
	}
	return out, excused
}

// stepsShown reports whether r's job failed in one step and skipped only
// steps that main's finished runs of its name skip as well.
func stepsShown(r CheckRun, mainRuns []CheckRun, steps func(CheckRun) ([]JobStep, bool)) bool {
	head, ok := steps(r)
	if !ok {
		return false
	}
	failed := 0
	var skipped []string
	for _, s := range head {
		switch s.Conclusion {
		case "success":
		case "skipped":
			skipped = append(skipped, s.Name)
		default: // failure, timed_out, cancelled, or not finished
			failed++
		}
	}
	if failed != 1 {
		return false
	}
	if len(skipped) == 0 {
		return true
	}
	seen := false
	for _, mr := range mainRuns {
		if mr.Name != r.Name || (mr.State != CheckPass && mr.State != CheckFail) {
			continue
		}
		ms, ok := steps(mr)
		if !ok {
			return false
		}
		if slices.ContainsFunc(ms, func(s JobStep) bool { return slices.Contains(skipped, s.Name) && s.Conclusion != "skipped" }) {
			return false
		}
		seen = true
	}
	return seen
}

var (
	// notATest is a failure in a log that names no test: a package that
	// didn't build or set up, a panic (which ends the whole test binary), a
	// timeout, or pytest's errors.
	notATest  = regexp.MustCompile(`(?m)\[(?:build|setup) failed\]|^\s*panic: |test timed out|^ERROR |\b\d+ errors?\b|^=+ ERRORS =+`)
	pkgResult = regexp.MustCompile(`^(ok|FAIL|\?)\s*\t\S+`)
)

// OnlyTests reports whether a log shows nothing failing apart from the tests
// it names: no failure of notATest's kinds, and no package red ("FAIL\tpkg")
// without a named test failing in it.
func OnlyTests(log string) bool {
	if notATest.MatchString(log) {
		return false
	}
	named := false
	for _, line := range strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n") {
		if failedTest.MatchString(line) {
			named = true
			continue
		}
		if m := pkgResult.FindStringSubmatch(line); m != nil {
			if m[1] == "FAIL" && !named {
				return false
			}
			named = false
		}
	}
	return true
}

// FailedTestKeys names a log's failing tests with their packages: Go's
// "--- FAIL: TestX" as pkg::TestX, from the "FAIL\tpkg" line that closes
// its package's output, and pytest's "FAILED path::test" as it is. ok is
// false when a failing Go test has no package line to place it in.
func FailedTestKeys(log string) (keys []string, ok bool) {
	var pending []string
	add := func(k string) {
		if !slices.Contains(keys, k) && len(keys) < 50 {
			keys = append(keys, k)
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n") {
		if m := failedTest.FindStringSubmatch(line); m != nil {
			if m[2] != "" {
				add(m[2])
			} else if !slices.Contains(pending, m[1]) {
				pending = append(pending, m[1])
			}
			continue
		}
		if m := pkgResult.FindStringSubmatch(line); m != nil {
			if m[1] == "FAIL" {
				pkg := strings.Fields(strings.TrimSpace(line[len(m[1]):]))[0]
				for _, n := range pending {
					add(pkg + "::" + n)
				}
			}
			pending = nil
		}
	}
	return keys, len(pending) == 0
}

// KeyDir is the directory of a test key's package: a pytest file's own
// directory, or a Go package's within module. ok is false when it can't be
// told (a Go package outside module, or no module).
func KeyDir(key, module string) (string, bool) {
	where, _, found := strings.Cut(key, "::")
	switch {
	case !found:
		return "", false
	case strings.HasSuffix(where, ".py"):
		return path.Dir(where), true
	case module == "":
		return "", false
	case where == module:
		return ".", true
	case strings.HasPrefix(where, module+"/"):
		return strings.TrimPrefix(where, module+"/"), true
	}
	return "", false
}

var goModule = regexp.MustCompile(`(?m)^module\s+"?([^\s"]+)`)

// Excused is Excuse for task's PR on p at head: the quarantine as evidence
// from outside task shows it, and files the head changes against main, a
// test whose package they touch (or whose package can't be placed) never
// excused.
func (e *Effects) Excused(p Project, main, task string, files []string, runs, mainRuns []CheckRun) ([]CheckRun, []string) {
	q := e.Quarantine(p.Name, task)
	if len(q) == 0 {
		return runs, nil
	}
	return Excuse(runs, mainRuns, e.JobRead(p.Repo), q, e.touched(p, main, files))
}

// touched reports whether a test key's package is one files (a head's
// changes against main) touch, or can't be placed.
func (e *Effects) touched(p Project, main string, files []string) func(key string) bool {
	module := ""
	if m := goModule.FindStringSubmatch(e.ReadFile(p, main, "go.mod")); m != nil {
		module = m[1]
	}
	return func(key string) bool {
		dir, ok := KeyDir(key, module)
		return !ok || slices.ContainsFunc(files, func(f string) bool { return path.Dir(f) == dir })
	}
}

// ExcusedLocal is Excused for the Hive's own red test check c of task's head
// on p: the quarantined tests it fails, when they are all it fails, or none.
// As with the forge's, it is shown, never assumed: c's full log is read (not
// its clipped output), places every failing test in its package, shows
// nothing else failing (OnlyTests), and every one is quarantined on evidence
// from outside task in a package files don't touch.
func (e *Effects) ExcusedLocal(p Project, main, task string, files []string, c Check) []string {
	if c.OK || c.CantRun != "" || c.Log == "" {
		return nil
	}
	q := e.Quarantine(p.Name, task)
	if len(q) == 0 {
		return nil
	}
	b, err := SafeRead(c.Log, 8<<20)
	if err != nil || len(b) == 0 {
		return nil
	}
	log := string(b)
	keys, placed := FailedTestKeys(log)
	touched := e.touched(p, main, files)
	if !placed || len(keys) == 0 || !OnlyTests(log) || slices.ContainsFunc(keys, func(k string) bool { return !slices.Contains(q, k) || touched(k) }) {
		return nil
	}
	return keys
}

// JobRead reads repo's forge checks' logs and steps, each once.
func (e *Effects) JobRead(repo string) JobRead {
	var jr JobRead
	if _, ok := e.Forge.(CheckLogs); ok {
		jr.Failure = func(r CheckRun) (CheckFailure, bool) { return e.CheckFailure(repo, r) }
	}
	if cs, ok := e.Forge.(CheckSteps); ok {
		jr.Steps = func(r CheckRun) ([]JobStep, bool) {
			if r.URL == "" {
				return nil, false
			}
			key := repo + " " + r.URL
			e.mu.Lock()
			steps, seen := e.steps[key]
			e.mu.Unlock()
			if seen {
				return steps, true
			}
			steps, err := cs.CheckSteps(repo, r)
			if err != nil {
				return nil, false
			}
			e.mu.Lock()
			if e.steps == nil || len(e.steps) > 500 {
				e.steps = map[string][]JobStep{}
			}
			e.steps[key] = steps
			e.mu.Unlock()
			return steps, true
		}
	}
	return jr
}

// Quarantine is the project's quarantined tests as evidence from outside
// task shows them, or none with no quarantine.
func (e *Effects) Quarantine(project, task string) []string {
	if e.Quarantined == nil {
		return nil
	}
	return e.Quarantined(project, task)
}

// ProseOnly says every file is prose (markdown, reStructuredText, AsciiDoc:
// docs, goals, the agents' prompts) and none is under a testdata directory,
// where tests read files as their data. A plain .txt file may be such data
// anywhere, so it isn't prose. The Hive's own tests ran every test that reads
// such a change on Linux, so a red forge check on it (another system, a flake)
// isn't its own.
func ProseOnly(files []string) bool {
	for _, f := range files {
		f = filepath.ToSlash(f)
		switch strings.ToLower(filepath.Ext(f)) {
		case ".md", ".rst", ".adoc":
		default:
			return false
		}
		if strings.HasPrefix(f, "testdata/") || strings.Contains(f, "/testdata/") {
			return false
		}
	}
	return len(files) > 0
}

// ChecksBesidesFailures sums up runs as ChecksState does with every failed
// check left out (and not required), and names those.
func ChecksBesidesFailures(runs []CheckRun, required []string) (state string, which []CheckRun, failed []string) {
	var kept []CheckRun
	for _, r := range runs {
		if r.State != CheckFail {
			kept = append(kept, r)
		} else if !slices.Contains(failed, r.Name) {
			failed = append(failed, r.Name)
		}
	}
	req := slices.DeleteFunc(slices.Clone(required), func(n string) bool { return slices.Contains(failed, n) })
	state, which = ChecksState(kept, req)
	return state, which, failed
}

// MainLookBack is how many of main's commits, newest first, MainAnswer reads
// for a finished run.
const MainLookBack = 10

// MainAnswer is main's runs of the checks that failed, each taken from the
// newest commit on main's first-parent line, from main back MainLookBack
// commits, where every run of that name has finished (passed or failed). A
// run still going, cancelled or that couldn't start is no answer: CI cancels
// a run on main when the next push supersedes it, and while merges keep
// coming main's newest commit may never finish one. A check with no answer
// keeps main's own runs of it. at names, per check, the older commit its
// answer came from.
func (e *Effects) MainAnswer(p Project, main string, failed []CheckRun) (runs []CheckRun, at map[string]string, err error) {
	if runs, err = e.Forge.Checks(p.Repo, main); err != nil {
		return nil, nil, err
	}
	var want []string
	for _, r := range failed {
		if r.State == CheckFail && !slices.Contains(want, r.Name) && !finished(runs, r.Name) {
			want = append(want, r.Name)
		}
	}
	if len(want) == 0 {
		return runs, nil, nil
	}
	out, err := Git(e.ws.Project(p.Name), "rev-list", "--first-parent", "-n", strconv.Itoa(MainLookBack), main)
	if err != nil {
		// A stale or missing clone: main's own runs are all there is, and
		// the journal says so once per main, so it doesn't pass unseen.
		if _, seen := e.unread.LoadOrStore(p.Name+"@"+main, true); !seen {
			e.j.MustAppend("effect.main_history_unread", map[string]string{"project": p.Name, "main": main, "error": Clip(err.Error(), 300)})
		}
		return runs, nil, nil
	}
	shas := strings.Fields(out)
	if len(shas) < 2 {
		return runs, nil, nil // no history to read: main's own runs are all there is
	}
	at = map[string]string{}
	for _, sha := range shas[1:] {
		if len(want) == 0 {
			break
		}
		older, err := e.Forge.Checks(p.Repo, sha)
		if err != nil {
			return nil, nil, err
		}
		want = slices.DeleteFunc(want, func(name string) bool {
			if !finished(older, name) {
				return false
			}
			runs = slices.DeleteFunc(runs, func(r CheckRun) bool { return r.Name == name })
			for _, r := range older {
				if r.Name == name {
					runs = append(runs, r)
				}
			}
			at[name] = sha
			return true
		})
	}
	return runs, at, nil
}

// finished reports whether runs hold a run named name and every one of them
// passed or failed.
func finished(runs []CheckRun, name string) bool {
	n := 0
	for _, r := range runs {
		if r.Name != name {
			continue
		}
		if r.State != CheckPass && r.State != CheckFail {
			return false
		}
		n++
	}
	return n > 0
}

// TestsOf names the failing tests in a failed check's log; ok is false when
// the log can't be read or names none (a build that broke, a step that isn't
// a test run).
type TestsOf func(CheckRun) (names []string, ok bool)

// newTests are the tests r fails that no run of its name on main fails; ok
// is false when a log on either side can't tell.
func newTests(r CheckRun, mainRuns []CheckRun, tests TestsOf) (fresh []string, ok bool) {
	if tests == nil {
		return nil, false
	}
	head, ok := tests(r)
	if !ok {
		return nil, false
	}
	var onMain []string
	for _, mr := range mainRuns {
		if mr.Name != r.Name {
			continue
		}
		names, ok := tests(mr)
		if !ok {
			return nil, false
		}
		onMain = append(onMain, names...)
	}
	for _, n := range head {
		if !slices.Contains(onMain, n) && len(fresh) < 10 {
			fresh = append(fresh, n)
		}
	}
	return fresh, true
}

// FailingTests reads the failing tests in a check's log on repo's forge, for
// ChecksAgainstMain, or nil when the forge can't read logs.
func (e *Effects) FailingTests(repo string) TestsOf {
	if _, ok := e.Forge.(CheckLogs); !ok {
		return nil
	}
	return func(r CheckRun) ([]string, bool) {
		f, ok := e.CheckFailure(repo, r)
		return f.Tests, ok && len(f.Tests) > 0
	}
}

// CheckFailure is what a failed check's log says: the tests it names as
// failing and the end of the step that failed.
type CheckFailure struct {
	Tests   []string `json:"tests,omitempty"`
	Excerpt string   `json:"excerpt,omitempty"`
	// Keys are the failing tests with their packages (FailedTestKeys).
	Keys []string `json:"keys,omitempty"`
	// OnlyTests says the log shows nothing failing apart from Keys.
	OnlyTests bool `json:"only_tests,omitempty"`
	// Head and Base are the commits the forge merged before running the
	// check (TestedMerge): a pull request's checks run on its head merged
	// with main as it was then, which may be older than main now.
	Head string `json:"head,omitempty"`
	Base string `json:"base,omitempty"`
}

// CheckFailure reads a failed check's log on repo's forge with the Hive's own
// token, so a worker (which holds none) gets it in its findings; ok is false
// when the forge can't read it. A log is read once: a run's log doesn't
// change, and another attempt has a URL of its own.
func (e *Effects) CheckFailure(repo string, r CheckRun) (CheckFailure, bool) {
	cl, ok := e.Forge.(CheckLogs)
	if !ok || r.URL == "" {
		return CheckFailure{}, false
	}
	key := repo + " " + r.URL
	e.mu.Lock()
	f, seen := e.failing[key]
	e.mu.Unlock()
	if seen {
		return f, true
	}
	log, err := cl.CheckLog(repo, r)
	if err != nil {
		return CheckFailure{}, false // not kept: the next pass tries again
	}
	keys, placed := FailedTestKeys(log)
	f = CheckFailure{Tests: FailedTests(log), Keys: keys, Excerpt: LogExcerpt(log, ExcerptLines), OnlyTests: placed && OnlyTests(log)}
	f.Head, f.Base = TestedMerge(log)
	e.mu.Lock()
	if e.failing == nil || len(e.failing) > 500 {
		e.failing = map[string]CheckFailure{}
	}
	e.failing[key] = f
	e.mu.Unlock()
	return f, true
}

// ExcerptLines is how much of a failed step's log goes in its findings.
const ExcerptLines = 100

// GitHub's job log marks a step's start with "##[group]Run " and an error
// with "##[error]", each line after a timestamp when the log is downloaded
// (and after a byte-order mark on its first line).
var (
	ghStepStart = regexp.MustCompile(`^(?:\x{FEFF})?(?:\d{4}-\d\d-\d\dT[\d:.]+Z )?##\[group\]Run `)
	ghStepError = regexp.MustCompile(`##\[error\]`)
)

// LogExcerpt is the last n lines of the step that failed in a CI job's log:
// the first step with an error mark (GitHub's "##[error]"), so a post step's
// error after it never hides it, up to that step's last error mark and from
// no earlier than its start. A log with no error mark gives its last n lines.
func LogExcerpt(log string, n int) string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(log, "\r\n", "\n"), "\n"), "\n")
	end := len(lines)
	first := slices.IndexFunc(lines, ghStepError.MatchString)
	if first >= 0 {
		end = first + 1
		for i := first + 1; i < len(lines) && !ghStepStart.MatchString(lines[i]); i++ {
			if ghStepError.MatchString(lines[i]) {
				end = i + 1
			}
		}
	}
	start := max(0, end-n)
	if first >= 0 {
		for i := end - 1; i >= start; i-- {
			if ghStepStart.MatchString(lines[i]) {
				start = i
				break
			}
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// ghTestMerge is the merge commit GitHub runs a pull request's checks on,
// whose message its checkout step prints ("HEAD is now at a9ae988 Merge
// <head> into <base>").
var ghTestMerge = regexp.MustCompile(`\bMerge ([0-9a-f]{40}) into ([0-9a-f]{40})\b`)

// TestedMerge is the head and base a CI job's log says it merged and ran
// on, or "" for both when it names no such merge (a push, not a PR).
func TestedMerge(log string) (head, base string) {
	if m := ghTestMerge.FindStringSubmatch(log); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

// CheckNames lists checks by name, for a message.
func CheckNames(runs []CheckRun) string {
	var names []string
	for _, r := range runs {
		if !slices.Contains(names, r.Name) {
			names = append(names, r.Name)
		}
	}
	return strings.Join(names, ", ")
}

// readOK reports whether sha has the reads it needs. A read is of a change's
// own diff, so it carries to a later head of it (main merged in) only while
// that head's own diff is the same, and a conflict resolution or anything
// else that changes the change's own files needs a new read.
func (e *Effects) readOK(p Project, ch Change, sha string) bool {
	project := p.Name
	if p.Self || len(ch.High) > 0 {
		// A release names the read of each merged head; a high class needs a
		// second, separate read.
		need := 1
		if len(ch.High) > 0 {
			need = 2
		}
		return len(e.Reads(project, sha, ch.DiffID)) >= need
	}
	if v, ok := e.verdict(project, sha, ch.DiffID); ok {
		return v.Verdict == "land"
	}
	h, ok := e.HostMade(project, sha)
	return ok && (h.Kind == "revert" || h.Kind == "setup")
}

// Reviewed reports whether sha, classified as ch, has the reads it needs to merge.
func (e *Effects) Reviewed(p Project, ch Change, sha string) bool { return e.readOK(p, ch, sha) }

// UpdateBranch merges main into the reviewed commit in a clean host checkout.
// The result is host-made; the read of the reviewed commit carries while the
// change's own diff stays the same, and it is tested again before it can
// merge. A base names the commit the merge is taken from instead of the
// common ancestor: the branch's own commit that
// main already holds as a squashed checkpoint, so only what came after it is
// laid onto main. A branch that has merged main since that checkpoint is
// merged plainly, from its newer common ancestor with main.
//
// Attributes (merge=union and the rest) are read from main, not the branch:
// a branch cut before main marked a file union merges it as main does, and a
// branch can't mark its own files union to make its conflicts disappear. A
// union merge that leaves a heading or line more times than either side had
// it is sent back as a conflict too (a *DoubledError), since union keeps
// both sides of an overlap and only the worker knows which to keep.
func (e *Effects) UpdateBranch(p Project, sha, mainSHA, base string) (string, error) {
	dir, err := e.cleanCheckout(p, sha, "update")
	if err != nil {
		return "", err
	}
	defer e.dropCheckout(dir)
	if _, err := Git(dir, "fetch", "-q", "origin", mainSHA); err != nil {
		return "", err
	}
	// attr.tree needs git 2.46; an older git ignores the key and reads the branch's own.
	attr := "attr.tree=" + mainSHA
	if base != "" && mergedSince(dir, sha, mainSHA, base) {
		// The branch merged main after its checkpoint, so its common
		// ancestor with main is newer than the checkpoint and already holds
		// the squashed copy: from the checkpoint, both sides would carry
		// main's changes and the branch's edits on top of them would conflict.
		base = ""
	}
	if base != "" {
		// Exit status 1 is a conflict; anything else (such as a git older
		// than 2.40, without --merge-base) is an error, not the worker's to fix.
		tree, err := Git(dir, "-c", attr, "merge-tree", "--write-tree", "--merge-base="+base, sha, mainSHA)
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
	} else if _, err := Git(dir, "-c", attr, "merge", "-q", "--no-edit", "-m", "Merge main into this branch", mainSHA); err != nil {
		Git(dir, "merge", "--abort")
		return "", ErrConflict
	}
	if d := doubled(dir, attr, sha, mainSHA); len(d) > 0 {
		return "", &DoubledError{Lines: d}
	}
	return e.hostCommit(p, dir, HostCommit{Project: p.Name, Kind: "update", Reviewed: sha, Main: mainSHA})
}

// mergedSince reports whether branch merged a commit of main after base:
// their common ancestor is not one base already holds.
func mergedSince(dir, branch, main, base string) bool {
	mb, err := Git(dir, "merge-base", branch, main)
	if err != nil {
		return false
	}
	_, err = Git(dir, "merge-base", "--is-ancestor", strings.TrimSpace(mb), base)
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}

// ErrConflict means a branch can't be brought up to date without the worker.
var ErrConflict = errors.New("conflicts with main")

// DoubledError is a conflict a union merge hid: each entry is "path: line"
// for a line the merge holds more times than either side did.
type DoubledError struct{ Lines []string }

func (d *DoubledError) Error() string {
	return "a union merge doubled lines: " + strings.Join(d.Lines, "; ")
}
func (d *DoubledError) Is(target error) bool { return target == ErrConflict }

// doubled lists, in the files both sides changed that main's attributes mark
// merge=union, each heading or line of some length that HEAD (their merge)
// holds more often than ours or theirs does. Blank lines, fences and rules
// repeat in any doc, so short lines that aren't headings are left out.
func doubled(dir, attr, ours, theirs string) []string {
	changed := func(from string) map[string]bool {
		out, _ := Git(dir, "-c", "core.quotePath=false", "diff", "--name-only", "-z", "--no-renames", from, "HEAD")
		m := map[string]bool{}
		for _, f := range strings.Split(out, "\x00") {
			if f != "" {
				m[f] = true
			}
		}
		return m
	}
	fromOurs, fromTheirs := changed(ours), changed(theirs)
	var out []string
	for f := range fromOurs {
		if !fromTheirs[f] {
			continue
		}
		if v, _ := Git(dir, "-c", attr, "check-attr", "merge", "--", f); !strings.HasSuffix(strings.TrimSpace(v), ": merge: union") {
			continue
		}
		count := func(rev string) map[string]int {
			s, _ := Git(dir, "show", rev+":"+f)
			m := map[string]int{}
			for _, l := range strings.Split(s, "\n") {
				if l = strings.TrimSpace(l); strings.HasPrefix(l, "#") || len(l) >= 12 {
					m[l]++
				}
			}
			return m
		}
		a, b := count(ours), count(theirs)
		for l, n := range count("HEAD") {
			if n > max(a[l], b[l]) {
				out = append(out, f+": "+l)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Revert makes a commit on top of main that reverts mergeSHA and records the
// lesson where the next worker reads it.
func (e *Effects) Revert(p Project, mainSHA, mergeSHA, lesson string) (string, error) {
	dir, err := e.cleanCheckout(p, mainSHA, "revert")
	if err != nil {
		return "", err
	}
	defer e.dropCheckout(dir)
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
	defer e.dropCheckout(dir)
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

// MainBundle writes main as a bundle a worker can fetch to resolve conflicts,
// and returns the commit it holds (read under the same lock, so it is the
// bundle's head even if main moves just after).
// It is written under a temp name and renamed, so a fetch running now sees the old or the new file whole.
func (e *Effects) MainBundle(p Project, path string) (string, error) {
	defer e.lock(p.Name)()
	dir := e.ws.Project(p.Name)
	head, err := Git(dir, "rev-parse", "--verify", p.Branch)
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if _, err := Git(dir, "bundle", "create", tmp, p.Branch); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return strings.TrimSpace(head), nil
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
