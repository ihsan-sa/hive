// Package api is the control center's view of the daemon: the types it
// returns and a client for its local HTTP endpoint.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/chat"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/feedback"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/improve"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/risk"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/system"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

// DaemonInfo is .hive/private/daemon.json.
type DaemonInfo struct {
	PID        int       `json:"pid"`
	Started    time.Time `json:"started"`
	Port       int       `json:"port"`
	Token      string    `json:"token"`
	Version    string    `json:"version"`
	Restarting bool      `json:"restarting,omitempty"` // set by a daemon handing over to a new one
}

// State is everything the control center shows.
type State struct {
	Version    string             `json:"version"`
	Channel    string             `json:"channel,omitempty"` // the releases auto-update follows: "latest" or "pre-release"
	Seq        int64              `json:"seq"`
	Workspace  string             `json:"workspace"`
	Sandbox    string             `json:"sandbox"`
	SandboxErr string             `json:"sandbox_err,omitempty"`
	Offline    string             `json:"offline,omitempty"` // what the Hive can't reach right now, in one plain line
	Projects   []ProjectView      `json:"projects"`
	Runs       []core.Run         `json:"runs"`
	Plans      []core.Plan        `json:"plans"`
	Sweeps     []improve.Sweep    `json:"sweeps"`
	StepBacks  []improve.StepBack `json:"stepbacks"`
	Asks       []core.Ask         `json:"asks"`
	Alerts     []Alert            `json:"alerts"`
	Updated    *Updated           `json:"updated,omitempty"`
	Jobs       []system.JobState  `json:"jobs"`
	Wakeups    []system.Wakeup    `json:"wakeups"`
	Checks     Checks             `json:"checks"`
	Modules    []ModuleView       `json:"modules"`
	Chats      []ChatView         `json:"chats"`
	Requests   []risk.Request     `json:"requests"`
	Feedback   []feedback.Item    `json:"feedback"` // open first, then the last few set aside
	Config     core.Config        `json:"config"`
	ConfigErr  string             `json:"config_err,omitempty"`
}

// Checks are the checks holding a slot and those waiting for one, in order.
type Checks struct {
	Running []core.SlotUse `json:"running,omitempty"`
	Waiting []core.SlotUse `json:"waiting,omitempty"`
}

// ProjectView is one project on the dashboard.
type ProjectView struct {
	core.Project
	Goals  string                `json:"goals"`
	Tasks  []work.Task           `json:"tasks"`
	Scores []improve.Measurement `json:"scores"`
	Best   *float64              `json:"best,omitempty"`
	Hold   *work.MergeHold       `json:"hold,omitempty"` // the person's hold on its merges
}

// Updated is the update that brought in this version, with its release notes;
// Unsigned when no release key was pinned to check it against.
type Updated struct {
	Seq      int64  `json:"seq"`
	To       string `json:"to"`
	Notes    string `json:"notes"`
	Unsigned bool   `json:"unsigned,omitempty"`
}

// Alert is a ledger alert.
type Alert struct {
	At    time.Time `json:"at"` // last seen
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	Count int       `json:"count"`
	Ref   string    `json:"ref,omitempty"` // a later alert with the same ref replaces it
	Key   string    `json:"-"`             // kind, project and cause: a repeat refreshes the alert
}

// ModuleView is one installed module.
type ModuleView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Release     string   `json:"release"`
	Projects    []string `json:"projects"`
	Hooks       []string `json:"hooks,omitempty"`
	Status      string   `json:"status,omitempty"` // a module with a program: running, or why not
}

// ChatView is one chat in the list.
type ChatView struct {
	chat.Chat
	Unread     int    `json:"unread"`
	LeadStatus string `json:"lead_status,omitempty"`
}

// RunDetail is an agent's step-by-step log.
type RunDetail struct {
	Run   core.Run `json:"run"`
	Steps []Step   `json:"steps"`
}

// Step is one line of an agent's log.
type Step struct {
	Turn int    `json:"turn"`
	Kind string `json:"kind"`
	Tool string `json:"tool,omitempty"`
	Text string `json:"text"`
}

// StepsFrom converts harness events.
func StepsFrom(turn int, evs []harness.Event) []Step {
	var out []Step
	for _, e := range evs {
		out = append(out, Step{Turn: turn, Kind: e.Kind, Tool: e.Tool, Text: e.Text})
	}
	return out
}

// Client talks to a running daemon.
type Client struct {
	Info DaemonInfo // daemon.json as Dial read it; Do never changes it
	ws   core.Workspace
	mu   sync.Mutex
	cur  DaemonInfo // the daemon Do last reached, which a restart moves
	http *http.Client
}

// Dial reads daemon.json and returns a client (without checking liveness).
func Dial(ws core.Workspace) (*Client, error) {
	info, err := readInfo(ws)
	if err != nil {
		return nil, err
	}
	return &Client{Info: info, cur: info, ws: ws, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

func readInfo(ws core.Workspace) (DaemonInfo, error) {
	var info DaemonInfo
	raw, err := os.ReadFile(ws.DaemonFile())
	if errors.Is(err, fs.ErrNotExist) {
		raw, err = os.ReadFile(filepath.Join(ws.Hive(), "daemon.json")) // a daemon from before .hive/private
	}
	if err != nil {
		return info, errors.New("the Hive isn't running")
	}
	return info, json.Unmarshal(raw, &info)
}

// restartWait covers a restart: the new daemon waits up to 10s for the old
// one to exit, then opens the workspace before it listens.
const restartWait = 30 * time.Second

// Do calls an endpoint; body may be nil; out may be nil. When the daemon
// can't be reached while a restart is under way (daemon.json says the old one
// is restarting, or already names a new one), Do keeps dialling for up to
// restartWait and sends the request to the new daemon. Otherwise a failed
// dial is returned at once.
func (c *Client) Do(method, path string, body, out any) error {
	c.mu.Lock()
	info := c.cur
	c.mu.Unlock()
	err, from := c.do(info, method, path, body, out), info.PID
	for end := time.Now().Add(restartWait); dialFailed(err) && time.Now().Before(end); {
		next, e := readInfo(c.ws)
		if e != nil || (!next.Restarting && next.PID == from) {
			break
		}
		time.Sleep(200 * time.Millisecond)
		info = next
		err = c.do(info, method, path, body, out)
	}
	if err == nil {
		c.mu.Lock()
		c.cur = info
		c.mu.Unlock()
	}
	return err
}

func dialFailed(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func (c *Client) do(info DaemonInfo, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", info.Port, path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+info.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Ping reports whether the daemon Dial found answers, trying once.
func (c *Client) Ping() bool {
	var v map[string]any
	return c.do(c.Info, "GET", "/api/ping", nil, &v) == nil
}

// State fetches the dashboard state.
func (c *Client) State() (State, error) {
	var s State
	return s, c.Do("GET", "/api/state", nil, &s)
}

// ConfigView is GET /api/config: every setting that read, and what config.json got wrong.
type ConfigView struct {
	core.Config
	LoadError string `json:"load_error,omitempty"`
}
