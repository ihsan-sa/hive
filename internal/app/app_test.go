package app

import (
	"encoding/json"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
)

// The risk module wraps core's "message" op when it is built, so a warden
// asking an agent only waits if the app builds risk after core.
func TestWardenMessagingTheRequesterWaitsInTheApp(t *testing.T) {
	ws := core.Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	cfg := ws.LoadConfig()
	cfg.Harness = "fake"
	cfg.MemFloorMB, cfg.MemPressureMax, cfg.CPUPressureMax = 0, 0, 0
	if err := ws.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a, err := New(ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.C.Close()
	spec := core.RunSpec{Harness: "fake", Checkout: t.TempDir()}
	spec.Role = "worker"
	worker, err := a.C.Runs.Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Role = "warden"
	spec.Parent = worker.ID
	warden, err := a.C.Runs.Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"to": "parent", "text": "which version?"})
	if _, err := a.C.Runs.Ops().Call(warden, "message", raw); err != nil {
		t.Fatal(err)
	}
	if w, _ := a.C.Runs.Get(warden.ID); w.Waiting == "" {
		t.Fatalf("a warden's message in the app's wiring left it not waiting: %+v", w)
	}
}
