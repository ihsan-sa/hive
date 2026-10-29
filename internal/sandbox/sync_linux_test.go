package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A synchronous command dies with the process that started it, killed
// outright: a check whose daemon is gone has no one to time it out.
func TestSyncCommandDiesWithItsDaemon(t *testing.T) {
	if os.Getenv("HIVE_SYNC_HELPER") == "1" {
		pidFile := os.Getenv("HIVE_SYNC_PID")
		go runSync([]string{"/bin/sh", "-c", "echo $$ > " + pidFile + "; exec sleep 300"}, nil, "", "", "", time.Hour, nil)
		time.Sleep(time.Hour)
		return
	}
	pidFile := t.TempDir() + "/pid"
	cmd := exec.Command(os.Args[0], "-test.run=^TestSyncCommandDiesWithItsDaemon$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HIVE_SYNC_HELPER=1", "HIVE_SYNC_PID="+pidFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := waitPid(t, pidFile)
	cmd.Process.Kill()
	cmd.Wait()
	gone(t, pid)
}

// What a command leaves running in its group ends when it returns.
func TestSyncCommandLeavesNothingRunning(t *testing.T) {
	pidFile := t.TempDir() + "/pid"
	code, err := runSync([]string{"/bin/sh", "-c", "sleep 300 & echo $! > " + pidFile + "; exit 3"}, nil, "", "", "", time.Minute, nil)
	if err != nil || code != 3 {
		t.Fatalf("code %d, %v", code, err)
	}
	gone(t, waitPid(t, pidFile))
}

func waitPid(t *testing.T, path string) int {
	t.Helper()
	for i := 0; i < 100; i++ {
		raw, _ := os.ReadFile(path)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the command never started")
	return 0
}

// waitTagged waits for pid to be prog carrying a bare command's tag. $! is
// the shell's fork, and on a loaded machine it may still be on its way
// through setsid to prog when the sweep looks: mid-exec, the kernel shows an
// empty environment, so the sweep would pass it by.
func waitTagged(t *testing.T, pid int, prog string) {
	t.Helper()
	dir := fmt.Sprintf("/proc/%d/", pid)
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		comm, _ := os.ReadFile(dir + "comm")
		env, _ := os.ReadFile(dir + "environ")
		if strings.TrimSpace(string(comm)) == prog && strings.Contains("\x00"+string(env), "\x00"+OwnerEnv+"=") {
			return
		}
	}
	t.Fatalf("pid %d never became a tagged %s", pid, prog)
}

// gone waits for pid to end; a zombie (ended, its parent not yet reaped it) counts.
func gone(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil || syscall.Kill(pid, 0) != nil {
			return
		}
		if s := string(raw); strings.HasPrefix(strings.Fields(s[strings.LastIndexByte(s, ')')+1:])[0], "Z") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("pid %d outlived its check", pid)
}

// A bare command's process that left its group (setsid) outlives the daemon
// that died under it; the next sweep ends it by its daemon's tag, and leaves
// a live daemon's alone.
func TestSweepEndsWhatADeadDaemonsBareCommandLeft(t *testing.T) {
	if os.Getenv("HIVE_SYNC_HELPER") == "2" {
		pidFile := os.Getenv("HIVE_SYNC_PID")
		go Bare{}.Run(Spec{Argv: []string{"/bin/sh", "-c", "setsid sleep 300 & echo $! > " + pidFile + "; exec sleep 300"}, Cwd: "/"}, time.Hour)
		time.Sleep(time.Hour)
		return
	}
	pidFile := t.TempDir() + "/pid"
	cmd := exec.Command(os.Args[0], "-test.run=^TestSweepEndsWhatADeadDaemonsBareCommandLeft$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HIVE_SYNC_HELPER=2", "HIVE_SYNC_PID="+pidFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := waitPid(t, pidFile)
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	waitTagged(t, pid, "sleep")
	live := exec.Command("sleep", "60")
	live.Env = append(os.Environ(), OwnerEnv+"="+owner)
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { live.Process.Kill(); live.Wait() })
	cmd.Process.Kill()
	cmd.Wait()
	time.Sleep(100 * time.Millisecond)
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the detached process ended with its daemon: nothing for the sweep to do")
	}
	if n := sweepOrphans(); n < 1 {
		t.Errorf("the sweep ended %d processes, want the orphan", n)
	}
	gone(t, pid)
	if syscall.Kill(live.Process.Pid, 0) != nil {
		t.Error("the sweep killed a live daemon's command")
	}
}

// A synchronous command says what it wrote to the disk, a child's writes
// (a shell running dd) among them. The file goes beside the test, not in
// TMPDIR, which may be a tmpfs that writes to no disk.
func TestRunSaysWhatItWroteToDisk(t *testing.T) {
	dir := diskTestDir(t)
	var disk Disk
	code, err := Bare{}.Run(Spec{Argv: []string{"/bin/sh", "-c", "dd if=/dev/zero of=" + dir + "/f bs=1M count=4 conv=fsync 2>/dev/null; true"}, Disk: &disk}, time.Minute)
	if code != 0 || err != nil {
		t.Fatalf("ran with %d, %v", code, err)
	}
	var fs syscall.Statfs_t
	if syscall.Statfs(dir, &fs); fs.Type == 0x01021994 { // tmpfs
		t.Skip("the folder the test writes to is on a tmpfs")
	}
	if disk.Wrote < 4<<20 {
		t.Errorf("wrote %d bytes, want at least 4 MiB", disk.Wrote)
	}
}

// diskTestDir makes the folder beside the test that a disk test writes to.
// A run cut short never reaches its cleanup, so it first removes the
// folders earlier runs left behind (.gitignore keeps them out of commits
// meanwhile); a folder under ten minutes old may be a parallel run's.
// Where the test's own folder can't be written (a canary's read-only
// checkout), it falls back to the temp folder, which the caller skips if
// that is a tmpfs, and skips if neither can be written.
func diskTestDir(t *testing.T) string {
	old, _ := filepath.Glob("disk-test-*")
	for _, d := range old {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() && time.Since(fi.ModTime()) > 10*time.Minute {
			os.RemoveAll(d)
		}
	}
	dir, err := os.MkdirTemp(".", "disk-test-")
	if errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		dir, err = os.MkdirTemp("", "disk-test-")
		if err != nil {
			t.Skipf("neither the test's folder nor the temp folder can be written: %v", err)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
