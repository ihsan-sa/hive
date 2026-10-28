package core

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A check's record says what its command wrote to the disk, so a sweep can
// name the checks that load a disk-bound machine. The file goes beside the
// test, not in the test's workspace, which may be on a tmpfs.
func TestCheckRecordsItsDisk(t *testing.T) {
	c, _, p, main := setupProject(t)
	dir, err := os.MkdirTemp(".", "disk-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	abs, _ := filepath.Abs(dir)
	var fs syscall.Statfs_t
	if syscall.Statfs(abs, &fs); fs.Type == 0x01021994 { // tmpfs
		t.Skip("the test's own folder is on a tmpfs")
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
