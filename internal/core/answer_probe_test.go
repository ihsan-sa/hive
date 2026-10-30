package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestAnswerProbe measures how long a worker's request waits for its answer
// while a lead's step is slow, the state the daemon logs under load ("a
// lead's step is still going after 5s"). It drives Runs the way app.loop
// does: a tick every second and 20ms after a request is seen, each tick
// followed by the modules' tick. Run with HIVE_ANSWER_PROBE=1; it prints
// "probe: key=value" lines and then the median answer time in ms.
func TestAnswerProbe(t *testing.T) {
	if os.Getenv("HIVE_ANSWER_PROBE") == "" {
		t.Skip("HIVE_ANSWER_PROBE unset")
	}
	const (
		leadStep = 2 * time.Second        // one slow op in the lead's outbox
		modTick  = 300 * time.Millisecond // the modules' tick after the core's
		asks     = 12
	)
	ws := Workspace{Root: t.TempDir()}
	if err := ws.Init(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	rs := NewRuns(ws, j, nil, NewTokens(ws), func() []string { return nil })
	rs.ops.Register(Op{Name: "slow", Fn: func(Run, json.RawMessage) (any, error) { time.Sleep(leadStep); return "ok", nil }})
	rs.ops.Register(Op{Name: "ping", Fn: func(Run, json.RawMessage) (any, error) { return "pong", nil }})
	for id, role := range map[string]string{"lead1": "lead", "w1": "worker"} {
		rs.runs[id] = &Run{RunSpec: RunSpec{ID: id, Role: role}, Status: StatusIdle}
		for _, d := range []string{"outbox", "inbox"} {
			os.MkdirAll(filepath.Join(ws.RunBox(id), d), 0o755)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The lead always has a slow request waiting.
	go func() {
		out := filepath.Join(ws.RunBox("lead1"), "outbox")
		for n := 0; ctx.Err() == nil; n++ {
			if es, _ := os.ReadDir(out); len(es) == 0 {
				os.WriteFile(filepath.Join(out, fmt.Sprintf("s%d.json", n)), []byte(`{"op":"slow"}`), 0o644)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// The daemon's loop, as app.loop.
	wake := make(chan struct{}, 1)
	nudge := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go rs.WatchRequests(ctx, 50*time.Millisecond, nudge)
	go func() {
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		for {
			rs.Tick(ws.LoadConfig())
			time.Sleep(modTick)
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
			case <-wake:
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	rnd := rand.New(rand.NewSource(1))
	var ms []float64
	box := ws.RunBox("w1")
	for i := range asks {
		time.Sleep(time.Duration(rnd.Intn(1000)) * time.Millisecond)
		id := fmt.Sprintf("p%d", i)
		tmp := filepath.Join(box, "outbox", "."+id+".tmp")
		os.WriteFile(tmp, []byte(`{"op":"ping"}`), 0o644)
		start := time.Now()
		os.Rename(tmp, filepath.Join(box, "outbox", id+".json"))
		resp := filepath.Join(box, "inbox", id+".json")
		for {
			if _, err := os.Stat(resp); err == nil {
				break
			}
			if time.Since(start) > time.Minute {
				t.Fatalf("no answer to %s in a minute", id)
			}
			time.Sleep(time.Millisecond)
		}
		ms = append(ms, float64(time.Since(start).Microseconds())/1000)
		os.Remove(resp)
	}
	slices.Sort(ms)
	fmt.Printf("probe: max_ms=%.0f\n", ms[len(ms)-1])
	fmt.Printf("probe: p90_ms=%.0f\n", ms[len(ms)*9/10])
	fmt.Printf("probe: min_ms=%.0f\n", ms[0])
	fmt.Printf("probe: %.0f\n", ms[len(ms)/2])
}
