package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The risk module wraps core's "message" op when it is built, so a warden
// asking an agent only waits if the app builds risk after core.
func TestWardenMessagingTheRequesterWaitsInTheApp(t *testing.T) {
	a, warden := appWithWarden(t, true)
	defer a.C.Close()
	raw, _ := json.Marshal(map[string]string{"to": "parent", "text": "which version?"})
	if _, err := a.C.Runs.Ops().Call(warden, "message", raw); err != nil {
		t.Fatal(err)
	}
	if w, _ := a.C.Runs.Get(warden.ID); w.Waiting == "" {
		t.Fatalf("a warden's message in the app's wiring left it not waiting: %+v", w)
	}
}

// A warden with no parent has no one to wait on: its message to "parent" fails
// loudly and it is not left waiting.
func TestWardenWithNoParentMessagingParentFails(t *testing.T) {
	a, warden := appWithWarden(t, false)
	defer a.C.Close()
	raw, _ := json.Marshal(map[string]string{"to": "parent", "text": "which version?"})
	if _, err := a.C.Runs.Ops().Call(warden, "message", raw); err == nil {
		t.Fatal("a message to the parent of a warden with none succeeded")
	}
	if w, _ := a.C.Runs.Get(warden.ID); w.Waiting != "" {
		t.Fatalf("a warden whose message failed is waiting: %+v", w)
	}
}

func appWithWarden(t *testing.T, withParent bool) (*App, core.Run) {
	t.Helper()
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax = 0, 0, 0
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a, err := New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := core.RunSpec{Harness: "fake", Checkout: t.TempDir()}
	spec.Role = "worker"
	worker, err := a.C.Runs.Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Role = "warden"
	if withParent {
		spec.Parent = worker.ID
	}
	warden, err := a.C.Runs.Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	return a, warden
}

// An isolated project's records from before they had their own folders move
// there on start, and no agent outside it reads its status or files work in it.
func TestIsolatedProjectRecordsMoveAndStayOutOfReach(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	c, err := core.Open(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Projects.Add(core.Project{Name: "open", Repo: "me/open"})
	c.Projects.Add(core.Project{Name: "secret", Repo: "me/secret", Isolated: true})
	c.J.MustAppend("risk.saved", map[string]string{"id": "r1", "project": "secret"})
	c.Person.Ask(core.Ask{Kind: "question", Title: "secret plan", Project: "secret"})
	c.Person.Ask(core.Ask{Kind: "question", Title: "spend notice"})
	c.Close()
	os.MkdirAll(filepath.Join(ws.Hive(), "risk", "r1"), 0o755)
	os.MkdirAll(ws.KB(), 0o755)
	// Built at run time: the Hive's own source, this file among it, is filed in the KB too.
	plan := strings.ToUpper("the secret plan")
	os.WriteFile(filepath.Join(ws.KB(), "plan.md"), []byte("---\ntitle: plan\nproject: secret\n---\n\n"+plan+"\n"), 0o644)
	os.WriteFile(filepath.Join(ws.KB(), "open.md"), []byte("---\ntitle: open\nproject: open\n---\n\nopen\n"), 0o644)

	a, err := New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !core.Exists(filepath.Join(ws.Hive(), "risk", "secret", "r1")) {
		t.Error("an isolated project's old warden folder stayed in the shared one")
	}
	if !core.Exists(filepath.Join(ws.KB(), "isolated", "secret", "plan.md")) || !core.Exists(filepath.Join(ws.KB(), "open.md")) {
		t.Error("the notes didn't move to the right folders")
	}
	if out, _ := core.Git(ws.KB(), "ls-files"); strings.Contains(out, "plan.md") {
		t.Error("git still tracks an isolated project's note")
	}
	if out, _ := core.Git(ws.KB(), "cat-file", "--batch-all-objects", "--batch"); strings.Contains(out, plan) || !strings.Contains(out, "open") {
		t.Error("the knowledge base's git objects still hold an isolated project's note")
	}
	ops := a.C.Runs.Ops()
	sweep := core.Run{RunSpec: core.RunSpec{ID: "sweep", Role: "improver", Project: "open"}}
	st, _ := ops.Call(sweep, "status", nil)
	if b, _ := json.Marshal(st); strings.Contains(string(b), "secret") || !strings.Contains(string(b), "open") {
		t.Errorf("status shows a normal run %s", b)
	}
	own, _ := ops.Call(core.Run{RunSpec: core.RunSpec{ID: "x", Project: "secret", Isolated: true}}, "status", nil)
	if b, _ := json.Marshal(own); !strings.Contains(string(b), "secret plan") || !strings.Contains(string(b), "spend notice") {
		t.Errorf("status hides an isolated project from its own run: %s", b)
	}
	if _, err := ops.Call(sweep, "brief", json.RawMessage(`{"title":"t","done":"d","project":"secret"}`)); err == nil {
		t.Error("a normal run filed work in an isolated project")
	}
}
