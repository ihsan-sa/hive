package core

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A check's record says what its command wrote to the disk, so a sweep can
// name the checks that load a disk-bound machine. The file goes beside the
// test, not in the test's workspace, which may be on a tmpfs.
func TestCheckRecordsItsDisk(t *testing.T) {
	c, _, p, main := setupProject(t)
	abs := diskTestDir(t)
	var fs syscall.Statfs_t
	if syscall.Statfs(abs, &fs); fs.Type == 0x01021994 { // tmpfs
		t.Skip("the folder the test writes to is on a tmpfs")
	}
	ch := c.Effects.RunCheck(p, main, "test", "dd if=/dev/zero of="+abs+"/f bs=1M count=4 conv=fsync 2>/dev/null")
	if !ch.OK {
		t.Fatalf("check failed: %s", ch.Output)
	}
	if ch.DiskWrote < 4<<20 {
		t.Errorf("recorded %d bytes written, want at least 4 MiB", ch.DiskWrote)
	}
	if got, _ := c.Effects.GetCheck(p.Name, main, "test"); got.DiskWrote != ch.DiskWrote {
		t.Errorf("journalled %d bytes written, returned %d", got.DiskWrote, ch.DiskWrote)
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
