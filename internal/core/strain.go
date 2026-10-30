package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// procRoot and cgroupRoot are where Linux's figures are read; a test points
// them at its own.
var (
	procRoot   = "/proc"
	cgroupRoot = "/sys/fs/cgroup"
)

// Strained says why the machine is too strained to start a turn, or "". It
// reads Linux's MemAvailable and memory and CPU pressure (PSI "some avg10",
// the share of the last ten seconds some task stalled); where those files
// don't exist it never holds. A limit of 0 turns its check off. Memory
// pressure that is a capped unit outside the Hive's own cgroup stalling at its
// own cap doesn't hold (cappedElsewhere): that is the unit's limit, not the
// machine's, and MemAvailable still holds on a real shortage.
func Strained(memFloorMB int, memPressureMax, cpuPressureMax float64) string {
	return strained(procRoot, memFloorMB, memPressureMax, cpuPressureMax)
}

func strained(proc string, memFloorMB int, memPressureMax, cpuPressureMax float64) string {
	if memFloorMB > 0 {
		if kb, ok := procField(proc+"/meminfo", "MemAvailable:"); ok && kb/1024 < float64(memFloorMB) {
			return fmt.Sprintf("machine low on memory (%.0f MB free, floor %d MB)", kb/1024, memFloorMB)
		}
	}
	for _, c := range []struct {
		what string
		max  float64
	}{{"memory", memPressureMax}, {"cpu", cpuPressureMax}} {
		if c.max <= 0 {
			continue
		}
		if p, ok := procField(proc+"/pressure/"+c.what, "avg10="); ok && p > c.max {
			if c.what == "memory" && cappedElsewhere(proc, cgroupRoot, c.max) != "" {
				continue
			}
			return fmt.Sprintf("machine under %s pressure (%.2f%%, limit %g%%)", c.what, p, c.max)
		}
	}
	return ""
}

// IOReason starts the reason IOStrained gives.
const IOReason = "machine under io pressure"

// IOStrained says why the disk is too saturated to start more work on it, or
// "": IO pressure's "some avg10" is over someMax, or its "full avg10" (every
// task stalled on the disk at once) over fullMax; a limit of 0 turns its
// check off, and where /proc/pressure/io doesn't exist it never holds. It
// reads IO pressure on its own, whatever the load: Loaded reads it only to
// tell whose a load is, and its disk-only reason holds nothing, so on 10-29
// turn starts rose from 5 to 13 per 10 minutes while IO pressure stood at
// some avg10 87% and 47 of the Hive's git processes sat in D state.
func IOStrained(someMax, fullMax float64) string { return ioStrained(procRoot, someMax, fullMax) }

func ioStrained(proc string, someMax, fullMax float64) string {
	raw, err := os.ReadFile(proc + "/pressure/io")
	if err != nil {
		return ""
	}
	for _, c := range []struct {
		line string
		max  float64
	}{{"full", fullMax}, {"some", someMax}} {
		if c.max <= 0 {
			continue
		}
		for _, l := range strings.Split(string(raw), "\n") {
			rest, ok := strings.CutPrefix(l, c.line+" ")
			if !ok {
				continue
			}
			_, v, _ := strings.Cut(rest, "avg10=")
			if p, err := strconv.ParseFloat(strings.SplitN(v, " ", 2)[0], 64); err == nil && p > c.max {
				return fmt.Sprintf("%s (%s avg10 %.0f%%, limit %g%%)", IOReason, c.line, p, c.max)
			}
		}
	}
	return ""
}

// Watched says whether any reading a limit turns on (Strained's, IOStrained's
// and Loaded's) exists on this machine: where none does (no /proc, as on
// macOS or Windows) they never hold, so nothing is seen at all.
func Watched(memFloorMB int, memPressureMax, cpuPressureMax, ioPressureMax, loadMax float64) bool {
	return watched(procRoot, memFloorMB, memPressureMax, cpuPressureMax, ioPressureMax, loadMax)
}

func watched(proc string, memFloorMB int, memPressureMax, cpuPressureMax, ioPressureMax, loadMax float64) bool {
	for _, r := range []struct {
		on   bool
		file string
	}{{memFloorMB > 0, "meminfo"}, {memPressureMax > 0, "pressure/memory"}, {cpuPressureMax > 0, "pressure/cpu"}, {ioPressureMax > 0, "pressure/io"}, {loadMax > 0, "loadavg"}} {
		if _, err := os.Stat(proc + "/" + r.file); r.on && err == nil {
			return true
		}
	}
	return false
}

// procField reads the number after the first occurrence of key in a /proc
// file. For a pressure file that is the "some" line, deliberately: "full"
// (every task stalled at once) is rarely reached before the machine is lost.
func procField(path, key string) (float64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	_, rest, _ := strings.Cut(string(raw), key)
	f, err := strconv.ParseFloat(strings.SplitN(strings.TrimSpace(rest), " ", 2)[0], 64)
	return f, err == nil
}

// cappedElsewhere names a cgroup outside this process's own that explains
// memory pressure over max, or "": one with memory.high or memory.max set,
// using at least 90% of it, whose own "some avg10" is over max. It says ""
// when this process's own cgroup is itself stalled over max, or when its
// cgroup can't be read (cgroup v1, no /sys/fs/cgroup), so nothing is excused
// that the Hive's own turns would feel.
func cappedElsewhere(proc, root string, max float64) string {
	self := ""
	raw, _ := os.ReadFile(proc + "/self/cgroup")
	for _, line := range strings.Split(string(raw), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			self = filepath.Clean("/" + p)
		}
	}
	if self == "" || self == "/" {
		return ""
	}
	if p, ok := procField(root+self+"/memory.pressure", "avg10="); !ok || p > max {
		return ""
	}
	found := ""
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel := "/" + strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
		if rel != "/" && (rel == self || strings.HasPrefix(rel, self+"/")) {
			return filepath.SkipDir // the Hive's own
		}
		if rel == "/" || strings.HasPrefix(self, rel+"/") {
			return nil // holds the Hive: its pressure is the Hive's too
		}
		if strings.Count(rel, "/") > 6 {
			return filepath.SkipDir
		}
		limit := cgroupNumber(path + "/memory.high")
		if l := cgroupNumber(path + "/memory.max"); limit <= 0 || l > 0 && l < limit {
			limit = l
		}
		if limit <= 0 || cgroupNumber(path+"/memory.current") < limit*0.9 {
			return nil
		}
		if p, ok := procField(path+"/memory.pressure", "avg10="); ok && p > max {
			found = rel
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// cgroupNumber reads a cgroup file holding one number, 0 for "max" or none.
func cgroupNumber(path string) float64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	return f
}

// LoadedReason starts every reason Loaded gives, so a caller can tell load
// alone from the other strains without matching its wording.
const LoadedReason = "machine loaded"

// DiskReason starts the reason Loaded gives when the load is tasks waiting
// on the disk, not the CPU. It starts with LoadedReason: it is load, read
// more closely.
const DiskReason = LoadedReason + " on its disk"

// loadCPUMin is the CPU pressure (PSI "some avg10", in %) under which a load
// over its limit isn't the CPU's: Linux counts a task waiting on the disk in
// the load average too. On 10-27 a load of 30-47 on 20 cores came with CPU
// pressure at 2.6% and IO pressure at 56%, and held the checks at 1 for 6.9
// of 12 hours, where cutting them hardly moved the load.
const loadCPUMin = 10.0

// loadCPUShare is the least CPU pressure, as a share of the IO pressure,
// for a load over its limit to be the CPU's when the disk is contended
// too: CPU pressure over loadCPUMin alone doesn't say so. On 10-28 IO
// pressure sat at 45-58% while CPU pressure wandered round 10%, so the same
// load read as the disk's one minute and the CPU's the next, and each
// "machine loaded" cut the checks back to 1: they spent 13.1 of 16.9 hours
// there.
const loadCPUShare = 0.5

// loadIOMin is the IO pressure (PSI "some avg10", in %) at or over which a
// load that isn't the CPU's is the disk's; under it, such a load strains
// nothing.
const loadIOMin = 10.0

// Loaded says why the machine is too loaded to take on more work, or "": its
// 1-minute load average per core is over max, and the CPU is what is
// contended, or the disk is (DiskReason): the load is the CPU's only when
// CPU pressure is over loadCPUMin and, with the disk contended too, at
// least loadCPUShare of IO pressure. Where /proc/loadavg doesn't exist
// it never holds, and a max of 0 turns it off; where pressure can't be read,
// load is the CPU's as before.
func Loaded(max float64) string { return loaded(procRoot, runtime.NumCPU(), max) }

func loaded(proc string, cores int, max float64) string {
	if max <= 0 {
		return ""
	}
	raw, err := os.ReadFile(proc + "/loadavg")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(raw))
	if len(f) == 0 {
		return ""
	}
	l, err := strconv.ParseFloat(f[0], 64)
	if err != nil || l/float64(cores) <= max {
		return ""
	}
	if cpu, ok := procField(proc+"/pressure/cpu", "avg10="); ok {
		io, iok := procField(proc+"/pressure/io", "avg10=")
		if cpu >= loadCPUMin && (!iok || io < loadIOMin || cpu >= io*loadCPUShare) {
			return fmt.Sprintf("%s (load %.0f on %d cores, limit %g per core)", LoadedReason, l, cores, max)
		}
		if !iok || io < loadIOMin {
			return ""
		}
		return fmt.Sprintf("%s (io pressure %.0f%%, cpu pressure %.0f%%; load %.0f on %d cores, limit %g per core)", DiskReason, io, cpu, l, cores, max)
	}
	return fmt.Sprintf("%s (load %.0f on %d cores, limit %g per core)", LoadedReason, l, cores, max)
}
