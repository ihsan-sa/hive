package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What an orphan wrote reaches whoever waits for the adopter, as bwrap's
// reaper does when bwrap exits without waiting for it: the test's shell
// exits at once, leaving a writer that only the adopter waits for.
func TestAdoptedOrphanDiskCounts(t *testing.T) {
	if dir := os.Getenv("HIVE_ADOPT_HELPER"); dir != "" {
		if err := adoptOrphans(); err != nil {
			fmt.Print("prctl: ", err)
			os.Exit(0)
		}
		write := "dd if=/dev/zero of=" + filepath.Join(dir, "f") + " bs=1M count=8 conv=fsync 2>/dev/null"
		if err := exec.Command("/bin/sh", "-c", "("+write+") & exit 0").Run(); err != nil {
			fmt.Print("run: ", err)
			os.Exit(0)
		}
		reapAdopted(time.Minute)
		var left syscall.WaitStatus
		if _, err := syscall.Wait4(-1, &left, syscall.WNOHANG, nil); err != syscall.ECHILD {
			fmt.Print("a child was left unreaped")
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	// A filesystem that counts no block writes (tmpfs) can't show it.
	probe := exec.Command("/bin/sh", "-c", "dd if=/dev/zero of="+filepath.Join(dir, "p")+" bs=1M count=8 conv=fsync 2>/dev/null")
	if err := probe.Run(); err != nil || diskUsed(probe.ProcessState).Wrote == 0 {
		t.Skip("this filesystem counts no block writes:", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestAdoptedOrphanDiskCounts$")
	cmd.Env = append(os.Environ(), "HIVE_ADOPT_HELPER="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("%v: %s", err, out)
	}
	if got := diskUsed(cmd.ProcessState).Wrote; got < 4<<20 {
		t.Fatalf("the adopter's waiter saw %d bytes written, want the orphan's 8 MiB", got)
	}
}

// With nothing to adopt, reaping returns at once rather than waiting its bound.
func TestReapAdoptedNothingLeft(t *testing.T) {
	start := time.Now()
	reapAdopted(time.Minute)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v with no child", d)
	}
}
