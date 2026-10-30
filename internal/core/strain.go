package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultTurnMB is the memory each agent turn is budgeted (Config.TurnMB):
// the one figure that paces the Hive. A turn (or a worker) starts while the
// memory left leaves this much for it and for each one starting beside it; a
// lead's or a warden's turn never waits (NeverWaits).
const DefaultTurnMB = 240

// MemWait starts every reason MemBudget gives, so a caller can tell the
// budget's wait from others without matching its live figures.
const MemWait = "waiting: memory"

// procRoot and cgroupRoot are where Linux's figures are read; a test points
// them at its own.
var (
	procRoot   = "/proc"
	cgroupRoot = "/sys/fs/cgroup"
)

// MemBudget says why one more turn, beside launching others already on their
// way, doesn't fit, or "". The limit is the Hive's cgroup's (the lower of
// memory.max and memory.high), what it uses there less its reclaimable file
// cache; with no cgroup limit it is the machine's MemTotal, what it uses
// what MemAvailable doesn't cover. Where none of those can be read it never
// holds, nor with turnMB 0.
func MemBudget(turnMB, launching int) string {
	return memBudget(procRoot, cgroupRoot, turnMB, launching)
}

func memBudget(proc, root string, turnMB, launching int) string {
	if turnMB <= 0 {
		return ""
	}
	limit, used := memRoom(proc, root)
	if limit <= 0 {
		return ""
	}
	need := float64(launching+1) * float64(turnMB) * (1 << 20)
	if used+need <= limit {
		return ""
	}
	return fmt.Sprintf("%s (%d of %d MB in use; each turn is budgeted %d MB)", MemWait, int(used/(1<<20)), int(limit/(1<<20)), turnMB)
}

// memRoom is the memory limit the Hive works under and what it uses of it,
// in bytes; 0, 0 when neither its cgroup nor the machine can be read.
func memRoom(proc, root string) (limit, used float64) {
	self := ""
	raw, _ := os.ReadFile(proc + "/self/cgroup")
	for _, line := range strings.Split(string(raw), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			self = filepath.Clean("/" + p)
		}
	}
	if self != "" && self != "/" {
		dir := root + self
		limit = cgroupNumber(dir + "/memory.max")
		if h := cgroupNumber(dir + "/memory.high"); h > 0 && (limit <= 0 || h < limit) {
			limit = h
		}
		if limit > 0 {
			used = cgroupNumber(dir + "/memory.current")
			if cache, ok := procField(dir+"/memory.stat", "inactive_file "); ok {
				used -= cache
			}
			return limit, max(used, 0)
		}
	}
	total, ok := procField(proc+"/meminfo", "MemTotal:")
	avail, ok2 := procField(proc+"/meminfo", "MemAvailable:")
	if !ok || !ok2 {
		return 0, 0
	}
	return total * 1024, (total - avail) * 1024
}

// procField reads the number after key in a /proc or cgroup file.
func procField(path, key string) (float64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	_, rest, found := strings.Cut(string(raw), key)
	if !found {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.SplitN(strings.TrimSpace(rest), " ", 2)[0], 64)
	return f, err == nil
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

// DefaultDiskFullMax is where Config.DiskFullMax starts: the share of the
// last minute (Linux IO pressure, "full avg60", percent) every running task
// sat stalled on the disk at once, past which the disk counts as saturated.
// On 10-30 it stood at ~60% from boot, some avg60 92%, a write ~0.8 s.
const DefaultDiskFullMax = 40

// DiskWait starts every reason DiskBusy gives.
const DiskWait = "waiting: disk"

// DiskBusy says why the disk is too saturated to start more work on it, or
// "": its IO pressure's full avg60 is over fullMax. Where /proc/pressure/io
// can't be read, or with fullMax 0, it never holds. Memory is a budget per
// turn (MemBudget); the disk has no figure per turn to budget, so it is read
// as a whole and starts of new work wait while it is over (the work module:
// new workers and checks past the first).
func DiskBusy(fullMax float64) string { return diskBusy(procRoot, fullMax) }

// DiskFull is the limit DiskBusy reads for c: its DiskFullMax, the default
// where unset, 0 (off) where below 0.
func (c Config) DiskFull() float64 {
	switch {
	case c.DiskFullMax < 0:
		return 0
	case c.DiskFullMax == 0:
		return DefaultDiskFullMax
	}
	return c.DiskFullMax
}

func diskBusy(proc string, fullMax float64) string {
	if fullMax <= 0 {
		return ""
	}
	raw, err := os.ReadFile(proc + "/pressure/io")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(l, "full ")
		if !ok {
			continue
		}
		_, v, _ := strings.Cut(rest, "avg60=")
		if p, err := strconv.ParseFloat(strings.SplitN(v, " ", 2)[0], 64); err == nil && p > fullMax {
			return fmt.Sprintf("%s (IO pressure full avg60 %.0f%%, limit %g%%)", DiskWait, p, fullMax)
		}
	}
	return ""
}
