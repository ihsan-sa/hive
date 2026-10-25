package work

import "testing"

// A machine over load_max alone holds every task even while an executor
// module has room, since launch would run it here; a strained one lets one
// task a pass go, to be handed to the executor, and asks it once.
func TestMachineHoldOnlyPassesAStrainedMachine(t *testing.T) {
	asked := 0
	room := func() bool { asked++; return true }
	loaded := machineHold("", "load 30 on 4 cores", room)
	for range 3 {
		if why := loaded(); why != "load 30 on 4 cores" {
			t.Fatalf("loaded only: %q", why)
		}
	}
	if asked != 0 {
		t.Fatalf("a loaded-only machine asked the executor %d times", asked)
	}
	strained := machineHold("machine low on memory", "", room)
	if why := strained(); why != "" {
		t.Fatalf("strained with room elsewhere: first task held: %q", why)
	}
	for range 2 {
		if why := strained(); why != "machine low on memory" {
			t.Fatalf("strained: a second task in one pass: %q", why)
		}
	}
	if asked != 1 {
		t.Errorf("asked the executor %d times in one pass, want 1", asked)
	}
	if why := machineHold("machine low on memory", "", func() bool { return false })(); why != "machine low on memory" {
		t.Errorf("strained with no room elsewhere: %q", why)
	}
	if why := machineHold("", "", room)(); why != "" {
		t.Errorf("an idle machine: %q", why)
	}
}
