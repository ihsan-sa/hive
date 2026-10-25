package runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/modules"
)

// opRunner is a workspace with the default modules and a module the person
// put in .modules/ by hand (no .git, so it runs as it is), with manifest.
func opRunner(t *testing.T, harness, manifest string) (*Runner, string) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = harness
	ws.SaveConfig(cfg)
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	mods := modules.New(ws, c.J)
	if err := mods.InstallDefaults(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ws.Modules(), "look")
	os.MkdirAll(dir, 0o755)
	if manifest != "" {
		os.WriteFile(filepath.Join(dir, "module.json"), []byte(manifest), 0o644)
	}
	return &Runner{C: c, Mods: mods, Agents: &agents.Agents{C: c, Mod: mods}}, dir
}

func call(t *testing.T, r *Runner, run core.Run, op, args string) (string, error) {
	t.Helper()
	out, err := r.C.Runs.Ops().Call(run, op, json.RawMessage(args))
	s, _ := out.(string)
	return s, err
}

// A module's op is a program run once per call: the call's arguments on
// stdin, the knowledge base and what the caller can't see in its
// environment, no credential. One that fails falls back to the built-in op
// it replaced, saying why, and the built-in comes back when the module goes.
func TestModuleOpRunsItsProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the op is a shell script")
	}
	t.Setenv("GH_TOKEN", "secret-token")
	r, dir := opRunner(t, "fake", `{"name":"look","ops":{"lookup":{"help":"{\"q\"}","exec":["sh","-c","cat; echo; echo kb=$HIVE_KB; echo hide=$HIVE_KB_HIDE; echo role=$HIVE_CALLER_ROLE; echo token=$GH_TOKEN"]}}}`)
	r.C.Runs.Ops().Register(core.Op{Name: "lookup", Fn: func(core.Run, json.RawMessage) (any, error) { return "built-in answer", nil }})
	r.Tick()
	worker := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker", Project: "p"}}
	out, err := call(t, r, worker, "lookup", `{"q":"zebra"}`)
	if err != nil || !strings.Contains(out, `{"q":"zebra"}`) || !strings.Contains(out, "kb="+r.C.WS.KB()) || !strings.Contains(out, "role=worker") {
		t.Fatalf("op answer %q, %v", out, err)
	}
	if strings.Contains(out, "secret-token") {
		t.Fatal("the op's program got the forge token")
	}
	if !slices.ContainsFunc(r.C.Runs.Ops().List(), func(o core.Op) bool { return o.Name == "lookup" && o.Help == `{"q"}` }) {
		t.Fatal("the module's help isn't the op's")
	}

	os.WriteFile(filepath.Join(dir, "module.json"), []byte(`{"name":"look","ops":{"lookup":{"exec":["sh","-c","echo broken >&2; exit 3"]}}}`), 0o644)
	r.Tick()
	if out, err := call(t, r, worker, "lookup", `{}`); err != nil || !strings.Contains(out, "its program failed") || !strings.Contains(out, "broken") || !strings.HasSuffix(out, "built-in answer") {
		t.Fatalf("a failing program should fall back to the built-in: %q, %v", out, err)
	}

	os.RemoveAll(dir)
	r.Tick()
	if out, _ := call(t, r, worker, "lookup", `{}`); out != "built-in answer" {
		t.Fatalf("with the module gone the built-in answers: %q", out)
	}
	if r.ModuleOp("lookup") {
		t.Fatal("no module answers lookup now")
	}
}

// A module from git answers no op until its review has read it, as its
// program doesn't start until then.
func TestModuleOpWaitsForItsReview(t *testing.T) {
	r, dir := opRunner(t, "fake", `{"name":"look","ops":{"lookup":{"exec":["true"]}}}`)
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "v1"}} {
		if _, err := core.Git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	r.Tick()
	_, err := call(t, r, core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker"}}, "lookup", `{}`)
	if err == nil || !strings.Contains(err.Error(), "waiting for its code review") {
		t.Fatalf("an unread module's op answered: %v", err)
	}
}

// An op's agent: asked with its argument, it starts a run of the module's
// role prompt, with read tools and a shell, a structured answer, its budget
// and a brief that names the question, on the module's small model when the
// harness is Claude Code and the person routes nothing else. It can't start
// another, and its answer shows as it digs.
func TestModuleOpAgent(t *testing.T) {
	manifest := `{"name":"look","ops":{"lookup":{"exec":["true"],"agent":{"arg":"deep","role":"look-researcher","prompt":"researcher.md","model":"haiku","budget_usd":0.3,"minutes":5}}}}`
	for _, harness := range []string{"fake", "claude"} {
		r, dir := opRunner(t, harness, manifest)
		os.WriteFile(filepath.Join(dir, "researcher.md"), []byte("# Your role: look researcher\n"), 0o644)
		r.Tick()
		lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
		id, err := r.StartAgent(lead, "lookup", "why was #111 reverted")
		if err != nil {
			t.Fatal(err)
		}
		run, ok := r.C.Runs.Get(id)
		if !ok || run.Role != "look-researcher" || run.Schema != AnswerSchema || !slices.Contains(run.Tools, "Bash") || run.ReadOnly ||
			!strings.Contains(run.System, "Your role: look researcher") || !strings.Contains(run.Brief, "why was #111 reverted") || !strings.Contains(run.Brief, "5 minutes") || run.Budget != 0.3 {
			t.Fatalf("%s: agent run %+v", harness, run.RunSpec)
		}
		if want := map[string]string{"fake": run.Model, "claude": "haiku"}[harness]; run.Model != want || harness == "fake" && run.Model == "haiku" {
			t.Fatalf("%s: the agent runs on %q", harness, run.Model)
		}
		if text, done := r.Answer(id); done || !strings.Contains(text, "still digging") {
			t.Fatalf("a running agent's answer: %q, %v", text, done)
		}
		if _, err := r.StartAgent(run, "lookup", "again"); err == nil {
			t.Fatal("the agent started another")
		}
	}
}

// The kb module's search is its own program: a call runs it with `go run`
// and returns each file once with why it matched.
func TestKBModuleSearch(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go to run the kb module's program")
	}
	r, _ := opRunner(t, "fake", "")
	kb := r.C.WS.KB()
	os.MkdirAll(filepath.Join(kb, "record", "task"), 0o755)
	os.WriteFile(filepath.Join(kb, "record", "task", "261024-015726-45d2d27ed7.md"), []byte("# task 261024-015726-45d2d27ed7\n\n### 2026-10-24 01:58 · task.saved · seq 28\n- pr.number: 111\n- stage: merged\n"), 0o644)
	r.Tick()
	if !r.ModuleOp("search") {
		t.Fatal("the kb module answers no search")
	}
	out, err := call(t, r, core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker"}}, "search", `{"query":"#111"}`)
	if err != nil || !strings.HasPrefix(out, "1. record/task/261024-015726-45d2d27ed7.md") || !strings.Contains(out, "why: is about #111") {
		t.Fatalf("kb search: %q, %v", out, err)
	}
}
