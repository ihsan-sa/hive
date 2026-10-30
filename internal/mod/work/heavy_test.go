package work

import (
	"testing"
	"time"
)

// A heavy command gets a lease while fewer than the limit run and nothing
// strains the machine, and always when none runs; a renewal keeps one going
// whatever the machine says, a lease given back frees its place, and one
// not renewed goes after heavyStale.
func TestHeavyLeasesPaceCommandsInTurns(t *testing.T) {
	m, _, _ := helperRig(t)
	now := time.Now()
	ask := func(run, id string, running, done bool, strain string) map[string]any {
		t.Helper()
		return m.heavyAsk(now, run, id, "pytest", running, done, strain, true, 2, 4)
	}
	if r := ask("w1", "a", false, false, "machine loaded (load 40 on 20 cores)"); r["go"] != true {
		t.Fatalf("the first heavy command waited under strain with none running: %v", r)
	}
	if r := ask("w2", "a", false, false, "machine loaded (load 40 on 20 cores)"); r["go"] == true {
		t.Fatalf("a second started while the machine is strained: %v", r)
	}
	if r := ask("w2", "a", false, false, ""); r["go"] != true {
		t.Fatalf("a second waited with room under the limit of 2: %v", r)
	}
	r := ask("w3", "a", false, false, "")
	if r["go"] == true || len(r["running"].([]string)) != 2 {
		t.Fatalf("a third started past the limit of 2, or the wait doesn't list both: %v", r)
	}
	if r := ask("w1", "a", true, false, "machine loaded (load 90 on 20 cores)"); r["go"] != true {
		t.Fatalf("a renewal was refused: %v", r)
	}
	ask("w1", "a", false, true, "")
	if m.Heavy() != 1 {
		t.Fatalf("%d leases held after one was given back, want 1", m.Heavy())
	}
	if r := ask("w3", "a", false, false, ""); r["go"] != true {
		t.Fatalf("the place given back wasn't taken: %v", r)
	}
	now = now.Add(heavyStale + time.Second)
	if r := ask("w4", "a", false, false, "machine loaded (load 40 on 20 cores)"); r["go"] != true {
		t.Fatalf("leases not renewed still held the machine: %v", r)
	}
	if r := ask("w9", "b", true, false, ""); r["go"] != true || m.Heavy() != 2 {
		t.Fatalf("a renewal the daemon didn't know (a restart under it) isn't counted: %v, %d held", r, m.Heavy())
	}
}
