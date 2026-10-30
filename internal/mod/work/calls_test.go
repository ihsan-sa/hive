package work

import (
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/core"
	"github.com/ihsan-sa/hive-standalone-build/internal/mod/calls"
)

// Each call that can let a change count with less of a read or check (the
// judge's tries and waits, the gate's time, reruns, can't-run tries, the
// kept-first hold) is refused to a worker, whose own change it could be; the
// lead makes it.
func TestWorkerCantSoftenItsOwnReads(t *testing.T) {
	m, _, _, _ := noCommitRig(t, 0)
	w := core.Run{RunSpec: core.RunSpec{ID: "w-1", Role: "worker"}}
	lead := core.Run{RunSpec: core.RunSpec{ID: "lead-1", Role: "lead"}}
	for _, c := range []struct{ name, value string }{
		{"work.judge_park_wait", "0"}, {"work.judge_tries", "0"}, {"work.judge_wait", "5m"},
		{"work.gate_timeout", "10m"}, {"work.reruns_per_head", "9"}, {"work.cant_run_tries", "99"}, {"work.kept_first_for", "0"},
	} {
		if _, err := calls.Make(m.C.J, w, c.name, c.value, "mine merges sooner"); err == nil {
			t.Errorf("a worker made %s", c.name)
		}
		if _, err := calls.Make(m.C.J, lead, c.name, c.value, "the lead's call"); err != nil {
			t.Errorf("the lead's %s: %v", c.name, err)
		}
	}
	if _, err := calls.Make(m.C.J, w, "work.draft_tell", "1h", "the lead should hear sooner"); err != nil {
		t.Errorf("a worker's call on a notice: %v", err)
	}
}
