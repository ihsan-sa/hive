package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ihsan-sa/hive-standalone-build/internal/harness"
	"github.com/ihsan-sa/hive-standalone-build/internal/sandbox"
)

type isolatedBox struct{ sandbox.Bare }

func (isolatedBox) Isolated() bool { return true }

// TestNestedMountPointsExist: bubblewrap can't make a mount point inside a
// read-only mount, so every mount nested in one must find its point already
// on the host. An isolated lead granted a module is the case that broke.
func TestNestedMountPointsExist(t *testing.T) {
	ws := Workspace{Root: t.TempDir()}
	rs := &Runs{ws: ws, tokens: func(string) ([]string, error) { return nil, nil }, tools: func() []string { return nil }}
	h, _ := harness.Get("fake")
	r := Run{RunSpec: RunSpec{ID: "lead-1", Harness: "fake", Checkout: t.TempDir(), Isolated: true, Modules: []string{"agents"}}}
	spec, err := rs.sandboxSpec(isolatedBox{}, h, r, 1, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, outer := range spec.Mounts {
		for _, m := range spec.Mounts[i+1:] {
			rel, ok := strings.CutPrefix(m.Inside, outer.Inside+"/")
			if outer.RW || !ok {
				continue
			}
			if _, err := os.Stat(filepath.Join(outer.Host, rel)); err != nil {
				t.Errorf("%s is mounted inside read-only %s but has no mount point: %v", m.Inside, outer.Inside, err)
			}
		}
	}
}
