package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	cfg.MemFloorMB = 0
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

// create_project makes a private repository unless asked otherwise; a
// public one in public_owners (Hive-RSI-Lab) is made and the person told
// after; a public one anywhere else waits for the person's yes, and is
// made only then. An existing repository keeps its visibility.
func TestCreateProjectVisibility(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = "fake"
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	forge := &core.Local{Dir: t.TempDir()}
	a, err := New(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	defer a.C.Close()
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	create := func(args string) map[string]any {
		t.Helper()
		out, err := a.C.Runs.Ops().Call(lead, "create_project", json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		raw, _ := json.Marshal(out)
		var m map[string]any
		json.Unmarshal(raw, &m)
		return m
	}
	visibility := func(name string) string {
		p, _ := a.C.Projects.Get(name)
		return p.Visibility
	}

	create(`{"name":"plain","description":"a private one"}`)
	if forge.IsPublic("local/plain") || visibility("plain") != "private" {
		t.Fatalf("the default isn't private: public %v, recorded %q", forge.IsPublic("local/plain"), visibility("plain"))
	}

	create(`{"repo":"Hive-RSI-Lab/hive-mod","description":"a module","visibility":"public"}`)
	if !forge.IsPublic("Hive-RSI-Lab/hive-mod") || visibility("hive-mod") != "public" {
		t.Fatal("a public repository in Hive-RSI-Lab wasn't made public")
	}
	if told := a.C.Person.Told(10); len(told) == 0 || !strings.Contains(told[0].Title, "Hive-RSI-Lab/hive-mod") {
		t.Fatalf("the person wasn't told of the public repository: %+v", told)
	}
	st, _ := a.C.Runs.Ops().Call(lead, "status", nil)
	if raw, _ := json.Marshal(st); !strings.Contains(string(raw), `"visibility":"public"`) {
		t.Fatalf("status doesn't show the visibility: %s", raw)
	}

	out := create(`{"repo":"someone/elsewhere","visibility":"public"}`)
	id, _ := out["waiting_on_person"].(string)
	if id == "" || core.Exists(forge.RepoPath("someone/elsewhere")) {
		t.Fatalf("a public repository outside the org didn't wait for the person: %v", out)
	}
	if _, ok := a.C.Projects.Get("elsewhere"); ok {
		t.Fatal("the project was made before the person's yes")
	}
	if _, err := a.C.Person.Answer(id, "yes", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200 && visibility("elsewhere") == ""; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if !forge.IsPublic("someone/elsewhere") || visibility("elsewhere") != "public" {
		t.Fatal("the person's yes didn't make the public repository")
	}
	a.C.Person.Handled()

	// An existing private repository asked to be public stays as it is.
	if _, err := forge.EnsureRepo("Hive-RSI-Lab/old", "", false); err != nil {
		t.Fatal(err)
	}
	out = create(`{"repo":"Hive-RSI-Lab/old","visibility":"public"}`)
	if forge.IsPublic("Hive-RSI-Lab/old") || out["note"] == nil {
		t.Fatalf("an existing repository's visibility changed, or no note said it didn't: %v", out)
	}

	if _, err := a.C.Runs.Ops().Call(lead, "create_project", json.RawMessage(`{"name":"x","visibility":"internal"}`)); err == nil {
		t.Fatal("an unknown visibility was taken")
	}

	// A walled project with a public repository asks, even in Hive-RSI-Lab.
	for _, sharing := range []string{"private", "isolated"} {
		out = create(`{"repo":"Hive-RSI-Lab/walled-` + sharing + `","sharing":"` + sharing + `","visibility":"public"}`)
		if out["waiting_on_person"] == nil || core.Exists(forge.RepoPath("Hive-RSI-Lab/walled-"+sharing)) {
			t.Fatalf("a public repository for a %s project didn't wait for the person: %v", sharing, out)
		}
	}

	// A bare name is the forge login's own account: public without asking
	// only once the person lists that login.
	if out = create(`{"name":"mine","visibility":"public"}`); out["waiting_on_person"] == nil {
		t.Fatalf("a public repository in the login's account didn't ask: %v", out)
	}
	cfg = ws.LoadConfig()
	cfg.PublicOwners = append(cfg.PublicOwners, "local")
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if out = create(`{"name":"mine2","visibility":"public"}`); out["waiting_on_person"] != nil || !forge.IsPublic("local/mine2") {
		t.Fatalf("a bare name in a listed login's account wasn't made public: %v", out)
	}
}

// The person's no to a public repository makes nothing, and the run that
// asked is told.
func TestCreateProjectPublicNo(t *testing.T) {
	// Not t.TempDir: the lead's fake turn may still be writing its folder as
	// the test ends, and TempDir's single RemoveAll then fails the test.
	root, err := os.MkdirTemp("", "create-public-no")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 50 && os.RemoveAll(root) != nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	})
	ws := core.Workspace{Root: root}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = "fake"
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	forge := &core.Local{Dir: t.TempDir()}
	a, err := New(ws, forge)
	if err != nil {
		t.Fatal(err)
	}
	defer a.C.Close()
	lead, err := a.C.Runs.Start(core.RunSpec{Role: "lead", Harness: "fake", Checkout: filepath.Join(root, "lead"), Persistent: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.C.Runs.Ops().Call(lead, "create_project", json.RawMessage(`{"repo":"someone/nope","visibility":"public"}`))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := out.(map[string]any)["waiting_on_person"].(string)
	if id == "" {
		t.Fatalf("didn't wait for the person: %v", out)
	}
	if _, err := a.C.Person.Answer(id, "no", ""); err != nil {
		t.Fatal(err)
	}
	told := func() bool {
		found := false
		a.C.J.Replay(func(e core.Event) {
			if e.Kind == "run.queued" && strings.Contains(string(e.Data), lead.ID) && strings.Contains(string(e.Data), "said no") {
				found = true
			}
		})
		return found
	}
	for i := 0; i < 200 && !told(); i++ {
		time.Sleep(25 * time.Millisecond)
	}
	if !told() {
		t.Fatal("the asking run wasn't told of the no")
	}
	// The no's handler may still be filing its events under the temp dir.
	a.C.Person.Handled()
	if _, ok := a.C.Projects.Get("nope"); ok || core.Exists(forge.RepoPath("someone/nope")) {
		t.Fatal("a no still made the project")
	}
}

// A merge on the project a module is installed from moves the module at
// once, not at the next hourly modules.update; a merge elsewhere leaves it.
func TestMergeOnAModulesProjectUpdatesIt(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	a, err := New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	src := filepath.Join(t.TempDir(), "o", "ledger")
	os.MkdirAll(src, 0o755)
	git := func(args ...string) {
		t.Helper()
		if out, err := core.Git(src, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(src, "ledger.md"), []byte("old\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "first")
	if _, err := a.Mods.Install(src, "ledger"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []core.Project{{Name: "ledger", Repo: "o/ledger", Branch: "main"}, {Name: "other", Repo: "o/other", Branch: "main"}} {
		if _, err := a.C.Projects.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(src, "ledger.md"), []byte("new\n"), 0o644)
	git("commit", "-qam", "fix")
	read := func() string {
		m, _ := a.Mods.Get("ledger")
		got, _ := os.ReadFile(filepath.Join(m.Dir, "ledger.md"))
		return string(got)
	}
	a.C.J.MustAppend("effect.merged", map[string]any{"project": "other", "pr": 1, "sha": "x", "merge_sha": "y"})
	a.updating.Wait()
	if got := read(); got != "old\n" {
		t.Fatalf("a merge on another project updated the module: %q", got)
	}
	a.C.J.MustAppend("effect.merged", map[string]any{"project": "ledger", "pr": 2, "sha": "x", "merge_sha": "y"})
	a.updating.Wait()
	if got := read(); got != "new\n" {
		t.Fatalf("a merge on the module's own project left it at %q", got)
	}
}
