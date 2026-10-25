package main

import (
	"encoding/json"
	"strings"
	"time"
)

// State is what the program keeps between passes, in its HOME: where it is
// in the journal, when it last dreamed, and what the record says now, folded
// from every event it read. It holds facts, not events, so it stays small.
type State struct {
	Cursor  int64             `json:"cursor"`
	Born    time.Time         `json:"born"` // its first start
	Last    time.Time         `json:"last"` // its last pass
	Tasks   map[string]*Task  `json:"tasks"`
	Runs    map[string]*Run   `json:"runs"`
	Gates   []Judged          `json:"gates"`
	Sweeps  []Report          `json:"sweeps"`
	Steps   map[string]Report `json:"steps"` // the last step-back, by project
	Said    []Said            `json:"said"`  // the person's latest lines
	Alerts  map[string]int    `json:"alerts"`
	Changed time.Time         `json:"changed"` // the newest event folded
}

// Task is a task's last state.
type Task struct {
	Title    string    `json:"title"`
	Project  string    `json:"project"`
	Stage    string    `json:"stage"`
	Reason   string    `json:"reason,omitempty"`
	At       time.Time `json:"at"`
	Merged   time.Time `json:"merged,omitzero"`
	Reverted time.Time `json:"reverted,omitzero"`
	Blocked  time.Time `json:"blocked,omitzero"`
}

// Run is a run's task and its last finish.
type Run struct {
	Task    string    `json:"task,omitempty"`
	Project string    `json:"project,omitempty"`
	Status  string    `json:"status,omitempty"`
	At      time.Time `json:"at,omitzero"`
}

// Judged is one judgement of the recursive gate.
type Judged struct {
	Project string    `json:"project"`
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason"`
	Head    string    `json:"head"`
	At      time.Time `json:"at"`
}

// Report is a sweep's or a step-back's outcome.
type Report struct {
	Project string    `json:"project,omitempty"`
	Status  string    `json:"status"`
	Text    string    `json:"text"`
	At      time.Time `json:"at"`
}

// Said is a line the person wrote in a chat.
type Said struct {
	Chat string    `json:"chat"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Event is a journal event as /api/events returns it.
type Event struct {
	Seq  int64           `json:"seq"`
	At   time.Time       `json:"at"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func newState() *State {
	return &State{Tasks: map[string]*Task{}, Runs: map[string]*Run{}, Steps: map[string]Report{}, Alerts: map[string]int{}}
}

const keepGates, keepSweeps, keepSaid = 200, 3, 20

// fold takes one event into the state. It returns the job a module.scheduled
// event asks this module for, or "idle" for hive.idle.
func (s *State) fold(e Event, me string) string {
	s.Cursor = max(s.Cursor, e.Seq)
	var d struct {
		ID, Title, Project, Stage, Reason, Task, Status, Outcome, Head, Note, Report, Chat, From, Text, Kind, Module, Job string
		Finish                                                                                                            struct{ Status string }
	}
	json.Unmarshal(e.Data, &d)
	switch e.Kind {
	case "task.saved":
		t := s.Tasks[d.ID]
		if t == nil {
			t = &Task{}
			s.Tasks[d.ID] = t
		}
		t.Title, t.Project, t.Stage, t.Reason, t.At = d.Title, d.Project, d.Stage, clip(d.Reason, 300), e.At
		switch d.Stage {
		case "merged", "done":
			if t.Merged.IsZero() {
				t.Merged = e.At
			}
		case "reverting":
			t.Reverted = e.At
		case "blocked":
			t.Blocked = e.At
		}
	case "run.created":
		s.Runs[d.ID] = &Run{Task: d.Task, Project: d.Project}
	case "run.finish":
		r := s.Runs[d.ID]
		if r == nil {
			r = &Run{}
			s.Runs[d.ID] = r
		}
		r.Status, r.At = d.Finish.Status, e.At
	case "gate.judged":
		s.Gates = append(s.Gates, Judged{Project: d.Project, Outcome: d.Outcome, Reason: clip(d.Reason, 300), Head: d.Head, At: e.At})
		if len(s.Gates) > keepGates {
			s.Gates = s.Gates[len(s.Gates)-keepGates:]
		}
	case "sweep.saved":
		if d.Status == "done" && d.Report != "" {
			s.Sweeps = append(s.Sweeps, Report{Status: d.Status, Text: clip(d.Report, 1500), At: e.At})
			if len(s.Sweeps) > keepSweeps {
				s.Sweeps = s.Sweeps[len(s.Sweeps)-keepSweeps:]
			}
		}
	case "stepback.saved":
		if d.Status != "running" && d.Status != "" {
			s.Steps[d.Project] = Report{Project: d.Project, Status: d.Status, Text: clip(firstOf(d.Report, d.Note), 600), At: e.At}
		}
	case "chat.posted":
		if d.From == "person" && strings.TrimSpace(d.Text) != "" {
			s.Said = append(s.Said, Said{Chat: d.Chat, Text: clip(d.Text, 300), At: e.At})
			if len(s.Said) > keepSaid {
				s.Said = s.Said[len(s.Said)-keepSaid:]
			}
		}
	case "alert.raised":
		s.Alerts[firstOf(d.Kind, "other")]++
	case "module.scheduled":
		if d.Module == me {
			return firstOf(d.Job, "kb.dream")
		}
		return ""
	case "hive.idle":
		return "idle"
	default:
		return ""
	}
	s.Changed = e.At
	return ""
}

func firstOf(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// taskOf is the task a note is about: its run's, else the one titled so.
func (s *State) taskOf(run, title string) (string, *Task) {
	if r := s.Runs[run]; r != nil && s.Tasks[r.Task] != nil {
		return r.Task, s.Tasks[r.Task]
	}
	id, best := "", (*Task)(nil)
	for k, t := range s.Tasks {
		if title != "" && strings.EqualFold(t.Title, title) && (best == nil || t.At.After(best.At) || (t.At.Equal(best.At) && k > id)) {
			id, best = k, t
		}
	}
	return id, best
}
