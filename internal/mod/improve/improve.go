// Package improve closes the loop: measure main after every merge, keep or
// revert against the best score the project has kept, and sweep the records
// for the next real gain.
package improve

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// Measurement is one measure of main.
type Measurement struct {
	Project string   `json:"project"`
	SHA     string   `json:"sha"`
	Task    string   `json:"task,omitempty"`
	TestsOK bool     `json:"tests_ok"`
	Score   *float64 `json:"score,omitempty"`
	Kept    bool     `json:"kept"`
	// NoResult says the check couldn't start (a tool it calls is missing), so
	// this is neither kept nor a failure, and main is measured again later.
	NoResult bool     `json:"no_result,omitempty"`
	Best     *float64 `json:"best,omitempty"` // best kept after this measure
	// Against is the merge's parent (main before it), scored beside it in
	// one sitting when it read past the tolerance below the best (#1865), or
	// where the best was read, measured again (#1343), when that can't be.
	// Paired is the merge's own reading in that sitting, judged against it.
	Against    *float64  `json:"against,omitempty"`
	AgainstSHA string    `json:"against_sha,omitempty"`
	Paired     *float64  `json:"paired,omitempty"`
	Version    string    `json:"version,omitempty"` // the score's version (Effects.ScoreVersion); a new one is judged against its parent under it
	Note       string    `json:"note"`
	At         time.Time `json:"at"`
	// Size is what the project's size command printed ("name value" lines),
	// for the Hive itself the core's size (docs/core.md): shown, never judged.
	Size map[string]float64 `json:"size,omitempty"`
}

// Sweep is one notice run.
type Sweep struct {
	ID      string    `json:"id"`
	Run     string    `json:"run"`
	Status  string    `json:"status"`
	Report  string    `json:"report,omitempty"`
	Asks    []string  `json:"asks,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

// Module is the self-improvement module.
type Module struct {
	C      *core.Core
	Agents *agents.Agents
	Work   *work.Module
	Chat   *chat.Module

	// OnKept is called after a merge is measured and kept.
	OnKept func(project, sha string)
	// Feedback lists agents' open feedback for the projects mine admits.
	Feedback func(mine func(project string) bool) string
	// Board keeps project's one problems-board entry under where: text
	// replaces what it says, and "" closes it (quarantine.go).
	Board func(project, where, text string)

	mu        sync.Mutex
	scores    map[string][]Measurement
	sweeps    map[string]*Sweep
	queue     chan work.Task
	lastSeen  int64 // journal seq at the last sweep start
	retryAt   map[string]time.Time
	pending   map[string]held      // project -> its newest merge waiting on a missing tool
	spells    map[string]spell     // task id -> its current stage and since when
	ended     map[string]time.Time // project@sha -> when its last test check was recorded (stalls.go)
	took      map[string][]float64 // task stage -> seconds its recent spells took
	stalls    map[string]*Stall
	fails     map[string][]core.Check                                                                 // project -> recent red test checks that name their failing tests
	heads     map[string][]string                                                                     // task id -> every head it had
	merged    map[string]time.Time                                                                    // merge sha -> when it merged
	sighted   map[string][]Sighting                                                                   // project\x00test -> its recent reds in the forge's checks (quarantine.go)
	fixes     map[string]string                                                                       // project\x00test -> the task filed to fix it
	lifted    map[string]time.Time                                                                    // project\x00test -> when its fix merged
	liftedBy  map[string]string                                                                       // project\x00test -> its fix's merge sha
	fixMerged map[string]bool                                                                         // fix tasks whose merge lifted their test
	boarded   map[string]string                                                                       // project -> the quarantined tests last put on the board
	boardRead bool                                                                                    // boarded has looked at every project once
	modules   map[string]string                                                                       // project -> its Go module path, "" for none (quarantine.go)
	kin       map[string]string                                                                       // task id -> the task it follows up or reverts
	judging   map[string]judgingMerge                                                                 // merge sha -> while its judge decides it (judge.go)
	judges    sync.WaitGroup                                                                          // judgeMerge calls in flight
	askJudge  func(p core.Project, t work.Task, ms Measurement, reason string) (core.Judgement, bool) // tests' stand-in for runJudge
	covered   map[string][]Covered                                                                    // project -> merges main already held when they closed
	cantRuns  map[string]int                                                                          // project@sha -> its checks that couldn't start

	stepbacks map[string]*StepBack
	busy      map[string]bool
	alerted   map[string]string // project -> the step-back alert last raised
	startMu   sync.Mutex        // one step-back starts at a time
}

// New loads measurements and sweeps.
func New(c *core.Core, a *agents.Agents, w *work.Module, ch *chat.Module) *Module {
	m := &Module{C: c, Agents: a, Work: w, Chat: ch, scores: map[string][]Measurement{}, sweeps: map[string]*Sweep{}, queue: make(chan work.Task, 64), retryAt: map[string]time.Time{}, pending: map[string]held{},
		spells: map[string]spell{}, ended: map[string]time.Time{}, took: map[string][]float64{}, stalls: map[string]*Stall{}, fails: map[string][]core.Check{}, heads: map[string][]string{}, merged: map[string]time.Time{}, sighted: map[string][]Sighting{}, fixes: map[string]string{}, lifted: map[string]time.Time{}, liftedBy: map[string]string{}, fixMerged: map[string]bool{}, boarded: map[string]string{}, modules: map[string]string{}, kin: map[string]string{}, judging: map[string]judgingMerge{}, covered: map[string][]Covered{}, cantRuns: map[string]int{}, stepbacks: map[string]*StepBack{}, busy: map[string]bool{}, alerted: map[string]string{}}
	apply := func(e core.Event) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.applyStall(e)
		m.applyQuarantine(e)
		switch e.Kind {
		case "score.measured":
			var ms Measurement
			e.Decode(&ms)
			m.scores[ms.Project] = append(m.scores[ms.Project], ms)
		case "score.cant-run":
			var x struct{ Project, SHA string }
			if e.Decode(&x) == nil {
				m.cantRuns[x.Project+"@"+x.SHA]++
			}
		case "score.covered":
			var x Covered
			if e.Decode(&x) == nil {
				x.At = e.At
				m.covered[x.Project] = append(m.covered[x.Project], x)
			}
		case "sweep.saved":
			var s Sweep
			e.Decode(&s)
			m.sweeps[s.ID] = &s
			if s.Status == "running" {
				m.lastSeen = e.Seq
			}
		case "effect.checked":
			var c core.Check
			e.Decode(&c)
			if c.Kind == "test" && !c.OK && len(c.Failed) > 0 && time.Since(c.At) < flakeWindow {
				fs := append(m.fails[c.Project], c)
				for len(fs) > 0 && time.Since(fs[0].At) > flakeWindow {
					fs = fs[1:]
				}
				m.fails[c.Project] = fs
			}
		case "effect.merged":
			var x struct {
				MergeSHA string `json:"merge_sha"`
			}
			if e.Decode(&x) == nil && x.MergeSHA != "" && time.Since(e.At) < flakeWindow {
				m.merged[x.MergeSHA] = e.At
			}
		case "task.saved":
			var t work.Task
			if e.Decode(&t) == nil {
				for _, sha := range []string{t.SHA, t.Checkpoint} {
					if sha != "" && !slices.Contains(m.heads[t.ID], sha) {
						m.heads[t.ID] = append(m.heads[t.ID], sha)
					}
				}
			}
		case "stepback.saved":
			var s StepBack
			e.Decode(&s)
			m.stepbacks[s.ID] = &s
		}
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	w.OnMerged = m.enqueue
	w.Retest = m.retest
	work.BestScore = m.Best
	work.Measured = m.measured
	c.Effects.Quarantined = m.QuarantinedTests
	c.Runs.Ops().Register(core.Op{Name: "pivot", Help: `{"case","evidence","project"} -> any agent proposes a pivot: a short case (what to stop, throw away or change, and what instead) and the evidence for it. It is filed as a fix, first in line, its worker weighs the case before acting and nothing bounds what it may throw away; the project's lead and the person hear it at once and turn it down with withdraw`, Fn: m.opPivot})
	go m.measureLoop()
	return m
}

// Scores returns a project's measurements, oldest first.
func (m *Module) Scores(project string) []Measurement {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Measurement{}, m.scores[project]...)
}

// Best returns the best kept score.
func (m *Module) Best(project string) (float64, bool) {
	k, ok := m.kept(project)
	if !ok {
		return 0, false
	}
	return *k.Best, true
}

// kept is the last kept measurement with a best score.
func (m *Module) kept(project string) (Measurement, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.scores[project]
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Kept && ms[i].Best != nil {
			return ms[i], true
		}
	}
	return Measurement{}, false
}

// version is the score's version at sha, or "" with no score.
func (m *Module) version(p core.Project, sha string, cfg core.ProjectConfig) string {
	if cfg.Score == nil {
		return ""
	}
	return m.C.Effects.ScoreVersion(p, sha, *cfg.Score)
}

// versionOf is x's score version, worked out at its commit when x was
// recorded before measurements carried one.
func (m *Module) versionOf(p core.Project, x Measurement) string {
	if x.Version != "" {
		return x.Version
	}
	return m.version(p, x.SHA, m.C.Effects.Config(p, x.SHA))
}

func (m *Module) measured(project, sha string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slices.ContainsFunc(m.covered[project], func(c Covered) bool { return c.SHA == sha }) {
		return true
	}
	for _, ms := range m.scores[project] {
		if ms.SHA == sha && !ms.NoResult && (ms.Kept || ms.Task != "") {
			return true
		}
	}
	return false
}

func (m *Module) hasBaseline(project string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ms := range m.scores[project] {
		if ms.Kept {
			return true
		}
	}
	return false
}

func (m *Module) record(ms Measurement) {
	ms.At = time.Now().UTC()
	m.C.J.MustAppend("score.measured", ms)
	core.AppendJSONL(filepath.Join(m.C.WS.Hive(), "scores", ms.Project+".jsonl"), ms)
}

// measure runs tests and the score on sha in clean checkouts. cant is the
// check that couldn't start for want of a tool (its CantRun set), which is no
// result either way; a check that did run clears the alert for its kind.
// A red on quarantined tests alone, quarantined on evidence from outside
// task in packages files (what sha changed) don't touch, is read past as the
// PRs' checks read it (core.Effects.ExcusedLocal), so one known-broken test
// can't leave main unscored. A merge (task set) whose files can't be read
// has nothing excused.
func (m *Module) measure(p core.Project, sha, task string, files []string) (ok bool, score *float64, note string, cant core.Check) {
	cfg := m.C.Effects.Config(p, sha)
	tc := m.C.Effects.RunCheck(p, sha, "test", cfg.Test)
	if tc.CantRun != "" {
		return false, nil, "tests " + tc.CantRun + ": " + core.Clip(tc.Output, 300), tc
	}
	m.Work.RanCheck(p.Name, "test")
	flaky := "" // a red that passed when what failed ran again
	var excused []string
	if task == "" || files != nil {
		excused = m.C.Effects.ExcusedLocal(p, sha, task, files, tc)
	}
	if len(excused) > 0 {
		flaky = "tests red only on quarantined " + strings.Join(excused, ", ") + " (read past, as PRs' checks do); "
	} else if !tc.OK {
		sum, flake, rc := m.redTests(p, sha, cfg.Test, tc)
		if rc.CantRun != "" {
			rc.Kind = "test"
			return false, nil, "tests red, then " + rc.CantRun + " when what failed ran again: " + core.Clip(rc.Output, 300), rc
		}
		if !flake {
			return false, nil, "tests red: " + sum, cant
		}
		flaky = "tests flaky (red, then green when what failed ran again; filed to fix): " + sum + "; "
	}
	if cfg.Score == nil {
		if flaky != "" {
			return true, nil, flaky + "no score configured", cant
		}
		return true, nil, "tests green (no score configured)", cant
	}
	sc := m.C.Effects.RunCheck(p, sha, "score", cfg.Score.Command)
	if sc.CantRun != "" {
		return false, nil, "score " + sc.CantRun + ": " + core.Clip(sc.Output, 300), sc
	}
	m.Work.RanCheck(p.Name, "score")
	if !sc.OK || sc.Score == nil {
		return true, nil, flaky + "score did not produce a number: " + core.Clip(sc.Output, 600), cant
	}
	return true, sc.Score, flaky + fmt.Sprintf("score %g", *sc.Score), cant
}

// mergeFiles is what t's merge changed on main: from the parent of its first
// merge (a checkpoint merged earlier is part of the change) to its merge.
// It is nil when that can't be read, and measure then excuses nothing.
func (m *Module) mergeFiles(p core.Project, t work.Task) []string {
	first := t.MergeSHA
	if len(t.Banks) > 0 {
		first = t.Banks[0].MergeSHA
	}
	out, err := core.Git(m.C.WS.Project(p.Name), "-c", "core.quotePath=false", "diff", "--name-only", "-z", "--no-renames", first+"^1", t.MergeSHA)
	if err != nil {
		return nil
	}
	return strings.FieldsFunc(out, func(r rune) bool { return r == 0 })
}

// bestAt is the commit the best kept score was read at: the newest kept
// measurement that scored it, itself or main measured again beside it. A
// fall is judged there, not at the last kept merge, so small falls kept one
// after another can't walk the baseline down.
func (m *Module) bestAt(project string, best float64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ms := m.scores[project]
	for i := len(ms) - 1; i >= 0; i-- {
		switch x := ms[i]; {
		case !x.Kept:
		case x.Score != nil && *x.Score == best:
			return x.SHA
		case x.Against != nil && *x.Against == best:
			return x.AgainstSHA
		}
	}
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].Kept && ms[i].Best != nil {
			return ms[i].SHA
		}
	}
	return ""
}

// scoreNow measures the score at sha, a commit already kept, again now, so
// a merge's reading is compared with one taken under the same load. It is
// false when there is no number, and the stored best stands.
func (m *Module) scoreNow(p core.Project, sha string) (float64, bool) {
	cfg := m.C.Effects.Config(p, sha)
	if cfg.Score == nil || sha == "" {
		return 0, false
	}
	defer m.Work.MeasuringMain(p.Name, sha)() // the merge's keep/revert waits on it
	sc := m.C.Effects.RunCheck(p, sha, "score", cfg.Score.Command)
	if sc.CantRun != "" || !sc.OK || sc.Score == nil {
		return 0, false
	}
	return *sc.Score, true
}

// paired scores a merge beside its parent (main before it) in one sitting,
// A B B A with first the merge's reading already taken, so both are read
// under the same load (issue #1865). Each side is its better reading: load
// only slows a run, so a spike on one reading is dropped, not averaged in.
// A merge that changed the score has its parent scored under the merge's
// evaluator (underScoreOf), so the change is still judged (#2090). It gives
// the parent, both readings and all four in order; false when the merge has
// no parent, its score can't be laid over it, or a reading gave no number.
func (m *Module) paired(p core.Project, sha string, first float64) (parent string, mine, theirs float64, readings string, ok bool) {
	out, err := core.Git(m.C.WS.Project(p.Name), "rev-parse", "-q", "--verify", sha+"^")
	parent = strings.TrimSpace(out)
	cfg := m.C.Effects.Config(p, sha)
	if err != nil || parent == "" || cfg.Score == nil {
		return "", 0, 0, "", false
	}
	at, under := parent, ""
	if m.version(p, parent, m.C.Effects.Config(p, parent)) != m.version(p, sha, cfg) {
		var done func()
		if at, done, err = m.underScoreOf(p, parent, sha, *cfg.Score); err != nil {
			return "", 0, 0, "", false
		}
		defer done()
		under = " under this merge's score"
	}
	defer m.Work.MeasuringMain(p.Name, sha)()
	read := func(at string) (float64, bool) {
		sc := m.C.Effects.RunCheck(p, at, "score", cfg.Score.Command)
		if sc.CantRun != "" || !sc.OK || sc.Score == nil {
			return 0, false
		}
		return *sc.Score, true
	}
	p1, ok1 := read(at)
	p2, ok2 := read(at)
	m2, ok3 := read(sha)
	if !ok1 || !ok2 || !ok3 {
		return "", 0, 0, "", false
	}
	return parent, cfg.Score.Best(first, m2), cfg.Score.Best(p1, p2), fmt.Sprintf("merge %g, parent%s %g, parent %g, merge %g", first, under, p1, p2, m2), true
}

// underScoreOf makes, in the project's clone, a commit holding parent's
// tree with sha's evaluator (its score's paths and ./ files, and
// .hive/project.json, which names the score) in place of parent's own, and
// a ref holding it while it is scored; done drops the ref. A check of it is
// the parent's code judged by the merge's score. Only the score runs on it:
// it carries the merge's whole project.json, test command included, so its
// tests would be the merge's, not the parent's.
func (m *Module) underScoreOf(p core.Project, parent, sha string, s core.Score) (string, func(), error) {
	dir := m.C.WS.Project(p.Name)
	paths := append([]string{".hive/project.json"}, s.Evaluator()...)
	if old := m.C.Effects.Config(p, parent).Score; old != nil {
		paths = append(paths, old.Evaluator()...) // one the merge dropped goes too
	}
	isEval := func(f string) bool {
		return slices.ContainsFunc(paths, func(e string) bool { return f == e || strings.HasPrefix(f, e+"/") })
	}
	// files lists at's files under the evaluator, as git ls-tree gives them.
	files := func(at string) ([]string, error) {
		out, err := core.Git(dir, "ls-tree", "-r", "-z", "--full-tree", at)
		var got []string
		for _, l := range strings.Split(out, "\x00") {
			if _, f, ok := strings.Cut(l, "\t"); ok && isEval(f) {
				got = append(got, l)
			}
		}
		return got, err
	}
	old, err := files(parent)
	if err != nil {
		return "", nil, err
	}
	now, err := files(sha)
	if err != nil {
		return "", nil, err
	}
	tmp, err := os.MkdirTemp("", "hive-underscore-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	git := func(args ...string) (string, error) { return core.GitEnvT(2*time.Minute, dir, env, args...) }
	if _, err := git("read-tree", parent); err != nil {
		return "", nil, err
	}
	gone := []string{"update-index", "--force-remove", "--"}
	for _, l := range old {
		_, f, _ := strings.Cut(l, "\t")
		gone = append(gone, f)
	}
	add := []string{"update-index", "--add"}
	for _, l := range now {
		meta, f, _ := strings.Cut(l, "\t")
		if m := strings.Fields(meta); len(m) == 3 {
			add = append(add, "--cacheinfo", m[0]+","+m[2]+","+f)
		}
	}
	// In batches, so a large evaluator (hive-eval's verdicts/) stays under
	// the argument-length limit.
	for _, args := range [][]string{gone, add} {
		head, n := 2, 1
		if args[1] == "--add" {
			n = 2 // --cacheinfo and its value
		}
		if args[1] == "--force-remove" {
			head = 3
		}
		for i := head; i < len(args); i += 500 * n {
			if _, err := git(append(slices.Clone(args[:head]), args[i:min(i+500*n, len(args))]...)...); err != nil {
				return "", nil, err
			}
		}
	}
	tree, err := git("write-tree")
	if err != nil {
		return "", nil, err
	}
	c, err := git("commit-tree", tree, "-p", parent, "-m", "Score "+core.Short(parent)+" under "+core.Short(sha)+"'s score")
	if err != nil {
		return "", nil, err
	}
	c = strings.TrimSpace(c)
	ref := "refs/hive/scored/" + core.Short(parent) + "-" + core.Short(sha)
	if _, err := core.Git(dir, "update-ref", ref, c); err != nil {
		return "", nil, err
	}
	return c, func() { core.Git(dir, "update-ref", "-d", ref) }, nil
}

// SelfSize is the Hive's own size command, run for a self project whose
// project.json names none while the script is in its tree. A merge counts
// lines only (SelfLines): the release build would hold a check slot for a
// minute on every merge, so the binary is measured at the start, which comes
// with every daemon start and Hive update.
const (
	SelfSize  = "sh tests/core-size.sh"
	SelfLines = SelfSize + " --lines"
)

// size runs the project's size command (project.json "size") on sha in a
// clean checkout and reads each "name value" line it prints; full says
// whether the Hive's own counts its binary too. It is nil when there is no
// command or it fails: a size is shown, never judged. A command that ran and
// gave nothing says so in the note, so the record shows sizes stopped.
func (m *Module) size(p core.Project, sha string, full bool) (map[string]float64, string) {
	var pc struct{ Size string }
	json.Unmarshal([]byte(m.C.Effects.ReadFile(p, sha, ".hive/project.json")), &pc)
	cmd := strings.TrimSpace(pc.Size)
	if cmd == "" && p.Self && m.C.Effects.ReadFile(p, sha, "tests/core-size.sh") != "" {
		cmd = SelfLines
		if full {
			cmd = SelfSize
		}
	}
	if cmd == "" {
		return nil, ""
	}
	c := m.C.Effects.RunCheck(p, sha, "size", cmd)
	if !c.OK || c.Log == "" {
		return nil, "; size not recorded: " + core.Clip(cmp.Or(c.Output, "the size command failed"), 200)
	}
	out, err := core.SafeRead(c.Log, 1<<20) // the full log: a check's Output is clipped to one line
	if err != nil {
		return nil, "; size not recorded: " + err.Error()
	}
	got := sizes(string(out))
	if got == nil {
		return nil, "; size not recorded: the size command printed no \"name value\" line"
	}
	return got, ""
}

// sizes reads "name value" lines; any other line is left out.
func sizes(out string) map[string]float64 {
	var got map[string]float64
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if got == nil {
			got = map[string]float64{}
		}
		got[f[0]] = v
	}
	return got
}

// Tick measures the starting point of projects that have none yet. A red
// start is measured again until there is a number.
func (m *Module) Tick() {
	m.Watch()
	m.boardQuarantined()
	if _, err := m.C.Sandbox(); err != nil {
		return
	}
	m.releaseHeld()
	for _, p := range m.C.Projects.List() {
		if !p.Ready || m.hasBaseline(p.Name) {
			continue
		}
		m.mu.Lock()
		due := time.Now().After(m.retryAt[p.Name])
		if due {
			m.retryAt[p.Name] = time.Now().Add(10 * time.Minute)
		}
		m.mu.Unlock()
		if !due {
			continue
		}
		go m.baseline(p)
	}
}

func (m *Module) baseline(p core.Project) {
	main, err := m.C.Effects.Sync(p)
	if err != nil || main == "" {
		m.mu.Lock()
		m.retryAt[p.Name] = time.Now().Add(30 * time.Second) // nothing to measure yet
		m.mu.Unlock()
		return
	}
	defer m.Work.MeasuringMain(p.Name, main)() // its checks go ahead of the PRs'
	cfg := m.C.Effects.Config(p, main)
	ok, score, note, cant := m.measure(p, main, "", nil)
	ms := Measurement{Project: p.Name, SHA: main, TestsOK: ok, Score: score, Note: "start: " + note, Version: m.version(p, main, cfg), NoResult: cant.CantRun != ""}
	if ms.NoResult {
		m.noResult(p, ms, cant.Kind) // Tick measures again in 10 minutes
		return
	}
	m.warnUnlisted(p, cfg)
	var sizeNote string
	ms.Size, sizeNote = m.size(p, main, true)
	ms.Note += sizeNote
	if ok && (score != nil || cfg.Score == nil) {
		ms.Kept, ms.Best = true, score
		m.record(ms)
		return
	}
	m.noStart(p, ms)
}

// noStart records a start with no number and raises the alert. Once it has
// failed more than once at one commit, the project's lead gets it as work,
// once per commit: a retry alone won't fix a command whose output can't be read.
func (m *Module) noStart(p core.Project, ms Measurement) {
	m.record(ms)
	note := strings.TrimPrefix(ms.Note, "start: ")
	text, key := baselineAlert(p.Name, ms.TestsOK, note)
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "baseline", "project": p.Name, "key": key, "text": text})
	n := 0
	for _, x := range m.Scores(p.Name) {
		if x.SHA == ms.SHA && !x.Kept && !x.NoResult && x.Task == "" {
			n++
		}
	}
	if n < 2 {
		return
	}
	head := "Main of " + p.Name + " at " + core.Short(ms.SHA) + " has failed"
	for _, x := range m.Chat.Messages("project:" + p.Name) {
		if x.From == "improve" && strings.HasPrefix(x.Text, head) {
			return // told already at this commit
		}
	}
	m.Chat.EnsureProject(p.Name)
	m.Chat.Post("project:"+p.Name, "", "improve", head+" to give a starting score more than once, so the keep-or-revert loop can't judge any change. "+
		"Look at the output below, fix the test or score command in .hive/project.json (the score is the last word of the output, or the one line that is a number alone), and ask the person if you can't:\n"+core.Clip(note, 1500))
}

// noResult records a measure whose check couldn't start, once per commit, and
// says which tool is missing, once while the project's alert is open. Nothing
// is kept or reverted over it.
func (m *Module) noResult(p core.Project, ms Measurement, kind string) {
	for _, x := range m.Scores(p.Name) {
		if x.SHA == ms.SHA && x.NoResult && x.Note == ms.Note {
			return // recorded and raised already at this commit
		}
	}
	m.record(ms)
	m.Work.RaiseCantRun(p.Name, kind,
		"main of "+p.Name+" at "+core.Short(ms.SHA)+" has no result: "+core.Clip(strings.TrimPrefix(ms.Note, "start: "), 200)+". That is a fault in the environment, not a verdict on the commit: nothing is reverted, and main is measured again every 10 minutes until it is fixed")
}

// warnUnlisted raises an alert when a score is set with no evaluator paths:
// only a ./ file its command names is then protected from a candidate, not a
// script that file calls, a test or its data.
func (m *Module) warnUnlisted(p core.Project, cfg core.ProjectConfig) {
	if cfg.Score != nil && len(cfg.Score.Paths) == 0 {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "score-paths", "ref": "score-paths:" + p.Name, "project": p.Name,
			"text": p.Name + "'s score lists no evaluator files in its `paths`, so a change could alter how it's scored; list the files the score reads in .hive/project.json"})
	}
}

// triedOut counts one more measure of sha whose check couldn't find its tool
// and says whether that makes work.CantRunTries: a commit can fake a missing
// tool (print "not found", exit 127), and its merge isn't held unjudged on
// that forever. A full disk or quota isn't counted: a commit can't fake that,
// and it only waits. The count is journaled, so a restart doesn't start it
// over, and it is one per measure however many checks that measure ran.
func (m *Module) triedOut(project, sha string, cant core.Check) bool {
	if cant.Tool == "" {
		return false
	}
	m.C.J.MustAppend("score.cant-run", map[string]string{"project": project, "sha": sha, "tool": cant.Tool})
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cantRuns[project+"@"+sha] >= work.CantRunTries
}

// closeCantRunAsks answers p's open asks for a tool a merge's measures
// couldn't find, once main measures with a result at sha: the tool runs now,
// or the merge that only claimed it was missing is gone.
func (m *Module) closeCantRunAsks(p core.Project, sha string) {
	measured := map[string]bool{}
	for _, x := range m.Scores(p.Name) {
		measured["cant-run:"+x.SHA] = true
	}
	for _, a := range m.C.Person.Open() {
		if a.Project == p.Name && a.Kind == "question" && measured[a.Ref] {
			m.C.Person.Answer(a.ID, "ok", "the Hive, not the person: main at "+core.Short(sha)+" measures with a result now")
		}
	}
}

// retryNoResult is how long a merge whose check couldn't start waits before
// it is measured again.
var retryNoResult = 10 * time.Minute

// held is a merge waiting to be measured again once its missing tool is there.
type held struct {
	t   work.Task
	due time.Time
}

// hold keeps a merge whose check couldn't start for Tick to measure again in
// retryNoResult. Only the newest such merge of a project waits: once the tool
// is there, measuring one a later merge has superseded would judge a main
// that is gone. After a restart Recover finds them all again (their only
// records are no_result), and they are held here the same way.
func (m *Module) hold(p core.Project, t work.Task) {
	m.mu.Lock()
	old, ok := m.pending[p.Name]
	m.mu.Unlock()
	if ok && old.t.MergeSHA != t.MergeSHA && !m.after(p, t.MergeSHA, old.t.MergeSHA) {
		return // a later merge waits already
	}
	m.mu.Lock()
	m.pending[p.Name] = held{t: t, due: time.Now().Add(retryNoResult)}
	m.mu.Unlock()
}

// releaseHeld queues the held merges whose wait is over.
func (m *Module) releaseHeld() {
	m.mu.Lock()
	var due []work.Task
	for name, h := range m.pending {
		if time.Now().After(h.due) {
			due = append(due, h.t)
			delete(m.pending, name)
		}
	}
	m.mu.Unlock()
	for _, t := range due {
		go func() { m.queue <- t }()
	}
}

// after says merge a came after merge b on p's main.
func (m *Module) after(p core.Project, a, b string) bool {
	if a == b {
		return false
	}
	_, err := core.Git(m.C.WS.Project(p.Name), "merge-base", "--is-ancestor", b, a)
	return err == nil
}

// superseded says a merge that had no result has since been passed by a later
// merge on the same project that was measured, so it isn't measured any more.
func (m *Module) superseded(p core.Project, sha string) bool {
	var noResult bool
	var later []string
	for _, x := range m.Scores(p.Name) {
		switch {
		case x.SHA == sha:
			noResult = noResult || x.NoResult
		case x.Task != "" && !x.NoResult:
			later = append(later, x.SHA)
		}
	}
	return noResult && slices.ContainsFunc(later, func(l string) bool { return m.after(p, l, sha) })
}

// Covered is a merge that wasn't measured on its own: main already held it
// when it was measured at By. Red says that measurement wasn't kept, so the
// change went in under a red main and a reader can't take it as cleared.
type Covered struct {
	Project string    `json:"project"`
	SHA     string    `json:"sha"`
	Task    string    `json:"task"`
	By      string    `json:"by"`
	Red     bool      `json:"red,omitempty"`
	At      time.Time `json:"-"`
}

// coveredBy is the measurement of a commit on p's main that contains sha, so
// measuring sha now would test old code (a pull request that landed hours
// before its task closed) and blame it on main. Main is linear, so the newest
// measurement with a result is the one to ask: one ancestry check, however
// long the history. It trusts measurements to be recorded in main's order, so
// a merge that comes back late (held for a missing tool, or requeued after a
// failed sync while a later merge was measured) is covered by that later
// measurement, red or not, as superseded already treats it; the Red mark on
// its score.covered line is what tells a reader the later red may be its.
func (m *Module) coveredBy(p core.Project, sha string) (Measurement, bool) {
	for _, x := range slices.Backward(m.Scores(p.Name)) {
		if !x.NoResult {
			return x, m.after(p, x.SHA, sha)
		}
	}
	return Measurement{}, false
}

// introduced says the merge itself brought in the command that calls the
// missing tool: the same command on main before it didn't name that tool. A
// misspelt tool is then the change's fault, and red, not a wait. A tool
// called from a script the command runs isn't seen, and still waits.
func (m *Module) introduced(p core.Project, sha string, cant core.Check) bool {
	parent, err := core.Git(m.C.WS.Project(p.Name), "rev-parse", "-q", "--verify", sha+"^")
	if err != nil || cant.Tool == "" {
		return false
	}
	command := func(at string) string {
		cfg := m.C.Effects.Config(p, at)
		if cant.Kind != "score" {
			return cfg.Test
		}
		if cfg.Score == nil {
			return ""
		}
		return cfg.Score.Command
	}
	names := func(cmd string) bool {
		return slices.Contains(strings.FieldsFunc(cmd, func(r rune) bool { return strings.ContainsRune(" \t\n;&|()<>`$\"'=", r) }), cant.Tool)
	}
	now, before := command(sha), command(strings.TrimSpace(parent))
	return now != before && names(now) && !names(before)
}

// enqueue queues a merge to be measured without waiting for room: it is
// called from a task's merge step, and a full queue (a restart's Recover
// finding many unmeasured merges, a slow measure) held the step, so its
// task sat in merged and never reached done. In order while there is room.
func (m *Module) enqueue(t work.Task) {
	select {
	case m.queue <- t:
	default:
		go func() { m.queue <- t }()
	}
}

// measureLoop measures merges as they come. When it is behind, it takes
// every merge waiting at once and measures each project's newest first, so
// main is judged as it stands now; the older ones then come through
// afterMerge covered by it, rather than each one holding the queue for a
// whole measurement of code main has long moved past.
func (m *Module) measureLoop() {
	for t := range m.queue {
		batch := []work.Task{t}
		for more := true; more; {
			select {
			case u := <-m.queue:
				batch = append(batch, u)
			default:
				more = false
			}
		}
		for _, t := range m.newestFirst(batch) {
			m.afterMerge(t)
		}
	}
}

// newestFirst puts each project's newest merge on main ahead of the rest,
// which keep their order.
func (m *Module) newestFirst(batch []work.Task) []work.Task {
	if len(batch) < 2 {
		return batch
	}
	newest := map[string]int{}
	for i, t := range batch {
		if j, ok := newest[t.Project]; !ok || m.after(core.Project{Name: t.Project}, t.MergeSHA, batch[j].MergeSHA) {
			newest[t.Project] = i
		}
	}
	var first, rest []work.Task
	for i, t := range batch {
		if newest[t.Project] == i {
			first = append(first, t)
		} else {
			rest = append(rest, t)
		}
	}
	return append(first, rest...)
}

// Recover re-queues merges whose measurement didn't happen (after a restart).
func (m *Module) Recover() {
	for _, t := range m.Work.List(func(t work.Task) bool { return t.MergeSHA != "" }) {
		if time.Since(t.Updated) >= 7*24*time.Hour {
			continue
		}
		for _, b := range t.Banks { // a checkpoint merged before the latest one
			if b.MergeSHA != t.MergeSHA && !m.measured(t.Project, b.MergeSHA) {
				c := t
				c.MergeSHA = b.MergeSHA
				m.enqueue(c)
			}
		}
		if !m.measured(t.Project, t.MergeSHA) {
			m.holdForJudge(t)
			m.enqueue(t)
		}
	}
}

// holdForJudge marks a merge whose judge run is still going after a restart
// as with its judge, so later merges of its project wait for the baseline it
// settles from the start, not only once it is measured again.
func (m *Module) holdForJudge(t work.Task) {
	for i := 0; i < work.JudgeTries; i++ {
		if r, ok := m.C.Runs.Get(fmt.Sprintf("j-%s-%s-%d", t.ID, core.Short(t.MergeSHA), i)); ok && !r.Terminal() {
			m.mu.Lock()
			if _, busy := m.judging[t.MergeSHA]; !busy {
				m.judging[t.MergeSHA] = judgingMerge{project: t.Project}
			}
			m.mu.Unlock()
			return
		}
	}
}

// afterMerge measures main at the merge and keeps or reverts.
func (m *Module) afterMerge(t work.Task) {
	m.mu.Lock()
	j := m.judging[t.MergeSHA]
	m.mu.Unlock()
	if j.running {
		return // with its judge already: measuring it again would only repeat the checks
	}
	// A mark found after a restart (holdForJudge) holds later merges until
	// this one is settled here, or with its judge again, which finds its run
	// by id; it stays only while a failed sync brings the merge back.
	later := false
	defer func() {
		m.mu.Lock()
		if j, ok := m.judging[t.MergeSHA]; ok && !j.running && !later {
			delete(m.judging, t.MergeSHA)
		}
		m.mu.Unlock()
	}()
	p, ok := m.C.Projects.Get(t.Project)
	if !ok || m.measured(p.Name, t.MergeSHA) || m.superseded(p, t.MergeSHA) {
		return
	}
	if m.judgingBefore(p.Name, t.MergeSHA) {
		// An earlier merge's judge decides the baseline this one is measured
		// against: wait for it, rather than judge the same fall again. A
		// mark of its own from a restart stays while a running judge holds
		// it; two marks alone never hold each other.
		later = m.judgeRunning(p.Name, t.MergeSHA)
		go func() { time.Sleep(judgeHold); m.queue <- t }()
		return
	}
	if _, err := m.C.Effects.Sync(p); err != nil {
		later = true
		go func() { time.Sleep(time.Minute); m.queue <- t }()
		return
	}
	if by, ok := m.coveredBy(p, t.MergeSHA); ok {
		m.C.J.MustAppend("score.covered", Covered{Project: p.Name, SHA: t.MergeSHA, Task: t.ID, By: by.SHA, Red: !by.Kept})
		return
	}
	defer m.Work.MeasuringMain(p.Name, t.MergeSHA)() // keep/revert, the release and the next baseline wait on it
	cfg := m.C.Effects.Config(p, t.MergeSHA)
	ok, score, note, cant := m.measure(p, t.MergeSHA, t.ID, m.mergeFiles(p, t))
	switch {
	case cant.CantRun != "" && m.introduced(p, t.MergeSHA, cant) && cant.Kind == "score":
		ok, score, note = true, nil, "score did not produce a number: this merge's score command calls "+cant.Tool+", which isn't installed where checks run: "+note
	case cant.CantRun != "" && m.introduced(p, t.MergeSHA, cant):
		ok, note = false, "tests red: this merge's test command calls "+cant.Tool+", which isn't installed where checks run: "+note
	case cant.CantRun != "" && m.triedOut(p.Name, t.MergeSHA, cant):
		ok, note = false, fmt.Sprintf("%s %s, %d tries in a row since it merged, which counts as red: %s", cant.Kind, cant.CantRun, work.CantRunTries, note)
		m.C.Person.Ask(core.Ask{Kind: "question", Ref: "cant-run:" + t.MergeSHA, Project: p.Name, Options: []string{"ok"},
			Title:  "Install " + cmp.Or(cant.Tool, "a tool") + " where checks run for " + p.Name + "?",
			Detail: "Main of " + p.Name + " at " + core.Short(t.MergeSHA) + " (" + t.Label() + ") " + cant.CantRun + " every time it was measured since it merged. That now counts as red, so the merge is reverted. If the tool is really missing, install it where checks run; if it isn't, the change only claimed it was."})
	case cant.CantRun != "":
		m.noResult(p, Measurement{Project: p.Name, SHA: t.MergeSHA, Task: t.ID, Note: note, Version: m.version(p, t.MergeSHA, cfg), NoResult: true}, cant.Kind)
		m.hold(p, t)
		return
	case cant.CantRun == "":
		m.closeCantRunAsks(p, t.MergeSHA)
	}
	flake := ""
	if !ok {
		flake = m.seenElsewhere(p, t)
	}
	if !ok && flake == "" && cant.CantRun == "" {
		flake = m.redBefore(p, t, cfg.Test)
	}
	k, hasBest := m.kept(p.Name)
	best := 0.0
	if hasBest {
		best = *k.Best
	}
	size, sizeNote := m.size(p, t.MergeSHA, false)
	note += sizeNote
	ms := Measurement{Project: p.Name, SHA: t.MergeSHA, Task: t.ID, TestsOK: ok, Score: score, Note: note, Version: m.version(p, t.MergeSHA, cfg), Size: size}
	changed := false
	if !hasBest || m.versionOf(p, k) != ms.Version {
		m.warnUnlisted(p, cfg)
		// The score itself changed: the old best can't be compared with it.
		changed, hasBest = hasBest, false
	}
	worse, dip := !ok, false
	reason := ""
	switch {
	case !ok && flake != "":
		ms.Note = flake + "; not reverted; " + note
		m.record(ms)
		return
	case !ok:
		reason = "the tests went red after it merged: " + strings.TrimPrefix(note, "tests red: ")
	case cfg.Score != nil && score == nil:
		worse, reason = true, "the score stopped producing a number"
	case changed && score != nil: // a merge that drops the score is kept with none, below
		// The merge is still judged (#2090): its parent is scored under the
		// merge's own score, beside it. Only when that gives no number does
		// the merge start a new baseline unjudged.
		parent, mine, theirs, readings, ok := m.paired(p, t.MergeSHA, *score)
		if !ok {
			ms.Note = "the score changed and its parent gave no number under the new one, so this starts a new baseline; " + note
			break
		}
		ms.Against, ms.AgainstSHA, ms.Paired = &theirs, parent, &mine
		if cfg.Score.WithinTolerance(mine, theirs) {
			ms.Kept, ms.Best = true, &mine
			ms.Note = fmt.Sprintf("kept: the score changed, and under the new one it reads %g against its parent %s's %g, in one sitting (%s); the baseline is %g from here; %s", mine, core.Short(parent), theirs, readings, mine, note)
			m.record(ms)
			if m.OnKept != nil {
				m.OnKept(p.Name, t.MergeSHA)
			}
			return
		}
		worse, dip, reason = true, true, fmt.Sprintf("the score changed, and under the new one it fell to %g from its parent %s's %g, both scored in one sitting (%s; tolerance %g)", mine, core.Short(parent), theirs, readings, cfg.Score.Tolerance)
	case cfg.Score != nil && hasBest && !cfg.Score.WithinTolerance(*score, best):
		// The score is speed, so it moves with the machine's load (issues
		// #1343, #1865): the same code read 18 points apart at different
		// times. The merge is judged against its parent scored beside it,
		// interleaved, and against the best only when that can't be.
		if parent, mine, theirs, readings, ok := m.paired(p, t.MergeSHA, *score); ok {
			ms.Against, ms.AgainstSHA, ms.Paired = &theirs, parent, &mine
			if cfg.Score.WithinTolerance(mine, theirs) {
				ms.Kept, ms.Best = true, &mine
				ms.Note = fmt.Sprintf("kept: %g is under the best kept %g, but scored beside its parent %s in one sitting (%s) it reads %g against the parent's %g, so the fall is the machine's load; the baseline is %g from here; %s", *score, best, core.Short(parent), readings, mine, theirs, mine, note)
				m.record(ms)
				if m.OnKept != nil {
					m.OnKept(p.Name, t.MergeSHA)
				}
				return
			}
			worse, dip, reason = true, true, fmt.Sprintf("the score fell to %g from its parent %s's %g, both scored in one sitting (%s; tolerance %g)", mine, core.Short(parent), theirs, readings, cfg.Score.Tolerance)
			break
		}
		at := m.bestAt(p.Name, best)
		if now, ok := m.scoreNow(p, at); ok {
			ms.Against, ms.AgainstSHA = &now, at
			if cfg.Score.WithinTolerance(*score, now) {
				b := cfg.Score.Best(*score, now)
				ms.Kept, ms.Best = true, &b
				ms.Note = fmt.Sprintf("kept: %g is under the best kept %g, but main where it was read (%s) scores %g measured now, so the fall is the machine's load; the baseline is %g from here; %s", *score, best, core.Short(at), now, b, note)
				m.record(ms)
				if m.OnKept != nil {
					m.OnKept(p.Name, t.MergeSHA)
				}
				return
			}
			worse, dip, reason = true, true, fmt.Sprintf("the score fell to %g from %g, main where the best kept %g was read (%s) measured right after it (tolerance %g)", *score, now, best, core.Short(at), cfg.Score.Tolerance)
			break
		}
		worse, dip, reason = true, true, fmt.Sprintf("the score fell to %g from the best kept %g (tolerance %g)", *score, best, cfg.Score.Tolerance)
	}
	if dip && t.Kind != "revert" && t.Fix() {
		// A known fix wins over a dip (issue #568): it stays, main's score
		// is the baseline from here (its better of two readings), and a brief looks into the dip; only
		// that look, judging the fix itself made things worse, reverts it.
		// One reading is not yet a baseline (issue #1788): the fix is scored
		// once more, and a second reading back within tolerance was no dip;
		// otherwise the better of the two is the baseline.
		bar := best
		if ms.Against != nil {
			bar = *ms.Against
		}
		b := *score
		if ms.Paired != nil {
			b = *ms.Paired // already read twice beside its parent
		} else if again, ok := m.scoreNow(p, t.MergeSHA); ok {
			b = cfg.Score.Best(b, again)
			if cfg.Score.WithinTolerance(again, bar) {
				b = cfg.Score.Best(again, bar)
				ms.Kept, ms.Best = true, &b
				ms.Note = fmt.Sprintf("kept: %g measured again, so the first reading's fall (%s) was noise; the baseline is %g from here; %s", again, reason, b, note)
				m.record(ms)
				if m.OnKept != nil {
					m.OnKept(p.Name, t.MergeSHA)
				}
				return
			}
			reason += fmt.Sprintf(", and %g measured again", again)
		}
		looked := m.lookIntoDip(p, t, reason, bar)
		ms.Kept, ms.Best = true, &b
		ms.Note = "kept as a fix (" + core.Clip(t.AimWhy, 200) + ") though " + reason + "; " + looked + "; " + note
		m.record(ms)
		if m.OnKept != nil {
			m.OnKept(p.Name, t.MergeSHA)
		}
		return
	}
	if !worse {
		ms.Kept = true
		if score != nil {
			b := *score
			if hasBest {
				b = cfg.Score.Best(*score, best)
			}
			ms.Best = &b
		} else if hasBest {
			ms.Best = &best
		}
		m.record(ms)
		if m.OnKept != nil {
			m.OnKept(p.Name, t.MergeSHA)
		}
		return
	}
	ms.Note = reason
	if ok {
		ms.Note += "; " + note
	}
	if ok && score != nil && t.Kind != "revert" {
		// Only the score says worse: a model's judgement decides, the score
		// being one thing it weighs (judge.go).
		m.mu.Lock()
		busy := m.judging[t.MergeSHA].running
		m.judging[t.MergeSHA] = judgingMerge{project: p.Name, running: true}
		m.mu.Unlock()
		if !busy {
			m.judges.Add(1)
			go m.judgeMerge(p, t, ms, reason)
		}
		return
	}
	m.revert(p, t, ms, reason)
}

// revert records main as worse after t's merge, and files its revert.
func (m *Module) revert(p core.Project, t work.Task, ms Measurement, reason string) {
	m.record(ms)
	if t.Kind == "revert" {
		m.C.Person.Ask(core.Ask{Kind: "question", Ref: "revert-bad:" + t.ID, Project: p.Name,
			Title:  "Main is still worse after a revert in " + p.Name,
			Detail: reason + ". The Hive won't revert a revert on its own.", Options: []string{"ok"}})
		return
	}
	if _, err := m.Work.Revert(t, t.MergeSHA, reason); err != nil {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "revert", "project": p.Name, "text": "could not file a revert: " + err.Error()})
	}
}

// dipLook begins the title of the brief that looks into a fix's score dip.
const dipLook = "Look into the score dip after "

// lookIntoDip files, once per merge, the brief that judges a fix's score
// dip: what caused it, and whether the fix itself made things worse. A dip
// whose cause is already being looked into, or already gone from main, files
// none (three of four looks on 2026-10-27 re-found one known cause): before
// is the score main had before it, and the result says what was done.
func (m *Module) lookIntoDip(p core.Project, t work.Task, reason string, before float64) string {
	title := core.Clip(dipLook+core.Short(t.MergeSHA)+": "+t.Label(), 120)
	if len(m.Work.List(func(x work.Task) bool { return x.Project == p.Name && x.Title == title })) > 0 {
		return "a brief looks into the dip"
	}
	// A look a worker will still read (AddKnown: new, queued, or working
	// with its worker live) takes this dip as more of what it looks into: a
	// second worker would only find the same cause. One past its worker, in
	// review or merge, takes nothing, and this dip gets its own look.
	open := m.Work.List(func(x work.Task) bool {
		return x.Project == p.Name && x.From == "improve" && strings.HasPrefix(x.Title, dipLook) && !x.Terminal()
	})
	slices.Reverse(open) // oldest first
	for _, o := range open {
		if _, ok := m.Work.AddKnown(o.ID, "improve", fmt.Sprintf("Main dipped again after %s (task %s, a fix: %s): %s. "+
			"If it is the same cause, say so; if it is another, name it too.", core.Short(t.MergeSHA), t.ID, core.Clip(t.AimWhy, 200), reason)); ok {
			return "the look into an earlier dip still open (" + o.ID + ") takes this one too"
		}
	}
	// Main gone past the merge and back within tolerance at its head: the
	// dip went with what came after (a fix to its cause, or the load).
	if cfg := m.C.Effects.Config(p, t.MergeSHA); cfg.Score != nil {
		if head, err := m.C.Effects.Sync(p); err == nil && head != "" && m.after(p, head, t.MergeSHA) {
			if now, ok := m.scoreNow(p, head); ok && cfg.Score.WithinTolerance(now, before) {
				return fmt.Sprintf("no look into the dip: main at %s scores %g measured now, within tolerance of the %g before it, so the dip is gone from main", core.Short(head), now, before)
			}
		}
	}
	if _, err := m.Work.File(work.Brief{Project: p.Name, From: "improve", Chat: t.Chat, Thread: t.Thread, Title: title,
		// The failure its known quotes is the fix's, not one of its own; a
		// revert it finds needed is marked a fix by its worker.
		Aim: work.AimGain, Why: "a look into a fix's score dip, judged on the score",
		Done: "You judged what made the score dip and said so in your account. If the fix itself made things worse, " +
			"revert it (`git revert " + t.MergeSHA + "`), name what got worse, and mark the revert a fix " +
			"(`hive agent aim --aim fix --why \"reverts " + core.Short(t.MergeSHA) + ": <what got worse>\"`) so it doesn't wait for the gate; if the dip is noise or comes from elsewhere, " +
			"say what, and win the score back without undoing the fix if you can.",
		Known: fmt.Sprintf("Task %s, a fix (%s), merged at %s and was kept, since a known fix wins over a score dip: %s. "+
			"The score is a data point, not the judge: a revert needs a judged read that the fix itself made things worse.",
			t.ID, core.Clip(t.AimWhy, 300), core.Short(t.MergeSHA), reason)}); err != nil {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "dip", "project": p.Name, "text": "could not file a look into a fix's score dip: " + err.Error()})
	}
	return "a brief looks into the dip"
}

// flakeWindow is how far back a test's failures elsewhere count as a flake.
const flakeWindow = 72 * time.Hour

// seenElsewhere is, for a merge whose red tests stayed red when what failed
// ran again (measure does that), what makes it a flake anyway: every test
// that failed also failed on main before this one merged. Only main's own
// checks count: a candidate's code can print any test name it likes. It
// raises an alert for one; "" means the merge broke them.
func (m *Module) seenElsewhere(p core.Project, t work.Task) string {
	c, ok := m.C.Effects.GetCheck(p.Name, t.MergeSHA, "retest")
	if !ok || c.OK {
		c, _ = m.C.Effects.GetCheck(p.Name, t.MergeSHA, "test")
	}
	where := m.failedBefore(p.Name, t, c.Failed, m.C.Effects.MainLine(p))
	if where == "" {
		return ""
	}
	flake := "flaky: " + strings.Join(c.Failed, ", ") + " failed twice after it merged, but also " + where + ", without this change"
	m.C.J.MustAppend("alert.raised", map[string]string{"kind": "flake", "project": p.Name, "sha": t.MergeSHA, "task": t.ID,
		"text": core.Clip(flake, 400) + " (" + p.Name + " at " + core.Short(t.MergeSHA) + "). Not reverted; the test wants steadying."})
	return flake
}

// failedBefore says where every one of names also failed at a commit on
// main that can't carry t's change: checked before t first merged (a
// checkpoint counts), and none of t's own heads or checkpoints.
// It returns "" when any of them failed only here.
func (m *Module) failedBefore(project string, t work.Task, names []string, main map[string]bool) string {
	if len(names) == 0 {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	merged, ok := m.merged[t.MergeSHA]
	if !ok {
		merged = time.Now()
		for _, c := range m.fails[project] {
			if c.SHA == t.MergeSHA && c.At.Before(merged) {
				merged = c.At // measured right after it merged
			}
		}
	}
	// A checkpoint merged earlier is part of the change too: only what was
	// checked before the task's first merge is without it.
	for _, b := range t.Banks {
		if at, ok := m.merged[b.MergeSHA]; ok && at.Before(merged) {
			merged = at
		}
	}
	var where []string
	for _, n := range names {
		found := ""
		for _, c := range m.fails[project] {
			if c.SHA != t.MergeSHA && main[c.SHA] && c.At.Before(merged) && !slices.Contains(m.heads[t.ID], c.SHA) && slices.Contains(c.Failed, n) {
				found = "at " + core.Short(c.SHA)
			}
		}
		if found == "" {
			return ""
		}
		if !slices.Contains(where, found) {
			where = append(where, found)
		}
	}
	return "failed " + strings.Join(where, ", ")
}

// Sweeps returns notice runs, newest first.
func (m *Module) Sweeps() []Sweep {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Sweep
	for _, s := range m.sweeps {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// StartSweep runs the notice agent over a digest of the records. Unless forced,
// it skips when nothing has happened since the last sweep.
func (m *Module) StartSweep(force bool) (Sweep, error) {
	for _, s := range m.Sweeps() {
		if s.Status == "running" {
			return s, nil
		}
	}
	m.mu.Lock()
	since := m.lastSeen
	m.mu.Unlock()
	if !force && !m.anythingNew(since) {
		return Sweep{}, fmt.Errorf("nothing new since the last sweep")
	}
	id := "sweep-" + core.NewID()
	dir, err := m.Agents.Scratch(id)
	if err != nil {
		return Sweep{}, err
	}
	spec := m.Agents.Spec(id, "sweep", "", dir, "Sweep the records in $HIVE_RUN/digest.md for the next real gains, write sweep.md, file asks with evidence, and finish.")
	spec.Inputs = map[string]string{"digest.md": m.Digest(since, "")}
	if _, err := m.C.Runs.Start(spec); err != nil {
		return Sweep{}, err
	}
	s := Sweep{ID: id, Run: id, Status: "running", Started: time.Now().UTC()}
	m.C.J.MustAppend("sweep.saved", s)
	return s, nil
}

func (m *Module) anythingNew(since int64) bool {
	for _, e := range m.C.J.Since(since) {
		switch e.Kind {
		case "score.measured", "task.saved", "run.turn_ended", "chat.posted", "feedback.saved":
			return true
		}
	}
	return false
}

// TickSweeps finishes sweeps whose run has ended.
func (m *Module) TickSweeps() {
	for _, s := range m.Sweeps() {
		if s.Status != "running" {
			continue
		}
		r, ok := m.C.Runs.Get(s.Run)
		if !ok || !r.Terminal() {
			continue
		}
		report, _ := core.SafeRead(filepath.Join(m.C.WS.RunBox(r.ID), "sweep.md"), 1<<20)
		dir := filepath.Join(m.C.WS.Hive(), "improve", "sweeps")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, s.ID+".md"), report, 0o644)
		for _, t := range m.Work.List(func(t work.Task) bool { return t.From == r.ID || t.JoinedBy(r.ID) }) {
			s.Asks = append(s.Asks, t.ID)
		}
		s.Status, s.Report, s.Ended = r.Status, core.Clip(string(report), 2000), time.Now().UTC()
		m.C.J.MustAppend("sweep.saved", s)
	}
}

// Digest summarizes scores, reverts, failures, chats and notes since seq,
// across every project but the hidden (private or isolated) ones, or from
// project only alone when only is set. A hidden project's records reach only
// its own step-back.
func (m *Module) Digest(since int64, only string) string {
	isolated := map[string]bool{}
	for _, p := range m.C.Projects.List() {
		isolated[p.Name] = p.Hidden()
	}
	mine := func(project string) bool { return project == only || only == "" && !isolated[project] }
	var b strings.Builder
	b.WriteString("# Digest for the improvement sweep\n\n")
	for _, p := range m.C.Projects.List() {
		if !mine(p.Name) {
			continue
		}
		fmt.Fprintf(&b, "## Project %s (%s)\n\n", p.Name, p.Repo)
		main, err := m.C.Effects.Sync(p)
		if err != nil || main == "" {
			main = p.Branch
		}
		goals := m.C.Effects.ReadFile(p, main, ".hive/goals.md")
		if strings.TrimSpace(goals) == "" {
			goals = p.Goals
		}
		fmt.Fprintf(&b, "Goals:\n%s\n\n", core.Clip(strings.TrimSpace(goals), 1500))
		if sc := m.C.Effects.Config(p, main).Score; sc != nil {
			fmt.Fprintf(&b, "Score: `%s`, %s is better.\n\n", sc.Command, sc.Direction)
		} else {
			b.WriteString("Score: none defined.\n\n")
		}
		ms := m.Scores(p.Name)
		if len(ms) > 10 {
			ms = ms[len(ms)-10:]
		}
		if best, ok := m.Best(p.Name); ok {
			fmt.Fprintf(&b, "Best kept score: %g\n\n", best)
		}
		b.WriteString("Recent measurements of main:\n")
		m.mu.Lock()
		cov := slices.Clone(m.covered[p.Name])
		m.mu.Unlock()
		if len(ms) > 0 { // covered merges from the same stretch as the measurements shown
			cov = slices.DeleteFunc(cov, func(c Covered) bool { return c.At.Before(ms[0].At) })
		}
		if len(cov) > 5 {
			cov = cov[len(cov)-5:]
		}
		type line struct {
			at   time.Time
			text string
		}
		var lines []line
		for _, x := range ms {
			sc := "-"
			if x.Score != nil {
				sc = fmt.Sprint(*x.Score)
			}
			kept := fmt.Sprint(x.Kept)
			if x.NoResult {
				kept = "no-result"
			}
			lines = append(lines, line{x.At, fmt.Sprintf("%s score=%s kept=%s %s", core.Short(x.SHA), sc, kept, core.Clip(x.Note, 200))})
		}
		for _, c := range cov {
			state := "kept"
			if c.Red {
				state = "red, so this change went in under a red main"
			}
			lines = append(lines, line{c.At, fmt.Sprintf("%s (task %s) not measured: main already held it when measured at %s, which was %s", core.Short(c.SHA), c.Task, core.Short(c.By), state)})
		}
		slices.SortStableFunc(lines, func(a, b line) int { return a.at.Compare(b.at) })
		for _, l := range lines {
			fmt.Fprintf(&b, "- %s %s\n", l.at.Format("01-02 15:04"), l.text)
		}
		b.WriteString("\nWork:\n")
		for i, t := range m.Work.List(func(t work.Task) bool { return t.Project == p.Name }) {
			if i >= 25 {
				break
			}
			fmt.Fprintf(&b, "- [%s] %s (%s) %s\n", t.Stage, t.Label(), t.ID, core.Clip(t.Reason+" "+t.Lesson, 300))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Runs that stopped, blocked or were flagged\n\n")
	for i, r := range m.C.Runs.List(func(r *core.Run) bool {
		return mine(r.Project) && (r.Status == core.StatusBlocked || r.Status == core.StatusQuiet || r.Status == core.StatusStopped || r.Retries > 0)
	}) {
		if i >= 20 {
			break
		}
		fmt.Fprintf(&b, "- %s %s [%s] %s\n", r.Role, r.ID, r.Status, core.Clip(r.Reason, 300))
	}
	b.WriteString("\n## Stalls the watcher acted on (limit = max(stall_floor_seconds, 3 x p90 of the stage's recent durations))\n\n")
	for i, s := range m.Stalls() {
		if i >= 20 {
			break
		}
		fmt.Fprintf(&b, "- %s %s in %s: limit %.0fs from %d samples; %s\n", s.At.Format("01-02 15:04"), s.What, s.Stage, s.Limit, s.Samples, s.Did)
	}
	b.WriteString("\n## Agents' notes\n\n")
	for i, r := range m.C.Runs.List(func(r *core.Run) bool { return mine(r.Project) }) {
		if i >= 30 {
			break
		}
		if notes, err := core.SafeRead(filepath.Join(m.C.WS.RunBox(r.ID), "notes.md"), 4000); err == nil && len(strings.TrimSpace(string(notes))) > 0 {
			fmt.Fprintf(&b, "### %s %s\n%s\n\n", r.Role, r.ID, core.Clip(string(notes), 1500))
		}
	}
	if m.Feedback != nil {
		if fb := m.Feedback(mine); fb != "" {
			b.WriteString("## Agents' feedback on the Hive, still open\n\n" + fb + "\n")
		}
	}
	b.WriteString("## Recent chat\n\n")
	for _, ch := range m.Chat.Chats() {
		if !mine(ch.Project) {
			continue
		}
		msgs := m.Chat.Messages(ch.ID)
		if len(msgs) > 12 {
			msgs = msgs[len(msgs)-12:]
		}
		for _, msg := range msgs {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", ch.ID, msg.Name, core.Clip(msg.Text, 240))
		}
	}
	return b.String()
}

// baselineAlert gives the text and dedup key of a start with no number. A
// missing program is keyed on its plain-words cause; any other failure on the
// whole note (the clipped, one-line check output) with digits blanked, so a
// repeat with fresh timings refreshes one alert while a different failure is a
// new one, even under a long fixed header like pytest's session banner.
func baselineAlert(project string, testsOK bool, note string) (text, key string) {
	kind := "test"
	if testsOK {
		kind = "score"
	}
	if why := MissingTool(project, kind, note); why != "" {
		return why + " Measuring again in 10 minutes.", why
	}
	return "the start of " + project + " has no number yet: " + core.Clip(note, 200) + "; measuring again in 10 minutes", kind + ": " + failureCause(note)
}

var digits = regexp.MustCompile(`\d+`)

// failureCause is a short digest of a failure note with runs of digits
// blanked, so timings and counts don't make a new cause.
func failureCause(note string) string {
	sum := sha256.Sum256([]byte(digits.ReplaceAllString(strings.Join(strings.Fields(note), " "), "#")))
	return hex.EncodeToString(sum[:8])
}

// notFound matches the shell's program-not-found lines (dash "sh: 1: X: not
// found", bash "bash: line 1: X: command not found") and the line Hive's own
// guard prints when a program lookup finds nothing.
var notFound = regexp.MustCompile(`(?m)(?:^|\s)(?:\S*/)?(?:ba|da)?sh: (?:line )?(?:\d+: )?([^\s:]+): (?:command )?not found|hive: missing program: ([^\n]+)`)

// MissingTool says in plain words which program the test or score command
// (kind) could not start, and how to get it, or "" when the output shows no
// such failure. A name starting with "-" means the command began with an empty
// program.
func MissingTool(project, kind, out string) string {
	m := notFound.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	so := "so " + project + "'s tests can't run"
	if kind == "score" {
		so = "so " + project + "'s score can't be measured"
	}
	if tool := m[1] + m[2]; !strings.HasPrefix(tool, "-") {
		return tool + " isn't installed, " + so + ". " + getIt(tool)
	}
	return "the " + kind + " command for " + project + " started with no program (an interpreter variable came out empty, so the interpreter it looks for isn't installed), " + so + ". " + getIt("the interpreter it needs")
}

// getIt says how the person gets a missing program onto the machine: install
// it themselves, or ask the lead, whose agent may be able to install it into
// the Hive's own tools or ask a warden to run the install.
func getIt(tool string) string {
	return "Install " + tool + " on this machine, or ask the lead in chat: an agent may be able to install it for the Hive's checks, or ask a warden to run the install for you."
}
