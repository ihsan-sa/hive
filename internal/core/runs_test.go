package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

// isolatedSB is a sandbox whose inside paths differ from host paths.
type isolatedSB struct{ sandbox.Bare }

func (isolatedSB) Isolated() bool { return true }

// A mount nested in a read-only mount needs its mount point on the host,
// because bwrap can't make it inside the read-only bind.
func TestNestedMountPointsExist(t *testing.T) {
	c, _ := testHive(t)
	r := Run{ID: "r1", Role: "lead", Harness: "fake", Isolated: true, Modules: []string{"agents", "kb"}}
	h, _ := harness.Get("fake")
	spec, err := c.Runs.sandboxSpec(isolatedSB{}, h, r, 1, false, filepath.Join(t.TempDir(), "p"))
	if err != nil {
		t.Fatal(err)
	}
	nested := 0
	for _, m := range spec.Mounts {
		for _, ro := range spec.Mounts {
			if ro.RW || !strings.HasPrefix(m.Inside, ro.Inside+"/") {
				continue
			}
			nested++
			if _, err := os.Stat(filepath.Join(ro.Host, strings.TrimPrefix(m.Inside, ro.Inside))); err != nil {
				t.Errorf("%s has no mount point in read-only %s: %v", m.Inside, ro.Inside, err)
			}
		}
	}
	if nested != 2 {
		t.Fatalf("nested mounts: %d, want 2 (%+v)", nested, spec.Mounts)
	}
}

// startSB records each turn's command instead of starting it.
type startSB struct {
	sandbox.Bare
	args *[]string
}

func (s startSB) Start(spec sandbox.Spec) (sandbox.Proc, error) {
	*s.args = spec.Argv
	return sandbox.Proc{}, nil
}

// A lead is long-lived, so its next turn takes the model the settings name
// now, and the run says which model that turn is on.
func TestLeadFollowsModelSetting(t *testing.T) {
	c, _ := testHive(t)
	var args []string
	c.Runs.sandbox = func() (sandbox.Sandbox, error) { return startSB{args: &args}, nil }
	c.Runs.ModelFor = func(Run) string { return "opus" }
	r, err := c.Runs.Start(RunSpec{ID: "lead1", Role: "lead", Harness: "claude", Model: "sonnet", Persistent: true, Checkout: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.Runs.launch(r)
	got, _ := c.Runs.Get("lead1")
	if got.Model != "opus" || !strings.Contains(strings.Join(args, " "), "opus") {
		t.Fatalf("model %q, args %q: want the turn on opus", got.Model, args)
	}
}
