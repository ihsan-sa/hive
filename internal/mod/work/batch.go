package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// Batching: several related briefs go to one worker, who does them all in one
// session and lands them as one PR, through one round of checks and reads.
// The per-task overhead (a queue wait, a worker's start, checks, reads and a
// merge for each) was most of the Hive's time and spend (docs/research/
// forced.md), while one plain Claude Code session did ten of the Hive's own
// issues in an hour (hive-eval's backlog run). The first task named is the
// batch; each other is added to it as a joined ask, its issue closed as
// covered by the batch's, as a withdraw into it does.

// Batch folds the tasks rest into the task into. into may be waiting or
// already being worked on (not yet pushed: its worker is told); each of rest
// must be a brief r may withdraw, taken the same way as into (fixes with
// fixes, so no gain skips the gate).
func (m *Module) Batch(r core.Run, into string, rest []string, why string) (Task, error) {
	if why = strings.TrimSpace(why); why == "" {
		return Task{}, errors.New("say why these go together with why (it goes on each issue and the record)")
	}
	if len(rest) == 0 {
		return Task{}, errors.New("name at least two tasks: --tasks <batch>,<id>,...")
	}
	t, ok := m.Get(into)
	if !ok || m.C.Runs.Walled(r, t.Project) != nil {
		return Task{}, fmt.Errorf("no task %q", into)
	}
	if err := m.reach(r, t.Project); err != nil {
		return Task{}, err
	}
	// Check every one first, so a refusal leaves nothing half batched.
	var folded []Task
	for _, id := range rest {
		o, ok := m.Get(id)
		switch {
		case !ok || m.C.Runs.Walled(r, o.Project) != nil:
			return Task{}, fmt.Errorf("no task %q", id)
		case id == into || o.Project != t.Project:
			return Task{}, fmt.Errorf("task %s can't go into %s: a batch is one task's other briefs on the same project", id, into)
		case o.Fix() != t.Fix():
			return Task{}, fmt.Errorf("task %s is a %s and %s a %s: batch fixes with fixes and gains with gains, so no gain merges without the gate", id, o.AimWord(), into, t.AimWord())
		}
		if err := m.mayChange(r, o, "batched"); err != nil {
			return Task{}, err
		}
		folded = append(folded, o)
	}
	for _, o := range folded {
		if !m.claim(into) {
			return Task{}, fmt.Errorf("task %s is busy; try again in a moment", into)
		}
		t, ok = m.Get(into)
		if !ok || t.Kind != "work" || t.Model != "" || !(t.Stage == StageNew || t.Queued() || t.Stage == StageWorking) || m.workerEnded(t) {
			m.release(into)
			return Task{}, fmt.Errorf("task %s is %s, past taking more asks: batch into a brief waiting for a worker, or one whose worker hasn't finished", into, t.Stage)
		}
		added := m.addJoin(&t, Join{From: o.From, Why: why, Chat: o.Chat, Thread: o.Thread, Task: o.ID, Issue: o.Issue.Number}, o.Title, o.Done, o.Known, "batched here by "+r.ID+" from task "+o.ID)
		m.save(&t, fmt.Sprintf("batched task %s in, by %s: %s", o.ID, r.ID, core.Clip(why, 300)))
		if t.Stage == StageWorking && t.Worker != "" {
			m.C.Runs.Send(t.Worker, "Another task is batched into yours, to land in the same PR. Do it in this session too, as its own commit, or say in your account what you left out:\n\n"+added)
		}
		m.release(into)
		if _, err := m.Withdraw(r, o.ID, into, "batched into task "+into+": "+why); err != nil {
			return t, fmt.Errorf("task %s's ask is in %s, but it couldn't be withdrawn: %w", o.ID, into, err)
		}
	}
	t, _ = m.Get(into)
	return t, nil
}

// Batched are the issues of the tasks batched into t.
func (t Task) Batched() []int {
	var out []int
	for _, j := range t.Joins {
		if j.Task != "" && j.Issue != 0 {
			out = append(out, j.Issue)
		}
	}
	return out
}

// batchNote is what a batched task's brief and PR say of the tasks in it, or
// "" when none is.
func batchNote(t *Task) string {
	n := 0
	for _, j := range t.Joins {
		if j.Task != "" {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("This task is a batch: %d more tasks were folded into it (each \"batched here\" under what's already known). Do them all in this one session, each as its own commit, and land them as this one PR; in your account say for each what you did, or what you left out and why.", n)
}

func (m *Module) opBatch(r core.Run, args json.RawMessage) (any, error) {
	var a struct {
		Tasks any
		Why   string
	}
	if err := core.Bind(args, &a); err != nil {
		return nil, err
	}
	var ids []string
	switch v := a.Tasks.(type) {
	case string:
		ids = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				ids = append(ids, strings.TrimSpace(s))
			}
		}
	}
	seen := map[string]bool{}
	ids = slices.DeleteFunc(ids, func(id string) bool { d := id == "" || seen[id]; seen[id] = true; return d })
	if len(ids) < 2 {
		return nil, errors.New("name the tasks: --tasks <batch>,<id>,... (the first is the batch, the rest fold into it)")
	}
	t, err := m.Batch(r, ids[0], ids[1:], a.Why)
	if err != nil {
		return nil, err
	}
	return map[string]string{"task": t.ID, "title": t.Label(), "stage": t.Stage, "batched": strings.Join(ids[1:], ","), "note": "one worker takes them all and lands them as one PR; the others are withdrawn into it"}, nil
}
