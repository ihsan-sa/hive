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
			return fmt.Sprintf("machine under %s pressure (%.2f%%, limit %g%%)", c.what, p, c.max) // CPUPressedReason for cpu
		}
	}
	return ""
}

// CPUPressedReason starts the reason Strained gives for CPU pressure alone
// (memory is read first), so a caller can treat it like load: both mostly
// come from the same builds and child Hives.
const CPUPressedReason = "machine under cpu pressure"

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

// Loaded says why the machine is too loaded to take on more work, or "": its
// 1-minute load average per core is over max. Where /proc/loadavg doesn't
// exist it never holds, and a max of 0 turns it off.
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
	return fmt.Sprintf("%s (load %.0f on %d cores, limit %g per core)", LoadedReason, l, cores, max)
}
