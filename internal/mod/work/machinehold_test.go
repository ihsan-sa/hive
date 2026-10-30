package work

import "testing"

// A machine over load_max alone holds tasks from the workers' start on even
// while an executor module has room, since launch would run them here, and
// lets them start below it, as a few workers aren't what loads it; a
// strained one lets one task a pass go, to be handed to the executor, and
// asks it once.
func TestMachineHoldOnlyPassesAStrainedMachine(t *testing.T) {
	asked := 0
	room := func() bool { asked++; return true }
	loaded := machineHold("", "", "machine loaded", 2, room)
	if why := loaded(1); why != "" {
		t.Errorf("loaded only, below the start: %q", why)
	}
	for range 3 {
		if why := loaded(2); why != "machine loaded" {
			t.Fatalf("loaded only, at the start: %q", why)
		}
	}
	if asked != 0 {
		t.Fatalf("a loaded-only machine asked the executor %d times", asked)
	}
	strained := machineHold("machine low on memory", "", "", 2, room)
	if why := strained(0); why != "" {
		t.Fatalf("strained with room elsewhere: first task held: %q", why)
	}
	for range 2 {
		if why := strained(0); why != "machine low on memory" {
			t.Fatalf("strained: a second task in one pass: %q", why)
		}
	}
	if asked != 1 {
		t.Errorf("asked the executor %d times in one pass, want 1", asked)
	}
	if why := machineHold("machine low on memory", "", "", 2, func() bool { return false })(0); why != "machine low on memory" {
		t.Errorf("strained with no room elsewhere: %q", why)
	}
	if why := machineHold("", "", "", 2, room)(5); why != "" {
		t.Errorf("an idle machine: %q", why)
	}
}
