package core

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// procRoot is where Linux's figures are read; a test points it at its own.
var procRoot = "/proc"

// Strained says why the machine is too strained to start a turn, or "". It
// reads Linux's MemAvailable and memory and CPU pressure (PSI "some avg10",
// the share of the last ten seconds some task stalled); where those files
// don't exist it never holds. A limit of 0 turns its check off.
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
			return fmt.Sprintf("machine under %s pressure (%.2f%%, limit %g%%)", c.what, p, c.max)
		}
	}
	return ""
}

// Watched says whether any reading a limit turns on (Strained's and
// Loaded's) exists on this machine: where none does (no /proc, as on macOS
// or Windows) they never hold, so nothing is seen at all.
func Watched(memFloorMB int, memPressureMax, cpuPressureMax, loadMax float64) bool {
	return watched(procRoot, memFloorMB, memPressureMax, cpuPressureMax, loadMax)
}

func watched(proc string, memFloorMB int, memPressureMax, cpuPressureMax, loadMax float64) bool {
	for _, r := range []struct {
		on   bool
		file string
	}{{memFloorMB > 0, "meminfo"}, {memPressureMax > 0, "pressure/memory"}, {cpuPressureMax > 0, "pressure/cpu"}, {loadMax > 0, "loadavg"}} {
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

// loadIOMin is the IO pressure (PSI "some avg10", in %) at or over which a
// load that isn't the CPU's is the disk's; under it, such a load strains
// nothing.
const loadIOMin = 10.0

// Loaded says why the machine is too loaded to take on more work, or "": its
// 1-minute load average per core is over max, and the CPU is what is
// contended, or the disk is (DiskReason). Where /proc/loadavg doesn't exist
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
	if cpu, ok := procField(proc+"/pressure/cpu", "avg10="); ok && cpu < loadCPUMin {
		io, ok := procField(proc+"/pressure/io", "avg10=")
		if !ok || io < loadIOMin {
			return ""
		}
		return fmt.Sprintf("%s (io pressure %.0f%%, cpu pressure %.0f%%; load %.0f on %d cores, limit %g per core)", DiskReason, io, cpu, l, cores, max)
	}
	return fmt.Sprintf("%s (load %.0f on %d cores, limit %g per core)", LoadedReason, l, cores, max)
}
