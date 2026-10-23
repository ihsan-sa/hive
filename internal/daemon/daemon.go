// Package daemon runs the Hive in the background and serves the control
// center's API on localhost, guarded by a token only the person's account can read.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/api"
	"github.com/ihsan-sa/hive-standalone-build/internal/app"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/projects"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/release"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
	"github.com/ihsan-sa/hive-standalone-build/internal/proc"
	"github.com/ihsan-sa/hive-standalone-build/internal/setup"
)

// Forge picks the forge: GitHub, or a local folder forge when HIVE_FORGE=local:<dir>.
func Forge() core.Forge {
	if v := os.Getenv("HIVE_FORGE"); strings.HasPrefix(v, "local:") {
		return &core.Local{Dir: strings.TrimPrefix(v, "local:")}
	}
	return core.GitHub{}
}

// Running returns a client if a daemon answers for ws.
func Running(ws core.Workspace) (*api.Client, bool) {
	c, err := api.Dial(ws)
	if err != nil {
		return nil, false
	}
	if !proc.Alive(proc.Handle{PID: c.Info.PID}) || !c.Ping() {
		return nil, false
	}
	return c, true
}

// Start launches the daemon in the background if it isn't running, and waits for it.
func Start(ws core.Workspace) (*api.Client, error) {
	if c, ok := Running(ws); ok {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	env := append(os.Environ(), "HIVE_HOME="+ws.Root)
	if _, err := proc.StartDetached([]string{exe, "daemon"}, env, ws.Root, "", filepath.Join(ws.Hive(), "daemon.log")); err != nil {
		return nil, err
	}
	for i := 0; i < 100; i++ {
		time.Sleep(200 * time.Millisecond)
		if c, ok := Running(ws); ok {
			return c, nil
		}
	}
	log, _ := os.ReadFile(filepath.Join(ws.Hive(), "daemon.log.err"))
	return nil, fmt.Errorf("the Hive didn't start: %s", core.Clip(string(log), 600))
}

// Stop asks the daemon to shut down. Running agents are separate processes
// and keep going; the next start reattaches to them.
func Stop(ws core.Workspace) error {
	c, ok := Running(ws)
	if !ok {
		return errors.New("the Hive isn't running")
	}
	c.Do("POST", "/api/shutdown", nil, nil)
	for i := 0; i < 50; i++ {
		time.Sleep(200 * time.Millisecond)
		if !proc.Alive(proc.Handle{PID: c.Info.PID}) {
			return nil
		}
	}
	return errors.New("the Hive didn't stop within 10s")
}

// Run is `hive daemon`: the background process.
func Run(ws core.Workspace, waitFor int) error {
	inUnit := inHiveUnit()
	from := "" // the version of the daemon this one replaces
	if waitFor > 0 {
		if c, err := api.Dial(ws); err == nil {
			from = c.Info.Version
		}
		if inUnit {
			// An old unit stops everything in it as the old daemon exits; outlive that and hand the restart to systemd below.
			signal.Ignore(syscall.SIGTERM)
		}
		for i := 0; i < 100 && proc.Alive(proc.Handle{PID: waitFor}); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if _, ok := Running(ws); ok {
		return errors.New("the Hive is already running")
	}
	exe := release.Rename(ws.Root, from)
	if inUnit && (waitFor > 0 || exe != "") {
		if exe == "" {
			exe, _ = executable()
		}
		if setup.RestartUnit(exe, ws.Root) {
			return nil
		}
	}
	if exe != "" {
		// This update renamed hive-v0 to hive: start the daemon under its new name.
		_, err := proc.StartDetached([]string{exe, "daemon"}, append(os.Environ(), "HIVE_HOME="+ws.Root), ws.Root, "", filepath.Join(ws.Hive(), "daemon.log"))
		return err
	}
	a, err := app.New(ws, Forge())
	if err != nil {
		return err
	}
	defer a.Close()
	if _, err := ws.ReadConfig(); err != nil {
		fmt.Fprintln(os.Stderr, "settings:", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	var tok [16]byte
	rand.Read(tok[:])
	info := api.DaemonInfo{PID: os.Getpid(), Started: time.Now().UTC(), Port: ln.Addr().(*net.TCPAddr).Port, Token: hex.EncodeToString(tok[:]), Version: core.Version}
	raw, _ := json.Marshal(info)
	if err := core.WriteFileAtomic(ws.DaemonFile(), raw, 0o600); err != nil {
		return err
	}
	defer func() {
		// A restart leaves daemon.json for the control center to follow.
		if cur, err := api.Dial(ws); err == nil && cur.Info.PID == os.Getpid() && !cur.Info.Restarting {
			os.Remove(ws.DaemonFile())
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &server{a: a, info: info, cancel: cancel}
	a.Runner.API = fmt.Sprintf("http://127.0.0.1:%d", info.Port)
	a.Release.Restart = func() { s.restart() }
	a.C.J.Subscribe(s.onEvent)
	a.C.J.MustAppend("hive.started", map[string]string{"version": core.Version, "pid": strconv.Itoa(os.Getpid())})
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; cancel() }()
	a.Run(ctx)
	a.C.J.MustAppend("hive.stopped", map[string]string{"pid": strconv.Itoa(os.Getpid())})
	shut, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	return srv.Shutdown(shut)
}

type server struct {
	a      *app.App
	info   api.DaemonInfo
	cancel func()
	mu     sync.Mutex
	alerts []api.Alert
}

func (s *server) onEvent(e core.Event) {
	if e.Kind != "alert.raised" {
		return
	}
	var a struct{ Kind, Text, Ref string }
	e.Decode(&a)
	s.mu.Lock()
	s.alerts = slices.DeleteFunc(s.alerts, func(x api.Alert) bool { return a.Ref != "" && x.Ref == a.Ref })
	s.alerts = append(s.alerts, api.Alert{At: e.At, Kind: a.Kind, Text: a.Text, Ref: a.Ref})
	if len(s.alerts) > 50 {
		s.alerts = s.alerts[len(s.alerts)-50:]
	}
	s.mu.Unlock()
}

var executable = os.Executable

// inHiveUnit reports whether this daemon runs in the Hive's systemd unit.
var inHiveUnit = setup.InUnit

// restart replaces this daemon with the (updated) binary on disk, and marks
// daemon.json so the control center waits for the new one.
func (s *server) restart() {
	exe, err := executable()
	if err != nil {
		return
	}
	exe = onDisk(exe)
	ws := s.a.C.WS
	env := append(os.Environ(), "HIVE_HOME="+ws.Root)
	if _, err := proc.StartDetached([]string{exe, "daemon", "--wait-for", strconv.Itoa(os.Getpid())}, env, ws.Root, "", filepath.Join(ws.Hive(), "daemon.log")); err == nil {
		info := s.info
		info.Restarting = true
		raw, _ := json.Marshal(info)
		core.WriteFileAtomic(ws.DaemonFile(), raw, 0o600)
		s.cancel()
	}
}

// onDisk is the path the new binary sits at: an update renames the running
// file to <name>.old, and Linux reports the running file by its new name.
func onDisk(exe string) string { return strings.TrimSuffix(exe, ".old") }

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(path string, fn func(r *http.Request) (any, error)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			tok, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !bearer || tok != s.info.Token {
				name, known, allowed := s.a.Runner.Allowed(tok, path)
				if !bearer || !allowed {
					code := http.StatusUnauthorized
					if known && bearer {
						code = http.StatusForbidden // a module calling outside its hooks
					}
					http.Error(w, `{"error":"unauthorized"}`, code)
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), moduleKey{}, name))
			}
			out, err := fn(r)
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(out)
		})
	}
	a := s.a
	h("/api/ping", func(*http.Request) (any, error) { return map[string]string{"version": core.Version}, nil })
	h("/api/state", func(*http.Request) (any, error) { return s.state(), nil })
	h("/api/chat", func(r *http.Request) (any, error) {
		if r.Method == http.MethodPost {
			var b struct{ Chat, Thread, Text string }
			if err := decode(r, &b); err != nil {
				return nil, err
			}
			if b.Chat == "" {
				b.Chat = "lead"
			}
			if err := s.reaches(r, s.projectOf("chat", b.Chat)); err != nil {
				return nil, err
			}
			return a.Chat.Post(b.Chat, b.Thread, moduleOf(r), b.Text)
		}
		id := r.URL.Query().Get("id")
		if err := s.reaches(r, s.projectOf("chat", id)); err != nil {
			return nil, err
		}
		return a.Chat.Messages(id), nil
	})
	h("/api/chat/read", func(r *http.Request) (any, error) {
		var b struct{ Chat string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		a.Chat.MarkRead(b.Chat)
		return "ok", nil
	})
	h("/api/chat/agent", func(r *http.Request) (any, error) {
		var b struct{ Run string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return a.Chat.EnsureAgent(b.Run)
	})
	h("/api/run", func(r *http.Request) (any, error) {
		run, ok := a.C.Runs.Get(r.URL.Query().Get("id"))
		if !ok {
			return nil, core.ErrNotFound
		}
		d := api.RunDetail{Run: run}
		for t := 1; t <= run.Turn; t++ {
			d.Steps = append(d.Steps, api.StepsFrom(t, a.C.Runs.ReadTurn(run, t).Steps)...)
		}
		if len(d.Steps) > 400 {
			d.Steps = d.Steps[len(d.Steps)-400:]
		}
		return d, nil
	})
	h("/api/run/stop", func(r *http.Request) (any, error) {
		var b struct{ ID, Reason string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if err := s.reaches(r, s.projectOf("run", b.ID)); err != nil {
			return nil, err
		}
		a.C.Runs.Stop(b.ID, orStr(b.Reason, "stopped by the person"))
		return "ok", nil
	})
	h("/api/task", func(r *http.Request) (any, error) {
		t, ok := a.Work.Get(r.URL.Query().Get("id"))
		if !ok {
			return nil, core.ErrNotFound
		}
		return t, nil
	})
	h("/api/task/stop", func(r *http.Request) (any, error) {
		var b struct{ ID, Reason string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if err := s.reaches(r, s.projectOf("task", b.ID)); err != nil {
			return nil, err
		}
		return "ok", a.Work.Stop(b.ID, orStr(b.Reason, "stopped by the person"))
	})
	h("/api/task/retry", func(r *http.Request) (any, error) {
		var b struct{ ID string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return "ok", a.Work.Retry(b.ID)
	})
	h("/api/ask", func(r *http.Request) (any, error) {
		var b work.Brief
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if mod := moduleOf(r); mod != "" {
			if err := s.reaches(r, b.Project); err != nil {
				return nil, err
			}
			b.From = strings.TrimSuffix(mod+"/"+b.From, "/") // the module, and where the ask came from
		} else {
			b.From = "person"
		}
		return a.Work.File(b)
	})
	h("/api/module/report", func(r *http.Request) (any, error) {
		var b map[string]any
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if b == nil {
			return nil, errors.New("a report is a JSON object")
		}
		b["module"] = moduleOf(r)
		a.C.J.MustAppend("module.reported", b)
		return "ok", nil
	})
	h("/api/answer", func(r *http.Request) (any, error) {
		var b struct{ ID, Answer, Note string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return a.C.Person.Answer(b.ID, b.Answer, b.Note)
	})
	h("/api/improve", func(r *http.Request) (any, error) { return a.Improve.StartSweep(true) })
	h("/api/job", func(r *http.Request) (any, error) {
		var b struct{ Name string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return "started", a.System.RunNow(b.Name)
	})
	h("/api/config", func(r *http.Request) (any, error) {
		if r.Method != http.MethodPost {
			// What config.json got wrong rides beside every setting that did read.
			c, err := a.C.WS.ReadConfig()
			out := struct {
				core.Config
				LoadError string `json:"load_error,omitempty"`
			}{Config: c}
			if err != nil {
				out.LoadError = err.Error()
			}
			return out, nil
		}
		var c core.Config
		if err := decode(r, &c); err != nil {
			return nil, err
		}
		cur := a.C.WS.LoadConfig()
		if c.Sandbox == "bare" && cur.BareAccepted == "" {
			return nil, errors.New("bare runs need the warning read first (`" + core.Cmd + " doctor --bare`)")
		}
		c.BareAccepted = cur.BareAccepted
		if err := c.Check(); err != nil {
			return nil, err
		}
		if err := a.C.WS.SaveConfig(c); err != nil {
			return nil, err
		}
		a.C.ResetSandbox()
		return c, nil
	})
	h("/api/module/access", func(r *http.Request) (any, error) {
		var b struct {
			Module, Project string
			Allow           bool
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return "ok", a.Mods.SetAccess(b.Module, b.Project, b.Allow)
	})
	h("/api/module/review", func(r *http.Request) (any, error) {
		var b struct{ Module string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return "ok", a.Runner.Retry(b.Module)
	})
	h("/api/project", func(r *http.Request) (any, error) {
		var b projects.Spec
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		p, _, err := a.Projects.Create(b)
		return p, err
	})
	h("/api/events", func(r *http.Request) (any, error) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		evs := a.C.J.Since(after)
		if moduleOf(r) != "" {
			// A module reads on from its cursor, so it gets the first events
			// after it, and only those of projects that granted it.
			var mine []core.Event
			for _, e := range evs {
				if len(mine) == 2000 {
					break
				}
				if p := s.eventProject(e); (p == "" && notice(e.Kind)) || s.reaches(r, p) == nil {
					mine = append(mine, e)
				}
			}
			return mine, nil
		}
		if len(evs) > 2000 {
			evs = evs[len(evs)-2000:]
		}
		return evs, nil
	})
	h("/api/update", func(*http.Request) (any, error) { return a.Release.Update() })
	h("/api/shutdown", func(*http.Request) (any, error) {
		go func() { time.Sleep(100 * time.Millisecond); s.cancel() }()
		return "stopping", nil
	})
	return mux
}

type moduleKey struct{}

// reaches refuses a module's request that touches a project the person has not
// granted it; "" is the whole workspace, which only a grant of "*" reaches.
func (s *server) reaches(r *http.Request, project string) error {
	if mod := moduleOf(r); mod != "" && !s.a.Mods.Reaches(mod, project) {
		return fmt.Errorf("module %s is not granted %s", mod, orStr(project, "every project"))
	}
	return nil
}

// projectOf names the project a chat, run or task belongs to.
func (s *server) projectOf(kind, id string) string {
	switch kind {
	case "chat":
		if c, ok := s.a.Chat.Get(id); ok && c.Project != "" {
			return c.Project
		}
		if run, ok := strings.CutPrefix(id, "agent:"); ok {
			return s.projectOf("run", run)
		}
	case "run":
		if run, ok := s.a.C.Runs.Get(id); ok {
			return run.Project
		}
	case "task":
		if t, ok := s.a.Work.Get(id); ok {
			return t.Project
		}
	}
	return ""
}

// eventProject names the project an event is about, from its own project or
// the run, task or chat it names; "" is the whole workspace.
func (s *server) eventProject(e core.Event) string {
	var d struct{ Project, Run, Task, Chat, ID string }
	e.Decode(&d)
	p := d.Project
	for _, ref := range [][2]string{{"run", d.Run}, {"task", d.Task}, {"chat", d.Chat}, {"run", d.ID}, {"task", d.ID}} {
		if p == "" && ref[1] != "" {
			p = s.projectOf(ref[0], ref[1])
		}
	}
	return p
}

// notice says an event is a workspace notice every module may read; any other
// event with no project (the lead's chat, the person's asks) needs a grant of "*".
func notice(kind string) bool {
	switch kind {
	case "hive.idle", "hive.started", "hive.stopped", "alert.raised":
		return true
	}
	return strings.HasPrefix(kind, "module.")
}

// moduleOf names the module that made a request, or "" for the person.
func moduleOf(r *http.Request) string {
	name, _ := r.Context().Value(moduleKey{}).(string)
	return name
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func decode(r *http.Request, v any) error {
	if r.Method != http.MethodPost {
		return errors.New("POST required")
	}
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *server) state() api.State {
	a := s.a
	cfg, cfgErr := a.C.WS.ReadConfig()
	st := api.State{Version: core.Version, Seq: a.C.J.Seq(), Workspace: a.C.WS.Root, Config: cfg}
	if sb, err := a.C.Sandbox(); err != nil {
		st.SandboxErr = err.Error()
	} else {
		st.Sandbox = sb.Name()
	}
	for _, p := range a.C.Projects.List() {
		pv := api.ProjectView{Project: p}
		pv.Goals = readGoals(a.C.WS.Project(p.Name))
		n := 0
		for _, t := range a.Work.List(func(t work.Task) bool { return t.Project == p.Name }) {
			if !t.Terminal() || n < 10 {
				t.Log = tail(t.Log, 12)
				pv.Tasks = append(pv.Tasks, t)
				if t.Terminal() {
					n++
				}
			}
		}
		sc := a.Improve.Scores(p.Name)
		if len(sc) > 20 {
			sc = sc[len(sc)-20:]
		}
		pv.Scores = sc
		if b, ok := a.Improve.Best(p.Name); ok {
			pv.Best = &b
		}
		st.Projects = append(st.Projects, pv)
	}
	recent := 0
	for _, r := range a.C.Runs.List(nil) {
		if !r.Terminal() || recent < 30 {
			r.System, r.Brief = "", core.Clip(r.Brief, 400)
			st.Runs = append(st.Runs, r)
			if r.Terminal() {
				recent++
			}
		}
	}
	st.Plans = a.C.Runs.Plans()
	sw := a.Improve.Sweeps()
	if len(sw) > 10 {
		sw = sw[:10]
	}
	st.Sweeps = sw
	st.StepBacks = a.Improve.StepBacks("")
	st.Asks = a.C.Person.Open()
	s.mu.Lock()
	st.Alerts = append([]api.Alert{}, s.alerts...)
	s.mu.Unlock()
	if cfgErr != nil { // live, so it goes once the file is fixed
		st.Alerts = append(st.Alerts, api.Alert{At: time.Now(), Kind: "settings", Text: cfgErr.Error()})
	}
	st.Jobs = a.System.Jobs()
	st.Wakeups = a.System.Wakeups()
	for _, m := range a.Mods.List() {
		st.Modules = append(st.Modules, api.ModuleView{Name: m.Name, Description: m.Description, Source: m.Source.Source, Release: m.Source.Release, Projects: m.Source.Projects,
			Hooks: m.Hooks, Status: a.Runner.Status(filepath.Base(m.Dir))})
	}
	for _, c := range a.Chat.Chats() {
		cv := api.ChatView{Chat: c, Unread: a.Chat.Unread(c.ID)}
		if c.Lead != "" {
			if r, ok := a.C.Runs.Get(c.Lead); ok {
				cv.LeadStatus = r.Status
				if r.Reason != "" {
					cv.LeadStatus += ": " + r.Reason
				}
			}
		}
		st.Chats = append(st.Chats, cv)
	}
	req := a.Risk.List()
	if len(req) > 20 {
		req = req[:20]
	}
	st.Requests = req
	return st
}

func tail[T any](s []T, n int) []T {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func readGoals(dir string) string {
	raw, _ := os.ReadFile(filepath.Join(dir, ".hive", "goals.md"))
	return string(raw)
}
