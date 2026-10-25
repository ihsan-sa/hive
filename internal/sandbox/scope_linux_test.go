//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resetScope(t *testing.T) {
	t.Helper()
	scope.tried, scope.prefix = time.Time{}, nil
	t.Cleanup(func() { scope.tried, scope.prefix = time.Time{}, nil })
}

// A check marked Scope runs through systemd-run --user --scope under the
// checks' slice and ceiling, and its exit code is still its own; the slice
// gets the same ceiling. A check not marked runs as it is.
func TestScopedCheckRunsUnderTheCeiling(t *testing.T) {
	resetScope(t)
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	os.WriteFile(filepath.Join(dir, "systemd-run"), []byte("#!/bin/sh\necho \"run $*\" >> "+calls+"\nwhile [ \"$1\" != -- ]; do shift; done\nshift\nexec \"$@\"\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\necho \"ctl $*\" >> "+calls+"\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	defer func(f func() int64) { ceiling = f }(ceiling)
	ceiling = func() int64 { return 8 << 30 }

	code, err := Bare{}.Run(Spec{Argv: []string{"sh", "-c", "exit 3"}, Scope: true}, time.Minute)
	if err != nil || code != 3 {
		t.Fatalf("scoped check: code %d, %v", code, err)
	}
	if code, err := (Bare{}).Run(Spec{Argv: []string{"sh", "-c", "exit 4"}}, time.Minute); err != nil || code != 4 {
		t.Fatalf("unscoped check: code %d, %v", code, err)
	}
	raw, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	props := "MemoryHigh=4294967296 MemoryMax=6442450944 MemorySwapMax=1073741824 CPUWeight=20 IOWeight=20"
	if len(lines) != 3 || lines[0] != "ctl --user set-property --runtime "+checksSlice+" "+props {
		t.Fatalf("calls:\n%s", raw)
	}
	flags := "run --user --scope --quiet --collect --slice=" + checksSlice + " -p " + strings.ReplaceAll(props, " ", " -p ") + " -- "
	sh, _ := exec.LookPath("sh")
	if lines[1] != flags+"true" || lines[2] != flags+sh+" -c exit 3" {
		t.Fatalf("calls:\n%s", raw)
	}
}

// Where systemd-run is there but no user manager answers, checks run as now.
func TestScopedWithoutAUserManager(t *testing.T) {
	resetScope(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "systemd-run"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	if got := Scoped([]string{"/bin/true"}); len(got) != 1 || got[0] != "/bin/true" {
		t.Fatalf("got %q", got)
	}
	// A user manager that answers later is used once the retry is due.
	os.WriteFile(filepath.Join(dir, "systemd-run"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if got := Scoped([]string{"/bin/true"}); len(got) != 1 {
		t.Fatalf("probed again before the retry was due: %q", got)
	}
	scope.tried = time.Now().Add(-scopeRetry - time.Second)
	if got := Scoped([]string{"/bin/true"}); len(got) == 1 || got[0] != filepath.Join(dir, "systemd-run") {
		t.Fatalf("not scoped once the retry was due: %q", got)
	}
}

// Under a real user manager, a check that grows past its ceiling is ended
// by the kernel while this process, the daemon's stand-in, keeps running
// on time. Skipped where no user manager answers (this sandbox, CI).
func TestCheckOverItsCeilingIsEnded(t *testing.T) {
	if os.Getenv("HIVE_TEST_HOG") != "" {
		var hog [][]byte
		for range 64 {
			b := make([]byte, 16<<20)
			for i := range b {
				b[i] = 1
			}
			hog = append(hog, b)
		}
		println("allocated", len(hog))
		return
	}
	if testing.Short() {
		t.Skip("allocates a gigabyte")
	}
	resetScope(t)
	defer func(f func() int64) { ceiling = f }(ceiling)
	ceiling = func() int64 { return 128 << 20 }
	defer func(s string) { checksSlice = s }(checksSlice)
	checksSlice = "app-hivechecktest.slice" // not the live Hive's
	if len(Scoped([]string{"true"})) == 1 {
		t.Skip("no systemd user manager answers here")
	}
	stop, worst := make(chan struct{}), make(chan time.Duration)
	go func() {
		var gap time.Duration
		last := time.Now()
		for {
			select {
			case <-stop:
				worst <- gap
				return
			case now := <-time.After(50 * time.Millisecond):
				gap, last = max(gap, now.Sub(last)), now
			}
		}
	}()
	out := filepath.Join(t.TempDir(), "out")
	code, err := Bare{}.Run(Spec{Argv: []string{os.Args[0], "-test.run=^TestCheckOverItsCeilingIsEnded$", "-test.count=1"},
		Env: []string{"HIVE_TEST_HOG=1"}, Stdout: out, Scope: true}, 2*time.Minute)
	close(stop)
	gap := <-worst
	raw, _ := os.ReadFile(out)
	if err != nil || code == 0 || strings.Contains(string(raw), "allocated") {
		t.Fatalf("the hog wasn't ended: code %d, %v\n%s", code, err, raw)
	}
	if gap > 2*time.Second {
		t.Fatalf("this process stalled %s while the hog ran", gap)
	}
}
