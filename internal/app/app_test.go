package app

import (
	"encoding/json"
	"testing"

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
