package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// A role's list replaces the default, the harness's API host joins any list,
// and no list leaves every public host open.
func TestEgressListPerRole(t *testing.T) {
	c, _ := testHive(t)
	h, _ := harness.Get("claude")
	allow := func(role string) []string {
		spec, err := c.Runs.sandboxSpec(sandbox.Bare{}, h, Run{RunSpec: RunSpec{ID: "r-" + role, Role: role, Checkout: t.TempDir()}}, 1, false, "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return spec.Allow
	}
	if got := allow("worker"); got != nil {
		t.Fatalf("no list set, yet the worker is held to %v", got)
	}
	cfg := c.WS.LoadConfig()
	cfg.Egress = Egress{Default: []string{"github.com"}, Roles: map[string][]string{"warden": {"pypi.org"}}}
	c.WS.SaveConfig(cfg)
	for role, want := range map[string][]string{
		"worker": {"github.com", "api.anthropic.com"},
		"warden": {"pypi.org", "api.anthropic.com"},
	} {
		if got := allow(role); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: allow %v, want %v", role, got, want)
		}
	}
}

// Hosts a turn's proxy refused are journalled once each when the turn ends.
func TestRefusedHostsAreJournalled(t *testing.T) {
	c, _ := testHive(t)
	r := Run{RunSpec: RunSpec{ID: "r1", Role: "worker"}, Turn: 1}
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out+".egress", []byte("evil.example\npaste.example\nevil.example\n"), 0o600)
	c.Runs.endTurn(r)
	var got egressRefused
	c.J.Replay(func(e Event) {
		if e.Kind == "run.egress_refused" {
			e.Decode(&got)
		}
	})
	if want := []string{"evil.example", "paste.example"}; got.ID != "r1" || !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("journalled %+v, want hosts %v", got, want)
	}
}

// A seat is a plain model on the workspace harness or names its own; a
// harness the Hive doesn't have is refused.
func TestTribunalSeats(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"tribunal_models":["opus",{"harness":"claude","model":"sonnet"}]}`), &c); err != nil {
		t.Fatal(err)
	}
	if want := []TribunalSeat{{Model: "opus"}, {Harness: "claude", Model: "sonnet"}}; !reflect.DeepEqual(c.TribunalModels, want) {
		t.Fatalf("seats %+v, want %+v", c.TribunalModels, want)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(c.TribunalModels); string(raw) != `["opus",{"harness":"claude","model":"sonnet"}]` {
		t.Fatalf("saved seats as %s, not as written", raw)
	}
	c.TribunalModels = append(c.TribunalModels, TribunalSeat{Harness: "none-such", Model: "x"})
	if c.Check() == nil {
		t.Fatal("a seat on a missing harness was accepted")
	}
}

// A turn whose output holds a second verdict trusts neither.
func TestSecondVerdictIsNoVerdict(t *testing.T) {
	c, _ := testHive(t)
	r := Run{RunSpec: RunSpec{ID: "r1", Harness: "claude"}}
	out := c.WS.TurnOut(r.ID, 1)
	os.MkdirAll(filepath.Dir(out), 0o755)
	one := `{"type":"result","result":"ok","structured_output":{"vote":"approve","reason":"x"}}` + "\n"
	os.WriteFile(out, []byte(one), 0o600)
	if tl := c.Runs.ReadTurn(r, 1); tl.Result == nil || tl.Result.Structured == nil {
		t.Fatalf("one verdict was lost: %+v", tl.Result)
	}
	os.WriteFile(out, []byte(one+one), 0o600)
	if tl := c.Runs.ReadTurn(r, 1); tl.Result == nil || tl.Result.Structured != nil {
		t.Fatalf("a second verdict was trusted: %+v", tl.Result)
	}
}
