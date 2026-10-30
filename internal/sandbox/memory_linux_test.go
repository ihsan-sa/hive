package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeCgroups points the cgroup files at a folder of plain files, with this
// process in the cgroup self.
func fakeCgroups(t *testing.T, self string) string {
	fs, proc := t.TempDir(), t.TempDir()
	a, b, c := cgroupFS, selfCgroup, memInfo
	t.Cleanup(func() { cgroupFS, selfCgroup, memInfo = a, b, c })
	cgroupFS, selfCgroup, memInfo = fs, filepath.Join(proc, "cgroup"), filepath.Join(proc, "meminfo")
	os.WriteFile(selfCgroup, []byte("0::"+self+"\n"), 0o644)
	os.WriteFile(memInfo, []byte("MemTotal:  8388608 kB\n"), 0o644)
	return fs
}

// The daemon moves into its unit's leaf with what an earlier one left, and
// turns the memory controller on for the subtree; outside the unit it
// touches nothing.
func TestSettleMemory(t *testing.T) {
	fs := fakeCgroups(t, "/app.slice/tmux.scope")
	if err := SettleMemory(); err != nil || daemonUnit() != "" {
		t.Fatalf("outside the unit: %v, %q", err, daemonUnit())
	}
	fs = fakeCgroups(t, "/app.slice/hive.service")
	unit := filepath.Join(fs, "app.slice", "hive.service")
	os.MkdirAll(unit, 0o755)
	os.WriteFile(filepath.Join(unit, "cgroup.procs"), []byte("7\n"+strconv.Itoa(os.Getpid())+"\n"), 0o644)
	if err := SettleMemory(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(unit, "cgroup.subtree_control")); string(raw) != "+memory" {
		t.Fatalf("subtree_control %q", raw)
	}
	if raw, _ := os.ReadFile(filepath.Join(unit, daemonLeaf, "cgroup.procs")); string(raw) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("the daemon didn't move to the leaf: %q", raw)
	}
	// Started in the leaf by systemd: it only turns the controller on.
	fs = fakeCgroups(t, "/app.slice/hive.service/daemon")
	unit = filepath.Join(fs, "app.slice", "hive.service")
	os.MkdirAll(filepath.Join(unit, daemonLeaf), 0o755)
	err := SettleMemory()
	os.WriteFile(filepath.Join(unit, "cgroup.subtree_control"), []byte("memory\n"), 0o644) // as the kernel reads it back
	if err != nil || daemonUnit() != unit {
		t.Fatalf("from the leaf: %v, %q", err, daemonUnit())
	}
}

// A run gets a cgroup of its own with half the unit's limit, the machine's
// memory where the unit has none, and says when the kernel killed in it.
func TestRunCgroup(t *testing.T) {
	fs := fakeCgroups(t, "/app.slice/hive.service/daemon")
	unit := filepath.Join(fs, "app.slice", "hive.service")
	os.MkdirAll(filepath.Join(unit, daemonLeaf), 0o755)
	if dir, err := enterRun(); dir != "" || err != nil {
		t.Fatalf("a run cgroup without the memory controller: %q, %v", dir, err)
	}
	os.WriteFile(filepath.Join(unit, "cgroup.subtree_control"), []byte("cpu memory pids\n"), 0o644)
	os.WriteFile(filepath.Join(unit, "memory.max"), []byte("max\n"), 0o644)
	if got := runLimit(unit); got != 4<<30 {
		t.Fatalf("with no unit limit a run gets %d, not half of 8 GiB", got)
	}
	os.WriteFile(filepath.Join(unit, "memory.high"), []byte(strconv.Itoa(14<<30)+"\n"), 0o644)
	dir, err := enterRun()
	if err != nil || dir != filepath.Join(unit, "run-"+strconv.Itoa(os.Getpid())) {
		t.Fatalf("run cgroup %q, %v", dir, err)
	}
	if got := cgroupInt(filepath.Join(dir, "memory.max")); got != 7<<30 {
		t.Fatalf("memory.max %d, not half of 14 GiB", got)
	}
	if got := cgroupInt(filepath.Join(dir, "memory.swap.max")); got != 7<<30/swapShare {
		t.Fatalf("memory.swap.max %d", got)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "cgroup.procs")); string(raw) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("sandbox-exec didn't move into its run: %q", raw)
	}
	os.WriteFile(filepath.Join(dir, "memory.events"), []byte("low 0\nhigh 0\nmax 3\noom 1\noom_kill 1\n"), 0o644)
	var errf bytes.Buffer
	leaveRun(dir, &errf)
	if !strings.Contains(errf.String(), "memory limit of 7168 MB") {
		t.Fatalf("no word of the kill: %q", errf.String())
	}
	if raw, _ := os.ReadFile(filepath.Join(unit, daemonLeaf, "cgroup.procs")); string(raw) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("sandbox-exec didn't move back: %q", raw)
	}
}

// A sweep removes a run cgroup whose sandbox-exec is gone, and keeps a live one's.
func TestSweepRuns(t *testing.T) {
	fs := fakeCgroups(t, "/hive.service/daemon")
	unit := filepath.Join(fs, "hive.service")
	os.MkdirAll(filepath.Join(unit, daemonLeaf), 0o755)
	os.WriteFile(filepath.Join(unit, "cgroup.subtree_control"), []byte("memory\n"), 0o644)
	os.Mkdir(filepath.Join(unit, "run-1"), 0o755) // empty, as the kernel's is once its processes die
	live := filepath.Join(unit, "run-2")
	os.Mkdir(live, 0o755)
	os.WriteFile(filepath.Join(live, "cgroup.procs"), []byte("2\n9\n"), 0o644)
	if n := sweepRuns(); n != 1 {
		t.Fatalf("swept %d, want 1", n)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatal("a live run's cgroup went")
	}
}
