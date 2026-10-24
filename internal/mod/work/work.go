// Package work is how work lands: an ask becomes a brief, an issue, a worker
// in a fresh checkout, a pull request, a gate (tests on the exact commit, and
// a reviewer's read for anything but small changes), and a merge pinned to the
// commit that passed. Every step is idempotent, so a restart resumes from the
// records without a second issue, run or pull request.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
)

// Stages.
const (
	StageNew       = "new"
	StageIssued    = "issued"
	StageWorking   = "working"
	StageLanding   = "landing"
	StageTesting   = "testing"
	StageReviewing = "reviewing"
	StageMerging   = "merging"
	StagePerson    = "waiting-person"
	StageMerged    = "merged"
	StageDone      = "done"
	StageBlocked   = "blocked"
	StageStopped   = "stopped"
	StageReverting = "reverting" // revert tasks start here
)

// Task is one piece of work from ask to merge.
type Task struct {
	ID        string            `json:"id"`
	Project   string            `json:"project"`
	Title     string            `json:"title"`
	Done      string            `json:"done"`
	Known     string            `json:"known"`
	From      string            `json:"from"` // run id, person, or sweep run
	Chat      string            `json:"chat,omitempty"`
	Thread    string            `json:"thread,omitempty"`
	Kind      string            `json:"kind"` // work | revert | helper (spawned, no issue)
	Stage     string            `json:"stage"`
	Reason    string            `json:"reason,omitempty"`
	Recheck   bool              `json:"recheck,omitempty"`   // landed again on the same commit, so its test runs again
	ByPerson  bool              `json:"by_person,omitempty"` // the person stopped it or left it open, so the lead can't bring it back
	Issue     core.Issue        `json:"issue"`
	Worker    string            `json:"worker,omitempty"`
	Base      string            `json:"base,omitempty"`
	Branch    string            `json:"branch,omitempty"`
	SHA       string            `json:"sha,omitempty"`
	Diff      string            `json:"diff,omitempty"`       // SHA's own diff (core.DiffID), which reads are bound to
	NeedsRead bool              `json:"needs_read,omitempty"` // the PR asks for a read from the box's perms seat or architect
	PR        core.PR           `json:"pr"`
	Round     int               `json:"round"`
	Conflicts int               `json:"conflicts"`
	Reviews   int               `json:"reviews"`
	Reviewer  string            `json:"reviewer,omitempty"`
	Findings  string            `json:"findings,omitempty"`
	MergeSHA  string            `json:"merge_sha,omitempty"`
	RevertOf  string            `json:"revert_of,omitempty"`  // task id
	RevertSHA string            `json:"revert_sha,omitempty"` // the merge being reverted
	Lesson    string            `json:"lesson,omitempty"`
	Advice    []string          `json:"advice,omitempty"`       // a land's reviewer advice, filed as one follow-up once merged
	FollowUp  string            `json:"follow_up_of,omitempty"` // the task whose review advice this follows up
	Inputs    map[string]string `json:"inputs,omitempty"`       // files its worker reads in $HIVE_RUN, kept off the issue
	// A checkpoint is the running worker's commit going through the gate while
	// it keeps working; once merged, Base moves to it and Banked counts it.
	Checkpoint string    `json:"checkpoint,omitempty"`
	Banked     int       `json:"banked,omitempty"`
	Banks      []Bank    `json:"banks,omitempty"`   // each merged checkpoint, oldest first
	Seen       string    `json:"seen,omitempty"`    // the worker checkout's reflog when last looked at
	Tried      string    `json:"tried,omitempty"`   // the newest commit already weighed as a checkpoint
	NoBank     string    `json:"no_bank,omitempty"` // why no more checkpoints are opened for this task
	Failures   int       `json:"failures"`
	CloseFail  int       `json:"close_fail,omitempty"` // failed tries to close the issue
	Attempt    int       `json:"attempt"`              // fresh workers handed this task
	Retry      time.Time `json:"retry,omitempty"`
	IssueOpen  bool      `json:"issue_open,omitempty"` // done, but its issue is still to close
	Log        []Entry   `json:"log,omitempty"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
}

// A Bank is a merged checkpoint: its pull request and the merge on main.
type Bank struct {
	PR       core.PR `json:"pr"`
	MergeSHA string  `json:"merge_sha"`
}

// PROf is the pull request that made a merge of this task: one of its
// checkpoints, or else its own.
func (t Task) PROf(mergeSHA string) core.PR {
	for _, b := range t.Banks {
		if b.MergeSHA == mergeSHA {
			return b.PR
		}
	}
	return t.PR
}

// Label is the title clipped for a heading, an issue, a PR or a message; the
// agents get the whole ask.
func (t Task) Label() string { return core.Clip(t.Title, 120) }

// Entry is one step in a task's timeline.
type Entry struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Terminal reports whether a task is finished.
func (t Task) Terminal() bool {
	switch t.Stage {
	case StageDone, StageBlocked, StageStopped:
		return true
	}
	return false
}

// Module runs tasks.
type Module struct {
	C      *core.Core
	Agents *agents.Agents
	Chat   *chat.Module

	// Steps sums up the outside steps a worker asked for, and whether every
	// one ran and succeeded (the risk module).
	Steps func(run string) (string, bool)

	// Weigh settles a merge that changes protected paths as autonomy says
	// (the risk module) and returns who decides; unset, the person does.
	Weigh func(core.Ask) string

	// OnMerged is called after a task's merge is recorded (the improve module measures).
	OnMerged func(t Task)

	mu    sync.Mutex
	tasks map[string]*Task
	busy  map[string]bool
	// mainRed holds project@sha for every main commit whose red tests were
	// already raised, so each is raised once whatever the tasks say now. It
	// grows by one key per red main commit and isn't pruned; that's small.
	mainRed map[string]bool
	// later is when a held task (waiting on the forge's checks or an outside
	// read) is next looked at; kept in memory, so a restart looks at once.
	later map[string]time.Time
	// offline is why the forge was last unreachable, until it answers again;
	// downs counts the failed steps and checks since, probe is when it may next
	// be checked, probing says a check is out, and online is when it last came
	// back.
	offline string
	downs   int
	probe   time.Time
	probing bool
	online  time.Time
}

// New loads tasks and registers the brief op.
func New(c *core.Core, a *agents.Agents, ch *chat.Module) *Module {
	m := &Module{C: c, Agents: a, Chat: ch, tasks: map[string]*Task{}, busy: map[string]bool{}, mainRed: map[string]bool{}, later: map[string]time.Time{}}
	apply := func(e core.Event) {
		if e.Kind == "alert.raised" {
			var a struct{ Kind, Project, SHA string }
			if e.Decode(&a) == nil && a.Kind == "main-red" && a.SHA != "" {
				m.mu.Lock()
				m.mainRed[a.Project+"@"+a.SHA] = true
				m.mu.Unlock()
			}
			return
		}
		if e.Kind != "task.saved" {
			return
		}
		var t Task
		e.Decode(&t)
		m.mu.Lock()
		m.tasks[t.ID] = &t
		m.mu.Unlock()
	}
	c.J.Replay(apply)
	c.J.Subscribe(apply)
	c.Runs.Ops().Register(core.Op{Name: "brief", Help: `{"title","done","known","project"}`, Fn: m.opBrief})
	c.Runs.Ops().Register(core.Op{Name: "recheck", Roles: []string{"lead"}, Help: `{"task","back"} -> a blocked or stopped task's commits are pushed and checked again (not one the person stopped); with back, its worker gets that text and works on`, Fn: m.opRecheck})
	c.Person.Handle("merge", m.onPersonMerge)
	return m
}

func (m *Module) save(t *Task, note string) {
	t.Updated = time.Now().UTC()
	if note != "" {
		t.Log = append(t.Log, Entry{At: t.Updated, Text: note})
		if len(t.Log) > 60 {
			t.Log = t.Log[len(t.Log)-60:]
		}
	}
	m.C.J.MustAppend("task.saved", t)
}

// Get returns a task.
func (m *Module) Get(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// List returns tasks, newest first.
func (m *Module) List(fn func(Task) bool) []Task {
	m.mu.Lock()
	var out []Task
	for _, t := range m.tasks {
		if fn == nil || fn(*t) {
			out = append(out, *t)
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Brief files an ask.
type Brief struct {
	Project string            `json:"project"`
	Title   string            `json:"title"`
	Done    string            `json:"done"`
	Known   string            `json:"known"`
	From    string            `json:"from"`
	Chat    string            `json:"chat"`
	Thread  string            `json:"thread"`
	Inputs  map[string]string `json:"-"`
	// FollowUp is the task whose review advice this brief follows up; only the
	// Hive sets it.
	FollowUp string `json:"-"`
}

// File creates a task from a brief.
func (m *Module) File(b Brief) (Task, error) {
	if strings.TrimSpace(b.Title) == "" {
		return Task{}, errors.New("a brief needs a title")
	}
	if b.Project == "" {
		names := []string{}
		for _, p := range m.C.Projects.List() {
			names = append(names, p.Name)
		}
		return Task{}, fmt.Errorf("say which project (one of: %s)", strings.Join(names, ", "))
	}
	if _, ok := m.C.Projects.Get(b.Project); !ok {
		return Task{}, fmt.Errorf("no project %q", b.Project)
	}
	t := &Task{ID: core.NewID(), Project: b.Project, Title: strings.TrimSpace(b.Title), Done: b.Done, Known: b.Known,
		From: b.From, Chat: b.Chat, Thread: b.Thread, FollowUp: b.FollowUp, Kind: "work", Stage: StageNew, Inputs: b.Inputs, Created: time.Now().UTC()}
	m.save(t, "filed by "+b.From)
	return *t, nil
}

func (m *Module) opBrief(r core.Run, args json.RawMessage) (any, error) {
	var b Brief
	if err := core.Bind(args, &b); err != nil {
		return nil, err
	}
	b.From = r.ID
	if b.Project == "" {
		b.Project = r.Project
	}
	// An agent with no project of its own names the target. For the lead the
	// only project is not a safe guess, since the ask may be a module or about
	// the Hive; a sweep's findings come from the records, so it may be.
	names := []string{}
	for _, p := range m.C.Projects.List() {
		if m.C.Runs.Walled(r, p.Name) == nil {
			names = append(names, p.Name)
		}
	}
	if b.Project == "" && r.Role == "sweep" && len(names) == 1 {
		b.Project = names[0]
	}
	if b.Project == "" {
		return nil, fmt.Errorf("say which project with --project (one of: %s) and tell the person why; a new module gets a project of its own first (create_project), then install_module --project", strings.Join(names, ", "))
	}
	if err := m.C.Runs.Walled(r, b.Project); err != nil {
		return nil, err
	}
	b.Chat, b.Thread = m.thread(r)
	t, err := m.File(b)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "note": "filed; it becomes an issue and a worker shortly"}, nil
}

// thread is the chat thread that hears about work r asks for: a lead's
// current thread, or the thread of the task r works on.
func (m *Module) thread(r core.Run) (string, string) {
	if r.Role == "lead" {
		return m.Chat.CurrentThread(r)
	}
	if parent, ok := m.taskOf(r.ID); ok {
		return parent.Chat, parent.Thread
	}
	return "", ""
}

// Spawn is the core's spawn hook. A helper worker on a project ships like any
// worker: it gets a task with no issue, so its commits go to their own branch
// and pull request, are tested, reviewed and merged, and its parent's thread
// hears how that went. It starts that run itself. Reviewers and helpers with no
// project go to the agents module, and the spawn op starts those.
func (m *Module) Spawn(parent core.Run, role, project, brief, model string) (core.RunSpec, error) {
	p, ok := m.C.Projects.Get(project)
	if role != "worker" || !ok {
		return m.Agents.Spawner(parent, role, project, brief, model)
	}
	base, err := m.C.Effects.Sync(p)
	if err != nil {
		return core.RunSpec{}, err
	}
	if base == "" { // no task can land a branch off an empty main, so its commits would go nowhere
		return core.RunSpec{}, fmt.Errorf("project %s has no commit on main yet, so a helper worker's commits could never land; file a brief instead", p.Name)
	}
	t := &Task{ID: core.NewID(), Project: p.Name, Title: strings.TrimSpace(brief), From: parent.ID, Kind: "helper",
		Stage: StageWorking, Base: base, Created: time.Now().UTC()}
	t.Chat, t.Thread = m.thread(parent)
	t.Worker, t.Branch = "w-"+t.ID, "hive/"+t.ID
	dir, err := m.C.Effects.NewCheckout(p, t.Worker, base)
	if err != nil {
		return core.RunSpec{}, err
	}
	s := m.Agents.Spec(t.Worker, "worker", p.Name, dir, brief+"\n\n(You are a helper spawned by "+parent.ID+"; your account goes back to it, and your commits get their own pull request as task "+t.ID+".)")
	s.Task, s.Parent = t.ID, parent.ID
	if model != "" {
		s.Model = model
	}
	// Like startWorker, the run starts before the task names it, so a task
	// never points at a run that doesn't exist.
	if _, err := m.C.Runs.Start(s); err != nil {
		return core.RunSpec{}, err
	}
	m.save(t, "helper of "+parent.ID+": worker "+t.Worker)
	return s, nil
}

func (m *Module) taskOf(runID string) (Task, bool) {
	for _, t := range m.List(nil) {
		if t.Worker == runID {
			return t, true
		}
	}
	return Task{}, false
}

// Revert files a revert of a merged task.
func (m *Module) Revert(of Task, mergeSHA, reason string) (Task, error) {
	for _, t := range m.List(func(t Task) bool { return t.Kind == "revert" && t.RevertSHA == mergeSHA }) {
		return t, nil // already reverting
	}
	t := &Task{ID: core.NewID(), Project: of.Project, Title: "Revert: " + core.Clip(of.Title, 112), Kind: "revert", Stage: StageReverting,
		From: "improve", Chat: of.Chat, Thread: of.Thread, RevertOf: of.ID, RevertSHA: mergeSHA, Lesson: reason, Created: time.Now().UTC()}
	m.save(t, "revert filed: "+reason)
	return *t, nil
}

// Tick advances every task that isn't already being worked on.
func (m *Module) Tick() {
	for _, t := range m.List(func(t Task) bool { return !t.Terminal() || t.IssueOpen || m.woke(t) }) {
		m.mu.Lock()
		if !m.due(t) || m.busy[t.ID] {
			m.mu.Unlock()
			continue
		}
		m.busy[t.ID] = true
		m.mu.Unlock()
		go func(t Task) {
			defer func() { m.mu.Lock(); delete(m.busy, t.ID); m.mu.Unlock() }()
			m.settle(&t, m.step(&t))
		}(t)
	}
}

// due reports whether a task's retry gap is over; a task saved before the
// forge came back goes at once. The caller holds m.mu.
func (m *Module) due(t Task) bool {
	if time.Now().Before(m.later[t.ID]) {
		return false
	}
	delete(m.later, t.ID)
	return !time.Now().Before(t.Retry) || !t.Updated.After(m.online)
}

// settle records how a step ended.
func (m *Module) settle(t *Task, err error) {
	why := ""
	if err != nil {
		why = core.Unreachable("GitHub", err.Error())
	}
	// Many steps succeed without touching GitHub, so only a direct check says
	// it's back, and the tasks share one check at a time that backs off as
	// they do. Any answer but a network failure counts as back.
	m.mu.Lock()
	probe := err == nil && m.offline != "" && !m.probing && !time.Now().Before(m.probe)
	downs := m.downs
	if probe {
		m.probe, m.probing = time.Now().Add(backoff(m.downs)), true
	}
	m.mu.Unlock()
	cwhy := ""
	if probe {
		if _, cerr := m.C.Effects.Forge.User(); cerr != nil {
			cwhy = core.Unreachable("GitHub", cerr.Error())
		}
	}
	m.mu.Lock()
	if probe {
		m.probing = false
	}
	switch {
	case why != "":
		m.offline = why
		m.downs++
	case probe && cwhy != "":
		m.offline = cwhy
		m.downs++
	case probe && m.downs == downs: // no step failed while it was out
		m.offline, m.downs, m.probe, m.online = "", 0, time.Time{}, time.Now().UTC()
	}
	downs = m.downs
	m.mu.Unlock()
	if err == nil {
		return
	}
	if why != "" {
		// An outage isn't the task's fault, so it waits it out without counting
		// a failure, checking back less often the longer it lasts.
		t.Retry = time.Now().Add(backoff(downs))
		m.save(t, why+"; retrying")
		return
	}
	t.Failures++
	t.Retry = time.Now().Add(time.Duration(min(t.Failures, 6)) * 30 * time.Second)
	msg := err.Error()
	if t.Failures >= 8 {
		m.giveUp(t, "gave up after repeated errors: "+msg)
		return
	}
	m.save(t, "error (will retry): "+core.Clip(msg, 300))
}

// backoff is the gap after the nth failed step of an outage: 30s doubling to
// a 10 minute ceiling.
func backoff(n int) time.Duration {
	return min(30*time.Second<<min(max(n, 1)-1, 5), 10*time.Minute)
}

// Offline says why the forge can't be reached, or "" when it can.
func (m *Module) Offline() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.offline
}

func (m *Module) project(t *Task) (core.Project, error) {
	p, ok := m.C.Projects.Get(t.Project)
	if !ok {
		return p, fmt.Errorf("project %s is gone", t.Project)
	}
	return p, nil
}

func marker(id string) string { return "<!-- hive-task:" + id + " -->" }

func (m *Module) step(t *Task) error {
	p, err := m.project(t)
	if err != nil && t.Stage == StageDone {
		t.IssueOpen = false
		m.save(t, fmt.Sprintf("%v, so issue #%d is left open", err, t.Issue.Number))
		return nil
	}
	if err != nil {
		return err
	}
	switch t.Stage {
	case StageNew:
		is, err := m.C.Effects.Forge.CreateIssue(p.Repo, t.Label(), m.issueBody(t, p), marker(t.ID))
		if err != nil {
			return err
		}
		t.Issue, t.Stage, t.Failures = is, StageIssued, 0
		m.save(t, fmt.Sprintf("issue #%d", is.Number))
	case StageIssued:
		return m.startWorker(t, p)
	case StageWorking:
		return m.checkWorker(t, p)
	case StageLanding:
		return m.land(t, p)
	case StageTesting:
		return m.test(t, p)
	case StageReviewing:
		return m.review(t, p)
	case StageMerging:
		return m.merge(t, p)
	case StageMerged:
		if err := m.followUp(t, p); err != nil {
			return err
		}
		t.Stage = StageDone
		m.save(t, "done")
	case StageDone:
		if t.IssueOpen {
			m.closeIssue(t, p)
		}
	case StageReverting:
		return m.prepareRevert(t, p)
	case StagePerson:
		// waiting on the person's answer (onPersonMerge)
	case StageBlocked: // woke: an answer reopened its worker
		t.Stage, t.Reason = StageWorking, ""
		m.save(t, "the worker was woken with an answer")
	}
	return nil
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(nothing yet)"
	}
	return s
}

func (m *Module) startWorker(t *Task, p core.Project) error {
	id := "w-" + t.ID
	if t.Attempt > 0 {
		id = fmt.Sprintf("w-%s-%d", t.ID, t.Attempt+1)
	}
	if _, ok := m.C.Runs.Get(id); !ok {
		base, err := m.C.Effects.Sync(p)
		if err != nil {
			return err
		}
		if base == "" {
			return errors.New("the repository is empty; the project isn't set up yet")
		}
		start := base
		if t.SHA != "" && t.Attempt > 0 {
			start = t.SHA // the same branch: the first worker's commits are kept
			base = t.Base
		}
		dir, err := m.C.Effects.NewCheckout(p, id, start)
		if err != nil {
			return err
		}
		spec := m.Agents.Spec(id, "worker", p.Name, dir, m.renderBrief(t, p, base))
		spec.Task, spec.Inputs = t.ID, t.Inputs
		if _, err := m.C.Runs.Start(spec); err != nil {
			return err
		}
		t.Base = base
	}
	if t.Branch == "" {
		t.Branch = "hive/" + t.ID
	}
	t.Worker, t.Stage, t.Failures = id, StageWorking, 0
	m.save(t, "worker "+id+" started")
	return nil
}

// issueBody is the issue a task is filed as. Its done line follows the brief's,
// read from the project's last synced main.
func (m *Module) issueBody(t *Task, p core.Project) string {
	done := doneFor(t, m.C.Effects.Config(p, "HEAD").Score != nil)
	return fmt.Sprintf("%s\n\n**Done looks like:** %s\n\n**Already known:** %s\n\n_Filed by the Hive (%s)._", t.Title, done, orNone(t.Known), t.From)
}

// renderBrief is the worker's first prompt. It quotes the project's lessons so
// an idea that was reverted isn't tried again blind.
func (m *Module) renderBrief(t *Task, p core.Project, base string) string {
	cfg := m.C.Effects.Config(p, base)
	var b strings.Builder
	fmt.Fprintf(&b, "# Brief: %s\n\nTask %s · project %s · issue #%d\n\n", t.Label(), t.ID, p.Name, t.Issue.Number)
	fmt.Fprintf(&b, "## The ask\n\n%s\n\n", t.Title)
	fmt.Fprintf(&b, "## What done looks like\n\n%s\n\n## What's already known\n\n%s\n\n", doneFor(t, cfg.Score != nil), orNone(t.Known))
	b.WriteString("## The project\n\n")
	b.WriteString("- Goals: `.hive/goals.md`. Read it; report against it.\n")
	fmt.Fprintf(&b, "- Tests: `%s`\n", orNone(cfg.Test))
	if cfg.Score != nil {
		fmt.Fprintf(&b, "- Score: `%s` (%s is better, tolerance %g).", cfg.Score.Command, cfg.Score.Direction, cfg.Score.Tolerance)
		if best, ok := m.bestScore(p.Name); ok {
			fmt.Fprintf(&b, " Best kept score: %g.", best)
		}
		b.WriteString(" Every merge is measured; a lower score is reverted.\n")
	}
	if cfg.Score != nil && t.From != "hive" { // as checkpoint() skips the Hive's own setup brief
		b.WriteString("- Commit each gain as you make it: a commit that beats the best kept score is tested, read and merged into main as a checkpoint while you keep working on top of it.\n")
	}
	if lessons := m.C.Effects.ReadFile(p, base, ".hive/lessons.md"); strings.TrimSpace(lessons) != "" {
		lines := strings.Split(strings.TrimSpace(lessons), "\n")
		var items []string
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "- ") {
				items = append(items, l)
			}
		}
		if len(items) > 12 {
			items = items[len(items)-12:]
		}
		if len(items) > 0 {
			b.WriteString("\n## Lessons from this project (`.hive/lessons.md`)\n\n")
			for _, l := range items {
				b.WriteString("> " + strings.TrimSpace(l) + "\n")
			}
		}
	}
	if prior := m.priorBlocked(t); prior != "" {
		b.WriteString("\n## Earlier attempts that stopped\n\n" + prior)
	}
	b.WriteString("\n## When you finish\n\nCommit on your branch, then run `hive agent finish --status done --account \"...\" --tested \"...\" --unsure \"...\"` and end your turn. If you can't finish, use `--status blocked` and say why.\n")
	return b.String()
}

// BestScore is provided by the improve module.
var BestScore func(project string) (float64, bool)

func (m *Module) bestScore(project string) (float64, bool) {
	if BestScore == nil {
		return 0, false
	}
	return BestScore(project)
}

func (m *Module) priorBlocked(t *Task) string {
	var b strings.Builder
	for _, o := range m.List(func(o Task) bool {
		return o.Project == t.Project && o.ID != t.ID && o.Stage == StageBlocked && similar(o.Title, t.Title)
	}) {
		fmt.Fprintf(&b, "- %s (%s): %s\n", o.Label(), o.ID, core.Clip(o.Reason, 300))
	}
	return b.String()
}

func similar(a, b string) bool {
	wa := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(a)) {
		if len(w) > 3 {
			wa[w] = true
		}
	}
	n := 0
	for _, w := range strings.Fields(strings.ToLower(b)) {
		if wa[w] {
			n++
		}
	}
	return n >= 2
}

func (m *Module) checkWorker(t *Task, p core.Project) error {
	r, ok := m.C.Runs.Get(t.Worker)
	if !ok {
		t.Stage = StageIssued
		m.save(t, "worker record missing; starting again")
		return nil
	}
	switch r.Status {
	case core.StatusDone:
		t.Stage, t.Failures = StageLanding, 0
		m.save(t, "worker finished: "+core.Clip(finishText(r), 200))
	case core.StatusBlocked:
		t.Stage, t.Reason = StageBlocked, finishText(r)
		m.save(t, "blocked: "+core.Clip(t.Reason, 300))
		if t.Issue.Number != 0 {
			m.C.Effects.Forge.Comment(p.Repo, t.Issue.Number, "The worker stopped:\n\n"+t.Reason)
		}
		m.C.J.MustAppend("kb.note", map[string]string{"title": "Blocked: " + t.Label(), "text": t.Reason, "project": t.Project, "run": r.ID})
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Blocked: %q (task %s). %s", t.Label(), strings.TrimSpace(t.ID+" "+t.PR.URL), core.Clip(t.Reason, 400)))
	case core.StatusStopped:
		// Only a stop from outside the pipeline (the control center) ends a
		// worker's run, so the task is the person's to bring back.
		t.Stage, t.Reason, t.ByPerson = StageStopped, "worker stopped: "+r.Reason, true
		m.save(t, t.Reason)
	case core.StatusQuiet:
		if !strings.Contains(t.Reason, "quiet") {
			t.Reason = "worker is quiet: " + r.Reason
			m.save(t, t.Reason)
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "quiet", "run": r.ID, "task": t.ID, "text": "worker " + r.ID + " keeps ending turns without finishing"})
		}
	default:
		return m.checkpoint(t, p, r)
	}
	return nil
}

// checkpoint banks a running worker's newest commit on a scored project when
// its score beats the best kept: it goes through the same tests, read and
// merge as a finished task while the worker keeps going. A commit shows as a
// change to the checkout's reflog, read without running git there. Once the
// worker has finished and been sent back, its next finish lands instead.
func (m *Module) checkpoint(t *Task, p core.Project, r core.Run) error {
	best, scored := m.bestScore(p.Name)
	if !scored || t.From == "hive" || t.NoBank != "" || t.Round > 0 || t.Conflicts > 0 { // the Hive's own setup brief keeps its done line (doneFor)
		return nil
	}
	sc := m.C.Effects.Config(p, "HEAD").Score
	fi, err := os.Lstat(filepath.Join(r.Checkout, ".git", "logs", "HEAD"))
	if sc == nil || err != nil {
		return nil
	}
	seen := fmt.Sprint(fi.Size(), fi.ModTime().UnixNano())
	if seen == t.Seen {
		return nil
	}
	// Seen moves only once the commit has been weighed, so a sandbox or
	// extract that fails is tried again, without counting against the task.
	if _, err := m.C.Sandbox(); err != nil {
		return nil
	}
	sha, err := m.C.Effects.Extract(p, r, t.Base)
	switch {
	case errors.Is(err, core.ErrNoChanges):
		t.Seen = seen
		m.save(t, "")
		return nil
	case errors.Is(err, core.ErrNotDescendant):
		t.Seen = seen
		m.save(t, "the worker's newest commit no longer builds on its merged checkpoint "+core.Short(t.Base))
		m.C.Runs.Send(t.Worker, rewound(t.Base))
		return nil
	case err != nil:
		t.Retry = time.Now().Add(2 * time.Minute)
		m.save(t, "could not take out the worker's commit to weigh it as a checkpoint (will retry): "+core.Clip(err.Error(), 300))
		return nil
	}
	t.Seen = seen
	if sha == t.Tried {
		m.save(t, "")
		return nil
	}
	t.Tried = sha
	// main is measured after a merge, so the last checkpoint's own score
	// counts until then; once measured, the kept best says what it was worth.
	if n := len(t.Banks); n > 0 && !m.measured(p.Name, t.Banks[n-1].MergeSHA) {
		if c, ok := m.C.Effects.GetCheck(p.Name, t.Base, "score"); ok && c.Score != nil {
			best = sc.Best(best, *c.Score)
		}
	}
	c, ok := m.C.Effects.GetCheck(p.Name, sha, "score")
	if !ok {
		c = m.C.Effects.RunCheck(p, sha, "score", sc.Command)
	}
	if c.Score == nil {
		m.save(t, fmt.Sprintf("the score gave no number on the worker's commit %s, so no checkpoint", core.Short(sha)))
		return nil
	}
	if !sc.Beats(*c.Score, best) {
		m.save(t, fmt.Sprintf("the worker's commit %s doesn't beat the best kept score %g, so no checkpoint", core.Short(sha), best))
		return nil
	}
	t.Checkpoint = sha
	return m.open(t, p, sha, fmt.Sprintf("A checkpoint of #%d while its worker keeps going: %s scores %g, above the best kept %g.", t.Issue.Number, core.Short(sha), *c.Score, best))
}

// rewound tells a worker its branch left a commit already merged as a checkpoint.
func rewound(base string) string {
	return fmt.Sprintf("Your newest commit no longer builds on %s, which is merged into main as a checkpoint, so the Hive can't take it. Bring that commit back into your branch (`git merge %s`, or rebase onto it), keep it as it is, commit, and carry on.", core.Short(base), base)
}

// Measured is provided by the improve module: whether a merge has been measured.
var Measured func(project, sha string) bool

func (m *Module) measured(project, sha string) bool {
	return Measured != nil && Measured(project, sha)
}

// dropCheckpoint leaves a checkpoint unmerged. The worker was never told of
// it and keeps going; its pull request takes the worker's later commits.
func (m *Module) dropCheckpoint(t *Task, p core.Project, why string) error {
	t.Checkpoint, t.Stage, t.Reason = "", StageWorking, ""
	m.save(t, "checkpoint not merged: "+core.Clip(why, 300))
	if t.PR.Number != 0 { // a conflict found before its pull request opened
		m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, "Not merged as a checkpoint: "+core.Clip(why, 3000)+"\n\nThe worker keeps going; this pull request takes its later commits.")
	}
	return nil
}

func finishText(r core.Run) string {
	if r.Finish == nil {
		return r.Result
	}
	s := r.Finish.Account
	if r.Finish.Tested != "" {
		s += "\n\n**Tested:** " + r.Finish.Tested
	}
	if r.Finish.Unsure != "" {
		s += "\n\n**Not sure of:** " + r.Finish.Unsure
	}
	return s
}

func (m *Module) land(t *Task, p core.Project) error {
	r, _ := m.C.Runs.Get(t.Worker)
	sha, err := m.C.Effects.Extract(p, r, t.Base)
	if errors.Is(err, core.ErrNoChanges) && t.Banked > 0 {
		if err := m.followUp(t, p); err != nil { // advice from the checkpoints' reads
			return err
		}
		t.Stage, t.IssueOpen, t.Failures = StageDone, true, 0
		m.save(t, "done: everything the worker made was merged as checkpoints")
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Done: %q; all of it was merged as checkpoints.", t.Label()))
		m.closeIssue(t, p)
		return nil
	}
	if errors.Is(err, core.ErrNoChanges) {
		m.noCommits(t, p, r)
		return nil
	}
	if errors.Is(err, core.ErrNotDescendant) && t.Banked > 0 {
		t.Conflicts++
		if t.Conflicts > 3 {
			m.giveUp(t, "the worker's branch keeps leaving its merged checkpoint "+core.Short(t.Base))
			return nil
		}
		if err := m.C.Runs.Reopen(t.Worker, rewound(t.Base)+" Then finish again."); err != nil {
			return err
		}
		t.Stage = StageWorking
		m.save(t, "the worker's finish left its merged checkpoint; sent back")
		return nil
	}
	if err != nil {
		return err
	}
	body := finishText(r)
	if t.Issue.Number != 0 {
		body += fmt.Sprintf("\n\nCloses #%d", t.Issue.Number)
	}
	return m.open(t, p, sha, body)
}

// open pushes the worker's commit to the task's branch, opens its pull request
// and sends it to be tested. Once a checkpoint is merged, main holds that
// commit squashed, so only what the worker made after it is laid onto main.
func (m *Module) open(t *Task, p core.Project, sha, body string) error {
	if t.Banked > 0 {
		main, err := m.C.Effects.Sync(p)
		if err != nil {
			return err
		}
		next, err := m.C.Effects.UpdateBranch(p, sha, main, t.Base)
		if errors.Is(err, core.ErrConflict) && t.Checkpoint != "" {
			return m.dropCheckpoint(t, p, "it conflicts with main")
		}
		if errors.Is(err, core.ErrConflict) {
			return m.conflict(t, p)
		}
		if err != nil {
			return err
		}
		sha = next
	}
	if err := m.C.Effects.Push(p, t.Branch, sha); err != nil {
		return err
	}
	if asksOutsideRead(body) {
		t.NeedsRead = true // kept once said, though a later round's account may not repeat it
	}
	pr, err := m.C.Effects.Forge.CreatePR(p.Repo, t.Branch, p.Branch, t.Label(), body+"\n\n"+marker(t.ID))
	if err != nil {
		return err
	}
	if t.PR.Number != 0 && t.SHA != sha {
		m.C.Effects.Forge.Comment(p.Repo, pr.Number, fmt.Sprintf("New commit %s after round %d.", core.Short(sha), t.Round))
	}
	// A worker that finishes again on the same commit thinks something outside
	// it changed (a tool installed, an answer), so its tests run again.
	t.PR, t.Recheck, t.SHA, t.Stage, t.Failures = pr, t.SHA == sha, sha, StageTesting, 0
	m.save(t, fmt.Sprintf("pushed %s, PR #%d", core.Short(sha), pr.Number))
	return nil
}

// noCommits ends a task whose worker made no commit. A helper's task is done, since its account already went to the agent that spawned it.
// An ask whose whole job was a step outside the checkout is done; anything else
// is blocked.
func (m *Module) noCommits(t *Task, p core.Project, r core.Run) {
	if t.Kind == "helper" {
		t.Stage = StageDone
		m.save(t, "the helper made no commits, so nothing to land")
		return
	}
	var steps string
	var ok bool
	if m.Steps != nil {
		steps, ok = m.Steps(r.ID)
	}
	if ok {
		t.Stage, t.IssueOpen, t.Failures = StageDone, true, 0
		m.save(t, "done outside the checkout, no commit:\n"+steps)
		m.C.Effects.Forge.Comment(p.Repo, t.Issue.Number, "Done with no commit, so nothing to merge. What ran outside the checkout:\n\n"+steps+"\n\n"+finishText(r))
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Done: %q, with no commit, so nothing to merge. What ran:\n%s", t.Label(), steps))
		m.closeIssue(t, p)
		return
	} else if steps != "" {
		steps = "\n\nSteps outside the checkout, not all done:\n" + steps
	}
	t.Stage, t.Reason = StageBlocked, "the worker finished without any commits: "+core.Clip(finishText(r), 300)+steps
	m.save(t, t.Reason)
	m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("%q finished with no change to merge. %s%s", t.Label(), core.Clip(finishText(r), 300), steps))
}

// closeIssue closes a done task's issue. A failure is journaled and a later
// tick tries again, waiting twice as long each time; after eight failures it
// gives up. The task stays done either way.
func (m *Module) closeIssue(t *Task, p core.Project) {
	if err := m.C.Effects.Forge.CloseIssue(p.Repo, t.Issue.Number); err != nil {
		t.CloseFail++
		if t.CloseFail >= 8 {
			t.IssueOpen = false
			m.save(t, "gave up closing the issue, left open on the forge: "+core.Clip(err.Error(), 300))
			return
		}
		t.Retry = time.Now().Add(time.Minute << t.CloseFail)
		m.save(t, "closing the issue failed (will retry): "+core.Clip(err.Error(), 300))
		return
	}
	t.IssueOpen = false
	m.save(t, fmt.Sprintf("closed issue #%d", t.Issue.Number))
}

func (m *Module) test(t *Task, p core.Project) error {
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return err
	}
	cfg := m.C.Effects.Config(p, main)
	c, ok := m.C.Effects.GetCheck(p.Name, t.SHA, "test")
	if !ok || t.Recheck {
		if _, err := m.C.Sandbox(); err != nil {
			return err
		}
		c, t.Recheck = m.C.Effects.RunCheck(p, t.SHA, "test", cfg.Test), false
		m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, fmt.Sprintf("Tests on %s: **%s**\n\n```\n%s\n```", core.Short(t.SHA), passFail(c.OK), core.Clip(c.Output, 1500)))
	}
	if !c.OK && t.Checkpoint != "" {
		return m.dropCheckpoint(t, p, "the tests failed on "+core.Short(t.SHA)+":\n\n"+core.Clip(c.Output, 1500))
	}
	if !c.OK {
		mc, err := m.mainTests(p, main, cfg.Test)
		if err != nil {
			return err
		}
		if !mc.OK {
			return m.waitOnMain(t, p, main, mc)
		}
		if ok, err := m.catchUp(t, p, main, "tests failed but pass on main"); ok || err != nil {
			return err
		}
		return m.sendBack(t, p, "The tests failed on your commit "+core.Short(t.SHA)+":\n\n"+core.Clip(c.Output, 3000))
	}
	ch, err := m.C.Effects.Classify(p, main, t.SHA)
	if err != nil {
		return err
	}
	t.Diff = ch.DiffID
	switch {
	case len(ch.Protected) > 0 && !m.C.Effects.Approved("merge", p.Name, t.SHA):
		t.Stage = StagePerson
		a := mergeAsk(t, p, ch)
		// Saved before Weigh: a warden's answer may reach onPersonMerge first.
		m.save(t, "waiting for an approval: changes "+strings.Join(ch.Protected, ", "))
		who := "person"
		if m.Weigh != nil {
			who = m.Weigh(a)
		} else {
			m.C.Person.Ask(a)
		}
		if who == "person" {
			m.Chat.Notify(t.Chat, t.Thread, approveNotice(t.Label(), ch.Protected, t.PR.URL))
		}
	case ch.Small || m.C.Effects.Reviewed(p, ch, t.SHA):
		t.Stage = StageMerging
		m.save(t, "tests passed; "+map[bool]string{true: "small change, no read needed", false: "already read"}[ch.Small])
	default:
		t.Stage = StageReviewing
		m.save(t, "tests passed; asking for a read")
	}
	return nil
}

// mainTests is main's own test result, measured once per main commit (the
// baseline and the post-merge measurement usually already have it).
func (m *Module) mainTests(p core.Project, main, command string) (core.Check, error) {
	if c, ok := m.C.Effects.GetCheck(p.Name, main, "test"); ok {
		return c, nil
	}
	if _, err := m.C.Sandbox(); err != nil {
		return core.Check{}, err
	}
	return m.C.Effects.RunCheck(p, main, "test", command), nil
}

// waitOnMain holds a red change while main's own tests are red, since the
// failure can't be told apart from main's. Main's failure is raised once per
// main commit, not once per waiting change; when main goes green the change
// is brought up to date and tested again.
func (m *Module) waitOnMain(t *Task, p core.Project, main string, mc core.Check) error {
	reason := "waiting on main: its own tests fail at " + core.Short(main) + ", so this failure isn't the worker's"
	if t.Reason == reason {
		return nil
	}
	m.mu.Lock()
	raised := m.mainRed[p.Name+"@"+main]
	m.mainRed[p.Name+"@"+main] = true
	m.mu.Unlock()
	if !raised {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "main-red", "project": p.Name, "sha": main,
			"text": "main's tests fail in " + p.Name + " at " + core.Short(main) + ", so changes with failing tests wait for main to go green: " + core.Clip(mc.Output, 300)})
	}
	t.Reason = reason
	m.save(t, reason)
	m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, "Main's own tests fail too at "+core.Short(main)+", so this isn't sent back to the worker. It waits for main to go green, then is brought up to date and tested again.")
	return nil
}

// catchUp brings t's branch up to date with main and sends it to be tested
// again. It reports false when the branch already contains main.
func (m *Module) catchUp(t *Task, p core.Project, main, why string) (bool, error) {
	if _, err := core.Git(m.C.WS.Project(p.Name), "merge-base", "--is-ancestor", main, t.SHA); err == nil {
		return false, nil
	}
	next, err := m.C.Effects.UpdateBranch(p, t.SHA, main, "")
	if errors.Is(err, core.ErrConflict) && t.Checkpoint != "" {
		return true, m.dropCheckpoint(t, p, "it conflicts with main")
	}
	if errors.Is(err, core.ErrConflict) {
		t.Reason = ""
		return true, m.conflict(t, p)
	}
	if err != nil {
		return true, err
	}
	if err := m.C.Effects.Push(p, t.Branch, next); err != nil {
		return true, err
	}
	t.SHA, t.Stage, t.Reason = next, StageTesting, ""
	m.save(t, why+"; branch brought up to date at "+core.Short(next)+", testing again")
	return true, nil
}

// mergeAsk asks the person to merge a change to protected files, carrying the
// whole diff of those files so they see every change they approve.
func mergeAsk(t *Task, p core.Project, ch core.Change) core.Ask {
	return core.Ask{Kind: "merge", Ref: t.ID + "@" + t.SHA, Project: p.Name, From: t.Worker,
		Title:  fmt.Sprintf("Merge %q? It changes %s", t.Label(), strings.Join(ch.Protected, ", ")),
		Detail: "These hold the project's goals, score or settings. PR: " + t.PR.URL, Options: []string{"merge", "leave open"},
		Diff: ch.ProtectedDiff}
}

// approveNotice tells the person a change is theirs and where to answer it.
func approveNotice(label string, protected []string, url string) string {
	return fmt.Sprintf("%q changes %s, which is yours to approve. Answer it in the control center's Waiting section, where you see the diff: in this chat, → then a. The PR: %s", label, strings.Join(protected, ", "), url)
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// VerdictSchema is the reviewer's structured answer.
const VerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["land","fix"]},"summary":{"type":"string"},"findings":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"line":{"type":"integer"},"issue":{"type":"string"}},"required":["issue"]}},"advice":{"type":"array","items":{"type":"string"}},"token":{"type":"string"}},"required":["verdict","summary","token"]}`

// reviewToken is copied from brief.md into the verdict, so a land shows the
// reviewer read its inputs.
func reviewToken(run string) string {
	sum := sha256.Sum256([]byte("review " + run))
	return hex.EncodeToString(sum[:6])
}

func (m *Module) review(t *Task, p core.Project) error {
	id := fmt.Sprintf("r-%s-%s-%d", t.ID, core.Short(t.SHA), t.Reviews)
	if n := len(m.C.Effects.Reads(p.Name, t.SHA, t.Diff)); n > 0 {
		id += fmt.Sprintf("-read%d", n+1) // a separate run for each further read
	}
	r, ok := m.C.Runs.Get(id)
	if !ok {
		main, err := m.C.Effects.Sync(p)
		if err != nil {
			return err
		}
		ch, err := m.C.Effects.Classify(p, main, t.SHA)
		if err != nil {
			return err
		}
		dir, err := m.C.Effects.NewCheckout(p, id, t.SHA)
		if err != nil {
			return err
		}
		c, _ := m.C.Effects.GetCheck(p.Name, t.SHA, "test")
		w, _ := m.C.Runs.Get(t.Worker)
		account := w.Brief + "\n\n## The worker's account\n\n" + finishText(w)
		if t.Kind == "revert" {
			account = fmt.Sprintf("The Hive made this revert itself; no worker wrote it. It reverts the merge %s because: %s\n\nIt also records that lesson in `.hive/lessons.md`. Check it undoes that merge and nothing else.", core.Short(t.RevertSHA), t.Lesson)
		}
		round := fmt.Sprintf("round %d", t.Round+1)
		if t.Checkpoint != "" {
			round = "a checkpoint"
			account = w.Brief + "\n\n## A checkpoint\n\nThe worker is still running and hasn't written its account yet. This commit is a checkpoint of its unfinished work, put up because it beats the best kept score; it merges on its own while the worker keeps going. Read it as a change that must be safe to merge as it stands, not as the finished task."
		}
		brief := fmt.Sprintf("Review %s of task %s: %q.\n\nThe brief, the exact diff and the test run are in $HIVE_RUN (brief.md, diff.patch, tests.txt). Give your verdict.", round, t.ID, t.Label())
		spec := m.Agents.Spec(id, "reviewer", p.Name, dir, brief)
		spec.ReadOnly, spec.Schema, spec.Task = true, VerdictSchema, t.ID
		spec.Inputs = map[string]string{"sha": t.SHA, "diff.patch": ch.Diff, "tests.txt": c.Output,
			"brief.md": "Review token: " + reviewToken(id) + "\n\n" + account + m.reviewRules(p, main)}
		if _, err := m.C.Runs.Start(spec); err != nil {
			return err
		}
		t.Reviewer = id
		m.save(t, "reviewer "+id+" reading "+core.Short(t.SHA))
		return nil
	}
	if !r.Terminal() {
		return nil
	}
	var v core.Verdict
	var tok struct{ Token string }
	json.Unmarshal(r.Structured, &tok)
	if err := json.Unmarshal(r.Structured, &v); err != nil || (v.Verdict != "land" && v.Verdict != "fix") || (v.Verdict == "land" && tok.Token != reviewToken(r.ID)) {
		t.Reviews++
		if t.Reviews >= 3 && t.Checkpoint != "" {
			t.Reviews = 0
			return m.dropCheckpoint(t, p, "the reviewer gave no usable verdict three times")
		}
		if t.Reviews >= 3 {
			m.giveUp(t, "the reviewer gave no usable verdict three times")
			return nil
		}
		m.save(t, "reviewer gave no usable verdict (a land must carry the token from its brief.md); asking again")
		return nil
	}
	v.Project, v.SHA, v.Run = p.Name, t.SHA, r.ID
	if err := m.C.Effects.RecordVerdict(v); err != nil {
		return err
	}
	var fb strings.Builder
	fmt.Fprintf(&fb, "**Review of %s: %s**\n\n%s\n", core.Short(t.SHA), v.Verdict, v.Summary)
	for _, f := range v.Findings {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(&fb, "\n- `%s` %s", loc, f.Issue)
	}
	for _, a := range v.Advice {
		fmt.Fprintf(&fb, "\n- Advice: %s", a)
	}
	m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, fb.String())
	if v.Verdict == "land" {
		// Advice with a land doesn't hold the merge; it is kept, from every read,
		// and filed as one follow-up once the change merges. A follow-up's own
		// advice stays on its pull request, so the chain stops there.
		keepAdvice(t, v.Advice)
		// Back through test(), which merges or asks a second, separate reader for a high class.
		t.Stage = StageTesting
		m.save(t, "reviewer: land")
		return nil
	}
	if t.Checkpoint != "" {
		return m.dropCheckpoint(t, p, "the reviewer asked for fixes:\n\n"+fb.String())
	}
	return m.sendBack(t, p, "The reviewer asked for fixes on "+core.Short(t.SHA)+":\n\n"+fb.String())
}

// keepAdvice adds a land's advice to the task once per item, unless the task
// is itself a follow-up.
func keepAdvice(t *Task, advice []string) {
	if t.FollowUp != "" {
		return
	}
	for _, a := range advice {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(t.Advice, a) {
			t.Advice = append(t.Advice, a)
		}
	}
}

// reviewRules are the project's standing rules, read from main so a change
// can't move the bar it is judged against. Lessons are among them: a revert
// the Hive made has no worker brief quoting them, and one may have landed
// since the worker started.
func (m *Module) reviewRules(p core.Project, main string) string {
	var b strings.Builder
	for _, f := range []string{"docs/REVIEW.md", ".hive/goals.md", ".hive/lessons.md"} {
		if s := strings.TrimSpace(m.C.Effects.ReadFile(p, main, f)); s != "" {
			fmt.Fprintf(&b, "\n\n## `%s` on main\n\n%s", f, s)
		}
	}
	return b.String()
}

// followUp files a merged task's review advice as one follow-up brief. The
// task is saved merged before this runs and done after, so a restart in
// between finds the brief already filed and doesn't file it again.
func (m *Module) followUp(t *Task, p core.Project) error {
	if len(t.Advice) == 0 || len(m.List(func(x Task) bool { return x.FollowUp == t.ID })) > 0 {
		return nil
	}
	url := t.PR.URL
	if url == "" && len(t.Banks) > 0 { // done on its checkpoints alone
		url = t.Banks[len(t.Banks)-1].PR.URL
	}
	_, err := m.File(Brief{Project: p.Name, From: t.Reviewer, Chat: t.Chat, Thread: t.Thread, FollowUp: t.ID,
		Title: "Follow up the review of " + core.Clip(t.Label(), 100),
		Done:  "Each item below is done, or judged not worth doing with the reason in your account.",
		Known: "The reviewer landed " + url + ", now merged, with this advice:\n\n- " + strings.Join(t.Advice, "\n- ")})
	return err
}

// sendBack returns findings to the same worker in the same checkout, a
// bounded number of times, after which the work stops and says so. A revert
// the Hive made has no worker, so it stops on the first findings.
func (m *Module) sendBack(t *Task, p core.Project, findings string) error {
	max := m.C.WS.LoadConfig().ReviewRounds
	if max <= 0 {
		max = 3
	}
	reason := ""
	switch {
	case t.Worker == "": // a host-made revert: nobody to send it back to
		reason = "the Hive made this commit itself, so no worker can fix it; what was found:\n\n" + core.Clip(findings, 1500)
	case t.Round >= max:
		reason = fmt.Sprintf("stopped after %d rounds of fixes; what remains:\n\n%s", t.Round, core.Clip(findings, 1500))
	}
	if reason != "" {
		m.giveUp(t, reason)
		m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, "Stopping here. "+t.Reason+"\n\nThe pull request stays open.")
		return nil
	}
	t.Round++
	t.Findings = findings
	if err := m.C.Runs.Reopen(t.Worker, findings+"\n\nFix these in your checkout, commit, and finish again."); err != nil {
		return err
	}
	t.Stage, t.Reviews = StageWorking, 0
	m.save(t, fmt.Sprintf("round %d: findings sent back to the worker", t.Round))
	return nil
}

func (m *Module) merge(t *Task, p core.Project) error {
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return err
	}
	if ok, err := m.catchUp(t, p, main, "main moved"); ok || err != nil {
		return err
	}
	// The security read first: it takes longest, so its line goes on the PR
	// while the forge's checks still run.
	if wait, err := m.outsideRead(t, p, main); wait || err != nil {
		return err
	}
	if wait, err := m.forgeChecks(t, p, main); wait || err != nil {
		return err
	}
	merged, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: t.PR.Number, SHA: t.SHA, Main: main, Subject: t.Label(), OutsideRead: t.NeedsRead})
	var np *core.NeedsPerson
	if errors.As(err, &np) {
		t.Stage = StagePerson
		m.save(t, np.Error())
		return nil
	}
	if err != nil {
		return err
	}
	t.MergeSHA, t.Stage, t.Reason = merged, StageMerged, ""
	if t.Checkpoint != "" {
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Merged a checkpoint of %s; its worker keeps going: %s", t.Label(), t.PR.URL))
		t.Base, t.Checkpoint, t.Stage = t.Checkpoint, "", StageWorking
		t.Banked++
		t.Banks = append(t.Banks, Bank{PR: t.PR, MergeSHA: merged}) // a revert of this merge names its PR
		t.Branch, t.PR = fmt.Sprintf("hive/%s-%d", t.ID, t.Banked), core.PR{}
		m.save(t, "checkpoint merged as "+core.Short(merged)+"; the worker keeps going")
		m.C.Runs.Send(t.Worker, fmt.Sprintf("Your commit %s beat the best kept score and is merged into main as a checkpoint. Keep going on top of it on your branch, and leave that commit as it is (no amend, reset or rebase past it): the Hive lays what you commit after it onto main, so you needn't fetch main.", core.Short(t.Base)))
		if m.OnMerged != nil {
			m.OnMerged(*t)
		}
		return nil
	}
	m.save(t, "merged as "+core.Short(merged))
	if t.Kind == "revert" {
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Reverted: %s. %s", strings.TrimPrefix(t.Label(), "Revert: "), t.Lesson))
	} else {
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Merged: %s %s", t.Label(), t.PR.URL))
	}
	if m.OnMerged != nil {
		m.OnMerged(*t)
	}
	return nil
}

// holdGap is how long a held task waits before the gate looks again.
var holdGap = time.Minute

// hold keeps t where it is for why, saying so on the PR once.
func (m *Module) hold(t *Task, p core.Project, why string) {
	m.mu.Lock()
	m.later[t.ID] = time.Now().Add(holdGap)
	m.mu.Unlock()
	if t.Reason == why {
		return
	}
	t.Reason = why
	m.save(t, why)
	m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, "Holding the merge: "+why+".")
}

// forgeChecks holds t until the forge's own checks (CI on every platform,
// which the Hive's own test doesn't cover) pass on exactly the head it would
// merge. A failure goes back to the worker as a red test does, unless main
// fails the same check; a check that can't start is the environment's, so t
// waits and it is raised, never sent back.
func (m *Module) forgeChecks(t *Task, p core.Project, main string) (bool, error) {
	runs, err := m.C.Effects.Forge.Checks(p.Repo, t.SHA)
	if err != nil {
		return true, err
	}
	st, which := core.ChecksState(runs)
	names := core.CheckNames(which)
	switch st {
	case core.CheckPending:
		m.hold(t, p, "waiting for the forge's checks on "+core.Short(t.SHA)+" ("+names+")")
		return true, nil
	case core.CheckCantStart:
		why := "the forge's checks " + names + " can't start on " + core.Short(t.SHA) + ", which is the environment's fault, not the change's; waiting for them to run"
		if t.Reason != why {
			m.raiseOnce(p, t.SHA+"/checks", "the forge's checks "+names+" can't start in "+p.Name+" (PR #"+fmt.Sprint(t.PR.Number)+" at "+core.Short(t.SHA)+"): "+checkDetail(which))
		}
		m.hold(t, p, why)
		return true, nil
	case core.CheckFail:
		mainRuns, err := m.C.Effects.Forge.Checks(p.Repo, main)
		if err != nil {
			return true, err
		}
		if _, onMain := core.ChecksState(mainRuns); slices.ContainsFunc(onMain, func(r core.CheckRun) bool {
			return r.State == core.CheckFail && strings.Contains(", "+names+", ", ", "+r.Name+", ")
		}) {
			why := "waiting on main: the forge's checks " + names + " fail on main at " + core.Short(main) + " too, so this failure isn't the worker's"
			if t.Reason != why {
				m.raiseOnce(p, main+"/checks", "the forge's checks "+names+" fail on main in "+p.Name+" at "+core.Short(main))
			}
			m.hold(t, p, why)
			return true, nil
		}
		t.Reason = ""
		return true, m.sendBack(t, p, "The forge's checks failed on your commit "+core.Short(t.SHA)+": "+checkDetail(which))
	}
	return false, nil
}

// checkDetail names each check with what it said and where to read it.
func checkDetail(runs []core.CheckRun) string {
	var b []string
	for _, r := range runs {
		s := r.Name
		if r.Detail != "" {
			s += " (" + r.Detail + ")"
		}
		if r.URL != "" {
			s += " " + r.URL
		}
		b = append(b, s)
	}
	return strings.Join(b, "; ")
}

// raiseOnce raises a main-red alert (something in the environment holds
// changes back) once per key, however many changes wait on it.
func (m *Module) raiseOnce(p core.Project, key, text string) {
	m.mu.Lock()
	raised := m.mainRed[p.Name+"@"+key]
	m.mainRed[p.Name+"@"+key] = true
	m.mu.Unlock()
	if !raised {
		m.C.J.MustAppend("alert.raised", map[string]string{"kind": "main-red", "project": p.Name, "sha": key, "text": text})
	}
}

// outsideRead holds a PR that touches a path on main's security list, or
// asked for a read from the box's perms seat or architect, until one is
// recorded as land on its own diff (hive read); a fix goes back to the
// worker with what the reader said.
func (m *Module) outsideRead(t *Task, p core.Project, main string) (bool, error) {
	if r, ok := m.C.Runs.Get(t.Worker); ok && !t.NeedsRead {
		t.NeedsRead = asksOutsideRead(finishText(r)) // a task landed before this was looked for
	}
	ch, err := m.C.Effects.Classify(p, main, t.SHA)
	if err != nil {
		return true, err
	}
	t.Diff = ch.DiffID
	if !t.NeedsRead && len(ch.Security) == 0 {
		return false, nil
	}
	o, ok := m.C.Effects.OutsideRead(p.Name, ch.DiffID)
	switch {
	case ok && o.Verdict == "land":
		return false, nil
	case ok && o.Verdict == "fix":
		t.Reason = ""
		return true, m.sendBack(t, p, fmt.Sprintf("The box's %s read %s and asked for fixes:\n\n%s", o.By, core.Short(o.SHA), orNone(o.Note)))
	}
	why := "the PR asks for one"
	if len(ch.Security) > 0 {
		why = "it changes " + strings.Join(ch.Security, ", ") + ", on " + core.SecurityList
	}
	m.hold(t, p, fmt.Sprintf("waiting on a security read from the box's perms seat or architect at %s, since %s; a read of an earlier head counts only while this change's own diff is the same (record one with `%s read %s %s land --by perms`)", core.Short(t.SHA), why, core.Cmd, t.ID, core.Short(t.SHA)))
	return true, nil
}

// asksOutsideRead reports whether a worker's account says the change needs a
// read from the box's perms seat or its architect (goals.md: sandbox,
// self-update and login changes).
func asksOutsideRead(s string) bool { return outsideReadRe.MatchString(s) }

var outsideReadRe = regexp.MustCompile(`(?i)\bperms seat\b|\bread\b[^.\n]{0,80}\barchitect\b|\barchitect\b[^.\n]{0,80}\bread\b`)

// RecordRead records an outside read of the head sha of task id (or of PR
// #id), given by the box's perms seat or architect, bound to that head's own
// diff. Only the person records one.
func (m *Module) RecordRead(id, sha, by, verdict, note string) (Task, error) {
	var t Task
	ok := false
	if n, err := strconv.Atoi(strings.TrimPrefix(id, "#")); err == nil {
		ts := m.List(func(x Task) bool { return x.PR.Number == n && !x.Terminal() })
		if len(ts) > 1 {
			return t, fmt.Errorf("PR #%d is open in more than one project; name the task", n)
		}
		if ok = len(ts) == 1; ok {
			t = ts[0]
		}
	} else {
		t, ok = m.Get(id)
	}
	if !ok {
		return t, fmt.Errorf("no task or PR %s", id)
	}
	p, err := m.project(&t)
	if err != nil {
		return t, err
	}
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return t, err
	}
	if sha == "" {
		return t, errors.New("name the head that was read, as it may have moved since")
	}
	full, err := core.Git(m.C.WS.Project(p.Name), "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err != nil {
		return t, fmt.Errorf("%s is not a commit in %s", sha, p.Name)
	}
	ch, err := m.C.Effects.Classify(p, main, full)
	if err != nil {
		return t, err
	}
	err = m.C.Effects.RecordRead(core.OutsideRead{Project: p.Name, SHA: full, Diff: ch.DiffID, PR: t.PR.Number, By: by, Verdict: verdict, Note: note})
	if err != nil {
		return t, err
	}
	m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, fmt.Sprintf("**The box's %s read %s: %s**\n\n%s", by, core.Short(full), verdict, note))
	return t, nil
}

func (m *Module) conflict(t *Task, p core.Project) error {
	t.Conflicts++
	if t.Conflicts > 3 {
		m.giveUp(t, "keeps conflicting with main")
		m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, "Stopping: the branch keeps conflicting with main. The pull request stays open.")
		return nil
	}
	if err := m.C.Effects.MainBundle(p, filepath.Join(m.C.WS.RunRO(t.Worker), "main.bundle")); err != nil {
		return err
	}
	msg := "Main moved and your branch conflicts with it. Fetch main with `git fetch \"$HIVE_RUN/main.bundle\" main`, merge FETCH_HEAD, resolve the conflicts, run the tests, commit, and finish again."
	if err := m.C.Runs.Reopen(t.Worker, msg); err != nil {
		return err
	}
	t.Stage = StageWorking
	m.save(t, "conflict with main sent back to the worker")
	return nil
}

func (m *Module) prepareRevert(t *Task, p core.Project) error {
	main, err := m.C.Effects.Sync(p)
	if err != nil {
		return err
	}
	of, _ := m.Get(t.RevertOf)
	ofPR := of.PROf(t.RevertSHA) // a checkpoint's merge has a PR of its own
	if t.SHA == "" {
		lesson := fmt.Sprintf("%s: %q (PR #%d, %s) was reverted: %s", time.Now().Format("2006-01-02"), of.Label(), ofPR.Number, core.Short(t.RevertSHA), t.Lesson)
		sha, err := m.C.Effects.Revert(p, main, t.RevertSHA, lesson)
		if err != nil {
			return err
		}
		t.SHA, t.Base, t.Branch = sha, main, "hive/revert-"+t.ID
		m.save(t, "revert commit "+core.Short(sha))
	}
	if err := m.C.Effects.Push(p, t.Branch, t.SHA); err != nil {
		return err
	}
	body := fmt.Sprintf("Reverts #%d (%s).\n\n%s\n\nThe lesson is recorded in `.hive/lessons.md`.\n\n%s", ofPR.Number, core.Short(t.RevertSHA), t.Lesson, marker(t.ID))
	pr, err := m.C.Effects.Forge.CreatePR(p.Repo, t.Branch, p.Branch, t.Label(), body)
	if err != nil {
		return err
	}
	if ofPR.Number != 0 {
		m.C.Effects.Forge.Comment(p.Repo, ofPR.Number, fmt.Sprintf("This change is being reverted in #%d: %s", pr.Number, t.Lesson))
	}
	t.PR, t.Stage = pr, StageTesting
	m.save(t, fmt.Sprintf("revert PR #%d", pr.Number))
	return nil
}

func (m *Module) onPersonMerge(a core.Ask) {
	id, sha, _ := strings.Cut(a.Ref, "@")
	t, ok := m.Get(id)
	if !ok || t.Stage != StagePerson {
		return
	}
	if a.Answer == "merge" || a.Answer == "yes" {
		m.C.Effects.Approve("merge", t.Project, sha)
		t.Stage = StageTesting // test() routes to a read or the merge
		m.save(&t, "the person approved")
		return
	}
	if p, err := m.project(&t); err == nil && t.Checkpoint != "" {
		// Later commits would carry the same change and ask again, so the
		// rest waits for the worker's finish.
		t.NoBank = "the person left a checkpoint open"
		m.dropCheckpoint(&t, p, strings.TrimSuffix("left open: "+a.Note, ": ")+"; no more checkpoints, the rest lands when the worker finishes")
		return
	}
	t.Stage, t.Reason, t.ByPerson = StageStopped, strings.TrimSuffix("left open: "+a.Note, ": "), true
	m.save(&t, t.Reason)
	m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("%q was %s: %s", t.Label(), t.Reason, t.PR.URL))
}

// Stop ends a task on purpose (from the control center).
func (m *Module) Stop(id, reason string) error {
	t, ok := m.Get(id)
	if !ok {
		return core.ErrNotFound
	}
	if t.Worker != "" {
		m.C.Runs.Stop(t.Worker, reason)
	}
	t.Stage, t.Reason, t.ByPerson = StageStopped, reason, true
	m.save(&t, "stopped: "+reason)
	return nil
}

// giveUp stops a task the pipeline can't take further and hands it to the
// lead, which brings it back with recheck or leaves it; no count ends it for good.
func (m *Module) giveUp(t *Task, reason string) {
	t.Stage, t.Reason = StageStopped, reason
	m.save(t, core.Clip(reason, 300))
	m.Chat.ToLead(t.Chat, t.Thread, fmt.Sprintf("Stopped %q (task %s): %s\n\nIt's yours: bring it back with recheck, or leave it.", t.Label(), strings.TrimSpace(t.ID+" "+t.PR.URL), core.Clip(reason, 1500)))
}

// woke reports whether a blocked task's worker is taking turns again.
func (m *Module) woke(t Task) bool {
	if t.Stage != StageBlocked || t.Worker == "" {
		return false
	}
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && !r.Terminal()
}

// Recheck brings a blocked or stopped task back with fresh counts: with no
// text its commits are pushed and checked again (what blocked it may be fixed
// now); with text its worker gets that and works on in the same checkout. A
// task the person stopped or left open stays theirs to bring back.
func (m *Module) Recheck(id, back string) error {
	t, ok := m.Get(id)
	if !ok {
		return core.ErrNotFound
	}
	if t.Stage != StageBlocked && t.Stage != StageStopped {
		return fmt.Errorf("task %s is %s, not blocked or stopped", id, t.Stage)
	}
	if t.ByPerson || m.workerStopped(t) {
		return fmt.Errorf("task %s was stopped by the person (%s); only they can bring it back", id, t.Reason)
	}
	t.Reason, t.Failures, t.Round, t.Reviews, t.Conflicts = "", 0, 0, 0, 0
	if back != "" {
		if err := m.C.Runs.Reopen(t.Worker, back); err != nil {
			return err
		}
		t.Stage = StageWorking
		m.save(&t, "sent back to its worker: "+core.Clip(back, 200))
		return nil
	}
	t.Stage = StageLanding
	m.save(&t, "checks run again on its commits")
	return nil
}

// workerStopped reports whether a task's worker run was stopped on purpose,
// which holds a task stopped before checkWorker marked it ByPerson.
func (m *Module) workerStopped(t Task) bool {
	r, ok := m.C.Runs.Get(t.Worker)
	return ok && t.Worker != "" && (r.Status == core.StatusStopped || r.StopAsked)
}

func (m *Module) opRecheck(r core.Run, args json.RawMessage) (any, error) {
	var a struct{ Task, Back string }
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	if t, ok := m.Get(a.Task); ok {
		if err := m.C.Runs.Walled(r, t.Project); err != nil {
			return nil, err
		}
	}
	if err := m.Recheck(a.Task, a.Back); err != nil {
		return nil, err
	}
	return map[string]string{"task": a.Task, "note": "back in the pipeline; you'll be told the outcome"}, nil
}

// Retry hands a blocked or stopped task to a fresh worker on the same branch
// with the first one's notes.
func (m *Module) Retry(id string) error {
	t, ok := m.Get(id)
	if !ok {
		return core.ErrNotFound
	}
	if t.Worker != "" {
		notes, _ := os.ReadFile(filepath.Join(m.C.WS.RunBox(t.Worker), "notes.md"))
		r, _ := m.C.Runs.Get(t.Worker)
		t.Known += fmt.Sprintf("\n\nA previous worker (%s) stopped: %s\nIts notes:\n%s", t.Worker, core.Clip(r.Reason+" "+finishText(r), 800), core.Clip(string(notes), 2000))
	}
	t.Attempt++
	t.Stage, t.Worker, t.Reason, t.Failures, t.Round = StageIssued, "", "", 0, 0
	t.ByPerson = false // the person brought it back; the lead may handle it from here
	m.save(&t, "handed to a fresh worker")
	return nil
}

// doneFor is the done line a brief carries. On a scored project an agent's own
// bar would cap the work, so the ask stands as done; the Hive's own setup brief
// and a review follow-up keep their line.
func doneFor(t *Task, scored bool) string {
	if scored && t.From != "hive" && t.FollowUp == "" {
		return "What the ask says, as far as you can take it."
	}
	return orNone(t.Done)
}
