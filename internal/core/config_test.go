package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// A module's hooks hold a pick (Held) and limit a turn's budget (TurnBudget):
// freeAt waits for the hold, and a turn's --max-budget-usd is the smaller of
// the run's own and the module's.
func TestModuleHoldsAPickAndLimitsATurn(t *testing.T) {
	c, _ := testHive(t)
	month := time.Now().Add(30 * 24 * time.Hour)
	c.Runs.Held = func(p Pick) time.Time {
		if p.Model == "held" {
			return month
		}
		return time.Time{}
	}
	if at := c.Runs.freeAt("open", "held", false); !at.Equal(month) {
		t.Fatalf("a held pick has room at %v, want %v", at, month)
	}
	if at := c.Runs.freeAt("open", "other", false); at.After(time.Now().Add(time.Minute)) {
		t.Fatalf("a pick no module holds waits until %v", at)
	}
	h, _ := harness.Get("open")
	r := Run{RunSpec: RunSpec{ID: "r", Role: "worker", Harness: "open", Model: "m", Budget: 5}}
	budget := func() string {
		spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"), nil, "")
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(spec.Argv, " ")
		_, after, _ := strings.Cut(args, "--max-budget-usd ")
		return strings.Fields(after + " ")[0]
	}
	c.Runs.TurnBudget = func(Run) float64 { return 2 }
	if b := budget(); b != "2" {
		t.Fatalf("budget %q, want the module's 2", b)
	}
	c.Runs.TurnBudget = func(Run) float64 { return 9 }
	if b := budget(); b != "5" {
		t.Fatalf("budget %q, want the run's own 5", b)
	}
}
