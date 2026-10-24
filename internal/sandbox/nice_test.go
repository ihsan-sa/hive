package sandbox

import (
	"os/exec"
	"runtime"
	"slices"
	"testing"
)

// A niced command runs under `nice -n` where there is one; at 0 it runs as it is.
func TestNicedPrefixesArgv(t *testing.T) {
	argv := []string{"sh", "-c", "true"}
	if got := Niced(argv, 0); !slices.Equal(got, argv) {
		t.Errorf("nice 0: %q, want argv as it is", got)
	}
	got := Niced(argv, 10)
	_, err := exec.LookPath("nice")
	if runtime.GOOS == "windows" || err != nil {
		if !slices.Equal(got, argv) {
			t.Errorf("no nice here: %q, want argv as it is", got)
		}
		return
	}
	if !slices.Equal(got, []string{"nice", "-n", "10", "sh", "-c", "true"}) {
		t.Errorf("nice 10: %q", got)
	}
}

// A level is relative to the parent's and capped where the kernel caps it.
func TestLoweredAddsAndCaps(t *testing.T) {
	for _, c := range []struct{ cur, by, want int }{{0, 15, 15}, {0, 10, 10}, {15, 15, 19}, {5, 10, 15}, {19, 2, 19}} {
		if got := Lowered(c.cur, c.by); got != c.want {
			t.Errorf("Lowered(%d, %d) = %d, want %d", c.cur, c.by, got, c.want)
		}
	}
}
