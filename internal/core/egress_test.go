package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// A role's list replaces the default, even an empty one, the harness's API
// host joins any list, and no list leaves every public host open.
func TestEgressListPerRole(t *testing.T) {
	c, _ := testHive(t)
	h, _ := harness.Get("claude")
	allow := func(role string) []string {
		spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), sandbox.Bare{}, h, Run{RunSpec: RunSpec{ID: "r-" + role, Role: role, Checkout: t.TempDir()}}, 1, false, "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return spec.Allow
	}
	if got := allow("worker"); got != nil {
		t.Fatalf("no list set, yet the worker is held to %v", got)
	}
	cfg := c.WS.LoadConfig()
	cfg.Egress = Egress{Default: []string{"github.com"}, Roles: map[string][]string{"warden": {"pypi.org"}, "lead": {}}}
	c.WS.SaveConfig(cfg)
	for role, want := range map[string][]string{
		"worker": {"github.com", "api.anthropic.com"}, // no key: the default
		"warden": {"pypi.org", "api.anthropic.com"},
		"lead":   {"api.anthropic.com"}, // an empty list: the API host only
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
	os.WriteFile(out+".egress", []byte("evil.example\npaste.example:443 unlisted\nevil.example\n127.0.0.1:11434 local\n"), 0o600)
	c.Runs.endTurn(r)
	var got egressRefused
	c.J.Replay(func(e Event) {
		if e.Kind == "run.egress_refused" {
			e.Decode(&got)
		}
	})
	if want := []string{"127.0.0.1", "evil.example", "paste.example"}; got.ID != "r1" || !reflect.DeepEqual(got.Hosts, want) {
		t.Fatalf("journalled %+v, want hosts %v", got, want)
	}
	why := map[string]string{}
	for _, f := range got.Refused {
		why[f.Dest] = f.Why
	}
	if len(why) != 3 || !strings.Contains(why["127.0.0.1:11434"], "egress grant 127.0.0.1:11434") ||
		!strings.Contains(why["paste.example:443"], "egress list") {
		t.Fatalf("journalled reasons %+v, want each refusal's reason and how a local one opens", got.Refused)
	}
}

// A granted local service reaches every sandboxed run's spec; a grant that
// isn't a loopback host:port is refused in the config and opens nothing.
func TestLocalGrantReachesTheSpec(t *testing.T) {
	c, _ := testHive(t)
	h, _ := harness.Get("claude")
	cfg := c.WS.LoadConfig()
	cfg.Egress = Egress{Default: []string{"github.com"}, Local: []string{"127.0.0.1:11434", "localhost:11434", "nas.lan:80"}}
	if err := cfg.Check(); err == nil || !strings.Contains(err.Error(), "nas.lan:80") {
		t.Fatalf("Check let a non-loopback grant through: %v", err)
	}
	c.WS.SaveConfig(cfg)
	spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), sandbox.Bare{}, h, Run{RunSpec: RunSpec{ID: "r1", Role: "worker", Checkout: t.TempDir()}}, 1, false, "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"127.0.0.1:11434", "[::1]:11434"}; !reflect.DeepEqual(spec.Local, want) {
		t.Fatalf("spec opens %v, want %v", spec.Local, want)
	}
}

// A host granted to one project reaches its runs' specs past their list, and
// no other project's; a grant Check refuses opens nothing.
func TestProjectGrantReachesItsRunsOnly(t *testing.T) {
	c, _ := testHive(t)
	h, _ := harness.Get("claude")
	cfg := c.WS.LoadConfig()
	cfg.Egress = Egress{Default: []string{"github.com"}, Projects: map[string][]string{"hifi": {"Broker.example.com:8883", "192.168.1.20:80", "nas.lan:80"}}}
	if err := cfg.Check(); err == nil || !strings.Contains(err.Error(), "nas.lan:80") {
		t.Fatalf("Check let a local name through: %v", err)
	}
	c.WS.SaveConfig(cfg)
	outside := func(project string) []string {
		spec, err := c.Runs.sandboxSpec(c.WS.LoadConfig(), sandbox.Bare{}, h, Run{RunSpec: RunSpec{ID: "r-" + project, Role: "worker", Project: project, Checkout: t.TempDir()}}, 1, false, "", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		return spec.Outside
	}
	if got, want := outside("hifi"), []string{"broker.example.com:8883", "192.168.1.20:80"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hifi's run opens %v, want %v", got, want)
	}
	if got := outside("other"); got != nil {
		t.Fatalf("another project's run opens %v", got)
	}
}

// Asking to open a local service is always the person's.
func TestLocalGrantIsThePersons(t *testing.T) {
	if got := CeilingHit("reach the local Ollama", "hive egress grant 127.0.0.1:11434"); got != "open" {
		t.Fatalf("a local grant fell to %q, not the person", got)
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
