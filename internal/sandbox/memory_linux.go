package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The Hive bounds its own memory (#2143). Its systemd unit has a soft cap and
// Delegate=yes (setup.unitText); the daemon moves into the unit's leaf
// cgroup "daemon" (SettleMemory), so the unit's subtree can have the memory
// controller, and each sandbox-exec moves into a cgroup of its own beside it,
// "run-<pid>", before it starts the command. A run may use half the Hive's
// limit (runShare); past it the kernel kills the run's biggest process, a
// runaway test, in place of swapping the whole machine. Where the daemon
// runs outside the unit, or the unit isn't delegated, nothing changes.
var (
	cgroupFS   = "/sys/fs/cgroup"
	selfCgroup = "/proc/self/cgroup"
	memInfo    = "/proc/meminfo"
)

const (
	unitName   = "hive.service"
	daemonLeaf = "daemon"
	runPrefix  = "run-"
	runShare   = 2 // a run's memory.max is the Hive's limit over this
	swapShare  = 4 // and its memory.swap.max its memory.max over this
)

// ownCgroup is this process's cgroup v2 path, "" where it has none.
func ownCgroup() string {
	raw, _ := os.ReadFile(selfCgroup)
	for _, line := range strings.Split(string(raw), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Clean("/" + p)
		}
	}
	return ""
}

// SettleMemory turns on the memory controller for the unit's subtree, so
// each run can have a cgroup with a limit beside the daemon's. systemd
// starts the daemon in the unit's leaf "daemon" (DelegateSubgroup=); one
// too old for that starts it in the unit's own cgroup, so it moves itself,
// and what an earlier daemon left there, into the leaf first. It does
// nothing outside the Hive's unit, and fails where the unit isn't delegated
// (a unit written before Delegate=yes, until systemd restarts it).
func SettleMemory() error {
	self := ownCgroup()
	if filepath.Base(self) == unitName {
		unit := filepath.Join(cgroupFS, self)
		leaf := filepath.Join(unit, daemonLeaf)
		if err := os.Mkdir(leaf, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(unit, "cgroup.procs"))
		if err != nil {
			return err
		}
		for _, pid := range strings.Fields(string(raw)) {
			if err := os.WriteFile(filepath.Join(leaf, "cgroup.procs"), []byte(pid), 0o644); err != nil && pid == strconv.Itoa(os.Getpid()) {
				return err
			}
		}
		self = filepath.Join(self, daemonLeaf)
	}
	if filepath.Base(self) != daemonLeaf || filepath.Base(filepath.Dir(self)) != unitName {
		return nil
	}
	return os.WriteFile(filepath.Join(cgroupFS, filepath.Dir(self), "cgroup.subtree_control"), []byte("+memory"), 0o644)
}

// daemonUnit is the unit's cgroup folder when this process sits in its
// leaf "daemon" with the memory controller on for the subtree, else "".
func daemonUnit() string {
	self := ownCgroup()
	if filepath.Base(self) != daemonLeaf || filepath.Base(filepath.Dir(self)) != unitName {
		return ""
	}
	unit := filepath.Join(cgroupFS, filepath.Dir(self))
	ctl, _ := os.ReadFile(filepath.Join(unit, "cgroup.subtree_control"))
	if !slices.Contains(strings.Fields(string(ctl)), "memory") {
		return ""
	}
	return unit
}

// runLimit is a run's memory.max: the Hive's limit (the unit's memory.high
// or memory.max, the lower, else the machine's memory) over runShare.
func runLimit(unit string) int64 {
	limit := int64(0)
	for _, f := range []string{"memory.max", "memory.high"} {
		if n := cgroupInt(filepath.Join(unit, f)); n > 0 && (limit == 0 || n < limit) {
			limit = n
		}
	}
	if limit == 0 {
		raw, _ := os.ReadFile(memInfo)
		if _, rest, ok := strings.Cut(string(raw), "MemTotal:"); ok {
			kb, _ := strconv.ParseInt(strings.Fields(rest)[0], 10, 64)
			limit = kb << 10
		}
	}
	return limit / runShare
}

// cgroupInt reads a cgroup file holding one number, 0 for "max" or none.
func cgroupInt(path string) int64 {
	raw, _ := os.ReadFile(path)
	n, _ := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return n
}

// enterRun moves this sandbox-exec into a cgroup of its own with the run's
// limit, before it starts the command, which inherits it. It returns the
// run's folder, "" where the daemon's unit gives none.
func enterRun() (string, error) {
	unit := daemonUnit()
	if unit == "" {
		return "", nil
	}
	dir := filepath.Join(unit, runPrefix+strconv.Itoa(os.Getpid()))
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return "", err
	}
	max := runLimit(unit)
	if max > 0 {
		os.WriteFile(filepath.Join(dir, "memory.max"), []byte(strconv.FormatInt(max, 10)), 0o644)
		os.WriteFile(filepath.Join(dir, "memory.swap.max"), []byte(strconv.FormatInt(max/swapShare, 10)), 0o644)
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		os.Remove(dir)
		return "", err
	}
	return dir, nil
}

// leaveRun says in errf when the kernel killed a process of the run at its
// limit, moves this sandbox-exec back to the daemon's leaf, kills what the
// command left in the run, and removes the run's cgroup.
func leaveRun(dir string, errf io.Writer) {
	if dir == "" {
		return
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "memory.events"))
	if _, rest, ok := strings.Cut(string(raw), "oom_kill "); ok {
		if n, _ := strconv.Atoi(strings.Fields(rest)[0]); n > 0 {
			fmt.Fprintf(errf, "sandbox: the command reached its memory limit of %d MB, and the kernel killed %d of its processes\n", cgroupInt(filepath.Join(dir, "memory.max"))>>20, n)
		}
	}
	// Still inside, it would kill itself with the rest.
	if os.WriteFile(filepath.Join(filepath.Dir(dir), daemonLeaf, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o644) == nil {
		dropRun(dir)
	}
}

// dropRun kills what is left in a run's cgroup and removes it.
func dropRun(dir string) bool {
	for i := 0; i < 20; i++ {
		if err := os.Remove(dir); err == nil || os.IsNotExist(err) {
			return true
		}
		os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0o644)
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// sweepRuns removes the run cgroups whose sandbox-exec is gone, killing
// what it left in them. A live sandbox-exec sits in its own run's cgroup.
func sweepRuns() int {
	unit := daemonUnit()
	if unit == "" {
		return 0
	}
	n := 0
	dirs, _ := filepath.Glob(filepath.Join(unit, runPrefix+"*"))
	for _, dir := range dirs {
		raw, _ := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
		if owner := strings.TrimPrefix(filepath.Base(dir), runPrefix); !slices.Contains(strings.Fields(string(raw)), owner) && dropRun(dir) {
			n++
		}
	}
	return n
}
