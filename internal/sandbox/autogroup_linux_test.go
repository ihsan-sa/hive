package sandbox

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// A session started under the launcher gets the sandbox's level in its
// autogroup, as one a harness starts for each shell command would; the
// launcher's own session, which here is the test's, keeps its own.
func TestLowerAutogroupsLowersSessionsStartedInside(t *testing.T) {
	ownPath := "/proc/self/autogroup"
	ownGroup, ownNice := readAutogroup(ownPath)
	if ownGroup == "" {
		t.Skip("no autogroups on this kernel")
	}
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep to start:", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	path := "/proc/" + strconv.Itoa(cmd.Process.Pid) + "/autogroup"
	var group string
	for i := 0; i < 50; i++ { // setsid runs in the child after the fork
		if group, _ = readAutogroup(path); group != "" && group != ownGroup {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if group == "" || group == ownGroup {
		t.Skip("the child's session has no autogroup of its own")
	}
	want := min(ownNice+7, 19)
	lowerAutogroups(os.Getpid(), want)
	if _, n := readAutogroup(path); n != want {
		t.Fatalf("the session started inside runs at autogroup nice %d, want %d", n, want)
	}
	if g, n := readAutogroup(ownPath); g != ownGroup || n != ownNice {
		t.Fatalf("the launcher's own session went to %s nice %d, want it left at %s nice %d", g, n, ownGroup, ownNice)
	}
	lowerAutogroups(os.Getpid(), want-1) // never raised back
	if _, n := readAutogroup(path); n != want {
		t.Fatalf("a lower level moved the group back to %d", n)
	}
}

func TestReadAutogroup(t *testing.T) {
	dir := t.TempDir()
	for text, want := range map[string]int{"/autogroup-12 nice 0\n": 0, "/autogroup-3 nice 10": 10, "garbage": -1} {
		p := dir + "/ag"
		os.WriteFile(p, []byte(text), 0o600)
		g, n := readAutogroup(p)
		if want < 0 {
			if g != "" {
				t.Errorf("%q read as %q", text, g)
			}
			continue
		}
		if g == "" || n != want {
			t.Errorf("%q read as %q %d, want nice %d", text, g, n, want)
		}
	}
}
