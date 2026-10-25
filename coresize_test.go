package hive

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// tests/core-size.sh reads the core's packages from docs/core.md and counts
// them; a package renamed or moved without docs/core.md fails here, not
// silently on the next merge's measure.
func TestCoreSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the size script runs where checks run, under a POSIX sh")
	}
	out, err := exec.Command("sh", "tests/core-size.sh", "--lines").CombinedOutput()
	if err != nil {
		t.Fatalf("tests/core-size.sh: %v\n%s", err, out)
	}
	got := map[string]int{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			t.Fatalf("not a \"name value\" line: %q", l)
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			t.Fatalf("not a count: %q", l)
		}
		got[f[0]] = n
	}
	if got["core_lines"] <= 0 || got["go_lines"] <= got["core_lines"] {
		t.Fatalf("core %d of %d lines: the core should be some of the Go, not none or all", got["core_lines"], got["go_lines"])
	}
}
