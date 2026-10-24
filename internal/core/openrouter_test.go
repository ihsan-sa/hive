package core

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// envSB is a sandbox that keeps the last turn's argv and environment.
type envSB struct {
	sandbox.Bare
	spec *sandbox.Spec
}

func (s envSB) Start(spec sandbox.Spec) (sandbox.Proc, error) {
	*s.spec = spec
	return sandbox.Proc{}, nil
}

func openRouterModels() map[string]OpenModel {
	return map[string]OpenModel{
		"nemotron": {Endpoint: "https://openrouter.ai/api/v1", Model: "nvidia/nemotron-3-ultra-550b-a55b:free"},
		"gpt-oss":  {Endpoint: "https://openrouter.ai/api/v1", Model: "openai/gpt-oss-120b:free"},
		"kimi":     {Endpoint: "https://openrouter.ai/api/v1", Model: "moonshotai/kimi-k2"},
		"qwen":     {Endpoint: "http://127.0.0.1:11434/v1", Model: "qwen2.5-coder:14b"},
	}
}

// A route puts its free OpenRouter models ahead of its paid ones, in the
// places its OpenRouter models hold; Claude and a local model keep theirs.
func TestFreeOpenRouterFirst(t *testing.T) {
	c := DefaultConfig()
	c.Models = openRouterModels()
	c.Routes = map[string][]string{"worker": {"claude:opus", "kimi", "qwen", "nemotron"}, "fallback": {"gpt-oss"}}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	if got, want := picks(c.Route("worker", "")), "claude:opus open:nemotron open:qwen open:gpt-oss open:kimi"; got != want {
		t.Errorf("worker route %s, want %s", got, want)
	}
	if got := picks(c.FreeOpenRouter()); got != "open:gpt-oss open:nemotron" {
		t.Errorf("free OpenRouter models: %s", got)
	}
	if !c.Models["kimi"].Paid() || c.Models["nemotron"].Paid() || c.Models["qwen"].Paid() || c.Models["qwen"].OpenRouter() {
		t.Error("paid is an OpenRouter model that isn't :free")
	}
	if c.OpenRouterCapUSD != 10 || c.OpenRouterStopUSD != 9 {
		t.Errorf("default budget $%v, stop $%v", c.OpenRouterCapUSD, c.OpenRouterStopUSD)
	}
	c.OpenRouterStopUSD = 11
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "openrouter_stop_usd") {
		t.Errorf("a stop past the cap: %v", err)
	}
}

// spend journals an OpenRouter turn that cost usd, as endTurn does.
func spend(c *Core, id string, usd float64) {
	c.Runs.append("run.turn_ended", turnEnded{ID: id, Turn: 1, Status: StatusIdle, OpenRouter: true,
		Session: NewUUID(), Result: &harness.Result{CostUSD: usd}})
}

// Paid OpenRouter models take turns until the month's OpenRouter spend
// reaches the person's stop; then a run moves to a free one, told why, the
// person is told once, and the dashboard shows the spend against the cap.
func TestOpenRouterBudgetStop(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = openRouterModels()
	c.WS.SaveConfig(cfg)
	var spec sandbox.Spec
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return envSB{spec: &spec}, nil }
	r, err := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "open", Model: "kimi", Brief: "the brief", Checkout: t.TempDir(),
		Route: []Pick{{Harness: "open", Model: "kimi"}}})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r, c.WS.LoadConfig())
	got, _ := c.Runs.Get("w1")
	if got.Model != "kimi" || !slices.Contains(spec.Env, "HIVE_OPEN_BUDGET=9.0000") {
		t.Fatalf("under the stop a paid model runs, with the budget left: %s, env %q", got.Model, spec.Env)
	}
	if b := c.Runs.OpenRouterBudget(c.WS.LoadConfig()); b == nil || b.Spent != 0 || b.Cap != 10 || b.Stopped || !b.Paid {
		t.Fatalf("dashboard before: %+v", b)
	}
	spend(c, "w1", 4)
	spend(c, "w1", 5.25)
	if b := c.Runs.OpenRouterBudget(c.WS.LoadConfig()); b.Spent != 9.25 || !b.Stopped {
		t.Fatalf("dashboard after $9.25: %+v", b)
	}
	if c.Runs.Spent(time.Now().UTC().Format("2006-01")) != 9.25 {
		t.Error("OpenRouter spend counts in the month's spend too")
	}
	r, _ = c.Runs.Get("w1")
	if p, _ := c.Runs.next(r); p.Model != "gpt-oss" {
		t.Fatalf("past the stop the run's next pick is %v, want the first free model", p)
	}
	c.Runs.launch(r, c.WS.LoadConfig())
	got, _ = c.Runs.Get("w1")
	if got.Model != "gpt-oss" || slices.ContainsFunc(spec.Env, func(e string) bool { return strings.HasPrefix(e, "HIVE_OPEN_BUDGET=") }) {
		t.Fatalf("past the stop the run moves to a free model, with no budget: %s, env %q", got.Model, spec.Env)
	}
	prompt, _ := os.ReadFile(c.WS.RunHost("w1") + "/turn-002.prompt")
	if !strings.Contains(string(prompt), "open:kimi is a paid OpenRouter model and this month's OpenRouter budget is spent") {
		t.Fatalf("the moved turn's prompt: %q", prompt)
	}
	cfg = c.WS.LoadConfig()
	c.tellOpenRouter(cfg)
	c.tellOpenRouter(cfg)
	told := 0
	for _, a := range c.Person.Told(20) {
		if strings.HasPrefix(a.ID, "openrouter-") {
			told++
			if !strings.Contains(a.Title, "$9.25 of your $10.00") || !strings.Contains(a.Detail, "gpt-oss, nemotron") {
				t.Errorf("the notice: %s / %s", a.Title, a.Detail)
			}
		}
	}
	if told != 1 {
		t.Fatalf("told %d times, want once", told)
	}
	// The person raises the stop: paid models are back.
	cfg.OpenRouterStopUSD = 10
	c.WS.SaveConfig(cfg)
	if c.Runs.Barred(c.WS.LoadConfig(), Pick{Harness: "open", Model: "kimi"}) {
		t.Error("a raised stop lets paid models run again")
	}
}

// Past the stop, a run whose picks hold no free model waits for the month
// to end rather than spend.
func TestOpenRouterBudgetWaitsWithNoFreeModel(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.Models = map[string]OpenModel{"kimi": openRouterModels()["kimi"]}
	c.WS.SaveConfig(cfg)
	var spec sandbox.Spec
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return envSB{spec: &spec}, nil }
	r, _ := c.Runs.Start(RunSpec{ID: "w1", Role: "worker", Harness: "open", Model: "kimi", Checkout: t.TempDir()})
	spend(c, "w1", 9)
	r, _ = c.Runs.Get("w1")
	c.Runs.launch(r, c.WS.LoadConfig())
	got, _ := c.Runs.Get("w1")
	if got.Turn != 0 || got.Status != StatusParked || !got.ParkedUntil.Equal(NextMonth(time.Now())) || !strings.Contains(got.Reason, "OpenRouter budget") {
		t.Fatalf("with no free model the run waits for next month: turn %d, %s until %v (%s)", got.Turn, got.Status, got.ParkedUntil, got.Reason)
	}
	if spec.Argv != nil {
		t.Fatal("no turn started")
	}
	// Still parked, a tick leaves it; once the person raises the stop, the
	// next tick starts it, without waiting for the month.
	c.Runs.step(got, c.WS.LoadConfig())
	if got, _ = c.Runs.Get("w1"); got.Turn != 0 {
		t.Fatal("a tick started a barred run")
	}
	cfg = c.WS.LoadConfig()
	cfg.OpenRouterStopUSD = 10
	c.WS.SaveConfig(cfg)
	c.Runs.step(got, c.WS.LoadConfig())
	if got, _ = c.Runs.Get("w1"); got.Turn != 1 || got.Model != "kimi" || !slices.Contains(spec.Env, "HIVE_OPEN_BUDGET=1.0000") {
		t.Fatalf("a raised stop starts the parked run: turn %d on %s, env %q", got.Turn, got.Model, spec.Env)
	}
	// A turn that didn't run on OpenRouter doesn't count against it.
	c2, _ := testHive(t)
	c2.Runs.append("run.turn_ended", turnEnded{ID: "x", Turn: 1, Status: StatusIdle, Result: &harness.Result{CostUSD: 50}})
	if s := c2.Runs.OpenRouterSpent(time.Now().UTC().Format("2006-01")); s != 0 {
		t.Errorf("a Claude turn counted $%v against OpenRouter", s)
	}
	if NextMonth(time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)) != time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) {
		t.Error("NextMonth across a year")
	}
}
