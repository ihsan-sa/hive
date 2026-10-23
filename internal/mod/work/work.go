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
	"sort"
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
	Kind      string            `json:"kind"` // work | revert
	Stage     string            `json:"stage"`
	Reason    string            `json:"reason,omitempty"`
	Issue     core.Issue        `json:"issue"`
	Worker    string            `json:"worker,omitempty"`
	Base      string            `json:"base,omitempty"`
	Branch    string            `json:"branch,omitempty"`
	SHA       string            `json:"sha,omitempty"`
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
	Inputs    map[string]string `json:"inputs,omitempty"` // files its worker reads in $HIVE_RUN, kept off the issue
	Failures  int               `json:"failures"`
	CloseFail int               `json:"close_fail,omitempty"` // failed tries to close the issue
	Attempt   int               `json:"attempt"`              // fresh workers handed this task
	Retry     time.Time         `json:"retry,omitempty"`
	IssueOpen bool              `json:"issue_open,omitempty"` // done, but its issue is still to close
	Log       []Entry           `json:"log,omitempty"`
	Created   time.Time         `json:"created"`
	Updated   time.Time         `json:"updated"`
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
}

// New loads tasks and registers the brief op.
func New(c *core.Core, a *agents.Agents, ch *chat.Module) *Module {
	m := &Module{C: c, Agents: a, Chat: ch, tasks: map[string]*Task{}, busy: map[string]bool{}, mainRed: map[string]bool{}}
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
}

// File creates a task from a brief.
func (m *Module) File(b Brief) (Task, error) {
	if strings.TrimSpace(b.Title) == "" {
		return Task{}, errors.New("a brief needs a title")
	}
	if b.Project == "" {
		ps := m.C.Projects.List()
		if len(ps) != 1 {
			names := []string{}
			for _, p := range ps {
				names = append(names, p.Name)
			}
			return Task{}, fmt.Errorf("say which project (one of: %s)", strings.Join(names, ", "))
		}
		b.Project = ps[0].Name
	}
	if _, ok := m.C.Projects.Get(b.Project); !ok {
		return Task{}, fmt.Errorf("no project %q", b.Project)
	}
	t := &Task{ID: core.NewID(), Project: b.Project, Title: strings.TrimSpace(b.Title), Done: b.Done, Known: b.Known,
		From: b.From, Chat: b.Chat, Thread: b.Thread, Kind: "work", Stage: StageNew, Inputs: b.Inputs, Created: time.Now().UTC()}
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
	if r.Role == "lead" {
		b.Chat, b.Thread = m.Chat.CurrentThread(r)
	} else if parent, ok := m.taskOf(r.ID); ok {
		b.Chat, b.Thread = parent.Chat, parent.Thread
	}
	t, err := m.File(b)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "note": "filed; it becomes an issue and a worker shortly"}, nil
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
	for _, t := range m.List(func(t Task) bool { return !t.Terminal() || t.IssueOpen }) {
		if time.Now().Before(t.Retry) {
			continue
		}
		m.mu.Lock()
		if m.busy[t.ID] {
			m.mu.Unlock()
			continue
		}
		m.busy[t.ID] = true
		m.mu.Unlock()
		go func(t Task) {
			defer func() { m.mu.Lock(); delete(m.busy, t.ID); m.mu.Unlock() }()
			if err := m.step(&t); err != nil {
				t.Failures++
				t.Retry = time.Now().Add(time.Duration(min(t.Failures, 6)) * 30 * time.Second)
				msg := err.Error()
				if t.Failures >= 8 {
					t.Stage, t.Reason = StageStopped, "gave up after repeated errors: "+msg
					m.save(&t, t.Reason)
					m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Stopped %q: %s", t.Label(), core.Clip(msg, 300)))
					return
				}
				m.save(&t, "error (will retry): "+core.Clip(msg, 300))
			}
		}(t)
	}
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
		m.C.Effects.Forge.Comment(p.Repo, t.Issue.Number, "The worker stopped:\n\n"+t.Reason)
		m.C.J.MustAppend("kb.note", map[string]string{"title": "Blocked: " + t.Label(), "text": t.Reason, "project": t.Project, "run": r.ID})
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Blocked: %q. %s", t.Label(), core.Clip(t.Reason, 400)))
	case core.StatusStopped:
		t.Stage, t.Reason = StageStopped, "worker stopped: "+r.Reason
		m.save(t, t.Reason)
	case core.StatusQuiet:
		if !strings.Contains(t.Reason, "quiet") {
			t.Reason = "worker is quiet: " + r.Reason
			m.save(t, t.Reason)
			m.C.J.MustAppend("alert.raised", map[string]string{"kind": "quiet", "run": r.ID, "task": t.ID, "text": "worker " + r.ID + " keeps ending turns without finishing"})
		}
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
	if errors.Is(err, core.ErrNoChanges) {
		m.noCommits(t, p, r)
		return nil
	}
	if err != nil {
		return err
	}
	if err := m.C.Effects.Push(p, t.Branch, sha); err != nil {
		return err
	}
	body := finishText(r) + fmt.Sprintf("\n\nCloses #%d\n\n%s", t.Issue.Number, marker(t.ID))
	pr, err := m.C.Effects.Forge.CreatePR(p.Repo, t.Branch, p.Branch, t.Label(), body)
	if err != nil {
		return err
	}
	if t.PR.Number != 0 && t.SHA != sha {
		m.C.Effects.Forge.Comment(p.Repo, pr.Number, fmt.Sprintf("New commit %s after round %d.", core.Short(sha), t.Round))
	}
	t.PR, t.SHA, t.Stage, t.Failures = pr, sha, StageTesting, 0
	m.save(t, fmt.Sprintf("pushed %s, PR #%d", core.Short(sha), pr.Number))
	return nil
}

// noCommits ends a task whose worker made no commit. An ask whose whole job
// was a step outside the checkout is done; anything else is blocked.
func (m *Module) noCommits(t *Task, p core.Project, r core.Run) {
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
	if !ok {
		if _, err := m.C.Sandbox(); err != nil {
			return err
		}
		c = m.C.Effects.RunCheck(p, t.SHA, "test", cfg.Test)
		m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, fmt.Sprintf("Tests on %s: **%s**\n\n```\n%s\n```", core.Short(t.SHA), passFail(c.OK), core.Clip(c.Output, 1500)))
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
	switch {
	case len(ch.Protected) > 0 && !m.C.Effects.Approved("merge", p.Name, t.SHA):
		t.Stage = StagePerson
		diff, _ := core.Git(m.C.WS.Project(p.Name), append([]string{"diff", ch.Base, t.SHA, "--"}, ch.Protected...)...)
		a := core.Ask{Kind: "merge", Ref: t.ID + "@" + t.SHA, Project: p.Name, From: t.Worker,
			Title:  fmt.Sprintf("Merge %q? It changes %s", t.Label(), strings.Join(ch.Protected, ", ")),
			Detail: fmt.Sprintf("These hold the project's goals, score or settings. PR: %s\n\n%s", t.PR.URL, core.Clip(diff, 6000)), Options: []string{"merge", "leave open"}}
		// Saved before Weigh: a warden's answer may reach onPersonMerge first.
		m.save(t, "waiting for an approval: changes "+strings.Join(ch.Protected, ", "))
		who := "person"
		if m.Weigh != nil {
			who = m.Weigh(a)
		} else {
			m.C.Person.Ask(a)
		}
		if who == "person" {
			m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("%q changes %s, which is yours to approve. It's waiting on you: %s", t.Label(), strings.Join(ch.Protected, ", "), t.PR.URL))
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
	next, err := m.C.Effects.UpdateBranch(p, t.SHA, main)
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

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// VerdictSchema is the reviewer's structured answer.
const VerdictSchema = `{"type":"object","properties":{"verdict":{"type":"string","enum":["land","fix"]},"summary":{"type":"string"},"findings":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"line":{"type":"integer"},"issue":{"type":"string"}},"required":["issue"]}},"token":{"type":"string"}},"required":["verdict","summary","token"]}`

// reviewToken is copied from brief.md into the verdict, so a land shows the
// reviewer read its inputs.
func reviewToken(run string) string {
	sum := sha256.Sum256([]byte("review " + run))
	return hex.EncodeToString(sum[:6])
}

func (m *Module) review(t *Task, p core.Project) error {
	id := fmt.Sprintf("r-%s-%s-%d", t.ID, core.Short(t.SHA), t.Reviews)
	if n := len(m.C.Effects.Reads(p.Name, t.SHA)); n > 0 {
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
		brief := fmt.Sprintf("Review round %d of task %s: %q.\n\nThe brief, the exact diff and the test run are in $HIVE_RUN (brief.md, diff.patch, tests.txt). Give your verdict.", t.Round+1, t.ID, t.Label())
		spec := m.Agents.Spec(id, "reviewer", p.Name, dir, brief)
		spec.ReadOnly, spec.Schema, spec.Task = true, VerdictSchema, t.ID
		spec.Inputs = map[string]string{"sha": t.SHA, "diff.patch": ch.Diff, "tests.txt": c.Output,
			"brief.md": "Review token: " + reviewToken(id) + "\n\n" + account}
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
		if t.Reviews >= 3 {
			t.Stage, t.Reason = StageStopped, "the reviewer gave no usable verdict three times"
			m.save(t, t.Reason)
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
	m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, fb.String())
	if v.Verdict == "land" {
		// Back through test(), which merges or asks a second, separate reader for a high class.
		t.Stage = StageTesting
		m.save(t, "reviewer: land")
		return nil
	}
	return m.sendBack(t, p, "The reviewer asked for fixes on "+core.Short(t.SHA)+":\n\n"+fb.String())
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
		t.Stage, t.Reason = StageStopped, reason
		m.save(t, "stopped: nothing more to send back")
		m.C.Effects.Forge.Comment(p.Repo, t.PR.Number, "Stopping here. "+t.Reason+"\n\nThe pull request stays open.")
		m.Chat.Notify(t.Chat, t.Thread, fmt.Sprintf("Stopped %q; the PR stays open: %s", t.Label(), t.PR.URL))
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
	merged, err := m.C.Effects.Merge(core.MergeRequest{Project: p, PR: t.PR.Number, SHA: t.SHA, Main: main, Subject: t.Label()})
	var np *core.NeedsPerson
	if errors.As(err, &np) {
		t.Stage = StagePerson
		m.save(t, np.Error())
		return nil
	}
	if err != nil {
		return err
	}
	t.MergeSHA, t.Stage = merged, StageMerged
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

func (m *Module) conflict(t *Task, p core.Project) error {
	t.Conflicts++
	if t.Conflicts > 3 {
		t.Stage, t.Reason = StageStopped, "keeps conflicting with main"
		m.save(t, t.Reason)
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
	if t.SHA == "" {
		lesson := fmt.Sprintf("%s: %q (PR #%d, %s) was reverted: %s", time.Now().Format("2006-01-02"), of.Label(), of.PR.Number, core.Short(t.RevertSHA), t.Lesson)
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
	body := fmt.Sprintf("Reverts #%d (%s).\n\n%s\n\nThe lesson is recorded in `.hive/lessons.md`.\n\n%s", of.PR.Number, core.Short(t.RevertSHA), t.Lesson, marker(t.ID))
	pr, err := m.C.Effects.Forge.CreatePR(p.Repo, t.Branch, p.Branch, t.Label(), body)
	if err != nil {
		return err
	}
	if of.PR.Number != 0 {
		m.C.Effects.Forge.Comment(p.Repo, of.PR.Number, fmt.Sprintf("This change is being reverted in #%d: %s", pr.Number, t.Lesson))
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
	t.Stage, t.Reason = StageStopped, strings.TrimSuffix("left open: "+a.Note, ": ")
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
	t.Stage, t.Reason = StageStopped, reason
	m.save(&t, "stopped: "+reason)
	return nil
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
	m.save(&t, "handed to a fresh worker")
	return nil
}

// doneFor is the done line a brief carries. On a scored project an agent's own
// bar would cap the work, so the ask stands as done; the Hive's own setup brief
// keeps its line.
func doneFor(t *Task, scored bool) string {
	if scored && t.From != "hive" {
		return "What the ask says, as far as you can take it."
	}
	return orNone(t.Done)
}
