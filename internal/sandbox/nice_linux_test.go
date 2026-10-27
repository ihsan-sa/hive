//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The level set for a sandboxed command holds for the fork that starts it:
// Linux nices a thread, so the goroutine stays on the thread it niced, and
// a command forked from it afterwards runs at that level. The check runs
// in a child process, so the niced thread stays out of this one, and it
// reads its own level first, so it holds under a daemon already niced.
func TestLowerPriorityHoldsForTheFork(t *testing.T) {
	if os.Getenv("HIVE_TEST_LOWER") != "" {
		read := func() int {
			t.Helper()
			out, err := exec.Command("sh", "-c", "ps -o nice= -p $$").Output()
			if err != nil {
				t.Fatal(err)
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(out)))
			if err != nil {
				t.Fatalf("nice read as %q", out)
			}
			return n
		}
		own := read()
		if err := lowerPriority(0); err != nil || read() != own {
			t.Fatalf("nice 0 moved the level from %d (%v); it leaves it as it is", own, err)
		}
		if err := lowerPriority(10); err != nil {
			t.Fatal(err)
		}
		if got, want := read(), Lowered(own, 10); got != want {
			t.Fatalf("forked at nice %d under %d, want %d", got, own, want)
		}
		fmt.Println("forked at the lowered level")
		return
	}
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps to read a level with")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLowerPriorityHoldsForTheFork$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HIVE_TEST_LOWER=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "forked at the lowered level") {
		t.Fatalf("child: %v\n%s", err, out)
	}
}
