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
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/stats"
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
	Newest     string             `json:"newest,omitempty"`  // the newest release out, as the last look for an update found
	Behind     int                `json:"behind,omitempty"`  // how many releases newer than Version are out
	Seq        int64              `json:"seq"`
	Workspace  string             `json:"workspace"`
	Sandbox    string             `json:"sandbox"`
	SandboxErr string             `json:"sandbox_err,omitempty"`
	Offline    string             `json:"offline,omitempty"` // what the Hive can't reach right now, in one plain line
	Held       []string           `json:"held,omitempty"`    // why no new turn starts right now (a restart, the knowledge base's seed)
	Projects   []ProjectView      `json:"projects"`
	Runs       []core.Run         `json:"runs"`
	Plans      []core.Plan        `json:"plans"`
	Down       []core.Down        `json:"down,omitempty"` // models that can't answer, which runs skip, each with its last answer
	Logins     []core.LoginState  `json:"logins"`         // each login's expiry and last renewal
	Sweeps     []improve.Sweep    `json:"sweeps"`
	StepBacks  []improve.StepBack `json:"stepbacks"`
	Asks       []core.Ask         `json:"asks"`
	Told       []core.Ask         `json:"told,omitempty"` // done for the person, newest first: nothing to answer
	Alerts     []Alert            `json:"alerts"`
	Updated    *Updated           `json:"updated,omitempty"`
	Jobs       []system.JobState  `json:"jobs"`
	Wakeups    []system.Wakeup    `json:"wakeups"`
	Checks     Checks             `json:"checks"`
	Workers    string             `json:"workers,omitempty"` // how many workers may work at once, and why (the work module's pace)
	Disk       core.DiskUse       `json:"disk"`              // the Hive's disk use and the room left, as the hive.clean job last measured
	Modules    []ModuleView       `json:"modules"`
	Chats      []ChatView         `json:"chats"`
	Requests   []risk.Request     `json:"requests"`
	Feedback   []feedback.Item    `json:"feedback"` // open first, then the last few set aside
	Stats      *stats.View        `json:"stats,omitempty"`
	Config     core.Config        `json:"config"`
	ConfigErr  string             `json:"config_err,omitempty"`
}

// Checks are the checks holding a slot and those waiting for one, in order.
type Checks struct {
	Running []core.SlotUse `json:"running,omitempty"`
	Waiting []core.SlotUse `json:"waiting,omitempty"`
	Limit   string         `json:"limit,omitempty"` // how many may run at once, and why (the work module's pace)
}

// ProjectView is one project on the dashboard.
type ProjectView struct {
	core.Project
	Goals  string                `json:"goals"`
	Tasks  []work.Task           `json:"tasks"`
	Scores []improve.Measurement `json:"scores"`
	Best   *float64              `json:"best,omitempty"`
	Hold   *work.MergeHold       `json:"hold,omitempty"` // the person's hold on its merges
	// Flakes are its quarantined tests, each with the task that fixes it.
	Flakes []improve.Flake `json:"flakes,omitempty"`
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
	Info   DaemonInfo                 // daemon.json as Dial read it; Do never changes it
	info   func() (DaemonInfo, error) // daemon.json now: the file, or what a Remote last heard
	remote bool                       // reached through a Remote's SSH proxy
	mu     sync.Mutex
	cur    DaemonInfo // the daemon Do last reached, which a restart moves
	http   *http.Client
}

// Dial reads daemon.json and returns a client (without checking liveness).
func Dial(ws core.Workspace) (*Client, error) {
	info, err := readInfo(ws)
	if err != nil {
		return nil, err
	}
	return &Client{Info: info, cur: info, info: func() (DaemonInfo, error) { return readInfo(ws) }, http: &http.Client{Timeout: 60 * time.Second}}, nil
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

// remoteLag is how long a Remote waits to hear of a new daemon (remote-info
// looks once a second) before taking a failed request as the Hive being down.
const remoteLag = 3 * time.Second

// Do calls an endpoint; body may be nil; out may be nil. When the daemon
// can't be reached while a restart is under way (daemon.json says the old one
// is restarting, or already names a new one), Do keeps dialling for up to
// restartWait and sends the request to the new daemon. Otherwise a failed
// dial is returned at once.
func (c *Client) Do(method, path string, body, out any) error {
	if c.remote {
		if _, e := c.info(); e != nil { // the ssh session ended: say so, not a refused request
			return fmt.Errorf("%w; quit and run `%s remote` again", e, core.Cmd)
		}
	}
	c.mu.Lock()
	info := c.cur
	c.mu.Unlock()
	err, from := c.do(info, method, path, body, out), info.PID
	if errors.Is(err, errUnauthorized) {
		// A new daemon on the old port has a new token: daemon.json names it.
		// Refused, the request did nothing, so sending it again is safe.
		if next, e := c.info(); e == nil && !next.Restarting && next.Token != info.Token {
			info = next
			err = c.do(info, method, path, body, out)
		}
	}
	start := time.Now()
	for end := start.Add(restartWait); c.dialFailed(err) && time.Now().Before(end); {
		next, e := c.info()
		// A Remote hears of a new daemon up to a second after the old one
		// goes, so it gives the news remoteLag to arrive.
		if e != nil || (!next.Restarting && next.PID == from && (!c.remote || time.Since(start) > remoteLag)) {
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

var errUnauthorized = errors.New("unauthorized")

// dialFailed: the request never went out, so sending it again is safe.
// Through SSH's proxy a daemon that's gone fails the proxy's CONNECT, which
// Go's SOCKS dialer reports as "socks connect". Anything later (a reset, an
// EOF, a timeout) may come after the daemon took the request, and is never
// sent again: a chat message would be posted twice.
func (c *Client) dialFailed(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && (op.Op == "dial" || c.remote && op.Op == "socks connect")
}

// Remote: the client reaches its daemon through a Remote's SSH session.
func (c *Client) Remote() bool { return c.remote }

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
		json.Unmarshal(raw, &e)
		switch {
		case resp.StatusCode == http.StatusUnauthorized && (e.Error == "" || e.Error == errUnauthorized.Error()):
			return errUnauthorized
		case resp.StatusCode == http.StatusUnauthorized: // Do's retry still sees it; the person reads why
			return fmt.Errorf("%w: %s", errUnauthorized, e.Error)
		case e.Error != "":
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
