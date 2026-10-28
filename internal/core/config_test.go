package core

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
)

// A seat config.json can't read is refused by name, and every setting after
// it, the floor and the egress list among them, is still read.
func TestBadSeatKeepsTheRestOfTheConfig(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(ws.ConfigPath(), []byte(`{"tribunal_models": ["opus", {"model": 5}], "floor": ["*.pem"], "egress": {"default": ["github.com"]}}`), 0o600)
	c, err := ws.ReadConfig()
	if err == nil || !strings.Contains(err.Error(), `{"model": 5}`) {
		t.Fatalf("error %v, want the bad seat named", err)
	}
	if !reflect.DeepEqual(c.Floor, []string{"*.pem"}) || !reflect.DeepEqual(c.Egress.Default, []string{"github.com"}) {
		t.Fatalf("lost settings after the bad seat: floor %v, egress %v", c.Floor, c.Egress)
	}
	if c.TribunalModels[0].Model != "opus" {
		t.Fatalf("seats %+v", c.TribunalModels)
	}
	// A save that skips Check (setup's bare accept) writes the seat back as written.
	if err := ws.SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.ReadConfig(); err == nil || !strings.Contains(err.Error(), "tribunal seat") {
		t.Fatalf("after a save, error %v, want the bad seat still refused", err)
	}
}

// A floor glob that can't match is refused; a good one and a plain entry pass.
func TestCheckRefusesABadFloorGlob(t *testing.T) {
	if err := (Config{Floor: []string{"secret["}}).Check(); err == nil || !strings.Contains(err.Error(), "secret[") {
		t.Fatalf("an unclosed [ passed: %v", err)
	}
	if err := (Config{Floor: []string{"*.pem", `C:\keys\`}}).Check(); err != nil {
		t.Fatal(err)
	}
}

// A seat written as null reads as an empty model, which Check refuses.
func TestCheckRefusesANullSeat(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"tribunal_models": ["opus", null]}`), &c); err != nil {
		t.Fatal(err)
	}
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "needs a model") {
		t.Fatalf("a null seat passed: %v", err)
	}
}

// Turning sharing on again keeps when it was first turned on, so events
// between the last report and now aren't skipped; off and on again starts over.
func TestSetShareOnAgainKeepsSince(t *testing.T) {
	var c Config
	c.SetShare(true)
	c.Share = "2026-01-02T03:04:05Z"
	id := c.InstallID
	c.SetShare(true)
	if c.Share != "2026-01-02T03:04:05Z" || c.InstallID != id {
		t.Fatalf("on again moved sharing to %q, id %q (was %q)", c.Share, c.InstallID, id)
	}
	// An id lost while on (a hand edit, a config from a share bundle) comes back.
	c.InstallID = ""
	c.SetShare(true)
	if c.Share != "2026-01-02T03:04:05Z" || c.InstallID == "" {
		t.Fatalf("on again without an id: share %q, id %q", c.Share, c.InstallID)
	}
	id = c.InstallID
	c.SetShare(false)
	c.SetShare(true)
	if c.Share == "2026-01-02T03:04:05Z" || c.InstallID != id {
		t.Fatalf("off then on kept %q, id %q (was %q)", c.Share, c.InstallID, id)
	}
}

// A paid model on OpenRouter is refused while OpenRouter is free-only (no
// openrouter_monthly_usd) or it has no price its spend is counted at; with
// both it passes, under the month's cap. A free one always passes.
func TestCheckAllowsAPaidOpenRouterModelUnderTheCap(t *testing.T) {
	paid := OpenModel{Endpoint: "https://openrouter.ai/api/v1", Model: "anthropic/claude-opus-5", Key: true}
	c := Config{Models: map[string]OpenModel{"or": paid}}
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "model or:") || !strings.Contains(err.Error(), "free-only") {
		t.Fatalf("a paid OpenRouter model passed with no cap: %v", err)
	}
	c.OpenRouterUSD = 10
	if err := c.Check(); err == nil || !strings.Contains(err.Error(), "no price") {
		t.Fatalf("an unpriced paid OpenRouter model passed: %v", err)
	}
	paid.Price = &Price{Input: 0.3, Output: 1.2}
	c.Models["or"] = paid
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	c = Config{Models: map[string]OpenModel{"or": {Endpoint: "https://openrouter.ai/api/v1", Model: "nvidia/nemotron-3-ultra-550b-a55b:free", Key: true}}}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
}

// Free ones first: a paid OpenRouter model on a route goes after the rest,
// whatever its place there; a run on it takes turns while the month's
// OpenRouter spend is under the cap, then waits for the next month, and a
// free model beside it is never held.
func TestPaidOpenRouterModelRunsLastAndUnderTheCap(t *testing.T) {
	c, _ := testHive(t)
	cfg := c.WS.LoadConfig()
	cfg.OpenRouterUSD = 10
	cfg.Models = map[string]OpenModel{
		"cheap": {Endpoint: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-v4", Key: true, Price: &Price{Input: 0.3, Output: 1.2}},
		"free":  {Endpoint: "https://openrouter.ai/api/v1", Model: "deepseek/deepseek-v4:free", Key: true},
	}
	cfg.Routes = map[string][]string{"worker": {"cheap", "free", "claude:opus"}}
	if err := cfg.Check(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range cfg.Route("worker", "") {
		got = append(got, p.String())
	}
	if want := []string{"open:free", "claude:opus", "open:cheap"}; !slices.Equal(got, want) {
		t.Fatalf("route %v, want %v", got, want)
	}
	c.Runs.tickDown(cfg)
	now := time.Now()
	if at := c.Runs.freeAt("open", "cheap", false); at.After(now.Add(time.Minute)) {
		t.Fatalf("a paid model under the cap waits until %v", at)
	}
	// Two turns of $6 each, as the run manager journals them, pass the $10 cap.
	c.J.MustAppend("run.created", RunSpec{ID: "w", Harness: "open", Model: "cheap"})
	for n := 1; n <= 2; n++ {
		c.J.MustAppend("run.turn_started", turnStarted{ID: "w", Session: fmt.Sprint(n), Turn: n})
		c.J.MustAppend("run.turn_ended", turnEnded{ID: "w", Turn: n, Session: fmt.Sprint(n), Result: &harness.Result{CostUSD: 6}, Status: StatusIdle})
	}
	y, m, _ := now.UTC().Date()
	if at := c.Runs.freeAt("open", "cheap", false); !at.After(time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second)) {
		t.Fatalf("a paid model past the month's cap has room at %v", at)
	}
	if at := c.Runs.freeAt("open", "free", false); at.After(now.Add(time.Minute)) {
		t.Fatalf("a free model waits until %v once the paid one is at the cap", at)
	}
}
