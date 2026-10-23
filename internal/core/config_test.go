package core

import (
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
