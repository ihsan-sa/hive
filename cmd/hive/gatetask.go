package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/gate"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
)

// cmdGateTask is `hive gate-task <spec.json>`, run by the recursive gate
// inside a side's sandbox, where the side's daemon listens on that sandbox's
// own loopback: it starts the side's Hive, gives it the task in the lead's
// chat as a person would, watches until the work lands, is given up, waits on
// the person or runs out of time, stops it, and writes what it saw to the
// spec's out file. The gate grades what landed, from outside.
func cmdGateTask(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: hive gate-task <spec.json>   (the recursive gate's driver, inside a side's sandbox)")
		return 2
	}
	raw, err := os.ReadFile(args[0])
	var spec gate.TaskSpec
	if err == nil {
		err = json.Unmarshal(raw, &spec)
	}
	if err != nil || spec.Out == "" {
		fmt.Fprintln(os.Stderr, "gate-task:", err)
		return 2
	}
	d := driveTask(spec)
	out, _ := json.Marshal(d)
	if err := os.WriteFile(spec.Out, out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gate-task:", err)
		return 1
	}
	return 0
}

// taskView is the little of a side's /api/state the driver reads, by name,
// so a side of another version still answers it.
type taskView struct {
	Projects []struct {
		Name  string `json:"name"`
		Tasks []struct {
			Title    string `json:"title"`
			Stage    string `json:"stage"`
			MergeSHA string `json:"merge_sha"`
		} `json:"tasks"`
	} `json:"projects"`
	Runs []struct {
		ID     string  `json:"id"`
		Status string  `json:"status"`
		Reason string  `json:"reason"`
		Cost   float64 `json:"cost"`
	} `json:"runs"`
	Asks []json.RawMessage `json:"asks"`
}

// taskWhere says where the side stands on the task (heval/arms.py judge):
// "landed" once a task merged and every task but review follow-ups settled,
// "gave up" once every task settled with none merged, "waiting on the person"
// when all that's left waits on them, else "".
func taskWhere(v taskView, project string) string {
	settled := map[string]bool{"merged": true, "done": true, "blocked": true, "stopped": true}
	merged, open, person, n := false, 0, 0, 0
	for _, p := range v.Projects {
		if p.Name != project {
			continue
		}
		for _, t := range p.Tasks {
			if strings.HasPrefix(t.Title, "Set up the Hive files") {
				continue
			}
			n++
			merged = merged || t.MergeSHA != ""
			if strings.HasPrefix(t.Title, "Follow up the review of") || settled[t.Stage] {
				continue
			}
			open++
			if t.Stage == "waiting-person" {
				person++
			}
		}
	}
	switch {
	case n == 0:
		return ""
	case open == 0 && merged:
		return "landed"
	case open == 0:
		return "gave up"
	case person == open:
		return "waiting on the person"
	}
	return ""
}

func driveTask(spec gate.TaskSpec) gate.TaskDrive {
	ws := core.Workspace{Root: spec.WS}
	log, err := os.Create(filepath.Join(filepath.Dir(spec.Out), "daemon.log"))
	if err != nil {
		return gate.TaskDrive{Outcome: "error: " + err.Error()}
	}
	defer log.Close()
	cmd := exec.Command(spec.Bin, "daemon")
	cmd.Dir = spec.WS
	cmd.Env = append(os.Environ(), "HIVE_HOME="+spec.WS, "HIVE_FORGE=local:"+spec.Forge)
	cmd.Stdout, cmd.Stderr = log, log
	proc.Group(cmd)
	if err := cmd.Start(); err != nil {
		return gate.TaskDrive{Outcome: "error: the side's daemon didn't start: " + err.Error()}
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	var c *api.Client
	defer func() {
		if c != nil {
			if st, err := state(c); err == nil {
				for _, r := range st.Runs {
					if r.Status == core.StatusRunning || r.Status == core.StatusQueued || r.Status == core.StatusParked || r.Status == core.StatusIdle {
						c.Do("POST", "/api/run/stop", map[string]string{"id": r.ID}, nil)
					}
				}
			}
			c.Do("POST", "/api/shutdown", nil, nil)
		}
		select {
		case <-exited:
		case <-time.After(30 * time.Second):
		}
		proc.KillGroup(cmd) // what it left in its group
	}()
	for end := time.Now().Add(2 * time.Minute); c == nil || !c.Ping(); time.Sleep(500 * time.Millisecond) {
		select {
		case <-exited:
			return gate.TaskDrive{Outcome: "error: the side's daemon exited at start"}
		default:
		}
		if time.Now().After(end) {
			return gate.TaskDrive{Outcome: "error: the side's daemon didn't answer in 2 minutes"}
		}
		c, _ = api.Dial(ws)
	}
	if err := c.Do("POST", "/api/project", map[string]string{"name": spec.Project, "repo": "me/" + spec.Project}, nil); err != nil {
		return gate.TaskDrive{Outcome: "error: adding the project: " + err.Error()}
	}
	if err := c.Do("POST", "/api/chat", map[string]string{"chat": "lead", "text": "In " + spec.Project + ": " + spec.Prompt}, nil); err != nil {
		return gate.TaskDrive{Outcome: "error: asking the lead: " + err.Error()}
	}
	var d gate.TaskDrive
	end := time.Now().Add(time.Duration(spec.WallS * float64(time.Second)))
	for d.Outcome == "" {
		select {
		case <-exited:
			d.Outcome = "the side's daemon exited"
			return d
		case <-time.After(5 * time.Second):
		}
		st, err := state(c)
		if err != nil {
			continue // a daemon busy restarting a turn: next look
		}
		d.Cost, d.Runs, d.Tasks = 0, len(st.Runs), 0
		for _, r := range st.Runs {
			d.Cost += r.Cost
			if strings.Contains(r.Reason, "no sandbox available") {
				d.Outcome = "error: " + r.Reason
			}
		}
		for _, p := range st.Projects {
			if p.Name == spec.Project {
				d.Tasks = len(p.Tasks)
			}
		}
		if d.Outcome == "" {
			d.Outcome = taskWhere(st, spec.Project)
		}
		if d.Outcome == "" && time.Now().After(end) {
			d.Outcome = fmt.Sprintf("out of time after %.0f min", spec.WallS/60)
		}
	}
	return d
}

func state(c *api.Client) (taskView, error) {
	var v taskView
	err := c.Do("GET", "/api/state", nil, &v)
	return v, err
}
