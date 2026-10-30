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
// way, doesn't fit, or "". The limit is the Hive's cgroup's or an
// ancestor's (the lowest memory.max or memory.high), what it uses there less its reclaimable file
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
	// The tightest limit on the way up: a daemon in its unit's leaf
	// "daemon" (sandbox.SettleMemory) works under the unit's.
	for ; self != "" && self != "/"; self = filepath.Dir(self) {
		dir := root + self
		l := cgroupNumber(dir + "/memory.max")
		if h := cgroupNumber(dir + "/memory.high"); h > 0 && (l <= 0 || h < l) {
			l = h
		}
		if l > 0 && (limit <= 0 || l < limit) {
			limit = l
			used = cgroupNumber(dir + "/memory.current")
			if cache, ok := procField(dir+"/memory.stat", "inactive_file "); ok {
				used -= cache
			}
		}
	}
	if limit > 0 {
		return limit, max(used, 0)
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
