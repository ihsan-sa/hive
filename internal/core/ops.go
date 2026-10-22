package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Request is what an agent writes into its outbox. The caller's identity is
// the run whose outbox it came from, never anything inside the file.
type Request struct {
	ID   string          `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}

// Response is written to the agent's inbox.
type Response struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// OpFunc handles one request from run r.
type OpFunc func(r Run, args json.RawMessage) (any, error)

// Op is a registered operation and who may call it.
type Op struct {
	Name  string
	Roles []string // empty: any role
	Help  string
	Fn    OpFunc
}

// Ops is the registry of mailbox operations. The core registers the
// operations it owns; modules register theirs.
type Ops struct {
	rs      *Runs
	mu      sync.Mutex
	ops     map[string]Op
	spawner Spawner
	routers []Router
}

// NewOps creates the registry with the core's own operations.
func NewOps(rs *Runs) *Ops {
	o := &Ops{rs: rs, ops: map[string]Op{}}
	o.registerCore()
	return o
}

// Register adds or replaces an operation.
func (o *Ops) Register(op Op) {
	o.mu.Lock()
	o.ops[op.Name] = op
	o.mu.Unlock()
}

// List returns registered operations sorted by name.
func (o *Ops) List() []Op {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []Op
	for _, op := range o.ops {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Call runs an op as run r (also used by the control API for the person).
func (o *Ops) Call(r Run, name string, args json.RawMessage) (any, error) {
	o.mu.Lock()
	op, ok := o.ops[name]
	o.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown op %q", name)
	}
	if len(op.Roles) > 0 && !contains(op.Roles, r.Role) {
		return nil, fmt.Errorf("op %q is not granted to a %s", name, r.Role)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	return op.Fn(r, args)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Drain handles every request waiting in a run's outbox.
func (o *Ops) Drain(r Run) {
	box := o.rs.ws.RunBox(r.ID)
	if !isRealDir(box) || !isRealDir(filepath.Join(box, "outbox")) || !isRealDir(filepath.Join(box, "inbox")) {
		return
	}
	entries, err := os.ReadDir(filepath.Join(box, "outbox"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(box, "outbox", e.Name())
		raw, err := SafeRead(path, 1<<20)
		os.Remove(path)
		reqID := strings.TrimSuffix(e.Name(), ".json")
		resp := Response{ID: reqID}
		var req Request
		if err == nil {
			err = json.Unmarshal(raw, &req)
		}
		if err == nil {
			var res any
			res, err = o.Call(r, req.Op, req.Args)
			resp.Result = res
		}
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
		}
		writeResponse(filepath.Join(box, "inbox"), reqID, resp)
	}
}

func isRealDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

// SafeRead reads a regular file the sandbox could have written, refusing
// symlinks and anything that changes between the check and the open.
func SafeRead(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("file changed while reading")
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

func writeResponse(inbox, id string, resp Response) {
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return
	}
	raw, _ := json.Marshal(resp)
	tmp := filepath.Join(inbox, "."+id+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	f.Write(raw)
	f.Close()
	if fi, err := os.Lstat(tmp); err != nil || !fi.Mode().IsRegular() {
		os.Remove(tmp)
		return
	}
	os.Rename(tmp, filepath.Join(inbox, id+".json"))
}

// Bind decodes args into v with a helpful error.
func Bind(args json.RawMessage, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("bad arguments: %v", err)
	}
	return nil
}

// Spawner creates checkouts and system prompts for spawned agents; the work
// module provides it.
type Spawner func(parent Run, role, project, brief, model string) (RunSpec, error)

// Router delivers a message to an address the core doesn't own (the person, a chat).
type Router func(from Run, to, text string) (bool, error)

// SetSpawner installs the work module's spawner.
func (o *Ops) SetSpawner(s Spawner) { o.spawner = s }

// AddRouter installs a message router (the chat module routes "person").
func (o *Ops) AddRouter(r Router) { o.routers = append(o.routers, r) }

func (o *Ops) registerCore() {
	o.Register(Op{Name: "message", Help: `{"to": "<run id | name | parent | person>", "text": "..."}`, Fn: func(r Run, a json.RawMessage) (any, error) {
		var m struct{ To, Text string }
		if err := Bind(a, &m); err != nil {
			return nil, err
		}
		if strings.TrimSpace(m.Text) == "" {
			return nil, errors.New("empty message")
		}
		return o.Deliver(r, m.To, m.Text)
	}})
	o.Register(Op{Name: "inbox", Help: `{} -> messages waiting for you (and marks them read)`, Fn: func(r Run, _ json.RawMessage) (any, error) {
		cur, _ := o.rs.Get(r.ID)
		if len(cur.Pending) > 0 {
			o.rs.append("run.delivered", runNote{ID: r.ID, N: len(cur.Pending)})
		}
		return cur.Pending, nil
	}})
	o.Register(Op{Name: "progress", Help: `{"text": "where you are, in your own words"}`, Fn: func(r Run, a json.RawMessage) (any, error) {
		var m struct{ Text string }
		if err := Bind(a, &m); err != nil {
			return nil, err
		}
		o.rs.Progress(r.ID, m.Text)
		return "noted", nil
	}})
	o.Register(Op{Name: "finish", Help: `{"status": "done|blocked", "account": "what you did", "tested": "...", "unsure": "..."}`, Fn: func(r Run, a json.RawMessage) (any, error) {
		var f Finish
		if err := Bind(a, &f); err != nil {
			return nil, err
		}
		if f.Status != "done" && f.Status != "blocked" {
			return nil, errors.New(`status must be "done" or "blocked"`)
		}
		if strings.TrimSpace(f.Account) == "" {
			return nil, errors.New("account is required: say what you did, or why you stopped")
		}
		o.rs.append("run.finish", struct {
			ID     string `json:"id"`
			Finish Finish `json:"finish"`
		}{r.ID, f})
		return "recorded; end your turn now", nil
	}})
	o.Register(Op{Name: "spawn", Help: `{"role": "worker", "brief": "...", "project": "(default: yours)", "model": "(optional)"} -> a helper agent; its account comes back to you as a message, and a worker's commits get their own pull request`, Fn: func(r Run, a json.RawMessage) (any, error) {
		var m struct{ Role, Brief, Project, Model string }
		if err := Bind(a, &m); err != nil {
			return nil, err
		}
		if m.Role == "" {
			m.Role = "worker"
		}
		if m.Role != "worker" && m.Role != "reviewer" {
			return nil, fmt.Errorf("agents may spawn workers or reviewers; for wider reach ask a warden (op warden)")
		}
		if m.Project == "" {
			m.Project = r.Project
		}
		if o.spawner == nil {
			return nil, errors.New("no spawner installed")
		}
		spec, err := o.spawner(r, m.Role, m.Project, m.Brief, m.Model)
		if err != nil {
			return nil, err
		}
		spec.Parent = r.ID
		child, err := o.rs.Start(spec)
		if err != nil {
			return nil, err
		}
		o.rs.Wait(r.ID, "helper "+child.ID)
		return map[string]string{"id": child.ID, "note": "started; its account will arrive as a message (end your turn to wait)"}, nil
	}})
	o.Register(Op{Name: "agents", Help: `{} -> live agents you can message`, Fn: func(r Run, _ json.RawMessage) (any, error) {
		var out []map[string]string
		for _, x := range o.rs.List(func(x *Run) bool { return !x.Terminal() }) {
			out = append(out, map[string]string{"id": x.ID, "name": x.Name, "role": x.Role, "project": x.Project, "status": x.Status, "progress": x.Progress})
		}
		return out, nil
	}})
}

// Deliver routes a message from r to an address.
func (o *Ops) Deliver(from Run, to, text string) (any, error) {
	if to == "parent" {
		to = from.Parent
	}
	for _, route := range o.routers {
		if ok, err := route(from, to, text); ok || err != nil {
			if err != nil {
				return nil, err
			}
			return "delivered", nil
		}
	}
	target, ok := o.rs.Find(to)
	if !ok {
		return nil, fmt.Errorf("no agent %q (try op agents)", to)
	}
	label := from.Name
	if label == "" {
		label = from.Role + " " + from.ID
	}
	msg := fmt.Sprintf("[message from %s (%s) at %s]\n%s", label, from.ID, time.Now().Format("15:04"), text)
	if err := o.rs.Send(target.ID, msg); err != nil {
		return nil, err
	}
	o.rs.append("msg.sent", map[string]string{"from": from.ID, "to": target.ID, "text": Clip(text, 2000)})
	return "delivered", nil
}
