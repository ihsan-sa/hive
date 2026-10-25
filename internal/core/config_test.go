package core

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
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
