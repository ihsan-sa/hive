package models_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/app"
	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/models"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/work"
)

func hiveWithModels(t *testing.T) (*app.App, core.Run) {
	t.Helper()
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax, cfg.LoadMax = 0, 0, 0, 0
	cfg.Models = map[string]core.OpenModel{"qwen": {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen2.5-coder:14b"}}
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(ws, &core.Local{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.C.Close() })
	a.Models.Probe = func(core.Workspace, string, core.OpenModel) error { return nil } // qwen answers
	if _, err := a.C.Projects.Add(core.Project{Name: "lab", Repo: "me/lab"}); err != nil {
		t.Fatal(err)
	}
	lead, err := a.C.Runs.Start(core.RunSpec{Role: "lead", Harness: "fake", Checkout: t.TempDir(), Persistent: true})
	if err != nil {
		t.Fatal(err)
	}
	return a, lead
}

func call(t *testing.T, a *app.App, r core.Run, op string, args any) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return a.C.Runs.Ops().Call(r, op, raw)
}

// The person routes a role by asking the lead: the lead's route op writes it
// into config.json and the journal, and new runs of that role go on it; a
// check seat's route waits for the person's yes.
func TestLeadRoutes(t *testing.T) {
	a, lead := hiveWithModels(t)
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "qwen,fake:opus"}); err != nil {
		t.Fatal(err)
	}
	cfg := a.C.WS.LoadConfig()
	if !slices.Equal(cfg.Routes["worker"], []string{"qwen", "fake:opus"}) {
		t.Fatalf("routes %v", cfg.Routes)
	}
	var routed []string
	a.C.J.Replay(func(e core.Event) {
		if e.Kind == "config.routed" {
			routed = append(routed, string(e.Data))
		}
	})
	if len(routed) != 1 || !strings.Contains(routed[0], `"by":"`+lead.ID) {
		t.Fatalf("journal: %q", routed)
	}
	// The route op saves config.json, but only the route: an agent can't
	// widen where public repositories go by riding along on it.
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "qwen,fake:opus", "public_owners": []string{"evil"}}); err != nil {
		t.Fatal(err)
	}
	if got := a.C.WS.LoadConfig().PublicOwners; !slices.Equal(got, cfg.PublicOwners) {
		t.Fatalf("a route op changed public_owners: %v", got)
	}
	spec := a.Agents.Spec("w1", "worker", "lab", t.TempDir(), "brief")
	if spec.Harness != "open" || spec.Model != "qwen" || len(spec.Route) != 2 || spec.Route[1] != (core.Pick{Harness: "fake", Model: "opus"}) {
		t.Fatalf("a new worker: %s:%s, route %v", spec.Harness, spec.Model, spec.Route)
	}

	for _, bad := range []map[string]any{
		{"role": "worker", "models": "nosuch:x"},
		{"role": "worker", "models": "open:gone"},
		{"role": "janitor", "models": "qwen"},
	} {
		if _, err := call(t, a, lead, "route", bad); err == nil {
			t.Errorf("route %v was taken", bad)
		}
	}
	worker, _ := a.C.Runs.Start(core.RunSpec{Role: "worker", Harness: "fake", Checkout: t.TempDir()})
	if _, err := call(t, a, worker, "route", map[string]any{"role": "worker", "models": "qwen"}); err == nil {
		t.Fatal("a worker routed the Hive")
	}

	res, err := call(t, a, lead, "route", map[string]any{"role": "reviewer", "models": []string{"qwen"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.C.WS.LoadConfig().Routes["reviewer"] != nil {
		t.Fatal("a check seat was routed without the person")
	}
	ask := res.(map[string]string)["ask"]
	if _, err := a.C.Person.Answer(ask, "yes", ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return slices.Equal(a.C.WS.LoadConfig().Routes["reviewer"], []string{"qwen"}) })

	// Clearing a route takes the role back to its model settings.
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": ""}); err != nil {
		t.Fatal(err)
	}
	if h, m := a.Agents.Model("worker", "lab"); h != "fake" || m != "opus" {
		t.Fatalf("a cleared route: %s:%s", h, m)
	}

	// A project's lead routes its own project only.
	plead, _ := a.C.Runs.Start(core.RunSpec{Role: "lead", Project: "lab", Harness: "fake", Checkout: t.TempDir(), Persistent: true})
	if _, err := call(t, a, plead, "route", map[string]any{"role": "worker", "models": "qwen"}); err != nil {
		t.Fatal(err)
	}
	cfg = a.C.WS.LoadConfig()
	if cfg.Routes["worker"] != nil || !slices.Equal(cfg.Projects["lab"].Routes["worker"], []string{"qwen"}) {
		t.Fatalf("a project lead's route: %v, %v", cfg.Routes, cfg.Projects)
	}
	if h, _ := a.Agents.Model("worker", "lab"); h != "open" {
		t.Fatal("the project's route isn't taken")
	}
}

// A trial runs one brief's worker on the model alone, with nowhere to fall
// back, and how it went is recorded once it ends.
func TestTrial(t *testing.T) {
	a, lead := hiveWithModels(t)
	if _, err := call(t, a, lead, "trial", map[string]any{"model": "qwen", "project": "lab"}); err == nil {
		t.Fatal("a trial with no title was filed")
	}
	res, err := call(t, a, lead, "trial", map[string]any{"model": "qwen", "project": "lab", "title": "add a README", "done": "a README says what lab is"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]string)["task"]
	tk, _ := a.Work.Get(id)
	if tk.Model != "qwen" || !strings.Contains(tk.Known, "trial of the model open:qwen") {
		t.Fatalf("task %+v", tk)
	}
	tk.Stage, tk.Reason = work.StageBlocked, "tests failed"
	a.C.J.MustAppend("task.saved", tk)
	a.Models.Tick()
	a.Models.Tick() // recorded once
	n := 0
	a.C.J.Replay(func(e core.Event) {
		if e.Kind == "model.trial" {
			n++
		}
	})
	view, _ := json.Marshal(a.Models.View(lead))
	if n != 1 || !strings.Contains(string(view), `"outcome":"blocked"`) || !strings.Contains(string(view), `"model":"qwen"`) {
		t.Fatalf("%d records; view %s", n, view)
	}
}

// A trial whose worker was ended for never calling finish records that the
// model doesn't finish, and routing stops sending it work until a later
// trial of it finishes.
func TestATrialThatDoesntFinishBenchesTheModel(t *testing.T) {
	a, lead := hiveWithModels(t)
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "qwen,fake:opus"}); err != nil {
		t.Fatal(err)
	}
	finished := false
	trial := func(reason string) {
		t.Helper()
		res, err := call(t, a, lead, "trial", map[string]any{"model": "qwen", "project": "lab", "title": "add a README"})
		if err != nil {
			t.Fatal(err)
		}
		tk, _ := a.Work.Get(res.(map[string]string)["task"])
		if finished {
			a.C.J.MustAppend("run.created", core.RunSpec{ID: "w-" + tk.ID, Role: "worker", Task: tk.ID})
			a.C.J.MustAppend("run.finish", map[string]any{"id": "w-" + tk.ID, "finish": core.Finish{Status: "blocked", Account: reason}})
		}
		tk.Stage, tk.Reason = work.StageBlocked, reason
		if reason == "" {
			tk.Stage = work.StageMerged
		}
		a.C.J.MustAppend("task.saved", tk)
		a.Models.Tick()
	}
	trial(core.NoFinish + ": it was nudged twice")
	view, _ := json.Marshal(a.Models.View(lead))
	if !strings.Contains(string(view), `"doesnt_finish":true`) {
		t.Fatalf("view %s", view)
	}
	if h, m := a.Agents.Model("worker", "lab"); h != "fake" || m != "opus" {
		t.Fatalf("a model that doesn't finish still gets work: %s:%s", h, m)
	}
	trial("")
	if h, _ := a.Agents.Model("worker", "lab"); h != "open" {
		t.Fatal("a model whose later trial finished is still benched")
	}
	// A worker that called finish wrote its own reason, so words of its
	// that read like the Hive's don't bench its model.
	finished = true
	trial(core.NoFinish + ": in my own words")
	if h, _ := a.Agents.Model("worker", "lab"); h != "open" {
		t.Fatal("a worker's own finish text benched its model")
	}
}

// A trial that stopped on the Hive's own trouble says nothing of the model:
// it is shown apart and doesn't bench it, and once the task comes back and
// ends, that end is recorded in its place.
func TestATrialStoppedOnTheHivesTroubleIsntCounted(t *testing.T) {
	a, lead := hiveWithModels(t)
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "qwen,fake:opus"}); err != nil {
		t.Fatal(err)
	}
	end := func(id, stage, reason string) {
		t.Helper()
		tk, _ := a.Work.Get(id)
		tk.Stage, tk.Reason = stage, reason
		a.C.J.MustAppend("task.saved", tk)
		a.Models.Tick()
	}
	file := func() string {
		t.Helper()
		res, err := call(t, a, lead, "trial", map[string]any{"model": "qwen", "project": "lab", "title": "add a README"})
		if err != nil {
			t.Fatal(err)
		}
		return res.(map[string]string)["task"]
	}
	first := file()
	end(first, work.StageBlocked, core.NoFinish+": it was nudged twice")
	second := file()
	end(second, work.StageStopped, "gave up after repeated errors: gh api .../check-runs: Resource not accessible by personal access token (HTTP 403)")
	view := a.Models.View(lead)
	if n := len(view["trials"].([]models.Trial)); n != 1 {
		t.Fatalf("%d trials counted; view %v", n, view)
	}
	if u := view["trials_not_counted"].([]models.Trial); len(u) != 1 || u[0].Task != second || u[0].Hive == "" {
		t.Fatalf("not counted: %+v", u)
	}
	if h, _ := a.Agents.Model("worker", "lab"); h != "fake" {
		t.Fatal("a trial stopped on the Hive's trouble cleared the model's bench")
	}
	// The Hive brings it back, and it merges: that is the model's result.
	end(second, work.StageMerged, "")
	view = a.Models.View(lead)
	if tr := view["trials"].([]models.Trial); len(tr) != 2 || len(view["trials_not_counted"].([]models.Trial)) != 0 {
		t.Fatalf("after it came back: %v", view)
	}
	if h, _ := a.Agents.Model("worker", "lab"); h != "open" {
		t.Fatal("the merged trial didn't clear the model's bench")
	}
	a.Models.Tick()
	n := 0
	a.C.J.Replay(func(e core.Event) {
		if e.Kind == "model.trial" {
			n++
		}
	})
	if n != 3 {
		t.Fatalf("%d records, want 3", n)
	}
}

// A trial recorded before the Hive's trouble was told apart is read as such.
func TestAnOldTrialStoppedOnTheHivesTroubleIsntCounted(t *testing.T) {
	a, lead := hiveWithModels(t)
	a.C.J.MustAppend("model.trial", models.Trial{Task: "x", Model: "qwen", Project: "lab", Outcome: work.StageStopped, Reason: "gave up after repeated errors: HTTP 403"})
	if v := a.Models.View(lead); len(v["trials"].([]models.Trial)) != 0 || len(v["trials_not_counted"].([]models.Trial)) != 1 {
		t.Fatalf("view %v", v)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// A route or trial onto a model that can't answer is refused with its answer
// and the fix, and nothing is routed; one that answers its probe is routed.
func TestRouteProbesTheModel(t *testing.T) {
	a, lead := hiveWithModels(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"message":"No endpoints found matching your data policy (Free model training violation (guardrail)). Configurable at https://openrouter.ai/workspaces/default/guardrails","code":404}}`))
	}))
	defer srv.Close()
	a.Models.Probe = models.Probe
	cfg := a.C.WS.LoadConfig()
	cfg.Models["router"] = core.OpenModel{Endpoint: srv.URL + "/api/v1", Model: "free/model:free"}
	if err := a.C.WS.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	_, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "fake:opus,router"})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "https://openrouter.ai/workspaces/default/guardrails") {
		t.Fatalf("the route onto a model that 404s: %v", err)
	}
	if r := a.C.WS.LoadConfig().Routes["worker"]; len(r) > 0 {
		t.Fatalf("routed anyway: %v", r)
	}
	if _, err := call(t, a, lead, "trial", map[string]any{"model": "router", "project": "lab", "title": "try it"}); err == nil {
		t.Fatal("a trial on a model that 404s was filed")
	}
	a.Models.Probe = func(core.Workspace, string, core.OpenModel) error { return nil }
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "fake:opus,router"}); err != nil {
		t.Fatal(err)
	}
	// A model already on the route, not known to fail, isn't probed again.
	a.Models.Probe = func(core.Workspace, string, core.OpenModel) error { t.Fatal("probed again"); return nil }
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "router,fake:opus"}); err != nil {
		t.Fatal(err)
	}
}

// A route changed in Settings goes on the record as the route op's does:
// by whom, in which project, and from what.
func TestSettingsRouteIsRecorded(t *testing.T) {
	a, _ := hiveWithModels(t)
	before := a.C.WS.LoadConfig()
	after := before
	after.Routes = map[string][]string{"worker": {"qwen"}}
	after.Projects = map[string]core.ProjectSettings{"lab": {Routes: map[string][]string{"reviewer": {"fake:opus"}}}}
	models.Record(a.C.J, before, after, "person (Settings)")
	models.Record(a.C.J, after, after, "person (Settings)") // nothing changed: nothing recorded
	var got []models.Route
	a.C.J.Replay(func(e core.Event) {
		var r models.Route
		if e.Kind == "config.routed" && e.Decode(&r) == nil {
			got = append(got, r)
		}
	})
	if len(got) != 2 || got[0].Role != "worker" || got[0].Project != "" || !slices.Equal(got[0].Models, []string{"qwen"}) ||
		got[1].Role != "reviewer" || got[1].Project != "lab" || got[1].By != "person (Settings)" || got[1].From != nil {
		t.Fatalf("recorded %+v", got)
	}
}

// A model whose workers on a project keep ending blocked stops being that
// project's first worker pick, and stays on its route to fall back to; a
// merge on it puts it back in front, and other projects keep their order.
func TestAModelThatKeepsEndingBlockedStopsBeingFirst(t *testing.T) {
	a, lead := hiveWithModels(t)
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "qwen,fake:opus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.C.Projects.Add(core.Project{Name: "other", Repo: "me/other"}); err != nil {
		t.Fatal(err)
	}
	n := 0
	ended := func(project, stage string) {
		t.Helper()
		n++
		tk, err := a.Work.File(work.Brief{Project: project, Title: fmt.Sprintf("task %d", n), From: lead.ID})
		if err != nil {
			t.Fatal(err)
		}
		a.C.J.MustAppend("run.created", core.RunSpec{ID: "w-" + tk.ID, Role: "worker", Task: tk.ID, Project: project, Harness: "open", Model: "qwen",
			Route: []core.Pick{{Harness: "open", Model: "qwen"}, {Harness: "open", Model: "other"}}})
		// It fell back to another model on the same harness: still qwen's.
		a.C.J.MustAppend("run.turn_started", map[string]any{"id": "w-" + tk.ID, "turn": 2, "model": "other", "harness": "open"})
		tk.Stage, tk.Updated = stage, time.Now().Add(time.Duration(n)*time.Second)
		a.C.J.MustAppend("task.saved", tk)
		a.Models.Tick()
	}
	first := func(project string) string {
		h, m := a.Agents.Model("worker", project)
		return h + ":" + m
	}
	ended("lab", work.StageBlocked)
	if got := first("lab"); got != "open:qwen" {
		t.Fatalf("one blocked task moved the pick: %s", got)
	}
	ended("lab", work.StageBlocked)
	if got := first("lab"); got != "fake:opus" {
		t.Fatalf("a model that keeps ending blocked is still first: %s", got)
	}
	if r := a.Agents.Route("worker", "lab"); len(r) != 2 || r[1].String() != "open:qwen" {
		t.Fatalf("the demoted pick left the route: %v", r)
	}
	if got := first("other"); got != "open:qwen" {
		t.Fatalf("another project's order moved: %s", got)
	}
	if h, _ := a.Agents.Model("reviewer", "lab"); h == "open" {
		t.Fatal("not a worker route")
	}
	view, _ := json.Marshal(a.Models.View(lead))
	if !strings.Contains(string(view), "open:qwen on lab goes after") {
		t.Fatalf("view %s", view)
	}
	// A restart routes as before it, without waiting for a tick.
	again := models.New(a.C, a.Work)
	if again.Demoted("lab", core.Pick{Harness: "open", Model: "qwen"}) == "" {
		t.Fatal("a restart forgot the demotion until its first tick")
	}
	ended("lab", work.StageMerged)
	if got := first("lab"); got != "open:qwen" {
		t.Fatalf("a merge didn't put the model back in front: %s", got)
	}
}

// A route can't name a model its harness doesn't offer.
func TestRouteRefusesModelHarnessLacks(t *testing.T) {
	a, lead := hiveWithModels(t)
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "claude:opus,codex:gpt-5"}); err == nil || !strings.Contains(err.Error(), "gpt-5") {
		t.Fatalf("codex:gpt-5 was accepted: %v", err)
	}
	if _, err := call(t, a, lead, "route", map[string]any{"role": "worker", "models": "claude:opus,codex:gpt-5.5"}); err != nil {
		t.Fatal(err)
	}
}

// A project with no lead of its own has its trial rounds run by a worker:
// on its own project only, never from a trial's own worker, and not twice
// while one is open.
func TestAWorkerFilesTrialsOnItsOwnProject(t *testing.T) {
	a, _ := hiveWithModels(t)
	if _, err := a.C.Projects.Add(core.Project{Name: "other", Repo: "me/other"}); err != nil {
		t.Fatal(err)
	}
	round, err := a.Work.File(work.Brief{Project: "lab", Title: "one trial round", From: "person"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := a.C.Runs.Start(core.RunSpec{Role: "worker", Project: "lab", Task: round.ID, Harness: "fake", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, a, w, "trial", map[string]any{"model": "qwen", "project": "other", "title": "wc"}); err == nil {
		t.Fatal("a worker filed a trial on another project")
	}
	res, err := call(t, a, w, "trial", map[string]any{"model": "qwen", "project": "lab", "title": "wc"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]string)["task"]
	if _, err := call(t, a, w, "trial", map[string]any{"model": "qwen", "project": "lab", "title": "wc"}); err == nil || !strings.Contains(err.Error(), id) {
		t.Fatalf("a second open trial of the same model and title: %v", err)
	}
	if _, err := call(t, a, w, "trial", map[string]any{"model": "qwen", "project": "lab", "title": "lru"}); err != nil {
		t.Fatal(err)
	}
	tw, err := a.C.Runs.Start(core.RunSpec{Role: "worker", Project: "lab", Task: id, Harness: "fake", Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, a, tw, "trial", map[string]any{"model": "qwen", "project": "lab", "title": "csvsum"}); err == nil {
		t.Fatal("a trial's own worker filed a trial")
	}
}
