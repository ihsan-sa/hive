package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// A module's ops are the one place the daemon runs a module's code on a
// call, not the module calling the API: an op is a command, started once per
// call from the module's folder with the call's arguments on stdin, that
// prints its answer and exits. Nothing listens and nothing is kept running,
// so a slow op holds up only the run that called it, as any op does. The
// program runs as a module's program does (its review cleared, the same
// scrubbed environment and HOME) and gets no token: what it reads comes in
// its environment, the knowledge base's folder and the folders the caller
// can't see. An op may also name an agent that answers a question instead:
// a short run of the module's own role prompt on a small model, held to a
// budget and a time limit, whose structured answer comes back as the reply.

// opTimeout bounds one call of an op's program; a first `go run` builds it.
const opTimeout = 2 * time.Minute

// AnswerSchema is an op agent's structured answer.
const AnswerSchema = `{"type":"object","properties":{"answer":{"type":"string"},"sources":{"type":"array","items":{"type":"string"}}},"required":["answer","sources"]}`

// modOp is an op a module answers, as registered.
type modOp struct {
	module string // the module's folder name
	spec   modules.Op
	before *core.Op // what answered this op before the module did, its fallback
}

// syncOps registers the ops of every installed module, keeping what each
// replaces as its fallback, and gives an op back to it when its module goes.
// It runs under r.mu.
func (r *Runner) syncOps(mods []modules.Module) {
	if r.ops == nil {
		r.ops = map[string]*modOp{}
	}
	registry := r.C.Runs.Ops()
	seen := map[string]bool{}
	for _, m := range mods {
		folder := filepath.Base(m.Dir)
		for name, spec := range m.Ops {
			if len(spec.Exec) == 0 || seen[name] {
				continue
			}
			seen[name] = true
			// A modOp never changes once made, so a call can read one unlocked.
			cur := r.ops[name]
			o := &modOp{module: folder, spec: spec}
			if cur != nil {
				o.before = cur.before
			} else {
				for _, x := range registry.List() {
					if x.Name == name {
						x := x
						o.before = &x
					}
				}
			}
			r.ops[name] = o
			registry.Register(core.Op{Name: name, Help: spec.Help, Fn: func(run core.Run, args json.RawMessage) (any, error) {
				return r.Call(run, name, args)
			}})
		}
	}
	for name, o := range r.ops {
		if !seen[name] {
			delete(r.ops, name)
			if o.before != nil {
				registry.Register(*o.before)
			}
		}
	}
}

// ModuleOp says whether a module answers op.
func (r *Runner) ModuleOp(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ops[name] != nil
}

// Call answers op for run: by the module's agent when the call asks for it,
// else by its program; a program that can't run falls back to what answered
// the op before, saying why.
func (r *Runner) Call(run core.Run, name string, args json.RawMessage) (any, error) {
	r.mu.Lock()
	o := r.ops[name]
	var why string
	var ok bool
	if o != nil {
		why, ok = r.opsWhy[o.module]
	}
	r.mu.Unlock()
	if o == nil {
		return nil, fmt.Errorf("unknown op %q", name)
	}
	if !ok {
		why = "not read yet"
	}
	if why != "" {
		return r.fallback(o, run, args, "module "+o.module+" can't answer yet: "+why)
	}
	if a := o.spec.Agent; a != nil {
		var q map[string]any
		json.Unmarshal(args, &q)
		if s, _ := q[a.Arg].(string); strings.TrimSpace(s) != "" {
			id, err := r.StartAgent(run, name, s)
			if err != nil {
				return nil, err
			}
			return r.waitAgent(id, a), nil
		}
	}
	out, err := r.exec(o, run, args)
	if err != nil {
		return r.fallback(o, run, args, err.Error())
	}
	return out, nil
}

func (r *Runner) fallback(o *modOp, run core.Run, args json.RawMessage, why string) (any, error) {
	if o.before == nil {
		return nil, errors.New(why)
	}
	out, err := o.before.Fn(run, args)
	if err != nil {
		return nil, err
	}
	if s, isText := out.(string); isText {
		return "(" + core.Clip(why, 300) + "; the built-in answer follows)\n\n" + s, nil
	}
	return out, nil
}

// env is a module program's environment, as start gives it, with HOME its own.
func (r *Runner) env(name string) []string {
	home := filepath.Join(r.C.WS.Hive(), "modules", name)
	os.MkdirAll(home, 0o700)
	env := scrub(os.Environ())
	if os.Getenv("GOCACHE") == "" {
		// A program run with `go run` builds into the person's own Go cache,
		// as go would with their HOME, not into a fresh one under its HOME.
		if dir, err := os.UserCacheDir(); err == nil {
			env = append(env, "GOCACHE="+filepath.Join(dir, "go-build"))
		}
	}
	return append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "APPDATA="+filepath.Join(home, ".config"), "HIVE_MODULE="+name)
}

// exec runs the op's program once.
func (r *Runner) exec(o *modOp, run core.Run, args json.RawMessage) (string, error) {
	m, found := r.Mods.Get(o.module)
	if !found {
		return "", fmt.Errorf("module %s is gone", o.module)
	}
	argv := append([]string{}, o.spec.Exec...)
	if strings.HasPrefix(argv[0], ".") {
		argv[0] = filepath.Join(m.Dir, argv[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = m.Dir
	cmd.Env = append(r.env(o.module), "HIVE_KB="+r.C.WS.KB(), "HIVE_KB_HIDE="+strings.Join(r.hiddenFrom(run), "\n"),
		"HIVE_CALLER="+run.ID, "HIVE_CALLER_ROLE="+run.Role, "HIVE_CALLER_PROJECT="+run.Project)
	cmd.Stdin = bytes.NewReader(args)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{&out, 1 << 20}, &limited{&errb, 64 << 10}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("ran past %s", opTimeout)
		}
		return "", fmt.Errorf("its program failed (%v): %s", err, core.Clip(strings.TrimSpace(errb.String()), 400))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// hiddenFrom is the knowledge base's folders run's sandbox covers: other
// hidden projects' notes and, while any is hidden, the record. The person
// sees all of it.
func (r *Runner) hiddenFrom(run core.Run) []string {
	var hide []string
	if run.Role == "person" {
		return nil
	}
	for _, p := range r.C.Projects.List() {
		if p.Hidden() && p.Name != run.Project {
			hide = append(hide, "isolated/"+p.Name, "record")
		}
	}
	return hide
}

type limited struct {
	b *bytes.Buffer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// StartAgent starts op's agent on question for run and returns its id. It
// runs in a scratch folder with read tools and a shell, sees what run's
// project sees, and may not start another.
func (r *Runner) StartAgent(run core.Run, name, question string) (string, error) {
	r.mu.Lock()
	o := r.ops[name]
	var why string
	var ok bool
	if o != nil {
		why, ok = r.opsWhy[o.module]
	}
	r.mu.Unlock()
	if o == nil || o.spec.Agent == nil {
		return "", fmt.Errorf("op %q has no agent", name)
	}
	if !ok || why != "" {
		return "", fmt.Errorf("module %s can't answer yet: %s", o.module, why)
	}
	a := o.spec.Agent
	if run.Role == a.Role {
		return "", errors.New("an op's agent can't start another; search and read instead")
	}
	m, found := r.Mods.Get(o.module)
	if !found {
		return "", fmt.Errorf("module %s is gone", o.module)
	}
	prompt, err := os.ReadFile(filepath.Join(m.Dir, filepath.FromSlash(a.Prompt)))
	if err != nil {
		return "", err
	}
	id := a.Role + "-" + core.NewID()
	dir, err := r.Agents.Scratch(id)
	if err != nil {
		return "", err
	}
	minutes := max(a.Minutes, 1)
	brief := fmt.Sprintf("Question (from %s): %s\n\nYour budget: about %d minutes and $%.2f. Answer within it; an answer that names what it couldn't find beats none.", run.ID, question, minutes, a.BudgetUSD)
	s := r.Agents.Spec(id, a.Role, run.Project, dir, brief)
	s.System += "\n\n" + string(prompt)
	cfg := r.C.WS.LoadConfig()
	if len(cfg.Routed(a.Role, run.Project)) == 0 && a.Model != "" && (s.Harness == "" || s.Harness == "claude") {
		s.Model = a.Model // small and cheap, unless the person routes the role
		if len(s.Route) > 0 {
			s.Route[0].Model = a.Model
		}
	}
	s.Tools, s.Schema = []string{"Read", "Grep", "Glob", "Bash"}, AnswerSchema
	if h, err := harness.Get(s.Harness); err == nil && harness.Capped(h, a.BudgetUSD) == nil {
		s.Budget = a.BudgetUSD
	}
	s.Inputs = map[string]string{"question.md": question + "\n"}
	if _, err := r.C.Runs.Start(s); err != nil {
		return "", err
	}
	r.C.J.MustAppend("module.op", map[string]any{"module": o.module, "op": name, "agent": id, "caller": run.ID, "question": core.Clip(question, 500)})
	return id, nil
}

// Answer is an op agent's answer once it has one; done is false while it digs.
func (r *Runner) Answer(id string) (text string, done bool) {
	run, ok := r.C.Runs.Get(id)
	if !ok {
		return "no such search: " + id, true
	}
	if !run.Terminal() {
		return fmt.Sprintf("still digging (%s, $%.2f so far)", id, run.Cost), false
	}
	var a struct {
		Answer  string
		Sources []string
	}
	if json.Unmarshal(run.Structured, &a) != nil || strings.TrimSpace(a.Answer) == "" {
		why := run.Reason
		if why == "" {
			why = run.Status
		}
		res := strings.TrimSpace(run.Result)
		if res != "" {
			res = "\n\nWhat it said:\n" + core.Clip(res, 4000)
		}
		return fmt.Sprintf("The deep search %s ended without an answer (%s, $%.2f).%s", id, why, run.Cost, res), true
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(a.Answer))
	if len(a.Sources) > 0 {
		b.WriteString("\n\nSources:\n")
		for _, s := range a.Sources {
			b.WriteString("- " + s + "\n")
		}
	}
	fmt.Fprintf(&b, "\n(deep search %s, $%.2f)", id, run.Cost)
	return strings.TrimSpace(b.String()), true
}

// waitAgent waits for an agent's answer up to its time limit, then stops it.
func (r *Runner) waitAgent(id string, a *modules.OpAgent) string {
	limit := time.Duration(max(a.Minutes, 1)) * time.Minute
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(time.Second) {
		if text, done := r.Answer(id); done {
			return text
		}
	}
	r.C.Runs.Stop(id, fmt.Sprintf("the deep search ran past its %d minutes", max(a.Minutes, 1)))
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(time.Second) {
		if text, done := r.Answer(id); done {
			return text
		}
	}
	return fmt.Sprintf("The deep search %s ran past its %d minutes and was stopped.", id, max(a.Minutes, 1))
}
