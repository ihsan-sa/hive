package models

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/agents"
)

// Free ones first, paid under the month's cap (the person, 10-24): a paid
// OpenRouter model goes last, its turns' spend is counted from the journal
// (a restart's replay too, a resumed session's running total once), each
// turn gets what is left less what the paid turns running were given, and
// once the month's spend reaches the cap it is held until the next month.
// A free model is never held, limited or moved.
func TestPaidOpenRouterModelUnderTheCap(t *testing.T) {
	j, err := core.OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	cfg := core.Config{OpenRouterUSD: 10, Models: map[string]core.OpenModel{
		"cheap": {Endpoint: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-v4", Key: true, Price: &core.Price{Input: 0.3, Output: 1.2}},
		"free":  {Endpoint: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-v4:free", Key: true},
	}}
	conf := func() core.Config { return cfg }
	cheap, free := core.Pick{Harness: "open", Model: "cheap"}, core.Pick{Harness: "open", Model: "free"}
	turn := func(id string, n int, session string, total float64) {
		j.MustAppend("run.turn_started", map[string]any{"id": id, "turn": n, "session": session, "harness": "open", "model": "cheap"})
		j.MustAppend("run.turn_ended", map[string]any{"id": id, "turn": n, "session": session, "result": harness.Result{CostUSD: total}})
	}
	turn("a", 1, "s1", 2)
	turn("a", 2, "s1", 3) // a running total: this turn spent 1
	p := NewPaid(j, conf)
	month := time.Now().UTC().Format("2006-01")
	if got := p.Spent(month); got != 3 {
		t.Fatalf("replayed spend $%v, want $3", got)
	}
	if !p.Last(cheap) || p.Last(free) || p.Last(core.Pick{Harness: "claude", Model: "opus"}) {
		t.Fatal("Last isn't the paid OpenRouter model alone")
	}
	if !p.Held(cheap).IsZero() {
		t.Fatal("held under the cap")
	}
	if b := p.TurnBudget(core.Run{RunSpec: core.RunSpec{ID: "b", Harness: "open", Model: "cheap"}}); b != 7 {
		t.Fatalf("turn budget $%v, want the $7 left", b)
	}
	if b := p.TurnBudget(core.Run{RunSpec: core.RunSpec{ID: "c", Harness: "open", Model: "cheap"}}); b != 0.01 {
		t.Fatalf("a second turn at once got $%v of the $7 the first was given", b)
	}
	if b := p.TurnBudget(core.Run{RunSpec: core.RunSpec{ID: "f", Harness: "open", Model: "free"}}); b != 0 {
		t.Fatalf("a free model's turn is limited to $%v", b)
	}
	turn("b", 1, "s2", 7)
	if got := p.Spent(month); got != 10 {
		t.Fatalf("spend $%v, want $10", got)
	}
	now := time.Now().UTC()
	if at := p.Held(cheap); !at.Equal(time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("at the cap it is held until %v, want the next month", at)
	}
	if !p.Held(free).IsZero() {
		t.Fatal("a free model is held")
	}
	cfg.OpenRouterUSD = 20
	if !p.Held(cheap).IsZero() {
		t.Fatal("still held after the cap was raised")
	}
}

// A route puts a paid OpenRouter model after the rest, wherever the person
// listed it: free ones first.
func TestPaidOpenRouterModelGoesLastOnARoute(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	cfg := core.DefaultConfig()
	cfg.OpenRouterUSD = 10
	cfg.Models = map[string]core.OpenModel{
		"cheap": {Endpoint: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-v4", Key: true, Price: &core.Price{Input: 0.3, Output: 1.2}},
		"free":  {Endpoint: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-v4:free", Key: true},
	}
	cfg.Routes = map[string][]string{"worker": {"cheap", "free", "claude:opus"}}
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	j, err := core.OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	a := &agents.Agents{C: &core.Core{WS: ws}, Last: NewPaid(j, ws.LoadConfig).Last}
	var got []string
	for _, p := range a.Route("worker", "") {
		got = append(got, p.String())
	}
	if want := []string{"open:free", "claude:opus", "open:cheap"}; !slices.Equal(got, want) {
		t.Fatalf("route %v, want %v", got, want)
	}
}
